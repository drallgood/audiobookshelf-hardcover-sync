package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	appLogger "github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Database wraps the GORM database connection
type Database struct {
	db     *gorm.DB
	logger *appLogger.Logger
}

// NewDatabase creates a new database connection
func NewDatabase(dbPath string, log *appLogger.Logger) (*Database, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Configure GORM logger
	gormLogger := logger.Default
	if log != nil {
		// Set GORM log level based on our logger level
		gormLogger = logger.Default.LogMode(logger.Silent) // We'll handle logging ourselves
	}

	// Open database connection
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// SQLite specific settings
	sqlDB.SetMaxOpenConns(1) // SQLite only supports one writer at a time
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	database := &Database{
		db:     db,
		logger: log,
	}

	// Run migrations
	if err := database.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	if log != nil {
		log.Info("Database connection established", map[string]interface{}{
			"path": dbPath,
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
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	return filepath.Join(dataDir, "audiobookshelf-sync.db")
}
