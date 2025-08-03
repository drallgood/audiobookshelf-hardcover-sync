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

// UserSyncStatus represents the sync status for a user
type UserSyncStatus struct {
	UserID      string     `json:"user_id"`
	UserName    string     `json:"user_name"`
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
	userStatuses  map[string]*UserSyncStatus
	statusMutex   stdSync.RWMutex
	activeSyncs   map[string]context.CancelFunc
	syncMutex     stdSync.RWMutex
}

// NewMultiUserService creates a new multi-user service
func NewMultiUserService(repo *database.Repository, globalConfig *config.Config, log *logger.Logger) *MultiUserService {
	return &MultiUserService{
		repository:   repo,
		logger:       log,
		globalConfig: globalConfig,
		userStatuses: make(map[string]*UserSyncStatus),
		activeSyncs:  make(map[string]context.CancelFunc),
	}
}

// GetUsers returns all active users
func (s *MultiUserService) GetUsers() ([]database.User, error) {
	return s.repository.ListUsers()
}

// GetUser returns a specific user with decrypted tokens
func (s *MultiUserService) GetUser(userID string) (*database.UserWithTokens, error) {
	return s.repository.GetUser(userID)
}

// CreateUser creates a new user
func (s *MultiUserService) CreateUser(userID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData) error {
	return s.repository.CreateUser(userID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig)
}

// UpdateUser updates user information
func (s *MultiUserService) UpdateUser(userID, name string) error {
	return s.repository.UpdateUser(userID, name)
}

// UpdateUserConfig updates user configuration
func (s *MultiUserService) UpdateUserConfig(userID, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData) error {
	return s.repository.UpdateUserConfig(userID, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig)
}

// DeleteUser deletes a user
func (s *MultiUserService) DeleteUser(userID string) error {
	// Cancel any active sync for this user
	if err := s.CancelSync(userID); err != nil {
		s.logger.Warn("Failed to cancel sync during user deletion", map[string]interface{}{
			"userID": userID,
			"error": err,
		})
	}
	
	// Remove from status tracking
	s.statusMutex.Lock()
	delete(s.userStatuses, userID)
	s.statusMutex.Unlock()
	
	return s.repository.DeleteUser(userID)
}

// GetUserStatus returns the sync status for a user
func (s *MultiUserService) GetUserStatus(userID string) *UserSyncStatus {
	s.statusMutex.RLock()
	defer s.statusMutex.RUnlock()
	
	status, exists := s.userStatuses[userID]
	if !exists {
		// Initialize status if it doesn't exist
		user, err := s.repository.GetUser(userID)
		if err != nil {
			return &UserSyncStatus{
				UserID: userID,
				Status: "error",
				Error:  err.Error(),
			}
		}
		
		syncState, _ := s.repository.GetSyncState(userID)
		
		status = &UserSyncStatus{
			UserID:   userID,
			UserName: user.User.Name,
			Status:   "idle",
		}
		
		if syncState != nil && syncState.LastSync != nil {
			status.LastSync = syncState.LastSync
		}
		
		s.userStatuses[userID] = status
	}
	
	// Return a copy to prevent external modification
	statusCopy := *status
	return &statusCopy
}

// GetAllUserStatuses returns sync status for all users
func (s *MultiUserService) GetAllUserStatuses() map[string]*UserSyncStatus {
	users, err := s.repository.ListUsers()
	if err != nil {
		s.logger.Error("Failed to list users for status", map[string]interface{}{
			"error": err.Error(),
		})
		return make(map[string]*UserSyncStatus)
	}
	
	s.statusMutex.RLock()
	defer s.statusMutex.RUnlock()
	
	statuses := make(map[string]*UserSyncStatus)
	for _, user := range users {
		status := s.GetUserStatus(user.ID)
		statuses[user.ID] = status
	}
	
	return statuses
}

// StartSync starts a sync operation for a specific user
func (s *MultiUserService) StartSync(userID string) error {
	// Check if sync is already running
	s.syncMutex.RLock()
	if _, exists := s.activeSyncs[userID]; exists {
		s.syncMutex.RUnlock()
		return fmt.Errorf("sync already running for user: %s", userID)
	}
	s.syncMutex.RUnlock()
	
	// Get user configuration
	userConfig, err := s.repository.GetUser(userID)
	if err != nil {
		return fmt.Errorf("failed to get user config: %w", err)
	}
	
	// Update status to syncing
	s.updateUserStatus(userID, &UserSyncStatus{
		UserID:   userID,
		UserName: userConfig.User.Name,
		Status:   "syncing",
		Progress: "Starting sync...",
	})
	
	// Create context for cancellation
	ctx, cancel := context.WithCancel(context.Background())
	
	// Store cancel function
	s.syncMutex.Lock()
	s.activeSyncs[userID] = cancel
	s.syncMutex.Unlock()
	
	// Start sync in goroutine
	go s.performSync(ctx, userID, userConfig)
	
	return nil
}

// CancelSync cancels a running sync operation for a user
func (s *MultiUserService) CancelSync(userID string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	
	cancel, exists := s.activeSyncs[userID]
	if !exists {
		return fmt.Errorf("no active sync for user: %s", userID)
	}
	
	cancel()
	delete(s.activeSyncs, userID)
	
	// Update status
	s.updateUserStatus(userID, &UserSyncStatus{
		UserID: userID,
		Status: "idle",
		Error:  "Sync cancelled by user",
	})
	
	s.logger.Info("Cancelled sync for user", map[string]interface{}{
		"user_id": userID,
	})
	
	return nil
}

