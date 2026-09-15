package multiuser

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	stdSync "sync"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

const maxStateFileComponentBytes = 255

// ErrProfileStateFileNameTooLong indicates that profile-specific state-file
// composition exceeds the supported filename-component baseline.
var ErrProfileStateFileNameTooLong = errors.New("profile-specific state filename exceeds 255 bytes")

// SyncProfileStatus represents the sync status for a profile
type SyncProfileStatus struct {
	ProfileID       string                  `json:"profile_id"`
	ProfileName     string                  `json:"profile_name"`
	Status          string                  `json:"status"` // "idle", "syncing", "error", "completed"
	DryRun          bool                    `json:"dry_run,omitempty"`
	LastSync        *time.Time              `json:"last_sync"`
	Error           string                  `json:"error,omitempty"`
	Progress        string                  `json:"progress,omitempty"`
	BooksTotal      int                     `json:"books_total,omitempty"`
	BooksSynced     int                     `json:"books_synced,omitempty"`
	BooksNotFound   []sync.BookNotFoundInfo `json:"books_not_found,omitempty"`
	Mismatches      []mismatch.BookMismatch `json:"mismatches,omitempty"`
	LastSyncSummary *sync.SyncSummary       `json:"last_sync_summary,omitempty"`
	Snapshot        *sync.SyncSnapshot      `json:"snapshot,omitempty"`
}

type activeSyncRun struct {
	generation uint64
	runID      string
	startedAt  time.Time
}

// MultiUserService manages sync operations for multiple users
type MultiUserService struct {
	repository      *database.Repository
	logger          *logger.Logger
	globalConfig    *config.Config
	profileStatuses map[string]*SyncProfileStatus
	statusMutex     stdSync.RWMutex
	activeSyncs     map[string]context.CancelFunc
	activeRuns      map[string]activeSyncRun
	nextGeneration  uint64
	syncMutex       stdSync.RWMutex
	syncWaitGroup   stdSync.WaitGroup
	syncServices    map[string]*sync.Service // Maps profile ID to its sync service
	serviceRuns     map[string]uint64
	servicesMutex   stdSync.RWMutex
}

// NewMultiUserService creates a new multi-user service
func NewMultiUserService(repo *database.Repository, globalConfig *config.Config, log *logger.Logger) *MultiUserService {
	return &MultiUserService{
		repository:      repo,
		logger:          log,
		globalConfig:    globalConfig,
		profileStatuses: make(map[string]*SyncProfileStatus),
		activeSyncs:     make(map[string]context.CancelFunc),
		activeRuns:      make(map[string]activeSyncRun),
		syncServices:    make(map[string]*sync.Service),
		serviceRuns:     make(map[string]uint64),
	}
}

// ListProfiles returns all active sync profiles
func (s *MultiUserService) ListProfiles() ([]database.SyncProfile, error) {
	return s.repository.ListProfiles()
}

// ListProfilesForUser returns profiles visible to an authenticated user.
func (s *MultiUserService) ListProfilesForUser(userID string, admin, authEnabled bool) ([]database.SyncProfile, error) {
	return s.repository.ListProfilesForUser(userID, admin, authEnabled)
}

// GetProfileMetadata returns a profile without decrypting its credentials.
func (s *MultiUserService) GetProfileMetadata(profileID string) (*database.SyncProfile, error) {
	return s.repository.GetProfileMetadata(profileID)
}

// GetProfile returns a specific profile with decrypted tokens
func (s *MultiUserService) GetProfile(profileID string) (*database.ProfileWithTokens, error) {
	return s.repository.GetProfile(profileID)
}

// CreateProfile creates a new sync profile
func (s *MultiUserService) CreateProfile(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData) error {
	return s.CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig, "")
}

// CreateProfileForUser creates a profile owned by ownerUserID.
func (s *MultiUserService) CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData, ownerUserID string) error {
	if err := s.validateProfileStateFile(profileID, syncConfig.StateFile); err != nil {
		return err
	}
	return s.repository.CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig, ownerUserID)
}

// UpdateProfile updates profile information
func (s *MultiUserService) UpdateProfile(profileID, name string) error {
	return s.repository.UpdateProfile(profileID, name)
}

