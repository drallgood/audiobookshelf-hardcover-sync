package multiuser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
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

// ErrEditionActionRequiresResolution prevents a second catalogue write while
// the prior run/item request is unresolved or terminal.
var ErrEditionActionRequiresResolution = errors.New("the previous edition request must be resolved before another create")

// SaveEditionAction persists one run/item action in the profile journal. It
// must be called while the profile operation gate is held when used by a
// mutating handler.
func (s *MultiUserService) SaveEditionAction(profileID, runID, absItemID string, action sync.EditionActionRecord) error {
	if s.repository == nil {
		return errors.New("edition action persistence requires a database repository")
	}
	payload, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("failed to encode edition action: %w", err)
	}
	return s.repository.SaveEditionAction(profileID, runID, absItemID, payload)
}

// GetEditionAction returns one exact profile/run/item journal entry.
func (s *MultiUserService) GetEditionAction(profileID, runID, absItemID string) (*sync.EditionActionRecord, bool, error) {
	if s.repository == nil {
		return nil, false, errors.New("edition action persistence requires a database repository")
	}
	payload, found, err := s.repository.GetEditionAction(profileID, runID, absItemID)
	if err != nil || !found {
		return nil, found, err
	}
	var action sync.EditionActionRecord
	if err := json.Unmarshal(payload, &action); err != nil {
		return nil, false, fmt.Errorf("failed to decode edition action: %w", err)
	}
	return &action, true, nil
}

// ListEditionActionsForRun returns the profile-scoped journal payloads for one
// run. Decrypted data is held only in memory for response projection.
func (s *MultiUserService) ListEditionActionsForRun(profileID, runID string) (map[string]sync.EditionActionRecord, error) {
	if s.repository == nil {
		return nil, errors.New("edition action persistence requires a database repository")
	}
	payloads, err := s.repository.ListEditionActionsForRun(profileID, runID)
	if err != nil {
		return nil, err
	}
	actions := make(map[string]sync.EditionActionRecord, len(payloads))
	for itemID, payload := range payloads {
		var action sync.EditionActionRecord
		if err := json.Unmarshal(payload, &action); err != nil {
			return nil, fmt.Errorf("failed to decode edition action for item %s: %w", itemID, err)
		}
		actions[itemID] = action
	}
	return actions, nil
}

