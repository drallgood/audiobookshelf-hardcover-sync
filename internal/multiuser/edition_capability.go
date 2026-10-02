package multiuser

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
)

// EditionCapabilityOperation identifies a Hardcover catalogue-write
// operation. Ebook insertion and regional Audible resolution have separate
// permissions and are reported independently.
type EditionCapabilityOperation string

const (
	EditionCapabilityInsertEdition EditionCapabilityOperation = "insert_edition"
	EditionCapabilityUpsertBook    EditionCapabilityOperation = "upsert_book"
)

// EditionCapabilityState is the evidence available for one catalogue-write
// operation under a profile's current Hardcover token.
type EditionCapabilityState string

const (
	EditionCapabilityAllowed    EditionCapabilityState = "allowed"
	EditionCapabilityDenied     EditionCapabilityState = "denied"
	EditionCapabilityUnverified EditionCapabilityState = "unverified"
)

// EditionCapabilityStatus describes capability evidence for one write
// operation. Unverified permits a later eligible, user-confirmed attempt with
// a warning; a known denial prevents an attempt.
type EditionCapabilityStatus struct {
	Operation  EditionCapabilityOperation `json:"operation"`
	Status     EditionCapabilityState     `json:"status"`
	CanAttempt bool                       `json:"can_attempt"`
	Reason     string                     `json:"reason,omitempty"`
	Warning    string                     `json:"warning,omitempty"`
}

// EditionCapability keeps ebook insertion separate from audiobook import.
type EditionCapability struct {
	Ebook     EditionCapabilityStatus `json:"ebook"`
	Audiobook EditionCapabilityStatus `json:"audiobook"`
	DryRun    bool                    `json:"dry_run"`
}

type editionCapabilityCacheKey struct {
	profileID string
	operation EditionCapabilityOperation
}

type editionCapabilityCacheEntry struct {
	tokenFingerprint string
	status           EditionCapabilityState
}

type editionCapabilityFlightKey struct {
	profileID        string
	operation        EditionCapabilityOperation
	tokenFingerprint string
	generation       uint64
}

type editionCapabilityRefreshKey struct {
	profileID        string
	tokenFingerprint string
	generation       uint64
}

type editionCapabilityFlight struct {
	done       chan struct{}
	status     EditionCapabilityState
	generation uint64
}

type editionCapabilityRefreshFlight struct {
	done       chan struct{}
	capability EditionCapability
	err        error
}

type editionCapabilityClientEntry struct {
	tokenFingerprint string
	client           *hardcover.Client
}

type profileHardcoverRateLimiterKey struct {
	profileID        string
	tokenFingerprint string
}

// EditionCapabilityForProfile returns operation-specific evidence for the
// profile's Hardcover token. Ebook insert_edition and audiobook upsert_book
// use separate validation-only probes. Results are cached by profile,
// operation, and current token.
func (s *MultiUserService) EditionCapabilityForProfile(ctx context.Context, profileID string) (EditionCapability, error) {
	return s.editionCapabilityForProfile(ctx, profileID, false)
}

// RefreshEditionCapabilityForProfile probes Hardcover again for the profile's
// current token, replacing any cached permission evidence. It shares the
// existing profile client and rate limiter with other Hardcover operations.
func (s *MultiUserService) RefreshEditionCapabilityForProfile(ctx context.Context, profileID string) (EditionCapability, error) {
	return s.editionCapabilityForProfile(ctx, profileID, true)
}

func (s *MultiUserService) editionCapabilityForProfile(ctx context.Context, profileID string, refresh bool) (EditionCapability, error) {
	profile, err := s.repository.GetProfileHardcoverSettings(profileID)
	if err != nil {
		return EditionCapability{}, fmt.Errorf("failed to load profile %s for edition capability: %w", profileID, err)
	}
	if profile == nil {
		s.invalidateEditionCapabilityProfile(profileID, true)
		return EditionCapability{}, fmt.Errorf("%w: %s", ErrProfileNotFound, profileID)
	}

	if strings.TrimSpace(profile.HardcoverToken) == "" {
		return EditionCapability{
			Ebook:     missingHardcoverTokenEditionCapability(EditionCapabilityInsertEdition),
			Audiobook: missingHardcoverTokenEditionCapability(EditionCapabilityUpsertBook),
			DryRun:    profile.SyncConfig.DryRun,
		}, nil
	}
	if profile.SyncConfig.DryRun {
		return EditionCapability{
			Ebook:     allowedEditionCapability(EditionCapabilityInsertEdition),
			Audiobook: allowedEditionCapability(EditionCapabilityUpsertBook),
			DryRun:    true,
		}, nil
	}
	if refresh {
		return s.refreshEditionCapabilityProbes(ctx, profileID, profile.HardcoverToken)
	}
	return s.probeEditionCapabilityForProfile(ctx, profileID, profile.HardcoverToken)
}