// performSync performs the actual sync operation for a user
func (s *MultiUserService) performSync(ctx context.Context, userID string, userConfig *database.UserWithTokens) {
	defer func() {
		// Clean up active sync tracking
		s.syncMutex.Lock()
		delete(s.activeSyncs, userID)
		s.syncMutex.Unlock()
	}()
	
	s.logger.Info("Starting sync for user", map[string]interface{}{
		"user_id":   userID,
		"user_name": userConfig.User.Name,
	})
	
	// Create user-specific config
	userSpecificConfig := s.createUserSpecificConfig(userConfig)
	
	// Create clients
	absClient := audiobookshelf.NewClient(userConfig.AudiobookshelfURL, userConfig.AudiobookshelfToken)
	hcClient := hardcover.NewClient(userConfig.HardcoverToken, s.logger)
	
	// Create sync service
	syncService, err := sync.NewService(absClient, hcClient, userSpecificConfig)
	if err != nil {
		s.updateUserStatus(userID, &UserSyncStatus{
			UserID: userID,
			Status: "error",
			Error:  fmt.Sprintf("Failed to create sync service: %v", err),
		})
		return
	}
	
	// Update status
	s.updateUserStatus(userID, &UserSyncStatus{
		UserID:   userID,
		UserName: userConfig.User.Name,
		Status:   "syncing",
		Progress: "Fetching books...",
	})
	
	// Perform sync
	err = syncService.Sync(ctx)
	if err != nil {
		if ctx.Err() == context.Canceled {
			s.updateUserStatus(userID, &UserSyncStatus{
				UserID: userID,
				Status: "idle",
				Error:  "Sync cancelled",
			})
		} else {
			s.updateUserStatus(userID, &UserSyncStatus{
				UserID: userID,
				Status: "error",
				Error:  err.Error(),
			})
		}
		s.logger.Error("Sync failed for user", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		return
	}
	
	// Update status to completed
	now := time.Now()
	s.updateUserStatus(userID, &UserSyncStatus{
		UserID:   userID,
		UserName: userConfig.User.Name,
		Status:   "completed",
		LastSync: &now,
	})
	
	// Update sync state in database
	if err := s.repository.UpdateSyncState(userID, "{}"); err != nil {
		s.logger.Error("Failed to update sync state", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
	}
	
	s.logger.Info("Sync completed for user", map[string]interface{}{
		"user_id":   userID,
		"user_name": userConfig.User.Name,
	})
}

// createUserSpecificConfig creates a config.Config instance for a specific user
func (s *MultiUserService) createUserSpecificConfig(userConfig *database.UserWithTokens) *config.Config {
	cfg := &config.Config{
		Server:    s.globalConfig.Server,
		RateLimit: s.globalConfig.RateLimit,
		Logging:   s.globalConfig.Logging,
		Paths:     s.globalConfig.Paths,
	}
	
	// Set user-specific configuration
	cfg.Audiobookshelf.URL = userConfig.AudiobookshelfURL
	cfg.Audiobookshelf.Token = userConfig.AudiobookshelfToken
	cfg.Hardcover.Token = userConfig.HardcoverToken
	
	// Parse sync interval
	if syncInterval, err := time.ParseDuration(userConfig.SyncConfig.SyncInterval); err == nil {
		cfg.App.SyncInterval = syncInterval
	} else {
		cfg.App.SyncInterval = s.globalConfig.App.SyncInterval
	}
	
	// Set sync configuration
	cfg.Sync.Incremental = userConfig.SyncConfig.Incremental
	cfg.Sync.StateFile = userConfig.SyncConfig.StateFile
	cfg.Sync.MinChangeThreshold = userConfig.SyncConfig.MinChangeThreshold
	cfg.Sync.Libraries.Include = userConfig.SyncConfig.Libraries.Include
	cfg.Sync.Libraries.Exclude = userConfig.SyncConfig.Libraries.Exclude
	
	// Set app configuration
	cfg.App.MinimumProgress = userConfig.SyncConfig.MinimumProgress
	cfg.App.SyncWantToRead = userConfig.SyncConfig.SyncWantToRead
	cfg.App.SyncOwned = userConfig.SyncConfig.SyncOwned
	cfg.App.DryRun = userConfig.SyncConfig.DryRun
	cfg.App.TestBookFilter = userConfig.SyncConfig.TestBookFilter
	cfg.App.TestBookLimit = userConfig.SyncConfig.TestBookLimit
	
	// Ensure user-specific state file path
	if cfg.Sync.StateFile == "" || cfg.Sync.StateFile == "./data/sync_state.json" {
		cfg.Sync.StateFile = fmt.Sprintf("./data/%s_sync_state.json", userConfig.User.ID)
	}
	
	return cfg
}

// updateUserStatus updates the status for a user
func (s *MultiUserService) updateUserStatus(userID string, status *UserSyncStatus) {
	s.statusMutex.Lock()
	defer s.statusMutex.Unlock()
	
	s.userStatuses[userID] = status
}

// IsUserSyncing checks if a user is currently syncing
func (s *MultiUserService) IsUserSyncing(userID string) bool {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()
	
	_, exists := s.activeSyncs[userID]
	return exists
}
