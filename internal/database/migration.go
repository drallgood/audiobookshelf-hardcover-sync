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

	// Check if we already have sync profiles in the database (not auth users)
	profiles, err := m.repository.ListProfiles()
	if err != nil {
		return fmt.Errorf("failed to check existing sync profiles: %w", err)
	}

	// Check if profiles have complete configurations (both profile and config)
	if len(profiles) > 0 {
		// Verify that profiles have proper configurations
		for _, profile := range profiles {
			profileWithTokens, err := m.repository.GetProfile(profile.ID)
			if err != nil || profileWithTokens.AudiobookshelfURL == "" {
				m.logger.Warn("Found profile without complete configuration, will recreate", map[string]interface{}{
					"profile_id": profile.ID,
					"error":      err,
				})
				// Delete incomplete profile and continue with migration
				if deleteErr := m.repository.DeleteProfile(profile.ID); deleteErr != nil {
					m.logger.Error("Failed to delete incomplete profile", map[string]interface{}{
						"profile_id": profile.ID,
						"error":      deleteErr.Error(),
					})
				}
				break // Continue with migration
			}
		}

		// If we get here, all profiles have complete configurations
		m.logger.Info("Sync profiles with complete configurations already exist, skipping migration", map[string]interface{}{
			"sync_profile_count": len(profiles),
		})
		return nil
	}

	m.logger.Info("Creating default sync user from config.yaml", map[string]interface{}{
		"config_path": configPath,
	})

	// Create default profile from single-user config
	profileID := "default"
	profileName := "Default Profile"

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
		SyncInterval:    cfg.Sync.SyncInterval.String(),
		MinimumProgress: cfg.Sync.MinimumProgress,
		SyncWantToRead:  cfg.Sync.SyncWantToRead,
		SyncOwned:       cfg.Sync.SyncOwned,
		IncludeEbooks:   cfg.Sync.IncludeEbooks,
		DryRun:          cfg.Sync.DryRun,
		TestBookFilter:  cfg.App.TestBookFilter,
		TestBookLimit:   cfg.App.TestBookLimit,
	}

	// Create profile in database
	err = m.repository.CreateProfile(
		profileID,
		profileName,
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
			"backup_path":   backupPath,
			"error":         err.Error(),
		})
	} else {
		m.logger.Info("Backed up original config file", map[string]interface{}{
			"original_path": configPath,
			"backup_path":   backupPath,
		})
	}

	m.logger.Info("Successfully migrated single-user config to multi-profile database", map[string]interface{}{
		"profile_id":   profileID,
		"profile_name": profileName,
		"config_path":  configPath,
		"backup_path":  backupPath,
	})

	return nil
}

// CheckMigrationNeeded checks if migration from single-user config is needed
func (m *MigrationManager) CheckMigrationNeeded(configPath string) (bool, error) {
	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return false, nil
	}

	// Check if we already have sync profiles in the database (not auth users)
	profiles, err := m.repository.ListProfiles()
	if err != nil {
		return false, fmt.Errorf("failed to check existing sync profiles: %w", err)
	}

	// Migration needed if config file exists but no sync profiles in database
	// (auth users are separate from sync profiles)
	return len(profiles) == 0, nil
}

// AutoMigrate performs automatic migration if needed
// Uses the provided database configuration to ensure consistency with main application
func AutoMigrate(dbConfig *DatabaseConfig, configPath string, log *logger.Logger) error {
	if dbConfig == nil {
		return fmt.Errorf("database configuration is required for migration")
	}

	log.Info("Using database for migration", map[string]interface{}{
		"path": dbConfig.Path,
		"type": dbConfig.Type,
	})

	// Create database connection
	db, err := NewDatabase(dbConfig, log)
	if err != nil {
		return fmt.Errorf("failed to create database connection: %w", err)
	}
	defer db.Close()

	// Determine data directory for encryption key based on database configuration
	encryptionDataDir := ""
	if dbConfig != nil && dbConfig.Type == DatabaseTypeSQLite && dbConfig.Path != "" {
		encryptionDataDir = filepath.Dir(dbConfig.Path)
	}

	// Create encryption manager
	encryptor, err := crypto.NewEncryptionManagerWithDataDir(encryptionDataDir, log)
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