// UpdateProfileConfig updates profile configuration
func (s *MultiUserService) UpdateProfileConfig(profileID, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData) error {
	if syncConfig.StateFile != "" {
		if err := s.validateProfileStateFile(profileID, syncConfig.StateFile); err != nil {
			return err
		}
	}
	return s.repository.UpdateUserConfig(profileID, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig)
}

// DeleteProfile deletes a sync profile
func (s *MultiUserService) DeleteProfile(profileID string) error {
	// Cancel any active sync for this profile
	if err := s.CancelSync(profileID); err != nil {
		s.logger.Warn("Failed to cancel sync during profile deletion", map[string]interface{}{
			"profileID": profileID,
			"error":     err,
		})
	}

	// Remove from status tracking
	s.statusMutex.Lock()
	delete(s.profileStatuses, profileID)
	s.statusMutex.Unlock()

	return s.repository.DeleteProfile(profileID)
}

// GetAllProfileStatuses returns the sync status for all profiles
func (s *MultiUserService) GetAllProfileStatuses() ([]*SyncProfileStatus, error) {
	profiles, err := s.repository.ListProfiles()
	if err != nil {
		return nil, fmt.Errorf("failed to list profiles: %w", err)
	}

	statuses := make([]*SyncProfileStatus, 0, len(profiles))

	for _, profile := range profiles {
		statuses = append(statuses, s.getAggregateProfileStatus(profile))
	}

	return statuses, nil
}

// getAggregateProfileStatus reads the active service's scalar snapshot while
// holding the profile-run lock that keeps service replacement from racing the
// read. Stored status is used for inactive runs so terminal and setup errors
// remain visible. This path deliberately avoids getProfileStatus because that
// endpoint needs the full snapshot and legacy detail arrays.
func (s *MultiUserService) getAggregateProfileStatus(profile database.SyncProfile) *SyncProfileStatus {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()

	status := s.getStoredAggregateStatus(profile)
	service, generation := s.currentSyncServiceLocked(profile.ID)
	if service == nil {
		return aggregateProfileStatus(profile, status)
	}

	snapshot := service.GetSnapshotStatus()
	if run, ok := s.activeRuns[profile.ID]; ok && run.generation == generation {
		snapshot.UserID = profile.ID
		snapshot.RunID = run.runID
		snapshot.RunStartedAt = run.startedAt
		if snapshot.State == "" || snapshot.State == "idle" {
			snapshot.State = "syncing"
		}
	}

	if status == nil {
		status = &SyncProfileStatus{
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
			Status:      "syncing",
		}
	}
	if status.ProfileID == "" {
		status.ProfileID = profile.ID
	}
	if status.ProfileName == "" {
		status.ProfileName = profile.Name
	}
	switch snapshot.State {
	case "completed":
		status.Status = "completed"
	case "failed":
		status.Status = "error"
	default:
		status.Status = "syncing"
	}
	status.Snapshot = &snapshot
	status.BooksTotal = int(snapshot.BooksTotal)
	status.BooksSynced = int(snapshot.BooksSynced)

	return aggregateProfileStatus(profile, status)
}

// getStoredAggregateStatus copies only the scalar fields needed by aggregate
// polling. In particular, terminal snapshots are reduced without copying
// their per-book outcomes, attention records, not-found entries, or mismatches.
func (s *MultiUserService) getStoredAggregateStatus(profile database.SyncProfile) *SyncProfileStatus {
	s.statusMutex.RLock()
	defer s.statusMutex.RUnlock()

	stored := s.profileStatuses[profile.ID]
	if stored == nil {
		return nil
	}

	status := &SyncProfileStatus{
		ProfileID:   stored.ProfileID,
		ProfileName: stored.ProfileName,
		Status:      stored.Status,
		DryRun:      stored.DryRun,
		Progress:    stored.Progress,
		BooksTotal:  stored.BooksTotal,
		BooksSynced: stored.BooksSynced,
	}
	if stored.LastSync != nil {
		lastSync := *stored.LastSync
		status.LastSync = &lastSync
	}
	if stored.Snapshot != nil {
		status.Snapshot = scalarSnapshot(stored.Snapshot)
	}
	return status
}

