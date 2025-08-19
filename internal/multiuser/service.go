package multiuser

import (
	"context"
	"fmt"
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

// SyncProfileStatus represents the sync status for a profile
type SyncProfileStatus struct {
	ProfileID          string                 `json:"profile_id"`
	ProfileName        string                 `json:"profile_name"`
	Status             string                 `json:"status"` // "idle", "syncing", "error", "completed"
	LastSync           *time.Time             `json:"last_sync"`
	Error              string                 `json:"error,omitempty"`
	Progress           string                 `json:"progress,omitempty"`
	BooksTotal         int                    `json:"books_total,omitempty"`
	BooksSynced        int                    `json:"books_synced,omitempty"`
	BooksNotFound      []sync.BookNotFoundInfo `json:"books_not_found,omitempty"`
	Mismatches         []mismatch.BookMismatch `json:"mismatches,omitempty"`
	LastSyncSummary    *sync.SyncSummary       `json:"last_sync_summary,omitempty"`
}

// MultiUserService manages sync operations for multiple users
type MultiUserService struct {
	repository      *database.Repository
	logger          *logger.Logger
	globalConfig    *config.Config
	profileStatuses map[string]*SyncProfileStatus
	statusMutex     stdSync.RWMutex
	activeSyncs     map[string]context.CancelFunc
	syncMutex       stdSync.RWMutex
	syncServices    map[string]*sync.Service // Maps profile ID to its sync service
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
		syncServices:    make(map[string]*sync.Service),
	}
}

// ListProfiles returns all active sync profiles
func (s *MultiUserService) ListProfiles() ([]database.SyncProfile, error) {
	return s.repository.ListProfiles()
}

// GetProfile returns a specific profile with decrypted tokens
func (s *MultiUserService) GetProfile(profileID string) (*database.ProfileWithTokens, error) {
	return s.repository.GetProfile(profileID)
}

// CreateProfile creates a new sync profile
func (s *MultiUserService) CreateProfile(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData) error {
	return s.repository.CreateProfile(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig)
}

// UpdateProfile updates profile information
func (s *MultiUserService) UpdateProfile(profileID, name string) error {
	return s.repository.UpdateProfile(profileID, name)
}

// UpdateProfileConfig updates profile configuration
func (s *MultiUserService) UpdateProfileConfig(profileID, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData) error {
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
	
	s.statusMutex.RLock()
	defer s.statusMutex.RUnlock()

	for _, profile := range profiles {
		status, exists := s.profileStatuses[profile.ID]
		if !exists || status == nil {
			// Create default status for profile
			status = &SyncProfileStatus{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Status:      "idle",
				LastSync:    nil,
			}
		} else {
			// Ensure we return a copy to avoid race conditions
			status = &SyncProfileStatus{
				ProfileID:   status.ProfileID,
				ProfileName: status.ProfileName,
				Status:      status.Status,
				LastSync:    status.LastSync,
				Error:       status.Error,
				Progress:    status.Progress,
				BooksTotal:  status.BooksTotal,
				BooksSynced: status.BooksSynced,
			}
		}

		// Ensure status is never empty
		if status.Status == "" {
			status.Status = "idle"
		}

		statuses = append(statuses, status)
	}

	return statuses, nil
}

// GetSyncService returns the sync service for a profile, if it exists
func (s *MultiUserService) GetSyncService(profileID string) (*sync.Service, bool) {
	s.servicesMutex.RLock()
	defer s.servicesMutex.RUnlock()
	service, exists := s.syncServices[profileID]
	return service, exists
}

