package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Repository provides database operations for users and configurations
type Repository struct {
	db        *Database
	encryptor *crypto.EncryptionManager
	logger    *logger.Logger
}

// NewRepository creates a new repository instance
func NewRepository(db *Database, encryptor *crypto.EncryptionManager, log *logger.Logger) *Repository {
	return &Repository{
		db:        db,
		encryptor: encryptor,
		logger:    log,
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

// CreateProfile creates a new sync profile with encrypted configuration
func (r *Repository) CreateProfile(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig SyncConfigData) error {
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
		syncState := ProfileSyncState{
			ProfileID: profileID,
			StateData: "{}",
		}
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

// ListProfiles retrieves all active sync profiles
func (r *Repository) ListProfiles() ([]SyncProfile, error) {
	var profiles []SyncProfile
	if err := r.db.GetDB().Preload("Config").Preload("SyncState").Where("active = ?", true).Find(&profiles).Error; err != nil {
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
			// Return default state if not found
			now := time.Now()
			return &ProfileSyncState{
				ProfileID: profileID,
				StateData: "{}",
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		}
		return nil, fmt.Errorf("failed to get sync state: %w", err)
	}
	return &state, nil
}

// UpdateSyncState updates the sync state for a sync profile
func (r *Repository) UpdateSyncState(state *ProfileSyncState) error {
	state.UpdatedAt = time.Now()

	// Check if state exists
	var existingState ProfileSyncState
	result := r.db.GetDB().Where("profile_id = ?", state.ProfileID).First(&existingState)

	if result.Error == nil {
		// Update existing state - use the existing CreatedAt
		state.CreatedAt = existingState.CreatedAt

		// Update the existing record
		if err := r.db.GetDB().Model(&existingState).Updates(state).Error; err != nil {
			r.logger.Error("Failed to update sync state", map[string]interface{}{
				"profile_id": state.ProfileID,
				"error":      err.Error(),
			})
			return fmt.Errorf("failed to update sync state: %w", err)
		}
	} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Create new state
		state.CreatedAt = time.Now()

		if err := r.db.GetDB().Create(state).Error; err != nil {
			r.logger.Error("Failed to create sync state", map[string]interface{}{
				"profile_id": state.ProfileID,
				"error":      err.Error(),
			})
			return fmt.Errorf("failed to create sync state: %w", err)
		}
	} else {
		return fmt.Errorf("failed to check for existing sync state: %w", result.Error)
	}

	r.logger.Info("Updated sync state", map[string]interface{}{
		"profile_id": state.ProfileID,
	})

	return nil
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