// aggregateProfileStatus projects a profile status for the unauthenticated
// aggregate endpoint. Detailed book metadata belongs on the authenticated
// per-profile status endpoint.
func aggregateProfileStatus(profile database.SyncProfile, status *SyncProfileStatus) *SyncProfileStatus {
	if status == nil {
		return &SyncProfileStatus{
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
			Status:      "idle",
		}
	}

	aggregate := &SyncProfileStatus{
		ProfileID:   status.ProfileID,
		ProfileName: status.ProfileName,
		Status:      status.Status,
		DryRun:      status.DryRun,
		LastSync:    status.LastSync,
		Progress:    status.Progress,
		BooksTotal:  status.BooksTotal,
		BooksSynced: status.BooksSynced,
	}
	if status.Snapshot != nil {
		aggregate.Snapshot = status.Snapshot
	}
	if aggregate.Status == "" {
		aggregate.Status = "idle"
	}
	return aggregate
}

// scalarSnapshot keeps aggregate polling cheap and prevents the public
// /api/status response from copying per-book outcome and attention records.
// The full snapshot remains available on the authenticated profile status and
// run-details endpoints.
func scalarSnapshot(snapshot *sync.SyncSnapshot) *sync.SyncSnapshot {
	if snapshot == nil {
		return nil
	}
	return &sync.SyncSnapshot{
		UserID:              snapshot.UserID,
		RunID:               snapshot.RunID,
		RunStartedAt:        snapshot.RunStartedAt,
		State:               snapshot.State,
		BooksTotal:          snapshot.BooksTotal,
		ProcessedSoFar:      snapshot.ProcessedSoFar,
		ProcessedCount:      snapshot.ProcessedCount,
		OutcomeCounts:       snapshot.OutcomeCounts,
		TotalBooksProcessed: snapshot.TotalBooksProcessed,
		BooksSynced:         snapshot.BooksSynced,
	}
}

// GetSyncService returns the sync service for a profile, if it exists
func (s *MultiUserService) GetSyncService(profileID string) (*sync.Service, bool) {
	service, _ := s.currentSyncService(profileID)
	return service, service != nil
}

// GetProfileStatus returns the sync status for a profile
func (s *MultiUserService) GetProfileStatus(profileID string) *SyncProfileStatus {
	return s.getProfileStatus(profileID, nil, nil)
}

// GetProfileSnapshot returns the current in-memory run snapshot for a profile
// without hydrating profile configuration from the repository. It is intended
// for callers that have already performed any required profile authorization.
func (s *MultiUserService) GetProfileSnapshot(profileID string) *sync.SyncSnapshot {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()

	s.statusMutex.RLock()
	var storedSnapshot *sync.SyncSnapshot
	if status := s.profileStatuses[profileID]; status != nil && status.Snapshot != nil {
		storedSnapshot = cloneSyncSnapshot(*status.Snapshot)
	}
	s.statusMutex.RUnlock()

	service, generation := s.currentSyncServiceLocked(profileID)
	if service == nil {
		return storedSnapshot
	}

	snapshot := service.GetSnapshot()
	if generation > 0 {
		if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
			snapshot.UserID = profileID
			snapshot.RunID = run.runID
			snapshot.RunStartedAt = run.startedAt
			if snapshot.State == "" || snapshot.State == "idle" {
				snapshot.State = "syncing"
			}
		}
	}
	if snapshot.UserID == "" {
		snapshot.UserID = profileID
	}

	if storedSnapshot == nil || storedSnapshot.RunID == "" || snapshot.RunID == "" || storedSnapshot.RunID == snapshot.RunID {
		return &snapshot
	}
	return storedSnapshot
}

// currentSyncService returns only the service belonging to the active run.
// A stale goroutine may remain briefly during cleanup, so its service must not
// be exposed after a replacement run has started.
func (s *MultiUserService) currentSyncService(profileID string) (*sync.Service, uint64) {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()
	return s.currentSyncServiceLocked(profileID)
}

