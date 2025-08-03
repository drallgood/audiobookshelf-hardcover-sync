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

	// Check if we already have sync users in the database (not auth users)
	users, err := m.repository.ListUsers()
	if err != nil {
		return fmt.Errorf("failed to check existing sync users: %w", err)
	}

	// Check if users have complete configurations (both user and config)
	if len(users) > 0 {
		// Verify that users have proper configurations
		for _, user := range users {
			userWithTokens, err := m.repository.GetUser(user.ID)
			if err != nil || userWithTokens.AudiobookshelfURL == "" {
				m.logger.Warn("Found user without complete configuration, will recreate", map[string]interface{}{
					"user_id": user.ID,
					"error": err,
				})
				// Delete incomplete user and continue with migration
				if deleteErr := m.repository.DeleteUser(user.ID); deleteErr != nil {
					m.logger.Error("Failed to delete incomplete user", map[string]interface{}{
						"user_id": user.ID,
						"error": deleteErr.Error(),
					})
				}
				break // Continue with migration
			}
		}
		
		// If we get here, all users have complete configurations
		m.logger.Info("Sync users with complete configurations already exist, skipping migration", map[string]interface{}{
			"sync_user_count": len(users),
		})
		return nil
	}

	m.logger.Info("Creating default sync user from config.yaml", map[string]interface{}{
		"config_path": configPath,
	})

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

	// Check if we already have sync users in the database (not auth users)
	users, err := m.repository.ListUsers()
	if err != nil {
		return false, fmt.Errorf("failed to check existing sync users: %w", err)
	}

	// Migration needed if config file exists but no sync users in database
	// (auth users are separate from sync users)
	return len(users) == 0, nil
}

// AutoMigrate performs automatic migration if needed
func AutoMigrate(dbPath, configPath string, log *logger.Logger) error {
	// Create database configuration using environment variables by default
	dbConfig := GetDatabaseConfigFromEnv()

	// Try to load the config file to override with any database settings
	cfg, err := config.Load(configPath)
	if err == nil && cfg != nil {
		// Override with config file settings if available
		if cfg.Database.Type != "" {
			dbConfig.Type = DatabaseType(cfg.Database.Type)
		}
		if cfg.Database.Host != "" {
			dbConfig.Host = cfg.Database.Host
		}
		if cfg.Database.Port != 0 {
			dbConfig.Port = cfg.Database.Port
		}
		if cfg.Database.Name != "" {
			dbConfig.Database = cfg.Database.Name
		}
		if cfg.Database.User != "" {
			dbConfig.Username = cfg.Database.User
		}
		if cfg.Database.Password != "" {
			dbConfig.Password = cfg.Database.Password
		}
		if cfg.Database.Path != "" {
			dbConfig.Path = cfg.Database.Path
		}
		if cfg.Database.SSLMode != "" {
			dbConfig.SSLMode = cfg.Database.SSLMode
		}
	}

	// If a specific dbPath was provided, use it (for backward compatibility)
	if dbPath != "" {
		dbConfig.Path = dbPath
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