// ClearEditionActionsForProfileItem removes all run-scoped attempts for an
// item after association success or an explicit forget operation.
func (s *MultiUserService) ClearEditionActionsForProfileItem(profileID, absItemID string) error {
	if s.repository == nil {
		return errors.New("edition action persistence requires a database repository")
	}
	return s.repository.DeleteEditionActionsForProfileItem(profileID, absItemID)
}

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
		if allowMatchingExisting {
			if clearErr := s.ClearEditionActionsForProfileItem(profileID, absItemID); clearErr != nil && s.logger != nil {
				s.logger.Warn("Verified edition recovery succeeded but its journal could not be cleared", map[string]interface{}{
					"profile_id": profileID, "abs_item_id": absItemID, "error": clearErr.Error(),
				})
			}
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
	if clearErr := s.ClearEditionActionsForProfileItem(profileID, absItemID); clearErr != nil && s.logger != nil {
		s.logger.Warn("Edition association was saved but its action journal could not be cleared", map[string]interface{}{
			"profile_id": profileID, "abs_item_id": absItemID, "error": clearErr.Error(),
		})
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
	// Provenance and the Audnexus confirmation time record how the local match
	// was first established. Recovery verifies the same stable identity again,
	// but it must neither conflict on a newly observed timestamp nor replace the
	// original saved metadata. Ownership verification is cached state about the
	// association, not part of its identity, so a fresh recovery result may omit it.
	existing.Provenance = ""
	verified.Provenance = ""
	existing.AudnexusConfirmedAt = time.Time{}
	verified.AudnexusConfirmedAt = time.Time{}
	existing.OwnershipVerifiedAt = 0
	verified.OwnershipVerifiedAt = 0
	existing.OwnershipTokenFingerprint = ""
	verified.OwnershipTokenFingerprint = ""
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

// AnnotateEditionAdditions overlays current saved additions and durable
// user-confirmed edition actions on a detached run-details snapshot. Historical
// outcomes and counts remain unchanged.
func (s *MultiUserService) AnnotateEditionAdditions(profileID string, snapshot *sync.SyncSnapshot) error {
	if snapshot == nil || snapshot.DryRun || (snapshot.State != "completed" && snapshot.State != "canceled") {
		return nil
	}
	actions, err := s.ListEditionActionsForRun(profileID, snapshot.RunID)
	if err != nil {
		return fmt.Errorf("load persisted edition actions: %w", err)
	}
	needsReview := false
	for i := range snapshot.BookOutcomes {
		record := &snapshot.BookOutcomes[i]
		if action, exists := actions[record.BookID]; exists && record.Outcome == sync.OutcomeNeedsReview {
			copyOf := action
			record.EditionAction = &copyOf
		}
		if record.Outcome == sync.OutcomeNeedsReview {
			needsReview = true
		}
	}
	if !needsReview {
		return nil
	}
	config, err := s.repository.GetProfileSyncConfig(profileID)
	if err != nil {
		return err
	}
	if err := s.validatePersistedProfileStateFile(profileID, config.StateFile); err != nil {
		return err
	}
	_, path, _, err := s.profileStateSourcePath(profileID, config.StateFile)
	if err != nil {
		return err
	}
	state, err := statepkg.LoadState(path)
	if err != nil {
		return fmt.Errorf("load saved edition additions: %w", err)
	}
	for i := range snapshot.BookOutcomes {
		record := &snapshot.BookOutcomes[i]
		association, exists := state.GetAssociation(record.BookID)
		associationMatchesSource := record.Outcome == sync.OutcomeNeedsReview && exists &&
			association.HardcoverBookID == record.HardcoverBookID &&
			strings.EqualFold(strings.TrimSpace(association.ReadingFormat), strings.TrimSpace(record.Format)) &&
			strings.EqualFold(strings.TrimSpace(association.SourceASIN), strings.TrimSpace(record.ASIN)) &&
			(record.ISBN == "" || isbn.Normalize(record.ISBN) == isbn.Normalize(association.SourceISBN10) || isbn.Normalize(record.ISBN) == isbn.Normalize(association.SourceISBN13))
		apiAssociation := associationMatchesSource && strings.HasPrefix(association.Provenance, "api_")
		submittedAction := record.EditionAction != nil && record.EditionAction.SubmittedBody != nil && record.EditionAction.Outcome != "not_submitted"
		regionalIdentifierMatches := true
		if submittedAction && record.EditionAction.Data != nil && record.EditionAction.Data.AudibleIdentifier != "" {
			regionalIdentifierMatches = strings.EqualFold(strings.TrimSpace(association.RegionalExternalID), strings.TrimSpace(record.EditionAction.Data.AudibleIdentifier))
		}
		unanchoredAudibleAssociation := exists && unanchoredAudibleAssociationMatchesRecord(*record, association)
		associationConfirmsAction := (associationMatchesSource || unanchoredAudibleAssociation) && submittedAction && regionalIdentifierMatches
		record.EditionAdded = apiAssociation || associationConfirmsAction || (unanchoredAudibleAssociation && !submittedAction)
		if record.EditionAdded {
			// The saved association is authoritative. It suppresses any stale
			// journal marker left behind by an interrupted cleanup or a later
			// ordinary sync that independently confirmed the same target.
			record.EditionAction = nil
		}
	}
	return nil
}

func unanchoredAudibleAssociationMatchesRecord(record sync.BookOutcomeRecord, association statepkg.Association) bool {
	if record.Outcome != sync.OutcomeNeedsReview || record.Reason != mismatch.ReasonAudibleImportAvailable ||
		strings.TrimSpace(record.HardcoverBookID) != "" || strings.TrimSpace(record.EditionID) != "" || association.ABSItemID != record.BookID ||
		!strings.EqualFold(strings.TrimSpace(record.Format), string(models.ReadingFormatAudiobook)) ||
		!strings.EqualFold(strings.TrimSpace(association.ReadingFormat), string(models.ReadingFormatAudiobook)) ||
		association.Provenance != "audible_import_unanchored" || association.AudnexusConfirmedAt.IsZero() {
		return false
	}
	bookID, bookErr := strconv.Atoi(strings.TrimSpace(association.HardcoverBookID))
	editionID, editionErr := strconv.Atoi(strings.TrimSpace(association.HardcoverEditionID))
	if bookErr != nil || editionErr != nil || bookID <= 0 || editionID <= 0 {
		return false
	}

	runASIN := record.SourceASIN
	if strings.TrimSpace(runASIN) == "" {
		runASIN = record.ASIN
	}
	runASIN, runASINValid := audnex.CanonicalASIN(runASIN)
	associationASIN, associationASINValid := audnex.CanonicalASIN(association.SourceASIN)
	if !runASINValid || !associationASINValid || runASIN != associationASIN {
		return false
	}
	runISBN10, runISBN13 := record.SourceISBN10, record.SourceISBN13
	if strings.TrimSpace(runISBN10) == "" && strings.TrimSpace(runISBN13) == "" {
		_, runISBN10, runISBN13 = statepkg.SourceIdentifiers(record.ASIN, record.ISBN)
	}
	if isbn.Normalize(runISBN10) != isbn.Normalize(association.SourceISBN10) ||
		isbn.Normalize(runISBN13) != isbn.Normalize(association.SourceISBN13) {
		return false
	}

	parts := strings.Split(strings.TrimSpace(association.RegionalExternalID), ":")
	if len(parts) != 2 {
		return false
	}
	regionalASIN, regionalASINValid := audnex.CanonicalASIN(parts[0])
	region := strings.ToLower(strings.TrimSpace(parts[1]))
	if !regionalASINValid || !audnexregion.IsRegion(region) ||
		!strings.EqualFold(strings.TrimSpace(association.AudnexusConfirmedRegion), region) ||
		!strings.EqualFold(strings.TrimSpace(association.Correction), regionalASIN+":"+region) {
		return false
	}
	return true
}