func (s *MultiUserService) currentSyncServiceLocked(profileID string) (*sync.Service, uint64) {
	s.servicesMutex.RLock()
	defer s.servicesMutex.RUnlock()
	service, exists := s.syncServices[profileID]
	generation := s.serviceRuns[profileID]
	activeRun, active := s.activeRuns[profileID]
	if !exists || service == nil {
		return nil, 0
	}
	if generation == 0 {
		return service, 0
	}
	if !active || activeRun.generation != generation {
		return nil, 0
	}
	return service, generation
}

func (s *MultiUserService) getProfileStatus(profileID string, profile *database.SyncProfile, profileState *database.ProfileSyncState) *SyncProfileStatus {
	// A status lookup can hydrate its fallback from the database. Do that
	// without holding syncMutex so a slow database cannot block a new run.
	s.statusMutex.RLock()
	status := cloneProfileStatus(s.profileStatuses[profileID])
	s.statusMutex.RUnlock()

	if status == nil {
		if profile == nil {
			if s.repository != nil {
				loaded, _ := s.GetProfile(profileID)
				if loaded != nil {
					profile = &loaded.Profile
					profileState = loaded.Profile.SyncState
				}
			}
		}
	}
	if (status == nil || status.LastSync == nil) && profileState == nil &&
		s.repository != nil && (status != nil || profile != nil) {
		profileState, _ = s.repository.GetSyncState(profileID)
	}

	// Re-read status and service state together after fallback I/O. A run can
	// start while either lookup is in progress, and its status must win over a
	// stale loaded fallback.
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()

	s.statusMutex.RLock()
	status = cloneProfileStatus(s.profileStatuses[profileID])
	s.statusMutex.RUnlock()
	if status == nil {
		if profile == nil {
			return &SyncProfileStatus{ProfileID: profileID, Status: "error", Error: "Profile not found"}
		}
		status = &SyncProfileStatus{ProfileID: profileID, ProfileName: profile.Name, Status: "idle"}
	}
	if status.Status == "" {
		status.Status = "idle"
	}
	if status.ProfileName == "" && profile != nil {
		status.ProfileName = profile.Name
	}
	if status.LastSync == nil && profileState != nil && profileState.LastSync != nil {
		lastSync := *profileState.LastSync
		status.LastSync = &lastSync
	}
	service, generation := s.currentSyncServiceLocked(profileID)
	if service == nil {
		return status
	}
	snapshot := service.GetSnapshot()
	if generation > 0 {
		if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
			snapshot.UserID = profileID
			snapshot.RunID = run.runID
			snapshot.RunStartedAt = run.startedAt
			if snapshot.State == "" || snapshot.State == "idle" {
				snapshot.State = "syncing"
			}
		}
	}
	if status.Snapshot == nil || status.Snapshot.RunID == "" || snapshot.RunID == "" || status.Snapshot.RunID == snapshot.RunID {
		applySnapshotToStatus(status, snapshot)
		if status.Snapshot.UserID == "" {
			status.Snapshot.UserID = profileID
		}
	}
	return status
}

// StartSync starts a sync operation for a specific profile
func (s *MultiUserService) StartSync(profileID string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	if _, exists := s.activeSyncs[profileID]; exists {
		return fmt.Errorf("sync already in progress for profile %s", profileID)
	}

	// Get profile config
	profileConfig, err := s.GetProfile(profileID)
	if err != nil {
		return fmt.Errorf("failed to get profile config: %w", err)
	}

	// Create cancellable context and store cancel
	ctx, cancel := context.WithCancel(context.Background())
	s.nextGeneration++
	run := activeSyncRun{
		generation: s.nextGeneration,
		startedAt:  time.Now().UTC(),
	}
	run.runID = fmt.Sprintf("%s-%d-%d", profileID, run.startedAt.UnixNano(), run.generation)
	s.activeSyncs[profileID] = cancel
	s.activeRuns[profileID] = run

	// Update initial status
	initialStatus := &SyncProfileStatus{
		ProfileID:   profileID,
		ProfileName: profileConfig.Profile.Name,
		Status:      "syncing",
		DryRun:      profileConfig.SyncConfig.DryRun,
		LastSync:    nil,
		Progress:    "Starting sync...",
	}
	applySnapshotToStatus(initialStatus, newRunSnapshot(profileID, run, "syncing"))
	s.updateProfileStatus(profileID, initialStatus)

	// Start the sync in background
	s.syncWaitGroup.Add(1)
	go func() {
		defer s.syncWaitGroup.Done()
		s.performSync(ctx, profileID, profileConfig, run.generation)
	}()
	return nil
}

