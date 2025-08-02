package database

import (
	"encoding/json"
	"fmt"
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

// UserWithTokens represents a user with decrypted tokens
type UserWithTokens struct {
	User                User   `json:"user"`
	AudiobookshelfURL   string `json:"audiobookshelf_url"`
	AudiobookshelfToken string `json:"audiobookshelf_token"`
	HardcoverToken      string `json:"hardcover_token"`
	SyncConfig          SyncConfigData `json:"sync_config"`
}

// CreateUser creates a new user with encrypted configuration
func (r *Repository) CreateUser(userID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig SyncConfigData) error {
	// Encrypt tokens
	encryptedABSToken, err := r.encryptor.Encrypt(audiobookshelfToken)
	if err != nil {
		r.logger.Error("Failed to encrypt Audiobookshelf token", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		return fmt.Errorf("failed to encrypt Audiobookshelf token: %w", err)
	}

	encryptedHCToken, err := r.encryptor.Encrypt(hardcoverToken)
	if err != nil {
		r.logger.Error("Failed to encrypt Hardcover token", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		return fmt.Errorf("failed to encrypt Hardcover token: %w", err)
	}

	// Serialize sync config
	syncConfigJSON, err := json.Marshal(syncConfig)
	if err != nil {
		r.logger.Error("Failed to marshal sync config", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		return fmt.Errorf("failed to marshal sync config: %w", err)
	}

	// Create user and config in a transaction
	return r.db.GetDB().Transaction(func(tx *gorm.DB) error {
		// Create user
		user := User{
			ID:     userID,
			Name:   name,
			Active: true,
		}
		if err := tx.Create(&user).Error; err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}

		// Create user config
		config := UserConfig{
			UserID:                       userID,
			AudiobookshelfURL:            audiobookshelfURL,
			AudiobookshelfTokenEncrypted: encryptedABSToken,
			HardcoverTokenEncrypted:      encryptedHCToken,
			SyncConfig:                   string(syncConfigJSON),
		}
		if err := tx.Create(&config).Error; err != nil {
			return fmt.Errorf("failed to create user config: %w", err)
		}

		// Create empty sync state
		syncState := SyncState{
			UserID:    userID,
			StateData: "{}",
		}
		if err := tx.Create(&syncState).Error; err != nil {
			return fmt.Errorf("failed to create sync state: %w", err)
		}

		r.logger.Info("Created new user", map[string]interface{}{
			"user_id": userID,
			"name":    name,
		})

		return nil
	})
}

// GetUser retrieves a user by ID with decrypted tokens
func (r *Repository) GetUser(userID string) (*UserWithTokens, error) {
	var user User
	var config UserConfig

	// Get user with config
	if err := r.db.GetDB().Where("id = ? AND active = ?", userID, true).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found: %s", userID)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if err := r.db.GetDB().Where("user_id = ?", userID).First(&config).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user config not found: %s", userID)
		}
		return nil, fmt.Errorf("failed to get user config: %w", err)
	}

	// Decrypt tokens
	audiobookshelfToken, err := r.encryptor.Decrypt(config.AudiobookshelfTokenEncrypted)
	if err != nil {
		r.logger.Error("Failed to decrypt Audiobookshelf token", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		return nil, fmt.Errorf("failed to decrypt Audiobookshelf token: %w", err)
	}

	hardcoverToken, err := r.encryptor.Decrypt(config.HardcoverTokenEncrypted)
	if err != nil {
		r.logger.Error("Failed to decrypt Hardcover token", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		return nil, fmt.Errorf("failed to decrypt Hardcover token: %w", err)
	}

	// Parse sync config
	var syncConfig SyncConfigData
	if err := json.Unmarshal([]byte(config.SyncConfig), &syncConfig); err != nil {
		r.logger.Error("Failed to unmarshal sync config", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		return nil, fmt.Errorf("failed to unmarshal sync config: %w", err)
	}

	return &UserWithTokens{
		User:                user,
		AudiobookshelfURL:   config.AudiobookshelfURL,
		AudiobookshelfToken: audiobookshelfToken,
		HardcoverToken:      hardcoverToken,
		SyncConfig:          syncConfig,
	}, nil
}

// ListUsers retrieves all active users
func (r *Repository) ListUsers() ([]User, error) {
	var users []User
	if err := r.db.GetDB().Where("active = ?", true).Find(&users).Error; err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	return users, nil
}

// UpdateUser updates user information
func (r *Repository) UpdateUser(userID, name string) error {
	result := r.db.GetDB().Model(&User{}).Where("id = ?", userID).Updates(User{
		Name:      name,
		UpdatedAt: time.Now(),
	})
	if result.Error != nil {
		return fmt.Errorf("failed to update user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found: %s", userID)
	}

	r.logger.Info("Updated user", map[string]interface{}{
		"user_id": userID,
		"name":    name,
	})

	return nil
}

// UpdateUserConfig updates user configuration with encrypted tokens
func (r *Repository) UpdateUserConfig(userID, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig SyncConfigData) error {
	// Encrypt tokens
	encryptedABSToken, err := r.encryptor.Encrypt(audiobookshelfToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt Audiobookshelf token: %w", err)
	}

	encryptedHCToken, err := r.encryptor.Encrypt(hardcoverToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt Hardcover token: %w", err)
	}

	// Serialize sync config
	syncConfigJSON, err := json.Marshal(syncConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal sync config: %w", err)
	}

	result := r.db.GetDB().Model(&UserConfig{}).Where("user_id = ?", userID).Updates(UserConfig{
		AudiobookshelfURL:            audiobookshelfURL,
		AudiobookshelfTokenEncrypted: encryptedABSToken,
		HardcoverTokenEncrypted:      encryptedHCToken,
		SyncConfig:                   string(syncConfigJSON),
		UpdatedAt:                    time.Now(),
	})
	if result.Error != nil {
		return fmt.Errorf("failed to update user config: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user config not found: %s", userID)
	}

	r.logger.Info("Updated user config", map[string]interface{}{
		"user_id": userID,
	})

	return nil
}

// DeleteUser soft deletes a user by setting active to false
func (r *Repository) DeleteUser(userID string) error {
	result := r.db.GetDB().Model(&User{}).Where("id = ?", userID).Update("active", false)
	if result.Error != nil {
		return fmt.Errorf("failed to delete user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found: %s", userID)
	}

	r.logger.Info("Deleted user", map[string]interface{}{
		"user_id": userID,
	})

	return nil
}

// GetSyncState retrieves sync state for a user
func (r *Repository) GetSyncState(userID string) (*SyncState, error) {
	var syncState SyncState
	if err := r.db.GetDB().Where("user_id = ?", userID).First(&syncState).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("sync state not found: %s", userID)
		}
		return nil, fmt.Errorf("failed to get sync state: %w", err)
	}
	return &syncState, nil
}

// UpdateSyncState updates sync state for a user
func (r *Repository) UpdateSyncState(userID, stateData string) error {
	now := time.Now()
	result := r.db.GetDB().Model(&SyncState{}).Where("user_id = ?", userID).Updates(SyncState{
		StateData: stateData,
		LastSync:  &now,
		UpdatedAt: now,
	})
	if result.Error != nil {
		return fmt.Errorf("failed to update sync state: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("sync state not found: %s", userID)
	}

	r.logger.Debug("Updated sync state", map[string]interface{}{
		"user_id": userID,
	})

	return nil
}

// UserExists checks if a user exists and is active
func (r *Repository) UserExists(userID string) (bool, error) {
	var count int64
	if err := r.db.GetDB().Model(&User{}).Where("id = ? AND active = ?", userID, true).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed to check user existence: %w", err)
	}
	return count > 0, nil
}
