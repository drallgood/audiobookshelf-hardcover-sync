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
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

// SyncProfileStatus represents the sync status for a profile
type SyncProfileStatus struct {
	ProfileID   string     `json:"profile_id"`
	ProfileName string     `json:"profile_name"`
	Status      string     `json:"status"` // "idle", "syncing", "error", "completed"
	LastSync    *time.Time `json:"last_sync"`
	Error       string     `json:"error,omitempty"`
	Progress    string     `json:"progress,omitempty"`
	BooksTotal  int        `json:"books_total,omitempty"`
	BooksSynced int        `json:"books_synced,omitempty"`
}

// MultiUserService manages sync operations for multiple users
type MultiUserService struct {
	repository    *database.Repository
	logger        *logger.Logger
	globalConfig  *config.Config
	profileStatuses map[string]*SyncProfileStatus
	statusMutex    stdSync.RWMutex
	activeSyncs    map[string]context.CancelFunc
	syncMutex      stdSync.RWMutex
}

// NewMultiUserService creates a new multi-user service
func NewMultiUserService(repo *database.Repository, globalConfig *config.Config, log *logger.Logger) *MultiUserService {
	return &MultiUserService{
		repository:     repo,
		logger:         log,
		globalConfig:   globalConfig,
		profileStatuses: make(map[string]*SyncProfileStatus),
		activeSyncs:    make(map[string]context.CancelFunc),
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
		if !exists {
			// Create default status for profile
			status = &SyncProfileStatus{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Status:      "idle",
				LastSync:    nil,
			}
		}
		statuses = append(statuses, status)
	}

	return statuses, nil
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
	
	return status
}

// StartSync starts a sync operation for a specific profile
func (s *MultiUserService) StartSync(profileID string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	
	// Check if already syncing
	if _, exists := s.activeSyncs[profileID]; exists {
		return fmt.Errorf("sync already in progress for profile %s", profileID)
	}
	
	// Get profile config
	profileConfig, err := s.GetProfile(profileID)
	if err != nil {
		return fmt.Errorf("failed to get profile config: %w", err)
	}
	
	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	s.activeSyncs[profileID] = cancel
	
	// Update status
	s.updateProfileStatus(profileID, &SyncProfileStatus{
		ProfileID:   profileID,
		ProfileName: profileConfig.Profile.Name,
		Status:      "syncing",
		LastSync:    nil,
	})
	
	// Start sync in a goroutine
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
	
	// Cancel the context
	cancel()
	
	// Remove from active syncs
	delete(s.activeSyncs, profileID)
	
	// Update status
	s.updateProfileStatus(profileID, &SyncProfileStatus{
		ProfileID: profileID,
		Status:    "idle",
		LastSync:  timePtr(time.Now()),
	})
	
	return nil
}

// performSync performs the actual sync operation for a profile
func (s *MultiUserService) performSync(ctx context.Context, profileID string, profileConfig *database.ProfileWithTokens) {
	defer func() {
		s.syncMutex.Lock()
		delete(s.activeSyncs, profileID)
		s.syncMutex.Unlock()
	}()
	
	// Create profile-specific config
	config := s.createProfileSpecificConfig(profileConfig)
	
	// Create ABS client
	audiobookshelfClient := audiobookshelf.NewClient(profileConfig.AudiobookshelfURL, profileConfig.AudiobookshelfToken)
	
	// Create Hardcover client
	hardcoverClient := hardcover.NewClient(profileConfig.HardcoverToken, s.logger)

	// Create sync service
	syncService, err := sync.NewService(audiobookshelfClient, hardcoverClient, config)
	if err != nil {
		s.updateProfileStatus(profileID, &SyncProfileStatus{
			ProfileID:   profileID,
			ProfileName: profileConfig.Profile.Name,
			Status:      "error",
			Error:       fmt.Sprintf("Failed to create sync service: %v", err),
		})
		return
	}
	
	// Update status to show sync started
	s.updateProfileStatus(profileID, &SyncProfileStatus{
		ProfileID:   profileID,
		ProfileName: profileConfig.Profile.Name,
		Status:      "syncing",
		Progress:    "Starting sync...",
	})
	
	// Run sync
	err = syncService.Sync(ctx)
	
	// Update final status
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
	}
	
	s.updateProfileStatus(profileID, status)
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