// WaitForSyncs waits for all sync goroutines started by this service to exit.
// It is used by lifecycle owners that must release resources, such as the
// logger and test databases, only after asynchronous work has stopped.
func (s *MultiUserService) WaitForSyncs() {
	s.syncWaitGroup.Wait()
}

// CancelSync cancels a running sync operation for a profile
func (s *MultiUserService) CancelSync(profileID string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	cancel, exists := s.activeSyncs[profileID]
	if !exists {
		return fmt.Errorf("no active sync for profile %s", profileID)
	}
	cancel()
	delete(s.activeSyncs, profileID)
	delete(s.activeRuns, profileID)

	finalStatus := &SyncProfileStatus{
		ProfileID: profileID,
		Status:    "idle",
		LastSync:  timePtr(time.Now()),
		Progress:  "Sync canceled",
	}
	// Persist last_sync to DB so UI can show it across restarts
	if state, err := s.repository.GetSyncState(profileID); err == nil {
		if state == nil {
			state = &database.ProfileSyncState{ProfileID: profileID, StateData: "{}"}
		}
		state.LastSync = finalStatus.LastSync
		_ = s.repository.UpdateSyncState(state)
	}

	s.updateProfileStatus(profileID, finalStatus)
	return nil
}

// performSync performs the actual sync operation for a profile
func (s *MultiUserService) performSync(ctx context.Context, profileID string, profileConfig *database.ProfileWithTokens, generation uint64) {
	// Ensure only this run's active marker is cleared when its goroutine ends.
	defer s.finishActiveRun(profileID, generation)
	// Create profile-specific config
	config := s.createProfileSpecificConfig(profileConfig)

	// Create clients
	absClient := audiobookshelf.NewClient(profileConfig.AudiobookshelfURL, profileConfig.AudiobookshelfToken)

	// Build Hardcover client config using global settings (rate limits/base URL)
	hcCfg := hardcover.DefaultClientConfig()
	if s.globalConfig != nil {
		if s.globalConfig.Hardcover.BaseURL != "" {
			hcCfg.BaseURL = s.globalConfig.Hardcover.BaseURL
		}
		if s.globalConfig.RateLimit.Rate > 0 {
			hcCfg.RateLimit = s.globalConfig.RateLimit.Rate
		}
		if s.globalConfig.RateLimit.MaxConcurrent > 0 {
			hcCfg.MaxConcurrent = s.globalConfig.RateLimit.MaxConcurrent
		}
	}

	s.logger.Debug("Initializing Hardcover client (multi-user)", map[string]interface{}{
		"profile_id":     profileID,
		"base_url":       hcCfg.BaseURL,
		"rate_limit":     hcCfg.RateLimit.String(),
		"max_concurrent": hcCfg.MaxConcurrent,
	})

	hcClient := hardcover.NewClientWithConfig(hcCfg, profileConfig.HardcoverToken, s.logger)

	// Create sync service
	syncService, err := sync.NewService(absClient, hcClient, config)
	if err != nil {
		status := &SyncProfileStatus{
			ProfileID:   profileID,
			ProfileName: profileConfig.Profile.Name,
			Status:      "error",
			Error:       fmt.Sprintf("Failed to create sync service: %v", err),
		}
		if run, ok := s.activeRun(profileID, generation); ok {
			applySnapshotToStatus(status, newRunSnapshot(profileID, run, "failed"))
			s.publishFinalStatus(profileID, generation, status)
		}
		return
	}

	// Store the sync service for status access
	if !s.registerSyncService(profileID, generation, syncService) {
		return
	}
	defer s.removeSyncService(profileID, generation, syncService)

	// Run the sync
	err = syncService.Sync(ctx)

	// Obtain summary
	summary := syncService.GetSummary()
	snapshot := s.normalizeRunSnapshot(profileID, generation, syncService.GetSnapshot())

	// Prepare final status
	status := &SyncProfileStatus{
		ProfileID:   profileID,
		ProfileName: profileConfig.Profile.Name,
		DryRun:      profileConfig.SyncConfig.DryRun,
		LastSync:    timePtr(time.Now()),
	}
	applySnapshotToStatus(status, snapshot)
	if status.Snapshot != nil && status.Snapshot.UserID == "" {
		status.Snapshot.UserID = profileID
	}

	if err != nil {
		status.Status = "error"
		status.Error = err.Error()
		s.logger.Error("Sync failed", map[string]interface{}{
			"profileID": profileID,
			"error":     err,
		})
	} else {
		status.Status = "completed"
		status.Progress = "Sync completed successfully"

		s.logger.Debug("Stored full sync summary in profile status", map[string]interface{}{
			"profileID":       profileID,
			"books_processed": summary.TotalBooksProcessed,
			"books_synced":    summary.BooksSynced,
			"books_not_found": len(summary.BooksNotFound),
			"mismatches":      len(summary.Mismatches),
		})
	}

	// Publish only if this run is still current. A stale run can finish after a
	// replacement starts and must not overwrite its status.
	s.publishFinalStatus(profileID, generation, status)
}