// GetProfileStatus returns the sync status for a profile
func (s *MultiUserService) GetProfileStatus(profileID string) *SyncProfileStatus {
	s.statusMutex.RLock()
	defer s.statusMutex.RUnlock()
	
	status, exists := s.profileStatuses[profileID]
	if !exists {
		// Check if profile exists in database
		profile, err := s.GetProfile(profileID)
		if err != nil {
			return &SyncProfileStatus{
				ProfileID:   profileID,
				Status:      "error",
				Error:       "Profile not found",
				LastSync:    nil,
			}
		}
		
		// Create default status for existing profile
		status = &SyncProfileStatus{
			ProfileID:   profileID,
			ProfileName: profile.Profile.Name,
			Status:      "idle",
			LastSync:    nil,
		}
	}
	
	// If we do not have an in-memory LastSync (e.g., after restart), hydrate from DB
	if status.LastSync == nil {
		if state, err := s.repository.GetSyncState(profileID); err == nil && state != nil && state.LastSync != nil {
			status.LastSync = state.LastSync
		}
	}
	
	// If there's an active sync service, get the latest status from it
	s.servicesMutex.RLock()
	defer s.servicesMutex.RUnlock()
	
	if svc, exists := s.syncServices[profileID]; exists {
		summary := svc.GetSummary()
		if summary != nil {
			status.BooksTotal = int(summary.TotalBooksProcessed)
			status.BooksSynced = int(summary.BooksSynced)
			
			// Create proper copies of the slices to avoid race conditions
			if len(summary.BooksNotFound) > 0 {
				status.BooksNotFound = make([]sync.BookNotFoundInfo, len(summary.BooksNotFound))
				copy(status.BooksNotFound, summary.BooksNotFound)
			} else {
				status.BooksNotFound = []sync.BookNotFoundInfo{}
			}
			
			if len(summary.Mismatches) > 0 {
				status.Mismatches = make([]mismatch.BookMismatch, len(summary.Mismatches))
				copy(status.Mismatches, summary.Mismatches)
			} else {
				status.Mismatches = []mismatch.BookMismatch{}
			}
			
			// Create a lightweight copy of the summary for last_sync_summary (counters only)
			summaryCopy := &sync.SyncSummary{
				UserID:              summary.UserID,
				TotalBooksProcessed: summary.TotalBooksProcessed,
				BooksSynced:         summary.BooksSynced,
				// Intentionally leave BooksNotFound and Mismatches empty to avoid duplication
				BooksNotFound:       []sync.BookNotFoundInfo{},
				Mismatches:          []mismatch.BookMismatch{},
			}
			status.LastSyncSummary = summaryCopy
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
    s.activeSyncs[profileID] = cancel

    // Update initial status
    s.updateProfileStatus(profileID, &SyncProfileStatus{
        ProfileID:   profileID,
        ProfileName: profileConfig.Profile.Name,
        Status:      "syncing",
        LastSync:    nil,
        Progress:    "Starting sync...",
    })

    // Start the sync in background
    go s.performSync(ctx, profileID, profileConfig)
    return nil
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
func (s *MultiUserService) performSync(ctx context.Context, profileID string, profileConfig *database.ProfileWithTokens) {
    // Ensure the active sync marker is cleared when this sync finishes
    defer func() {
        s.syncMutex.Lock()
        delete(s.activeSyncs, profileID)
        s.syncMutex.Unlock()
    }()
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
        if s.globalConfig.RateLimit.Burst > 0 {
            hcCfg.Burst = s.globalConfig.RateLimit.Burst
        }
        if s.globalConfig.RateLimit.MaxConcurrent > 0 {
            hcCfg.MaxConcurrent = s.globalConfig.RateLimit.MaxConcurrent
        }
    }

    s.logger.Debug("Initializing Hardcover client (multi-user)", map[string]interface{}{
        "profile_id":     profileID,
        "base_url":       hcCfg.BaseURL,
        "rate_limit":     hcCfg.RateLimit.String(),
        "burst":          hcCfg.Burst,
        "max_concurrent": hcCfg.MaxConcurrent,
    })

    hcClient := hardcover.NewClientWithConfig(hcCfg, profileConfig.HardcoverToken, s.logger)

    // Create sync service
    syncService, err := sync.NewService(absClient, hcClient, config)
    if err != nil {
        s.updateProfileStatus(profileID, &SyncProfileStatus{
            ProfileID:   profileID,
            ProfileName: profileConfig.Profile.Name,
            Status:      "error",
            Error:       fmt.Sprintf("Failed to create sync service: %v", err),
        })
        return
    }

    // Store the sync service for status access
    s.servicesMutex.Lock()
    s.syncServices[profileID] = syncService
    s.servicesMutex.Unlock()
    defer func() {
        s.servicesMutex.Lock()
        delete(s.syncServices, profileID)
        s.servicesMutex.Unlock()
    }()

    // Run the sync
    err = syncService.Sync(ctx)

    // Obtain summary
    summary := syncService.GetSummary()

    // Prepare final status
    status := &SyncProfileStatus{
        ProfileID:   profileID,
        ProfileName: profileConfig.Profile.Name,
        LastSync:    timePtr(time.Now()),
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
        status.BooksTotal = int(summary.TotalBooksProcessed)
        status.BooksSynced = int(summary.BooksSynced)

        // Store full data at top level
        status.BooksNotFound = summary.BooksNotFound
        status.Mismatches = summary.Mismatches

        // Lightweight last_sync_summary (counters only)
        summaryCopy := &sync.SyncSummary{
            UserID:              summary.UserID,
            TotalBooksProcessed: summary.TotalBooksProcessed,
            BooksSynced:         summary.BooksSynced,
            BooksNotFound:       []sync.BookNotFoundInfo{},
            Mismatches:          []mismatch.BookMismatch{},
        }
        status.LastSyncSummary = summaryCopy

        s.logger.Debug("Stored full sync summary in profile status", map[string]interface{}{
            "profileID":       profileID,
            "books_processed": summary.TotalBooksProcessed,
            "books_synced":    summary.BooksSynced,
            "books_not_found": len(summary.BooksNotFound),
            "mismatches":      len(summary.Mismatches),
        })
    }

    // Persist last_sync to DB so it's available across restarts
    if state, err := s.repository.GetSyncState(profileID); err == nil {
        if state == nil {
            state = &database.ProfileSyncState{ProfileID: profileID, StateData: "{}"}
        }
        state.LastSync = status.LastSync
        _ = s.repository.UpdateSyncState(state)
    }

    // Update final status atomically
    s.statusMutex.Lock()
    s.profileStatuses[profileID] = status
    s.statusMutex.Unlock()
}

// createProfileSpecificConfig creates a config.Config instance for a specific profile
func (s *MultiUserService) createProfileSpecificConfig(profileConfig *database.ProfileWithTokens) *config.Config {
	// Create a copy of the global config
	config := *s.globalConfig
	
	// Override with profile-specific settings
	config.Audiobookshelf.URL = profileConfig.AudiobookshelfURL
	config.Audiobookshelf.Token = profileConfig.AudiobookshelfToken
	config.Hardcover.Token = profileConfig.HardcoverToken
	
	// Apply sync config from profile if available
	syncConfig := profileConfig.SyncConfig
	if syncConfig.SyncInterval != "" { // Check if sync config has been set
		duration, err := time.ParseDuration(syncConfig.SyncInterval)
		if err != nil {
			s.logger.Warn("Invalid sync interval, using default", map[string]interface{}{
				"profileID": profileConfig.Profile.ID,
				"interval":  syncConfig.SyncInterval,
				"error":     err,
			})
			duration = 1 * time.Hour // Default to 1 hour if invalid
		}
		
		config.Sync.Incremental = syncConfig.Incremental
		config.Sync.StateFile = syncConfig.StateFile
		config.Sync.MinChangeThreshold = syncConfig.MinChangeThreshold
		config.Sync.Libraries.Include = syncConfig.Libraries.Include
		config.Sync.Libraries.Exclude = syncConfig.Libraries.Exclude
		config.Sync.SyncInterval = duration
		config.Sync.MinimumProgress = syncConfig.MinimumProgress
		config.Sync.SyncWantToRead = syncConfig.SyncWantToRead
		config.Sync.SyncOwned = syncConfig.SyncOwned
		config.Sync.DryRun = syncConfig.DryRun
	}
	
	return &config
}

// updateProfileStatus updates the status for a profile
func (s *MultiUserService) updateProfileStatus(profileID string, status *SyncProfileStatus) {
	s.statusMutex.Lock()
	defer s.statusMutex.Unlock()
	s.profileStatuses[profileID] = status
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
