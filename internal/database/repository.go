package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Repository provides database operations for users and configurations
type Repository struct {
	db                *Database
	encryptor         *crypto.EncryptionManager
	logger            *logger.Logger
	maxSyncRunReports int
}

// NewRepository creates a new repository instance
func NewRepository(db *Database, encryptor *crypto.EncryptionManager, log *logger.Logger) *Repository {
	return &Repository{
		db:                db,
		encryptor:         encryptor,
		logger:            log,
		maxSyncRunReports: maxSyncRunReports,
	}
}

// ProfileWithTokens represents a sync profile with decrypted tokens
type ProfileWithTokens struct {
	Profile             SyncProfile    `json:"profile"`
	AudiobookshelfURL   string         `json:"audiobookshelf_url"`
	AudiobookshelfToken string         `json:"audiobookshelf_token"`
	HardcoverToken      string         `json:"hardcover_token"`
	SyncConfig          SyncConfigData `json:"sync_config"`
}

const maxSyncRunReports = 10

// ErrStaleSyncRunReport indicates that a terminal report belongs to a run
// that is no longer the profile's current accepted attempt.
var ErrStaleSyncRunReport = errors.New("stale sync run report")

const interruptedSyncRunError = "sync run interrupted by application restart"

// SetSyncRunReportRetention configures the number of terminal reports retained
// per profile. Non-positive values preserve the historical default.
func (r *Repository) SetSyncRunReportRetention(limit int) {
	if limit <= 0 {
		limit = maxSyncRunReports
	}
	r.maxSyncRunReports = limit
}