func newRunSnapshot(profileID string, run activeSyncRun, state string) sync.SyncSnapshot {
	return sync.SyncSnapshot{
		UserID:           profileID,
		RunID:            run.runID,
		RunStartedAt:     run.startedAt,
		State:            state,
		BookOutcomes:     make([]sync.BookOutcomeRecord, 0),
		AttentionRecords: make([]sync.BookOutcomeRecord, 0),
		BooksNotFound:    make([]sync.BookNotFoundInfo, 0),
		Mismatches:       make([]mismatch.BookMismatch, 0),
	}
}

func (s *MultiUserService) activeRun(profileID string, generation uint64) (activeSyncRun, bool) {
	if generation == 0 {
		return activeSyncRun{}, false
	}
	s.syncMutex.RLock()
	run, ok := s.activeRuns[profileID]
	s.syncMutex.RUnlock()
	return run, ok && run.generation == generation
}

func (s *MultiUserService) normalizeRunSnapshot(profileID string, generation uint64, snapshot sync.SyncSnapshot) sync.SyncSnapshot {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()
	if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
		snapshot.UserID = profileID
		snapshot.RunID = run.runID
		snapshot.RunStartedAt = run.startedAt
		if snapshot.State == "" || snapshot.State == "idle" {
			snapshot.State = "syncing"
		}
	}
	if snapshot.UserID == "" {
		snapshot.UserID = profileID
	}
	return snapshot
}

func (s *MultiUserService) registerSyncService(profileID string, generation uint64, service *sync.Service) bool {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	run, ok := s.activeRuns[profileID]
	if !ok || run.generation != generation {
		return false
	}
	s.servicesMutex.Lock()
	s.syncServices[profileID] = service
	s.serviceRuns[profileID] = generation
	s.servicesMutex.Unlock()
	return true
}

func (s *MultiUserService) removeSyncService(profileID string, generation uint64, service *sync.Service) {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	s.servicesMutex.Lock()
	if s.syncServices[profileID] == service && s.serviceRuns[profileID] == generation {
		delete(s.syncServices, profileID)
		delete(s.serviceRuns, profileID)
	}
	s.servicesMutex.Unlock()
}

func (s *MultiUserService) finishActiveRun(profileID string, generation uint64) {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
		delete(s.activeRuns, profileID)
		delete(s.activeSyncs, profileID)
	}
}

func (s *MultiUserService) publishFinalStatus(profileID string, generation uint64, status *SyncProfileStatus) bool {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	if status == nil {
		return false
	}
	run, ok := s.activeRuns[profileID]
	if !ok || run.generation != generation {
		return false
	}
	if s.repository != nil {
		if state, err := s.repository.GetSyncState(profileID); err == nil {
			if state == nil {
				state = &database.ProfileSyncState{ProfileID: profileID, StateData: "{}"}
			}
			state.LastSync = status.LastSync
			_ = s.repository.UpdateSyncState(state)
		}
	}
	s.statusMutex.Lock()
	s.profileStatuses[profileID] = cloneProfileStatus(status)
	s.statusMutex.Unlock()
	return true
}

