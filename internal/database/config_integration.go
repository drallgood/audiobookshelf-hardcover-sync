package database

import (
	"os"
	"strings"
)

// ConfigDatabase represents the database configuration from config.yaml
// This mirrors the structure in internal/config but avoids circular imports
type ConfigDatabase struct {
	Type           string `yaml:"type"`
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	Name           string `yaml:"name"`
	User           string `yaml:"user"`
	Password       string `yaml:"password"`
	Path           string `yaml:"path"`
	SSLMode        string `yaml:"ssl_mode"`
	ConnectionPool struct {
		MaxOpenConns    int `yaml:"max_open_conns"`
		MaxIdleConns    int `yaml:"max_idle_conns"`
		ConnMaxLifetime int `yaml:"conn_max_lifetime"`
	} `yaml:"connection_pool"`
}

// NewDatabaseConfigFromConfig creates a DatabaseConfig from the application config
// This function provides a clean interface for config.yaml integration
func NewDatabaseConfigFromConfig(configDB *ConfigDatabase) *DatabaseConfig {
	if configDB == nil {
		return GetDatabaseConfigFromEnv()
	}

	config := &DatabaseConfig{
		Type: DatabaseTypeSQLite, // Default fallback
		Path: GetDefaultDatabasePath(),
	}

	// Parse database type
	if configDB.Type != "" {
		switch strings.ToLower(configDB.Type) {
		case "postgresql", "postgres":
			config.Type = DatabaseTypePostgreSQL
		case "mysql":
			config.Type = DatabaseTypeMySQL
		case "mariadb":
			config.Type = DatabaseTypeMariaDB
		case "sqlite":
			config.Type = DatabaseTypeSQLite
		}
	}

	// Configure based on database type
	if config.Type != DatabaseTypeSQLite {
		// Non-SQLite database configuration
		config.Host = getStringWithFallback(configDB.Host, "localhost")
		config.Database = getStringWithFallback(configDB.Name, "audiobookshelf_sync")
		config.Username = getStringWithFallback(configDB.User, "sync_user")
		config.Password = configDB.Password
		config.SSLMode = getStringWithFallback(configDB.SSLMode, "prefer")

		// Set default ports based on database type
		if configDB.Port > 0 {
			config.Port = configDB.Port
		} else {
			switch config.Type {
			case DatabaseTypePostgreSQL:
				config.Port = 5432
			case DatabaseTypeMySQL, DatabaseTypeMariaDB:
				config.Port = 3306
			}
		}

		// Connection pool settings
		config.MaxOpenConns = getIntWithFallback(configDB.ConnectionPool.MaxOpenConns, 25)
		config.MaxIdleConns = getIntWithFallback(configDB.ConnectionPool.MaxIdleConns, 5)
		config.ConnMaxLifetime = getIntWithFallback(configDB.ConnectionPool.ConnMaxLifetime, 60)
	} else {
		// SQLite configuration
		if configDB.Path != "" {
			config.Path = configDB.Path
		}
	}

	// Environment variables take precedence over config file
	envConfig := GetDatabaseConfigFromEnv()
	
	// Only override if environment variable is explicitly set
	if envConfig.Type != DatabaseTypeSQLite || isEnvSet("DATABASE_TYPE") {
		config.Type = envConfig.Type
	}
	if isEnvSet("DATABASE_HOST") {
		config.Host = envConfig.Host
	}
	if isEnvSet("DATABASE_PORT") {
		config.Port = envConfig.Port
	}
	if isEnvSet("DATABASE_NAME") {
		config.Database = envConfig.Database
	}
	if isEnvSet("DATABASE_USER") {
		config.Username = envConfig.Username
	}
	if isEnvSet("DATABASE_PASSWORD") {
		config.Password = envConfig.Password
	}
	if isEnvSet("DATABASE_PATH") {
		config.Path = envConfig.Path
	}
	if isEnvSet("DATABASE_SSL_MODE") {
		config.SSLMode = envConfig.SSLMode
	}
	if isEnvSet("DATABASE_MAX_OPEN_CONNS") {
		config.MaxOpenConns = envConfig.MaxOpenConns
	}
	if isEnvSet("DATABASE_MAX_IDLE_CONNS") {
		config.MaxIdleConns = envConfig.MaxIdleConns
	}
	if isEnvSet("DATABASE_CONN_MAX_LIFETIME") {
		config.ConnMaxLifetime = envConfig.ConnMaxLifetime
	}

	return config
}

// Helper functions
func getStringWithFallback(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func getIntWithFallback(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func isEnvSet(key string) bool {
	_, exists := os.LookupEnv(key)
	return exists
}