// AcceptSyncRun atomically assigns a generation, advances attempt metadata,
// and writes a complete queued report. Callers must prepare the sanitized
// queued snapshot before invoking it so no durable placeholder is observable.
func (r *Repository) AcceptSyncRun(report *SyncRunReport) (*SyncRunReport, error) {
	if report == nil {
		return nil, errors.New("sync run report is required")
	}
	if report.ProfileID == "" {
		return nil, errors.New("profile ID is required")
	}
	if report.RunID == "" {
		return nil, errors.New("run ID is required")
	}
	if report.Phase != SyncRunPhaseQueued {
		return nil, errors.New("accepted sync run report must be queued")
	}
	if report.QueuedAt == nil || report.QueuedAt.IsZero() {
		now := time.Now().UTC()
		report.QueuedAt = &now
	}
	if report.SnapshotJSON == "" {
		return nil, errors.New("accepted sync run report snapshot is required")
	}
	queuedAt := report.QueuedAt.UTC()

	err := r.db.GetDB().Transaction(func(tx *gorm.DB) error {
		state, err := loadOrCreateSyncStateForUpdate(tx, report.ProfileID)
		if err != nil {
			return err
		}

		// The state row is authoritative for normal operation. Taking the
		// maximum from existing reports also prevents a manually restored or
		// partially migrated database from reusing a generation.
		var highestGeneration uint64
		if err := tx.Model(&SyncRunReport{}).
			Where("profile_id = ?", report.ProfileID).
			Select("COALESCE(MAX(generation), 0)").
			Scan(&highestGeneration).Error; err != nil {
			return fmt.Errorf("failed to inspect sync run generations: %w", err)
		}
		generation := state.LastAttemptedGeneration + 1
		if highestGeneration >= generation {
			generation = highestGeneration + 1
		}

		state.LastAttemptedAt = &queuedAt
		state.LastAttemptedRunID = report.RunID
		state.LastAttemptedGeneration = generation
		if err := tx.Save(state).Error; err != nil {
			return fmt.Errorf("failed to reserve sync run generation: %w", err)
		}

		report.Generation = generation
		report.QueuedAt = &queuedAt
		if err := tx.Create(report).Error; err != nil {
			return fmt.Errorf("failed to create queued sync run report: %w", err)
		}
		if err := deleteStaleQueuedSyncRunReports(tx, report.ProfileID, report.RunID); err != nil {
			return err
		}
		if err := r.retainNewestSyncRunReports(tx, report.ProfileID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

func deleteStaleQueuedSyncRunReports(tx *gorm.DB, profileID, currentRunID string) error {
	if err := tx.Where("profile_id = ? AND phase = ? AND run_id <> ?", profileID, SyncRunPhaseQueued, currentRunID).
		Delete(&SyncRunReport{}).Error; err != nil {
		return fmt.Errorf("failed to remove stale queued sync run reports: %w", err)
	}
	return nil
}

// UpsertSyncRunReportContext transactionally stores a terminal run report,
// retains only the newest configured reports for the profile, advances success
// metadata only for a newer completed non-dry run, and propagates cancellation
// to the database transaction.
func (r *Repository) UpsertSyncRunReportContext(ctx context.Context, report *SyncRunReport) error {
	if report == nil {
		return errors.New("sync run report is required")
	}
	if report.ProfileID == "" {
		return errors.New("profile ID is required")
	}
	if report.RunID == "" {
		return errors.New("run ID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing SyncRunReport
		findErr := tx.Where("profile_id = ? AND run_id = ?", report.ProfileID, report.RunID).First(&existing).Error
		effectiveGeneration := report.Generation
		effectivePhase := report.Phase
		if findErr == nil {
			if effectiveGeneration == 0 {
				effectiveGeneration = existing.Generation
			}
			if effectivePhase == "" {
				effectivePhase = existing.Phase
			}
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("failed to find sync run report: %w", findErr)
		}
		if isTerminalSyncRunPhase(effectivePhase) && effectiveGeneration > 0 {
			var state ProfileSyncState
			stateErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("profile_id = ?", report.ProfileID).First(&state).Error
			if stateErr == nil && (state.LastAttemptedRunID != report.RunID || state.LastAttemptedGeneration != effectiveGeneration) {
				return fmt.Errorf("%w: profile %s run %s generation %d is not current", ErrStaleSyncRunReport, report.ProfileID, report.RunID, effectiveGeneration)
			}
			if stateErr != nil && !errors.Is(stateErr, gorm.ErrRecordNotFound) {
				return fmt.Errorf("failed to inspect current sync run: %w", stateErr)
			}
		}
		alreadyCompleted := false
		switch {
		case findErr == nil:
			alreadyCompleted = existing.Phase == SyncRunPhaseCompleted && !existing.DryRun
			mergeSyncRunReportDefaults(report, &existing)
			if err := tx.Save(report).Error; err != nil {
				return fmt.Errorf("failed to update sync run report: %w", err)
			}
		case errors.Is(findErr, gorm.ErrRecordNotFound):
			if report.SnapshotJSON == "" {
				report.SnapshotJSON = "{}"
			}
			if err := tx.Create(report).Error; err != nil {
				return fmt.Errorf("failed to create sync run report: %w", err)
			}
		default:
			return fmt.Errorf("failed to find sync run report: %w", findErr)
		}

		if err := r.retainNewestSyncRunReports(tx, report.ProfileID); err != nil {
			return err
		}

		if report.Phase != SyncRunPhaseCompleted || report.DryRun || report.Generation == 0 || alreadyCompleted {
			return nil
		}
		state, err := loadOrCreateSyncStateForUpdate(tx, report.ProfileID)
		if err != nil {
			return err
		}
		// A completion can advance durable success only for the run that is
		// still the newest accepted attempt. Reports from a canceled or
		// replaced worker may be retained for truthful history, but they must
		// never make a stale run look successful after a newer reservation.
		if report.Generation != state.LastAttemptedGeneration ||
			report.RunID != state.LastAttemptedRunID {
			return nil
		}
		finishedAt := report.FinishedAt
		if finishedAt == nil {
			now := time.Now().UTC()
			finishedAt = &now
		}
		state.LastSuccessfulAt = finishedAt
		if err := tx.Save(state).Error; err != nil {
			return fmt.Errorf("failed to advance successful sync metadata: %w", err)
		}
		return nil
	})
}

// mergeSyncRunReportDefaults preserves lifecycle data recorded by an initial
// queued report when a terminal update only supplies the fields it changed.
// It also keeps dry-run provenance immutable for a run.
func mergeSyncRunReportDefaults(report, existing *SyncRunReport) {
	if report.Generation == 0 {
		report.Generation = existing.Generation
	}
	if report.Phase == "" {
		report.Phase = existing.Phase
	}
	if !report.DryRun && existing.DryRun {
		report.DryRun = true
	}
	if report.QueuedAt == nil {
		report.QueuedAt = existing.QueuedAt
	}
	if report.ProcessingStartedAt == nil {
		report.ProcessingStartedAt = existing.ProcessingStartedAt
	}
	if report.LastActivityAt == nil {
		report.LastActivityAt = existing.LastActivityAt
	}
	if report.LastProcessedAt == nil {
		report.LastProcessedAt = existing.LastProcessedAt
	}
	if report.FinishedAt == nil {
		report.FinishedAt = existing.FinishedAt
	}
	if report.RunError == "" {
		report.RunError = existing.RunError
	}
	if report.SnapshotJSON == "" {
		report.SnapshotJSON = existing.SnapshotJSON
	}
}

// GetSyncRunReport fetches one report in its profile scope.
func (r *Repository) GetSyncRunReport(profileID, runID string) (*SyncRunReport, error) {
	var report SyncRunReport
	if err := r.db.GetDB().Where("profile_id = ? AND run_id = ?", profileID, runID).First(&report).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get sync run report: %w", err)
	}
	return &report, nil
}

// ListTerminalSyncRunReports returns newest terminal reports for a profile,
// bounded to the retention window. Filtering before the limit keeps abandoned
// queued reports from hiding the newest retained terminal report at restart.
func (r *Repository) ListTerminalSyncRunReports(profileID string, limit int) ([]SyncRunReport, error) {
	retention := r.syncRunReportRetention()
	if limit <= 0 || limit > retention {
		limit = retention
	}
	var reports []SyncRunReport
	if err := r.db.GetDB().Where("profile_id = ? AND phase IN ?", profileID, []string{
		SyncRunPhaseCompleted, SyncRunPhaseCanceled, SyncRunPhaseFailed,
	}).Order("generation DESC").Order("run_id DESC").Limit(limit).Find(&reports).Error; err != nil {
		return nil, fmt.Errorf("failed to list terminal sync run reports: %w", err)
	}
	return reports, nil
}

func loadOrCreateSyncStateForUpdate(tx *gorm.DB, profileID string) (*ProfileSyncState, error) {
	var state ProfileSyncState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("profile_id = ?", profileID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state = ProfileSyncState{ProfileID: profileID}
		if err := tx.Create(&state).Error; err != nil {
			return nil, fmt.Errorf("failed to create sync state: %w", err)
		}
		return &state, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load sync state: %w", err)
	}
	return &state, nil
}

func (r *Repository) retainNewestSyncRunReports(tx *gorm.DB, profileID string) error {
	var reports []SyncRunReport
	if err := tx.Where("profile_id = ? AND phase IN ?", profileID, []string{
		SyncRunPhaseCompleted, SyncRunPhaseCanceled, SyncRunPhaseFailed,
	}).
		Order("generation DESC").Order("run_id DESC").Find(&reports).Error; err != nil {
		return fmt.Errorf("failed to list sync run reports for retention: %w", err)
	}
	retention := r.syncRunReportRetention()
	if len(reports) <= retention {
		return nil
	}
	keepRunIDs := make([]string, retention)
	for i := range keepRunIDs {
		keepRunIDs[i] = reports[i].RunID
	}
	if err := tx.Where("profile_id = ? AND phase IN ? AND run_id NOT IN ?", profileID, []string{
		SyncRunPhaseCompleted, SyncRunPhaseCanceled, SyncRunPhaseFailed,
	}, keepRunIDs).Delete(&SyncRunReport{}).Error; err != nil {
		return fmt.Errorf("failed to retain newest sync run reports: %w", err)
	}
	return nil
}

func (r *Repository) syncRunReportRetention() int {
	if r.maxSyncRunReports <= 0 {
		return maxSyncRunReports
	}
	return r.maxSyncRunReports
}

func isTerminalSyncRunPhase(phase string) bool {
	switch phase {
	case SyncRunPhaseCompleted, SyncRunPhaseCanceled, SyncRunPhaseFailed:
		return true
	default:
		return false
	}
}

// ReconcileInterruptedSyncRunReports converts durable non-terminal reports to
// failed before workers are admitted after a process restart. Unknown legacy
// phases are treated as interrupted as well; report snapshots and history are
// preserved while the terminal error explains why completion was unavailable.
func (r *Repository) ReconcileInterruptedSyncRunReports() error {
	return r.db.GetDB().Transaction(func(tx *gorm.DB) error {
		var reports []SyncRunReport
		if err := tx.Where("phase NOT IN ? OR phase IS NULL", []string{
			SyncRunPhaseCompleted, SyncRunPhaseCanceled, SyncRunPhaseFailed,
		}).Find(&reports).Error; err != nil {
			return fmt.Errorf("failed to find interrupted sync run reports: %w", err)
		}
		for i := range reports {
			now := time.Now().UTC()
			reports[i].Phase = SyncRunPhaseFailed
			reports[i].RunError = interruptedSyncRunError
			reports[i].FinishedAt = &now
			if err := tx.Save(&reports[i]).Error; err != nil {
				return fmt.Errorf("failed to reconcile interrupted sync run report %s: %w", reports[i].RunID, err)
			}
		}
		profiles := make(map[string]struct{}, len(reports))
		for _, report := range reports {
			profiles[report.ProfileID] = struct{}{}
		}
		var terminalProfileIDs []string
		if err := tx.Model(&SyncRunReport{}).
			Where("phase IN ?", []string{SyncRunPhaseCompleted, SyncRunPhaseCanceled, SyncRunPhaseFailed}).
			Distinct("profile_id").Pluck("profile_id", &terminalProfileIDs).Error; err != nil {
			return fmt.Errorf("failed to find profiles with terminal sync run reports: %w", err)
		}
		for _, profileID := range terminalProfileIDs {
			profiles[profileID] = struct{}{}
		}
		for profileID := range profiles {
			if err := r.retainNewestSyncRunReports(tx, profileID); err != nil {
				return err
			}
		}
		return nil
	})
}

// CreateProfile creates a new sync profile with encrypted configuration
func (r *Repository) CreateProfile(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig SyncConfigData) error {
	return r.CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig, "")
}

// CreateProfileForUser creates a sync profile owned by the given user. An
// empty owner preserves the ownerless legacy profile behavior.
func (r *Repository) CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig SyncConfigData, ownerUserID string) error {
	// Encrypt tokens
	encryptedABSToken, err := r.encryptor.Encrypt(audiobookshelfToken)
	if err != nil {
		r.logger.Error("Failed to encrypt Audiobookshelf token", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		return fmt.Errorf("failed to encrypt Audiobookshelf token: %w", err)
	}

	encryptedHCToken, err := r.encryptor.Encrypt(hardcoverToken)
	if err != nil {
		r.logger.Error("Failed to encrypt Hardcover token", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		return fmt.Errorf("failed to encrypt Hardcover token: %w", err)
	}

	// Serialize sync config
	syncConfigJSON, err := json.Marshal(syncConfig)
	if err != nil {
		r.logger.Error("Failed to marshal sync config", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		return fmt.Errorf("failed to marshal sync config: %w", err)
	}

	// Create profile and config in a transaction
	return r.db.GetDB().Transaction(func(tx *gorm.DB) error {
		// Create profile
		profile := SyncProfile{
			ID:     profileID,
			Name:   name,
			Active: true,
		}
		if ownerUserID != "" {
			profile.OwnerUserID = &ownerUserID
		}
		if err := tx.Create(&profile).Error; err != nil {
			return fmt.Errorf("failed to create sync profile: %w", err)
		}

		// Create profile config
		config := SyncProfileConfig{
			ProfileID:                    profileID,
			AudiobookshelfURL:            audiobookshelfURL,
			AudiobookshelfTokenEncrypted: encryptedABSToken,
			HardcoverTokenEncrypted:      encryptedHCToken,
			SyncConfig:                   string(syncConfigJSON),
		}
		if err := tx.Create(&config).Error; err != nil {
			return fmt.Errorf("failed to create sync profile config: %w", err)
		}

		// Create empty sync state
		syncState := ProfileSyncState{ProfileID: profileID}
		if err := tx.Create(&syncState).Error; err != nil {
			return fmt.Errorf("failed to create sync state: %w", err)
		}

		r.logger.Info("Created new user", map[string]interface{}{
			"user_id": profileID,
			"name":    name,
		})

		return nil
	})
}

// GetProfile retrieves a sync profile by ID with decrypted tokens
func (r *Repository) GetProfile(profileID string) (*ProfileWithTokens, error) {
	// Get profile with config
	var profile SyncProfile
	if err := r.db.GetDB().Preload("Config").Preload("SyncState").First(&profile, "id = ? AND active = ?", profileID, true).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get sync profile: %w", err)
	}

	if profile.Config == nil {
		return nil, fmt.Errorf("sync profile config not found")
	}

	// Decrypt tokens
	audiobookshelfToken, err := r.encryptor.Decrypt(profile.Config.AudiobookshelfTokenEncrypted)
	if err != nil {
		fields := map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		}
		if isLikelyEncryptionKeyMismatch(err) {
			fields["hint"] = "encryption key mismatch suspected; ensure ENCRYPTION_KEY, DATA_DIR, paths.data_dir and volume mounts are consistent with when tokens were created"
		}

		r.logger.Error("Failed to decrypt Audiobookshelf token", fields)
		return nil, fmt.Errorf("failed to decrypt Audiobookshelf token: %w", err)
	}

	hardcoverToken, err := r.encryptor.Decrypt(profile.Config.HardcoverTokenEncrypted)
	if err != nil {
		fields := map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		}
		if isLikelyEncryptionKeyMismatch(err) {
			fields["hint"] = "encryption key mismatch suspected; ensure ENCRYPTION_KEY, DATA_DIR, paths.data_dir and volume mounts are consistent with when tokens were created"
		}

		r.logger.Error("Failed to decrypt Hardcover token", fields)
		return nil, fmt.Errorf("failed to decrypt Hardcover token: %w", err)
	}

	// Parse sync config
	var syncConfig SyncConfigData
	if profile.Config.SyncConfig != "" {
		if err := json.Unmarshal([]byte(profile.Config.SyncConfig), &syncConfig); err != nil {
			r.logger.Error("Failed to parse sync config", map[string]interface{}{
				"profile_id": profileID,
				"error":      err.Error(),
			})
			return nil, fmt.Errorf("failed to parse sync config: %w", err)
		}
	}

	return &ProfileWithTokens{
		Profile:             profile,
		AudiobookshelfURL:   profile.Config.AudiobookshelfURL,
		AudiobookshelfToken: audiobookshelfToken,
		HardcoverToken:      hardcoverToken,
		SyncConfig:          syncConfig,
	}, nil
}

// GetProfileMetadata retrieves an active profile without loading its config or
// decrypting its tokens. It is used for authorization decisions.
func (r *Repository) GetProfileMetadata(profileID string) (*SyncProfile, error) {
	var profile SyncProfile
	if err := r.db.GetDB().Where("id = ? AND active = ?", profileID, true).First(&profile).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get sync profile metadata: %w", err)
	}
	return &profile, nil
}