// createProfileSpecificConfig creates a config.Config instance for a specific profile
func (s *MultiUserService) createProfileSpecificConfig(profileConfig *database.ProfileWithTokens) *config.Config {
	// Create a copy of the global config
	config := *s.globalConfig

	// Override with profile-specific settings
	config.Audiobookshelf.URL = profileConfig.AudiobookshelfURL
	config.Audiobookshelf.Token = profileConfig.AudiobookshelfToken
	config.Hardcover.Token = profileConfig.HardcoverToken

	// Apply sync config from profile
	syncConfig := profileConfig.SyncConfig

	// Always apply the sync config from the profile
	// The config in the profile should already have the correct values (either defaults or explicitly set)

	// Parse sync interval if provided
	if syncConfig.SyncInterval != "" {
		duration, err := time.ParseDuration(syncConfig.SyncInterval)
		if err != nil {
			s.logger.Warn("Invalid sync interval, using default", map[string]interface{}{
				"profileID": profileConfig.Profile.ID,
				"interval":  syncConfig.SyncInterval,
				"error":     err,
			})
			duration = 1 * time.Hour // Default to 1 hour if invalid
		}
		config.Sync.SyncInterval = duration
	}

	// Apply all sync config values from the profile
	config.Sync.Incremental = syncConfig.Incremental
	// Make state file path profile-specific to avoid conflicts. Keep this
	// derivation centralized because profile creation/update validation must
	// inspect the exact path used by runtime setup.
	config.Sync.StateFile = s.profileSpecificStatePath(profileConfig.Profile.ID, syncConfig.StateFile)
	config.Sync.MinChangeThreshold = syncConfig.MinChangeThreshold
	config.Sync.Libraries.Include = syncConfig.Libraries.Include
	config.Sync.Libraries.Exclude = syncConfig.Libraries.Exclude
	config.Sync.MinimumProgress = syncConfig.MinimumProgress
	config.Sync.SyncWantToRead = syncConfig.SyncWantToRead
	config.Sync.ProcessUnreadBooks = syncConfig.ProcessUnreadBooks
	config.Sync.SyncOwned = syncConfig.SyncOwned
	config.Sync.IncludeEbooks = syncConfig.IncludeEbooks
	config.Sync.DryRun = syncConfig.DryRun
	config.Sync.TestBookFilter = syncConfig.TestBookFilter
	config.Sync.TestBookLimit = syncConfig.TestBookLimit
	config.Audiobookshelf.AudnexusRegion = syncConfig.AudnexusRegion

	// Debug logging to verify the config is being applied correctly
	s.logger.Debug("Applied sync config for profile", map[string]interface{}{
		"profileID":            profileConfig.Profile.ID,
		"process_unread_books": config.Sync.ProcessUnreadBooks,
		"incremental":          config.Sync.Incremental,
		"sync_want_to_read":    config.Sync.SyncWantToRead,
	})

	return &config
}

func (s *MultiUserService) profileSpecificStatePath(profileID, configuredPath string) string {
	statePath := configuredPath
	if statePath == "" {
		if s.globalConfig != nil && s.globalConfig.Paths.DataDir != "" {
			statePath = fmt.Sprintf("%s/sync_state.json", strings.TrimSuffix(s.globalConfig.Paths.DataDir, "/"))
		} else {
			statePath = "/data/sync_state.json"
		}
	} else if !filepath.IsAbs(statePath) && s.globalConfig != nil && s.globalConfig.Paths.DataDir != "" {
		statePath = filepath.Join(s.globalConfig.Paths.DataDir, statePath)
	}
	return fmt.Sprintf("%s.%s", strings.TrimSuffix(statePath, ".json"), profileID)
}

func (s *MultiUserService) validateProfileStateFile(profileID, configuredPath string) error {
	derivedPath := s.profileSpecificStatePath(profileID, configuredPath)
	if len([]byte(filepath.Base(derivedPath))) > maxStateFileComponentBytes {
		return fmt.Errorf("%w: %q", ErrProfileStateFileNameTooLong, filepath.Base(derivedPath))
	}
	return nil
}

func cloneBookMismatch(record mismatch.BookMismatch) mismatch.BookMismatch {
	copyOf := record
	copyOf.AuthorIDs = append([]int(nil), record.AuthorIDs...)
	copyOf.NarratorIDs = append([]int(nil), record.NarratorIDs...)
	return copyOf
}

