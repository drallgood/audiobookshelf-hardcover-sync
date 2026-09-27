package multiuser

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

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

const (
	editionCapabilityDefiniteTTL   = 5 * time.Minute
	editionCapabilityUnverifiedTTL = 15 * time.Second
)

type editionCapabilityCacheKey struct {
	profileID string
	operation EditionCapabilityOperation
}

type editionCapabilityCacheEntry struct {
	tokenFingerprint string
	status           EditionCapabilityState
	expiresAt        time.Time
}

type editionCapabilityFlightKey struct {
	profileID        string
	operation        EditionCapabilityOperation
	tokenFingerprint string
	generation       uint64
}

type editionCapabilityFlight struct {
	done       chan struct{}
	status     EditionCapabilityState
	generation uint64
}

type editionCapabilityClientEntry struct {
	tokenFingerprint string
	client           *hardcover.Client
}

type profileHardcoverRateLimiterEntry struct {
	tokenFingerprint string
	limiter          *util.RateLimiter
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

	ebookState := s.cachedEditionCapabilityProbe(ctx, profileID, EditionCapabilityInsertEdition, profile.HardcoverToken)
	audiobookState := s.cachedEditionCapabilityProbe(ctx, profileID, EditionCapabilityUpsertBook, profile.HardcoverToken)
	return EditionCapability{
		Ebook:     editionCapabilityStatus(EditionCapabilityInsertEdition, ebookState),
		Audiobook: editionCapabilityStatus(EditionCapabilityUpsertBook, audiobookState),
		DryRun:    false,
	}, nil
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
		if cached.tokenFingerprint == fingerprint && time.Now().Before(cached.expiresAt) {
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
	duration := editionCapabilityDefiniteTTL
	if state == EditionCapabilityUnverified {
		duration = editionCapabilityUnverifiedTTL
	}
	if s.editionCapabilityGenerations[profileID] == flight.generation {
		if cached, ok := s.editionCapabilityCache[cacheKey]; !ok || cached.tokenFingerprint == fingerprint {
			s.editionCapabilityCache[cacheKey] = editionCapabilityCacheEntry{
				tokenFingerprint: fingerprint,
				status:           state,
				expiresAt:        time.Now().Add(duration),
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
	if entry, ok := s.profileHardcoverRateLimiters[key]; ok {
		return entry.limiter
	}
	clientConfig := s.hardcoverClientConfig()
	limiter := util.NewRateLimiter(clientConfig.RateLimit, clientConfig.MaxConcurrent, s.logger)
	s.profileHardcoverRateLimiters[key] = profileHardcoverRateLimiterEntry{
		tokenFingerprint: fingerprint,
		limiter:          limiter,
	}
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