// ListProfiles retrieves all active sync profiles
func (r *Repository) ListProfiles() ([]SyncProfile, error) {
	return r.ListProfilesForUser("", false, false)
}

// ListProfilesForUser lists active profiles visible to an authenticated user.
// Ownerless profiles remain visible only to administrators when auth is on.
func (r *Repository) ListProfilesForUser(userID string, admin, authEnabled bool) ([]SyncProfile, error) {
	var profiles []SyncProfile
	query := r.db.GetDB().Preload("Config").Preload("SyncState").Where("active = ?", true)
	if authEnabled && !admin {
		query = query.Where("owner_user_id = ?", userID)
	}
	if err := query.Find(&profiles).Error; err != nil {
		return nil, fmt.Errorf("failed to list sync profiles: %w", err)
	}
	return profiles, nil
}

// UpdateProfile updates sync profile information
func (r *Repository) UpdateProfile(profileID, name string) error {
	result := r.db.GetDB().Model(&SyncProfile{}).
		Where("id = ? AND active = ?", profileID, true).
		Updates(map[string]interface{}{
			"name":       name,
			"updated_at": time.Now(),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to update sync profile: %w", result.Error)
	}

	return nil
}

// UpdateUserConfig updates user configuration with encrypted tokens
// If audiobookshelfToken or hardcoverToken are empty, the existing tokens will be preserved
func (r *Repository) UpdateUserConfig(profileID, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig SyncConfigData) error {
	// Get existing config to preserve tokens and sync config if not provided
	var existingConfig SyncProfileConfig
	if err := r.db.GetDB().Where("profile_id = ?", profileID).First(&existingConfig).Error; err != nil {
		return fmt.Errorf("failed to get existing user config: %w", err)
	}

	// Use existing tokens if new ones are empty
	var encryptedABSToken, encryptedHCToken string
	var err error

	if audiobookshelfToken != "" {
		encryptedABSToken, err = r.encryptor.Encrypt(audiobookshelfToken)
		if err != nil {
			return fmt.Errorf("failed to encrypt Audiobookshelf token: %w", err)
		}
	} else {
		encryptedABSToken = existingConfig.AudiobookshelfTokenEncrypted
	}

	if hardcoverToken != "" {
		encryptedHCToken, err = r.encryptor.Encrypt(hardcoverToken)
		if err != nil {
			return fmt.Errorf("failed to encrypt Hardcover token: %w", err)
		}
	} else {
		encryptedHCToken = existingConfig.HardcoverTokenEncrypted
	}

	// Merge with existing sync config to preserve values not being updated
	var existingSyncConfig SyncConfigData
	if existingConfig.SyncConfig != "" {
		if err := json.Unmarshal([]byte(existingConfig.SyncConfig), &existingSyncConfig); err != nil {
			return fmt.Errorf("failed to unmarshal existing sync config: %w", err)
		}
	}

	// Only update sync config if it's not empty (has at least one field set)
	// This prevents clearing all values when only updating tokens
	finalSyncConfig := existingSyncConfig
	if !syncConfig.IsEmpty() {
		// Merge the new config with existing, preserving unset values
		if syncConfig.Incremental || existingSyncConfig.Incremental {
			finalSyncConfig.Incremental = syncConfig.Incremental
		}
		if syncConfig.StateFile != "" {
			finalSyncConfig.StateFile = syncConfig.StateFile
		}
		if syncConfig.MinChangeThreshold != 0 {
			finalSyncConfig.MinChangeThreshold = syncConfig.MinChangeThreshold
		}
		if syncConfig.SyncInterval != "" {
			finalSyncConfig.SyncInterval = syncConfig.SyncInterval
		}
		if syncConfig.MinimumProgress != 0 {
			finalSyncConfig.MinimumProgress = syncConfig.MinimumProgress
		}
		if syncConfig.SyncWantToRead || existingSyncConfig.SyncWantToRead {
			finalSyncConfig.SyncWantToRead = syncConfig.SyncWantToRead
		}
		// For ProcessUnreadBooks, we need to explicitly check if it was provided
		// since false is a valid value that should be preserved
		finalSyncConfig.ProcessUnreadBooks = syncConfig.ProcessUnreadBooks
		if syncConfig.SyncOwned || existingSyncConfig.SyncOwned {
			finalSyncConfig.SyncOwned = syncConfig.SyncOwned
		}
		if syncConfig.IncludeEbooks || existingSyncConfig.IncludeEbooks {
			finalSyncConfig.IncludeEbooks = syncConfig.IncludeEbooks
		}
		if syncConfig.DryRun || existingSyncConfig.DryRun {
			finalSyncConfig.DryRun = syncConfig.DryRun
		}
		if syncConfig.TestBookFilter != "" {
			finalSyncConfig.TestBookFilter = syncConfig.TestBookFilter
		}
		if syncConfig.TestBookLimit != 0 {
			finalSyncConfig.TestBookLimit = syncConfig.TestBookLimit
		}
		if len(syncConfig.Libraries.Include) > 0 {
			finalSyncConfig.Libraries.Include = syncConfig.Libraries.Include
		}
		if len(syncConfig.Libraries.Exclude) > 0 {
			finalSyncConfig.Libraries.Exclude = syncConfig.Libraries.Exclude
		}
	}

	// Serialize sync config
	syncConfigJSON, err := json.Marshal(finalSyncConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal sync config: %w", err)
	}

	// Update config
	updates := map[string]interface{}{
		"AudiobookshelfURL":            audiobookshelfURL,
		"AudiobookshelfTokenEncrypted": encryptedABSToken,
		"HardcoverTokenEncrypted":      encryptedHCToken,
		"SyncConfig":                   string(syncConfigJSON),
		"UpdatedAt":                    time.Now(),
	}

	// Only include fields that have values to avoid overwriting with zero values
	result := r.db.GetDB().Model(&SyncProfileConfig{}).Where("profile_id = ?", profileID).Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to update user config: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user config not found: %s", profileID)
	}

	r.logger.Info("Updated user config", map[string]interface{}{
		"profile_id": profileID,
	})

	return nil
}

