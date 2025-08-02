package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// MigrationManager handles migration from single-user config to multi-user database
type MigrationManager struct {
	repository *Repository
	logger     *logger.Logger
}

// NewMigrationManager creates a new migration manager
func NewMigrationManager(repo *Repository, log *logger.Logger) *MigrationManager {
	return &MigrationManager{
		repository: repo,
		logger:     log,
	}
}

// MigrateFromSingleUserConfig migrates from single-user config file to multi-user database
func (m *MigrationManager) MigrateFromSingleUserConfig(configPath string) error {
	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		m.logger.Info("No existing config file found, skipping migration", map[string]interface{}{
			"config_path": configPath,
		})
		return nil
	}

	// Load existing config
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load existing config: %w", err)
	}

	// Check if we already have users in the database
	users, err := m.repository.ListUsers()
	if err != nil {
		return fmt.Errorf("failed to check existing users: %w", err)
	}

	if len(users) > 0 {
		m.logger.Info("Users already exist in database, skipping migration", map[string]interface{}{
			"user_count": len(users),
		})
		return nil
	}

	// Create default user from single-user config
	userID := "default"
	userName := "Default User"

	// Convert config to sync config data
	syncConfig := SyncConfigData{
		Incremental:        cfg.Sync.Incremental,
		StateFile:          cfg.Sync.StateFile,
		MinChangeThreshold: cfg.Sync.MinChangeThreshold,
		Libraries: struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		}{
			Include: cfg.Sync.Libraries.Include,
			Exclude: cfg.Sync.Libraries.Exclude,
		},
		SyncInterval:    cfg.App.SyncInterval.String(),
		MinimumProgress: cfg.App.MinimumProgress,
		SyncWantToRead:  cfg.App.SyncWantToRead,
		SyncOwned:       cfg.App.SyncOwned,
		DryRun:          cfg.App.DryRun,
		TestBookFilter:  cfg.App.TestBookFilter,
		TestBookLimit:   cfg.App.TestBookLimit,
	}

	// Create user in database
	err = m.repository.CreateUser(
		userID,
		userName,
		cfg.Audiobookshelf.URL,
		cfg.Audiobookshelf.Token,
		cfg.Hardcover.Token,
		syncConfig,
	)
	if err != nil {
		return fmt.Errorf("failed to create default user: %w", err)
	}

	// Backup original config file
	backupPath := configPath + ".backup." + time.Now().Format("20060102-150405")
	if err := copyFile(configPath, backupPath); err != nil {
		m.logger.Warn("Failed to backup original config file", map[string]interface{}{
			"original_path": configPath,
			"backup_path":  backupPath,
			"error":        err.Error(),
		})
	} else {
		m.logger.Info("Backed up original config file", map[string]interface{}{
			"original_path": configPath,
			"backup_path":  backupPath,
		})
	}

	m.logger.Info("Successfully migrated single-user config to multi-user database", map[string]interface{}{
		"user_id":     userID,
		"user_name":   userName,
		"config_path": configPath,
		"backup_path": backupPath,
	})

	return nil
}

// CheckMigrationNeeded checks if migration from single-user config is needed
func (m *MigrationManager) CheckMigrationNeeded(configPath string) (bool, error) {
	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return false, nil
	}

	// Check if we already have users in the database
	users, err := m.repository.ListUsers()
	if err != nil {
		return false, fmt.Errorf("failed to check existing users: %w", err)
	}

	// Migration needed if config file exists but no users in database
	return len(users) == 0, nil
}

// AutoMigrate performs automatic migration if needed
func AutoMigrate(dbPath, configPath string, log *logger.Logger) error {
	// Create database connection
	db, err := NewDatabase(dbPath, log)
	if err != nil {
		return fmt.Errorf("failed to create database connection: %w", err)
	}
	defer db.Close()

	// Create encryption manager
	encryptor, err := crypto.NewEncryptionManager(log)
	if err != nil {
		return fmt.Errorf("failed to create encryption manager: %w", err)
	}

	// Create repository
	repo := NewRepository(db, encryptor, log)

	// Create migration manager
	migrationManager := NewMigrationManager(repo, log)

	// Check if migration is needed
	needed, err := migrationManager.CheckMigrationNeeded(configPath)
	if err != nil {
		return fmt.Errorf("failed to check migration status: %w", err)
	}

	if !needed {
		log.Debug("Migration not needed", nil)
		return nil
	}

	// Perform migration
	if err := migrationManager.MigrateFromSingleUserConfig(configPath); err != nil {
		return fmt.Errorf("failed to migrate from single-user config: %w", err)
	}

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Read source file
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}

	// Write destination file
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("failed to write destination file: %w", err)
	}

	return nil
}

// GetDefaultConfigPath returns the default path for the config file
func GetDefaultConfigPath() string {
	// Check common config file locations
	candidates := []string{
		"config.yaml",
		"config.yml",
		"./config/config.yaml",
		"./config/config.yml",
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Return default if none found
	return "config.yaml"
}
