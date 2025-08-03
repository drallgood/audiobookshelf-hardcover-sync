package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	appLogger "github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// DatabaseDriver interface defines the contract for database drivers
type DatabaseDriver interface {
	Connect(config *DatabaseConfig, log *appLogger.Logger) (*gorm.DB, error)
	GetDialector(config *DatabaseConfig) gorm.Dialector
	PrepareDatabase(config *DatabaseConfig) error
	GetMigrationOptions() *gorm.Config
}

// SQLiteDriver implements DatabaseDriver for SQLite
type SQLiteDriver struct{}

func (d *SQLiteDriver) Connect(config *DatabaseConfig, log *appLogger.Logger) (*gorm.DB, error) {
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

	// Open database connection
	db, err := gorm.Open(d.GetDialector(config), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SQLite database: %w", err)
	}

	// Configure connection pool for SQLite
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// SQLite specific settings
	sqlDB.SetMaxOpenConns(1) // SQLite doesn't support concurrent writes
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

func (d *SQLiteDriver) GetDialector(config *DatabaseConfig) gorm.Dialector {
	return sqlite.Open(config.Path)
}

func (d *SQLiteDriver) PrepareDatabase(config *DatabaseConfig) error {
	// Ensure the directory exists
	dir := filepath.Dir(config.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}
	return nil
}

func (d *SQLiteDriver) GetMigrationOptions() *gorm.Config {
	return &gorm.Config{}
}

// PostgreSQLDriver implements DatabaseDriver for PostgreSQL
type PostgreSQLDriver struct{}

func (d *PostgreSQLDriver) Connect(config *DatabaseConfig, log *appLogger.Logger) (*gorm.DB, error) {
	// Configure GORM logger
	gormLogger := logger.Default.LogMode(logger.Silent)
	if log != nil {
		gormLogger = logger.Default.LogMode(logger.Silent)
	}

	// Open database connection
	db, err := gorm.Open(d.GetDialector(config), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL database: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Set connection pool settings
	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(config.ConnMaxLifetime) * time.Minute)

	return db, nil
}

func (d *PostgreSQLDriver) GetDialector(config *DatabaseConfig) gorm.Dialector {
	return postgres.Open(config.GetDSN())
}

func (d *PostgreSQLDriver) PrepareDatabase(config *DatabaseConfig) error {
	// PostgreSQL databases are typically created externally
	// This could be extended to auto-create databases if needed
	return nil
}

func (d *PostgreSQLDriver) GetMigrationOptions() *gorm.Config {
	return &gorm.Config{}
}

// MySQLDriver implements DatabaseDriver for MySQL/MariaDB
type MySQLDriver struct{}

func (d *MySQLDriver) Connect(config *DatabaseConfig, log *appLogger.Logger) (*gorm.DB, error) {
	// Configure GORM logger
	gormLogger := logger.Default.LogMode(logger.Silent)
	if log != nil {
		gormLogger = logger.Default.LogMode(logger.Silent)
	}

	// Open database connection
	db, err := gorm.Open(d.GetDialector(config), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL database: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Set connection pool settings
	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(config.ConnMaxLifetime) * time.Minute)

	return db, nil
}

func (d *MySQLDriver) GetDialector(config *DatabaseConfig) gorm.Dialector {
	return mysql.Open(config.GetDSN())
}

func (d *MySQLDriver) PrepareDatabase(config *DatabaseConfig) error {
	// MySQL databases are typically created externally
	// This could be extended to auto-create databases if needed
	return nil
}

func (d *MySQLDriver) GetMigrationOptions() *gorm.Config {
	return &gorm.Config{}
}

// GetDatabaseDriver returns the appropriate driver for the given database type
func GetDatabaseDriver(dbType DatabaseType) (DatabaseDriver, error) {
	switch dbType {
	case DatabaseTypeSQLite:
		return &SQLiteDriver{}, nil
	case DatabaseTypePostgreSQL:
		return &PostgreSQLDriver{}, nil
	case DatabaseTypeMySQL, DatabaseTypeMariaDB:
		return &MySQLDriver{}, nil
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}
}

// ConnectWithFallback attempts to connect to the configured database,
// falling back to SQLite if the connection fails
func ConnectWithFallback(config *DatabaseConfig, log *appLogger.Logger) (*gorm.DB, *DatabaseConfig, error) {
	// Validate the primary configuration
	if err := config.Validate(); err != nil {
		if log != nil {
			log.Warn("Invalid database configuration, falling back to SQLite", map[string]interface{}{
				"error": err.Error(),
				"type":  config.Type,
			})
		}
		return connectSQLiteFallback(log)
	}

	// Try to connect with the configured database
	driver, err := GetDatabaseDriver(config.Type)
	if err != nil {
		if log != nil {
			log.Warn("Unsupported database type, falling back to SQLite", map[string]interface{}{
				"error": err.Error(),
				"type":  config.Type,
			})
		}
		return connectSQLiteFallback(log)
	}

	db, err := driver.Connect(config, log)
	if err != nil {
		if log != nil {
			log.Warn("Failed to connect to configured database, falling back to SQLite", map[string]interface{}{
				"error": err.Error(),
				"type":  config.Type,
				"host":  config.Host,
			})
		}
		return connectSQLiteFallback(log)
	}

	if log != nil {
		log.Info("Successfully connected to database", map[string]interface{}{
			"type": config.Type,
			"host": config.Host,
		})
	}

	return db, config, nil
}

// connectSQLiteFallback creates a fallback SQLite connection
func connectSQLiteFallback(log *appLogger.Logger) (*gorm.DB, *DatabaseConfig, error) {
	fallbackConfig := &DatabaseConfig{
		Type: DatabaseTypeSQLite,
		Path: GetDefaultDatabasePath(),
	}

	driver := &SQLiteDriver{}
	db, err := driver.Connect(fallbackConfig, log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to fallback SQLite database: %w", err)
	}

	if log != nil {
		log.Info("Connected to fallback SQLite database", map[string]interface{}{
			"path": fallbackConfig.Path,
		})
	}

	return db, fallbackConfig, nil
}
