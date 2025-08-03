package database

import (
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/gorm"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	appLogger "github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Database wraps the GORM database connection
type Database struct {
	db     *gorm.DB
	config *DatabaseConfig
	logger *appLogger.Logger
}

// NewDatabase creates a new database connection using the provided configuration
func NewDatabase(config *DatabaseConfig, log *appLogger.Logger) (*Database, error) {
	if config == nil {
		// Use environment-based configuration with SQLite fallback
		config = GetDatabaseConfigFromEnv()
	}

	// Connect with fallback to SQLite
	db, actualConfig, err := ConnectWithFallback(config, log)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Create database instance
	database := &Database{
		db:     db,
		config: actualConfig,
		logger: log,
	}

	// Run migrations
	if err := database.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	if log != nil {
		log.Info("Database connection established", map[string]interface{}{
			"type": actualConfig.Type,
			"path": actualConfig.Path,
			"host": actualConfig.Host,
		})
	}

	return database, nil
}

// migrate runs database migrations
func (d *Database) migrate() error {
	if d.logger != nil {
		d.logger.Info("Running database migrations", nil)
	}

	// Auto-migrate the schema
	err := d.db.AutoMigrate(
		&User{},
		&UserConfig{},
		&SyncState{},
		&auth.AuthUser{},
		&auth.AuthSession{},
		&auth.AuthProvider{},
	)
	if err != nil {
		return fmt.Errorf("failed to auto-migrate: %w", err)
	}

	if d.logger != nil {
		d.logger.Info("Database migrations completed successfully", nil)
	}

	return nil
}

// Close closes the database connection
func (d *Database) Close() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	if d.logger != nil {
		d.logger.Info("Database connection closed", nil)
	}

	return nil
}

// GetDB returns the underlying GORM database instance
func (d *Database) GetDB() *gorm.DB {
	return d.db
}

// Health checks the database connection
func (d *Database) Health() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	return nil
}

// GetDefaultDatabasePath returns the default path for the database file
func GetDefaultDatabasePath() string {
	// First check for explicit database path
	if dbPath := os.Getenv("DATABASE_PATH"); dbPath != "" {
		return dbPath
	}

	// Then use DATA_DIR or fallback directories
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		// Use the same directory detection as getDataDirForFallback
		if _, err := os.Stat("/data"); err == nil {
			dataDir = "/data"
		} else if _, err := os.Stat("/app/data"); err == nil {
			dataDir = "/app/data"
		} else {
			dataDir = "./data"
		}
	}

	// Ensure the db subdirectory exists
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		// If we can't create the directory, just use dataDir directly
		return filepath.Join(dataDir, "audiobookshelf-hardcover-sync.db")
	}

	return filepath.Join(dbDir, "audiobookshelf-hardcover-sync.db")
}
