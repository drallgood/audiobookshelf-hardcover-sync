package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	appLogger "github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	
	// Pure Go SQLite driver (no CGO required)
	"gorm.io/driver/sqlite"
	_ "modernc.org/sqlite"
)

// PureSQLiteDriver implements DatabaseDriver for SQLite using pure Go implementation
type PureSQLiteDriver struct{}

func (d *PureSQLiteDriver) Connect(config *DatabaseConfig, log *appLogger.Logger) (*gorm.DB, error) {
	// Ensure the directory exists for SQLite
	if err := d.PrepareDatabase(config); err != nil {
		return nil, err
	}

	// Configure GORM logger
	gormLogger := logger.Default.LogMode(logger.Silent)
	if log != nil {
		// We'll handle logging ourselves
		gormLogger = logger.Default.LogMode(logger.Silent)
	}

	// Open database connection with pure Go SQLite driver
	db, err := gorm.Open(d.GetDialector(config), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SQLite database (pure Go): %w", err)
	}

	// Configure connection pool for SQLite
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// SQLite specific settings
	sqlDB.SetMaxOpenConns(1) // SQLite works best with single connection
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// Enable WAL mode and other optimizations for SQLite
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		log.Warn("Failed to enable WAL mode", map[string]interface{}{
			"error": err.Error(),
		})
	}
	if err := db.Exec("PRAGMA synchronous=NORMAL").Error; err != nil {
		log.Warn("Failed to set synchronous mode", map[string]interface{}{
			"error": err.Error(),
		})
	}
	if err := db.Exec("PRAGMA foreign_keys=ON").Error; err != nil {
		log.Warn("Failed to enable foreign keys", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return db, nil
}

func (d *PureSQLiteDriver) GetDialector(config *DatabaseConfig) gorm.Dialector {
	// Use pure Go SQLite driver by specifying the driver name
	return sqlite.Dialector{
		DriverName: "sqlite", // This will use modernc.org/sqlite when imported
		DSN:        config.Path,
	}
}

func (d *PureSQLiteDriver) PrepareDatabase(config *DatabaseConfig) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(config.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}
	return nil
}

func (d *PureSQLiteDriver) GetMigrationOptions() *gorm.Config {
	return &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: false,
	}
}