func (s *MultiUserService) probeEditionCapabilityForProfile(ctx context.Context, profileID, token string) (EditionCapability, error) {
	ebookState := s.cachedEditionCapabilityProbe(ctx, profileID, EditionCapabilityInsertEdition, token)
	audiobookState := s.cachedEditionCapabilityProbe(ctx, profileID, EditionCapabilityUpsertBook, token)
	return EditionCapability{
		Ebook:     editionCapabilityStatus(EditionCapabilityInsertEdition, ebookState),
		Audiobook: editionCapabilityStatus(EditionCapabilityUpsertBook, audiobookState),
		DryRun:    false,
	}, nil
}

func (s *MultiUserService) refreshEditionCapabilityProbes(ctx context.Context, profileID, token string) (EditionCapability, error) {
	fingerprint := hardcoverTokenFingerprint(token)
	s.hardcoverClientMutex.Lock()
	key := editionCapabilityRefreshKey{
		profileID: profileID, tokenFingerprint: fingerprint,
		generation: s.editionCapabilityGenerations[profileID],
	}
	if flight, ok := s.editionCapabilityRefreshFlights[key]; ok {
		s.hardcoverClientMutex.Unlock()
		return waitForEditionCapabilityRefresh(ctx, flight)
	}

	for cacheKey := range s.editionCapabilityCache {
		if cacheKey.profileID == profileID {
			delete(s.editionCapabilityCache, cacheKey)
		}
	}
	key.generation++
	s.editionCapabilityGenerations[profileID] = key.generation
	flight := &editionCapabilityRefreshFlight{done: make(chan struct{})}
	s.editionCapabilityRefreshFlights[key] = flight
	s.hardcoverClientMutex.Unlock()

	capability, err := s.probeEditionCapabilityForProfile(ctx, profileID, token)
	s.hardcoverClientMutex.Lock()
	flight.capability = capability
	flight.err = err
	delete(s.editionCapabilityRefreshFlights, key)
	close(flight.done)
	s.hardcoverClientMutex.Unlock()
	return capability, err
}

func waitForEditionCapabilityRefresh(ctx context.Context, flight *editionCapabilityRefreshFlight) (EditionCapability, error) {
	select {
	case <-ctx.Done():
		return EditionCapability{
			Ebook:     unverifiedEditionCapability(EditionCapabilityInsertEdition),
			Audiobook: unverifiedEditionCapability(EditionCapabilityUpsertBook),
		}, nil
	case <-flight.done:
		return flight.capability, flight.err
	}
}

func (s *MultiUserService) cachedEditionCapabilityProbe(ctx context.Context, profileID string, operation EditionCapabilityOperation, token string) EditionCapabilityState {
	fingerprint := hardcoverTokenFingerprint(token)
	cacheKey := editionCapabilityCacheKey{profileID: profileID, operation: operation}

	s.hardcoverClientMutex.Lock()
	generation := s.editionCapabilityGenerations[profileID]
	flightKey := editionCapabilityFlightKey{
		profileID: profileID, operation: operation, tokenFingerprint: fingerprint, generation: generation,
	}
	if cached, ok := s.editionCapabilityCache[cacheKey]; ok {
		if cached.tokenFingerprint == fingerprint {
			s.hardcoverClientMutex.Unlock()
			return cached.status
		}
		delete(s.editionCapabilityCache, cacheKey)
	}
	if inFlight, ok := s.editionCapabilityFlights[flightKey]; ok {
		done := inFlight.done
		s.hardcoverClientMutex.Unlock()
		select {
		case <-ctx.Done():
			return EditionCapabilityUnverified
		case <-done:
			return inFlight.status
		}
	}
	flight := &editionCapabilityFlight{done: make(chan struct{}), generation: generation}
	s.editionCapabilityFlights[flightKey] = flight
	client := s.editionCapabilityProbeClientLocked(profileID, token, fingerprint)
	s.hardcoverClientMutex.Unlock()

	status := hardcover.EditionCapabilityProbeUnverified
	switch operation {
	case EditionCapabilityInsertEdition:
		status = client.ProbeInsertEditionCapability(ctx)
	case EditionCapabilityUpsertBook:
		status = client.ProbeUpsertBookCapability(ctx)
	}
	state := EditionCapabilityState(status)

	s.hardcoverClientMutex.Lock()
	flight.status = state
	if ctx.Err() == nil && s.editionCapabilityGenerations[profileID] == flight.generation {
		if cached, ok := s.editionCapabilityCache[cacheKey]; !ok || cached.tokenFingerprint == fingerprint {
			s.editionCapabilityCache[cacheKey] = editionCapabilityCacheEntry{
				tokenFingerprint: fingerprint,
				status:           state,
			}
		}
	}
	delete(s.editionCapabilityFlights, flightKey)
	close(flight.done)
	s.hardcoverClientMutex.Unlock()
	return state
}