// DeleteProfile soft deletes a sync profile by setting active to false
func (r *Repository) DeleteProfile(profileID string) error {
	result := r.db.GetDB().Model(&SyncProfile{}).Where("id = ?", profileID).Update("active", false)
	if result.Error != nil {
		return fmt.Errorf("failed to delete sync profile: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("sync profile not found: %s", profileID)
	}

	r.logger.Info("Deleted sync profile", map[string]interface{}{
		"profile_id": profileID,
	})

	return nil
}

// GetSyncState retrieves the sync state for a sync profile
func (r *Repository) GetSyncState(profileID string) (*ProfileSyncState, error) {
	var state ProfileSyncState
	if err := r.db.GetDB().Where("profile_id = ?", profileID).First(&state).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return &ProfileSyncState{ProfileID: profileID}, nil
		}
		return nil, fmt.Errorf("failed to get sync state: %w", err)
	}
	return &state, nil
}

// UserExists checks if a sync profile exists and is active
func (r *Repository) UserExists(profileID string) (bool, error) {
	var count int64
	if err := r.db.GetDB().Model(&SyncProfile{}).Where("id = ? AND active = ?", profileID, true).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed to check profile existence: %w", err)
	}
	return count > 0, nil
}

func isLikelyEncryptionKeyMismatch(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, crypto.ErrInvalidCiphertext) {
		return true
	}

	return strings.Contains(err.Error(), "cipher: message authentication failed")
}