func cloneSyncSummary(summary *sync.SyncSummary) *sync.SyncSummary {
	if summary == nil {
		return nil
	}
	copyOf := &sync.SyncSummary{
		UserID:              summary.UserID,
		TotalBooksProcessed: summary.TotalBooksProcessed,
		BooksSynced:         summary.BooksSynced,
		BooksTotal:          summary.BooksTotal,
		BooksNotFound:       append([]sync.BookNotFoundInfo(nil), summary.BooksNotFound...),
		Mismatches:          make([]mismatch.BookMismatch, len(summary.Mismatches)),
	}
	for i, record := range summary.Mismatches {
		copyOf.Mismatches[i] = cloneBookMismatch(record)
	}
	return copyOf
}

func cloneSyncSnapshot(snapshot sync.SyncSnapshot) *sync.SyncSnapshot {
	copyOf := snapshot
	copyOf.BookOutcomes = append([]sync.BookOutcomeRecord(nil), snapshot.BookOutcomes...)
	copyOf.AttentionRecords = append([]sync.BookOutcomeRecord(nil), snapshot.AttentionRecords...)
	copyOf.BooksNotFound = append([]sync.BookNotFoundInfo(nil), snapshot.BooksNotFound...)
	copyOf.Mismatches = make([]mismatch.BookMismatch, len(snapshot.Mismatches))
	for i, record := range snapshot.Mismatches {
		copyOf.Mismatches[i] = cloneBookMismatch(record)
	}
	return &copyOf
}

func applySnapshotToStatus(status *SyncProfileStatus, snapshot sync.SyncSnapshot) {
	if status == nil {
		return
	}
	status.Snapshot = cloneSyncSnapshot(snapshot)
	status.BooksTotal = int(snapshot.BooksTotal)
	status.BooksSynced = int(snapshot.BooksSynced)
	status.BooksNotFound = append([]sync.BookNotFoundInfo(nil), snapshot.BooksNotFound...)
	status.Mismatches = make([]mismatch.BookMismatch, len(snapshot.Mismatches))
	for i, record := range snapshot.Mismatches {
		status.Mismatches[i] = cloneBookMismatch(record)
	}
	status.LastSyncSummary = &sync.SyncSummary{
		UserID:              snapshot.UserID,
		TotalBooksProcessed: snapshot.TotalBooksProcessed,
		BooksSynced:         snapshot.BooksSynced,
		BooksTotal:          snapshot.BooksTotal,
		BooksNotFound:       []sync.BookNotFoundInfo{},
		Mismatches:          []mismatch.BookMismatch{},
	}
}

func cloneProfileStatus(status *SyncProfileStatus) *SyncProfileStatus {
	if status == nil {
		return nil
	}
	copyOf := *status
	if status.LastSync != nil {
		lastSync := *status.LastSync
		copyOf.LastSync = &lastSync
	}
	copyOf.BooksNotFound = append([]sync.BookNotFoundInfo(nil), status.BooksNotFound...)
	copyOf.Mismatches = make([]mismatch.BookMismatch, len(status.Mismatches))
	for i, record := range status.Mismatches {
		copyOf.Mismatches[i] = cloneBookMismatch(record)
	}
	copyOf.LastSyncSummary = cloneSyncSummary(status.LastSyncSummary)
	if status.Snapshot != nil {
		copyOf.Snapshot = cloneSyncSnapshot(*status.Snapshot)
	}
	return &copyOf
}

// updateProfileStatus updates the status for a profile
func (s *MultiUserService) updateProfileStatus(profileID string, status *SyncProfileStatus) {
	s.statusMutex.Lock()
	defer s.statusMutex.Unlock()
	s.profileStatuses[profileID] = cloneProfileStatus(status)
}

// timePtr returns a pointer to a time.Time value
func timePtr(t time.Time) *time.Time {
	return &t
}

// IsProfileSyncing checks if a profile is currently syncing
func (s *MultiUserService) IsProfileSyncing(profileID string) bool {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()

	_, exists := s.activeSyncs[profileID]
	return exists
}