func hardcoverTokenFingerprint(token string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
}

func (s *MultiUserService) invalidateEditionCapabilityProfile(profileID string, invalidateRateLimiter bool) {
	s.hardcoverClientMutex.Lock()
	for key := range s.editionCapabilityCache {
		if key.profileID == profileID {
			delete(s.editionCapabilityCache, key)
		}
	}
	delete(s.editionCapabilityClients, profileID)
	if invalidateRateLimiter {
		for key := range s.profileHardcoverRateLimiters {
			if key.profileID == profileID {
				delete(s.profileHardcoverRateLimiters, key)
			}
		}
	}
	s.editionCapabilityGenerations[profileID]++
	s.hardcoverClientMutex.Unlock()
}

// editionCapabilityProbeClientLocked returns the profile's cached Hardcover
// client so its configured rate limiter is shared with sync and creation
// clients. It must be called with hardcoverClientMutex held.
func (s *MultiUserService) editionCapabilityProbeClientLocked(profileID, token, fingerprint string) *hardcover.Client {
	if entry, ok := s.editionCapabilityClients[profileID]; ok && entry.tokenFingerprint == fingerprint {
		return entry.client
	}
	clientConfig := s.hardcoverClientConfig()
	clientConfig.RateLimiter = s.profileHardcoverRateLimiterLocked(profileID, fingerprint)
	client := hardcover.NewClientWithConfig(clientConfig, token, s.logger)
	s.editionCapabilityClients[profileID] = editionCapabilityClientEntry{tokenFingerprint: fingerprint, client: client}
	return client
}

// profileHardcoverRateLimiterLocked returns the per-profile, per-token limiter.
// It must be called with hardcoverClientMutex held.
func (s *MultiUserService) profileHardcoverRateLimiterLocked(profileID, fingerprint string) *util.RateLimiter {
	key := profileHardcoverRateLimiterKey{profileID: profileID, tokenFingerprint: fingerprint}
	if limiter, ok := s.profileHardcoverRateLimiters[key]; ok {
		return limiter
	}
	clientConfig := s.hardcoverClientConfig()
	limiter := util.NewRateLimiter(clientConfig.RateLimit, clientConfig.MaxConcurrent, s.logger)
	s.profileHardcoverRateLimiters[key] = limiter
	return limiter
}

func unverifiedEditionCapability(operation EditionCapabilityOperation) EditionCapabilityStatus {
	return EditionCapabilityStatus{
		Operation:  operation,
		Status:     EditionCapabilityUnverified,
		CanAttempt: true,
		Warning:    "permission_unverified",
	}
}

func missingHardcoverTokenEditionCapability(operation EditionCapabilityOperation) EditionCapabilityStatus {
	return EditionCapabilityStatus{
		Operation:  operation,
		Status:     EditionCapabilityDenied,
		CanAttempt: false,
		Reason:     "hardcover_token_missing",
	}
}

func allowedEditionCapability(operation EditionCapabilityOperation) EditionCapabilityStatus {
	return EditionCapabilityStatus{
		Operation:  operation,
		Status:     EditionCapabilityAllowed,
		CanAttempt: true,
	}
}

func editionCapabilityStatus(operation EditionCapabilityOperation, state EditionCapabilityState) EditionCapabilityStatus {
	switch state {
	case EditionCapabilityAllowed:
		return allowedEditionCapability(operation)
	case EditionCapabilityDenied:
		return EditionCapabilityStatus{
			Operation:  operation,
			Status:     EditionCapabilityDenied,
			CanAttempt: false,
			Reason:     "insufficient_scope",
		}
	default:
		return unverifiedEditionCapability(operation)
	}
}
