package multiuser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

// ErrEditionAssociationSaveAfterRemoteSuccess distinguishes a verified
// Hardcover result from a failure to persist the local association. Callers
// should verify the Hardcover result before retrying because a retry may
// create another edition.
var ErrEditionAssociationSaveAfterRemoteSuccess = errors.New("Hardcover edition created but local association could not be saved")

// ErrEditionCreateLocalFailure marks a local profile, repository, or state
// failure before any Hardcover mutation has been attempted.
var ErrEditionCreateLocalFailure = errors.New("local edition creation setup failed")

// ErrEditionCreateDryRun indicates that edition creation is disabled for a
// profile currently configured for dry run.
var ErrEditionCreateDryRun = errors.New("edition creation is disabled while the profile is in dry run")

// ErrEditionAssociationAlreadyExists prevents an old create request from
// replacing a confirmed mapping saved by a later sync or create operation.
var ErrEditionAssociationAlreadyExists = errors.New("Audiobookshelf item already has a confirmed Hardcover association")

// GetLaterUsableSyncRunOutcome returns the newest retained completed or canceled,
// non-dry-run outcome for an item from a run newer than afterRunID.
func (s *MultiUserService) GetLaterUsableSyncRunOutcome(profileID, afterRunID, absItemID string) (sync.BookOutcomeRecord, bool, error) {
	if s.repository == nil {
		return sync.BookOutcomeRecord{}, false, nil
	}
	reports, err := s.repository.ListTerminalSyncRunReports(profileID, 0)
	if err != nil {
		return sync.BookOutcomeRecord{}, false, err
	}
	var latestOutcome *sync.BookOutcomeRecord
	for i := range reports {
		report := &reports[i]
		if report.RunID == afterRunID {
			if latestOutcome != nil {
				return *latestOutcome, true, nil
			}
			return sync.BookOutcomeRecord{}, false, nil
		}
		if (report.Phase != database.SyncRunPhaseCompleted && report.Phase != database.SyncRunPhaseCanceled) || report.DryRun {
			continue
		}
		snapshot, err := snapshotFromRetainedReport(profileID, report)
		if err != nil {
			return sync.BookOutcomeRecord{}, false, err
		}
		if snapshot == nil {
			continue
		}
		for _, outcome := range snapshot.BookOutcomes {
			if outcome.BookID == absItemID && latestOutcome == nil {
				copyOf := outcome
				latestOutcome = &copyOf
				break
			}
		}
	}
	// A cached current run may not have a terminal report yet. In that case,
	// retained reports are older and cannot supersede the requested snapshot.
	return sync.BookOutcomeRecord{}, false, nil
}

// EditionCreateOperation performs a user-confirmed remote edition operation
// while the profile run gate and state-file lock are held. It must return a
// complete association only after Hardcover has returned a verified result.
type EditionCreateOperation func(profile *database.ProfileWithTokens) (statepkg.Association, error)

// EditionResyncOperation runs after a created edition's association has been
// saved, while the profile gate and state-file lock are still held. It receives
// the state loaded under that lock (already containing the association) and the
// locked state path. It reports its own result to the caller, so a resync
// failure never undoes or masks the edition that now exists.
type EditionResyncOperation func(profile *database.ProfileWithTokens, syncState *statepkg.State, statePath string)

// CreateEditionWithAssociation runs an edition create and persists its
// profile-local association as one guarded operation. The callback runs after
// confirming no sync is active and after acquiring the state-file lock.
func (s *MultiUserService) CreateEditionWithAssociation(ctx context.Context, profileID, absItemID string, operation EditionCreateOperation) error {
	return s.CreateEditionWithAssociationAndResync(ctx, profileID, absItemID, operation, nil)
}

// CreateEditionWithAssociationAndResync is CreateEditionWithAssociation plus an
// optional resync that runs under the same profile gate and state-file lock
// once the association is durably saved. Like the create itself, it is refused
// up front (ErrSyncAlreadyActive) while a full sync is active for the profile.
func (s *MultiUserService) CreateEditionWithAssociationAndResync(ctx context.Context, profileID, absItemID string, operation EditionCreateOperation, resync EditionResyncOperation) error {
	return s.createEditionWithAssociationAndResync(ctx, profileID, absItemID, operation, resync, false)
}

// RecoverEditionAssociation verifies a previously submitted remote operation
// and saves its association under the same profile and state-file guards used
// by edition creation. If the exact association is already saved, the
// verification succeeds idempotently. Ordinary create requests continue to
// reject existing associations.
func (s *MultiUserService) RecoverEditionAssociation(ctx context.Context, profileID, absItemID string, operation EditionCreateOperation) error {
	return s.createEditionWithAssociationAndResync(ctx, profileID, absItemID, operation, nil, true)
}

func (s *MultiUserService) createEditionWithAssociationAndResync(ctx context.Context, profileID, absItemID string, operation EditionCreateOperation, resync EditionResyncOperation, allowMatchingExisting bool) error {
	if profileID == "" || absItemID == "" {
		return errors.New("profile ID and ABS item ID are required")
	}
	if operation == nil {
		return errors.New("edition create operation is required")
	}
	if ctx == nil {
		return errors.New("edition create context is required")
	}

	// Track this profile operation with sync starts so shutdown and profile
	// deletion cannot race an accepted remote mutation.
	s.admissionMutex.Lock()
	if s.shuttingDown {
		s.admissionMutex.Unlock()
		return ErrServiceShuttingDown
	}
	if _, deleting := s.deletingProfiles[profileID]; deleting {
		s.admissionMutex.Unlock()
		return ErrProfileDeleting
	}
	if _, deleted := s.deletedProfiles[profileID]; deleted {
		s.admissionMutex.Unlock()
		return ErrProfileNotFound
	}
	gate := s.profileGate(profileID)
	s.startWaitGroup.Add(1)
	gate.startWaitGroup.Add(1)
	s.admissionMutex.Unlock()
	defer s.endSyncStart(gate)

	if err := lockProfileGateContext(ctx, gate); err != nil {
		return err
	}
	defer gate.mu.Unlock()
	if gate.deleted {
		return ErrProfileNotFound
	}

	s.syncMutex.RLock()
	_, active := s.activeSyncs[profileID]
	s.syncMutex.RUnlock()
	if active {
		return fmt.Errorf("%w for profile %s", ErrSyncAlreadyActive, profileID)
	}

	profile, err := s.GetProfile(profileID)
	if err != nil {
		return fmt.Errorf("failed to load profile %s for edition creation: %w: %w", profileID, ErrEditionCreateLocalFailure, err)
	}
	if profile == nil {
		return fmt.Errorf("%w: %s", ErrProfileNotFound, profileID)
	}
	if profile.SyncConfig.DryRun {
		return ErrEditionCreateDryRun
	}
	if err := s.validatePersistedProfileStateFile(profileID, profile.SyncConfig.StateFile); err != nil {
		return fmt.Errorf("invalid persisted state file for profile %s: %w: %w", profileID, ErrEditionCreateLocalFailure, err)
	}

	statePath := s.profileSpecificStatePath(profileID, profile.SyncConfig.StateFile)
	fileLock, err := statepkg.AcquireFileLock(statePath)
	if err != nil {
		if errors.Is(err, statepkg.ErrStateFileLocked) {
			return fmt.Errorf("%w for profile %s: %w", ErrProfileStateBusy, profileID, err)
		}
		return fmt.Errorf("failed to lock state file for profile %s: %w: %w", profileID, ErrEditionCreateLocalFailure, err)
	}
	defer func() { _ = fileLock.Close() }()

	_, loadPath, isLegacy, err := s.profileStateSourcePath(profileID, profile.SyncConfig.StateFile)
	if err != nil {
		return fmt.Errorf("failed to locate state file for profile %s: %w: %w", profileID, ErrEditionCreateLocalFailure, err)
	}
	if !isLegacy {
		loadPath = fileLock.StatePath()
	}
	state, err := statepkg.LoadState(loadPath)
	if err != nil {
		return fmt.Errorf("failed to load state file for profile %s: %w: %w", profileID, ErrEditionCreateLocalFailure, err)
	}
	existing, exists := state.GetAssociation(absItemID)
	if exists && !allowMatchingExisting {
		return fmt.Errorf("%w: %s", ErrEditionAssociationAlreadyExists, absItemID)
	}

	association, err := operation(profile)
	if err != nil {
		return err
	}
	if association.ABSItemID != absItemID {
		return fmt.Errorf("%w: edition create operation returned an association for a different ABS item", ErrEditionAssociationSaveAfterRemoteSuccess)
	}
	if exists {
		if !sameRecoveredEditionAssociation(existing, association) {
			return fmt.Errorf("%w: a different confirmed association is already saved for %s", ErrEditionAssociationAlreadyExists, absItemID)
		}
		return nil
	}
	if err := state.SetAssociation(association); err != nil {
		return fmt.Errorf("%w: invalid confirmed edition association: %w", ErrEditionAssociationSaveAfterRemoteSuccess, err)
	}
	state.InvalidateItemCheckpoints(absItemID)
	if err := state.Save(fileLock.StatePath()); err != nil {
		return fmt.Errorf("%w: %w", ErrEditionAssociationSaveAfterRemoteSuccess, err)
	}
	if isLegacy {
		s.backupMigratedLegacyProfileState(profileID, loadPath)
	}
	if resync != nil {
		resync(profile, state, fileLock.StatePath())
	}
	return nil
}

func sameRecoveredEditionAssociation(existing, verified statepkg.Association) bool {
	// Provenance records how the local match was first established. Recovery
	// verifies the same identifiers again but must preserve the original value.
	existing.Provenance = ""
	verified.Provenance = ""
	return existing == verified
}

// lockProfileGateContext waits for the shared profile gate while allowing an
// edition-create request to abandon its queued position when its context ends.
func lockProfileGateContext(ctx context.Context, gate *profileRunGate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if gate.mu.TryLock() {
			if err := ctx.Err(); err != nil {
				gate.mu.Unlock()
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ResyncBook synchronizes one Audiobookshelf item for a profile through the
// regular per-book sync path. The caller must hold the profile gate and the
// state-file lock for statePath, which CreateEditionWithAssociationAndResync
// guarantees while it runs a resync operation.
func (s *MultiUserService) ResyncBook(ctx context.Context, profile *database.ProfileWithTokens, book models.AudiobookshelfBook, syncState *statepkg.State, statePath string) (sync.BookResyncResult, error) {
	return s.resyncBook(ctx, profile, book, nil, syncState, statePath)
}

// ResyncBookWithEdition reuses the fresh edition snapshot returned by the
// operation that created or confirmed the association. SyncBookWithEdition
// validates the snapshot against the locked state before it skips an edition
// lookup.
func (s *MultiUserService) ResyncBookWithEdition(ctx context.Context, profile *database.ProfileWithTokens, book models.AudiobookshelfBook, verifiedEdition *models.Edition, syncState *statepkg.State, statePath string) (sync.BookResyncResult, error) {
	return s.resyncBook(ctx, profile, book, verifiedEdition, syncState, statePath)
}

func (s *MultiUserService) resyncBook(ctx context.Context, profile *database.ProfileWithTokens, book models.AudiobookshelfBook, verifiedEdition *models.Edition, syncState *statepkg.State, statePath string) (sync.BookResyncResult, error) {
	absClient, err := audiobookshelf.NewClientWithNetworkTrust(profile.AudiobookshelfURL, profile.AudiobookshelfToken, s.AudiobookshelfNetworkTrust())
	if err != nil {
		return sync.BookResyncResult{}, fmt.Errorf("invalid Audiobookshelf client configuration: %w", err)
	}
	service, err := sync.NewServiceWithRunIdentity(absClient, s.NewHardcoverClientForProfile(profile.Profile.ID, profile.HardcoverToken), s.createProfileSpecificConfig(profile), "", time.Time{})
	if err != nil {
		return sync.BookResyncResult{}, fmt.Errorf("failed to create sync service: %w", err)
	}
	if verifiedEdition == nil {
		return service.SyncBook(ctx, book, syncState, statePath)
	}
	return service.SyncBookWithEdition(ctx, book, verifiedEdition, syncState, statePath)
}

// NewHardcoverClient constructs a standalone Hardcover client with deployment-
// wide endpoint and pacing settings. Use NewHardcoverClientForProfile when a
// profile-scoped shared limiter is required.
func (s *MultiUserService) NewHardcoverClient(token string) *hardcover.Client {
	return hardcover.NewClientWithConfig(s.hardcoverClientConfig(), token, s.logger)
}

// NewHardcoverClientForProfile constructs a token-specific client that shares
// the profile's request limiter with sync, capability probes, and other create
// clients. A token change receives a fresh limiter.
func (s *MultiUserService) NewHardcoverClientForProfile(profileID, token string) *hardcover.Client {
	clientConfig := s.hardcoverClientConfig()
	fingerprint := hardcoverTokenFingerprint(token)
	s.hardcoverClientMutex.Lock()
	clientConfig.RateLimiter = s.profileHardcoverRateLimiterLocked(profileID, fingerprint)
	s.hardcoverClientMutex.Unlock()
	return hardcover.NewClientWithConfig(clientConfig, token, s.logger)
}

func (s *MultiUserService) hardcoverClientConfig() *hardcover.ClientConfig {
	clientConfig := hardcover.DefaultClientConfig()
	if s.globalConfig != nil {
		if s.globalConfig.Hardcover.BaseURL != "" {
			clientConfig.BaseURL = s.globalConfig.Hardcover.BaseURL
		}
		if s.globalConfig.RateLimit.Rate > 0 {
			clientConfig.RateLimit = s.globalConfig.RateLimit.Rate
		}
		if s.globalConfig.RateLimit.MaxConcurrent > 0 {
			clientConfig.MaxConcurrent = s.globalConfig.RateLimit.MaxConcurrent
		}
	}
	return clientConfig
}
