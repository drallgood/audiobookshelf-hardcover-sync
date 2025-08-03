package database

import (
	"fmt"
	"os"
	"strings"
)

// DatabaseType represents the supported database types
type DatabaseType string

const (
	DatabaseTypeSQLite     DatabaseType = "sqlite"
	DatabaseTypePostgreSQL DatabaseType = "postgresql"
	DatabaseTypeMySQL      DatabaseType = "mysql"
	DatabaseTypeMariaDB    DatabaseType = "mariadb"
)

// DatabaseConfig holds the configuration for database connections
type DatabaseConfig struct {
	Type     DatabaseType `json:"type" yaml:"type"`
	Host     string       `json:"host,omitempty" yaml:"host,omitempty"`
	Port     int          `json:"port,omitempty" yaml:"port,omitempty"`
	Database string       `json:"database,omitempty" yaml:"database,omitempty"`
	Username string       `json:"username,omitempty" yaml:"username,omitempty"`
	Password string       `json:"password,omitempty" yaml:"password,omitempty"`
	SSLMode  string       `json:"ssl_mode,omitempty" yaml:"ssl_mode,omitempty"`
	Path     string       `json:"path,omitempty" yaml:"path,omitempty"` // For SQLite
	
	// Connection pool settings
	MaxOpenConns    int `json:"max_open_conns,omitempty" yaml:"max_open_conns,omitempty"`
	MaxIdleConns    int `json:"max_idle_conns,omitempty" yaml:"max_idle_conns,omitempty"`
	ConnMaxLifetime int `json:"conn_max_lifetime,omitempty" yaml:"conn_max_lifetime,omitempty"` // in minutes
}

// GetDatabaseConfigFromEnv creates a database config from environment variables
func GetDatabaseConfigFromEnv() *DatabaseConfig {
	config := &DatabaseConfig{
		Type: DatabaseTypeSQLite, // Default fallback
		Path: GetDefaultDatabasePath(),
	}

	// Check for database type
	if dbType := os.Getenv("DATABASE_TYPE"); dbType != "" {
		switch strings.ToLower(dbType) {
		case "postgresql", "postgres":
			config.Type = DatabaseTypePostgreSQL
		case "mysql":
			config.Type = DatabaseTypeMySQL
		case "mariadb":
			config.Type = DatabaseTypeMariaDB
		case "sqlite":
			config.Type = DatabaseTypeSQLite
		default:
			// Invalid type, fallback to SQLite
			config.Type = DatabaseTypeSQLite
		}
	}

	// For non-SQLite databases, get connection parameters
	if config.Type != DatabaseTypeSQLite {
		config.Host = getEnvWithDefault("DATABASE_HOST", "localhost")
		config.Database = getEnvWithDefault("DATABASE_NAME", "audiobookshelf_sync")
		config.Username = getEnvWithDefault("DATABASE_USER", "")
		config.Password = os.Getenv("DATABASE_PASSWORD")
		config.SSLMode = getEnvWithDefault("DATABASE_SSL_MODE", "prefer")
		
		// Set default ports based on database type
		switch config.Type {
		case DatabaseTypePostgreSQL:
			config.Port = getEnvIntWithDefault("DATABASE_PORT", 5432)
		case DatabaseTypeMySQL, DatabaseTypeMariaDB:
			config.Port = getEnvIntWithDefault("DATABASE_PORT", 3306)
		}
		
		// Connection pool settings
		config.MaxOpenConns = getEnvIntWithDefault("DATABASE_MAX_OPEN_CONNS", 25)
		config.MaxIdleConns = getEnvIntWithDefault("DATABASE_MAX_IDLE_CONNS", 5)
		config.ConnMaxLifetime = getEnvIntWithDefault("DATABASE_CONN_MAX_LIFETIME", 60)
	}

	// For SQLite, allow custom path
	if config.Type == DatabaseTypeSQLite {
		if dbPath := os.Getenv("DATABASE_PATH"); dbPath != "" {
			config.Path = dbPath
		}
	}

	return config
}

// Validate checks if the database configuration is valid
func (c *DatabaseConfig) Validate() error {
	switch c.Type {
	case DatabaseTypeSQLite:
		if c.Path == "" {
			return fmt.Errorf("SQLite database path is required")
		}
	case DatabaseTypePostgreSQL, DatabaseTypeMySQL, DatabaseTypeMariaDB:
		if c.Host == "" {
			return fmt.Errorf("database host is required for %s", c.Type)
		}
		if c.Database == "" {
			return fmt.Errorf("database name is required for %s", c.Type)
		}
		if c.Port <= 0 {
			return fmt.Errorf("valid database port is required for %s", c.Type)
		}
	default:
		return fmt.Errorf("unsupported database type: %s", c.Type)
	}
	return nil
}

// GetDSN returns the data source name for the database connection
func (c *DatabaseConfig) GetDSN() string {
	switch c.Type {
	case DatabaseTypeSQLite:
		return c.Path
	case DatabaseTypePostgreSQL:
		dsn := fmt.Sprintf("host=%s port=%d dbname=%s sslmode=%s",
			c.Host, c.Port, c.Database, c.SSLMode)
		if c.Username != "" {
			dsn += fmt.Sprintf(" user=%s", c.Username)
		}
		if c.Password != "" {
			dsn += fmt.Sprintf(" password=%s", c.Password)
		}
		return dsn
	case DatabaseTypeMySQL, DatabaseTypeMariaDB:
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			c.Username, c.Password, c.Host, c.Port, c.Database)
		return dsn
	default:
		return ""
	}
}

// getEnvWithDefault gets an environment variable with a default value
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvIntWithDefault gets an integer environment variable with a default value
func getEnvIntWithDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue := parseInt(value); intValue > 0 {
			return intValue
		}
	}
	return defaultValue
}

// parseInt safely parses an integer string
func parseInt(s string) int {
	var result int
	for _, char := range s {
		if char >= '0' && char <= '9' {
			result = result*10 + int(char-'0')
		} else {
			return 0 // Invalid integer
		}
	}
	return result
}

// DatabaseConfigFromAppConfig converts application config to database config
// This allows using config.yaml for database configuration
func DatabaseConfigFromAppConfig(appConfig interface{}) *DatabaseConfig {
	// Use reflection to extract database config from app config
	// This is a safe way to handle the conversion without importing the config package
	if appConfig == nil {
		return GetDatabaseConfigFromEnv()
	}
	
	// Try to extract database configuration using interface{} and type assertions
	// This avoids circular imports between config and database packages
	if configMap, ok := appConfig.(map[string]interface{}); ok {
		if dbConfig, exists := configMap["database"]; exists {
			return parseDatabaseConfigFromMap(dbConfig)
		}
	}
	
	// Fallback to environment-based configuration
	return GetDatabaseConfigFromEnv()
}

// parseDatabaseConfigFromMap parses database config from a map[string]interface{}
func parseDatabaseConfigFromMap(dbConfigInterface interface{}) *DatabaseConfig {
	dbMap, ok := dbConfigInterface.(map[string]interface{})
	if !ok {
		return GetDatabaseConfigFromEnv()
	}
	
	config := &DatabaseConfig{
		Type: DatabaseTypeSQLite, // Default fallback
		Path: GetDefaultDatabasePath(),
	}
	
	// Parse database type
	if dbType, ok := dbMap["type"].(string); ok && dbType != "" {
		switch strings.ToLower(dbType) {
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
	
	// Parse connection parameters for non-SQLite databases
	if config.Type != DatabaseTypeSQLite {
		if host, ok := dbMap["host"].(string); ok {
			config.Host = host
		}
		if port, ok := dbMap["port"].(int); ok {
			config.Port = port
		}
		if name, ok := dbMap["name"].(string); ok {
			config.Database = name
		}
		if user, ok := dbMap["user"].(string); ok {
			config.Username = user
		}
		if password, ok := dbMap["password"].(string); ok {
			config.Password = password
		}
		if sslMode, ok := dbMap["ssl_mode"].(string); ok {
			config.SSLMode = sslMode
		}
		
		// Parse connection pool settings
		if poolConfig, ok := dbMap["connection_pool"].(map[string]interface{}); ok {
			if maxOpen, ok := poolConfig["max_open_conns"].(int); ok {
				config.MaxOpenConns = maxOpen
			}
			if maxIdle, ok := poolConfig["max_idle_conns"].(int); ok {
				config.MaxIdleConns = maxIdle
			}
			if lifetime, ok := poolConfig["conn_max_lifetime"].(int); ok {
				config.ConnMaxLifetime = lifetime
			}
		}
		
		// Set defaults if not specified
		if config.MaxOpenConns == 0 {
			config.MaxOpenConns = 25
		}
		if config.MaxIdleConns == 0 {
			config.MaxIdleConns = 5
		}
		if config.ConnMaxLifetime == 0 {
			config.ConnMaxLifetime = 60
		}
		if config.SSLMode == "" {
			config.SSLMode = "prefer"
		}
	}
	
	// Parse SQLite path
	if config.Type == DatabaseTypeSQLite {
		if path, ok := dbMap["path"].(string); ok && path != "" {
			config.Path = path
		}
	}
	
	// Override with environment variables if they exist (env takes precedence)
	envConfig := GetDatabaseConfigFromEnv()
	if os.Getenv("DATABASE_TYPE") != "" {
		config.Type = envConfig.Type
	}
	if os.Getenv("DATABASE_HOST") != "" {
		config.Host = envConfig.Host
	}
	if os.Getenv("DATABASE_PORT") != "" {
		config.Port = envConfig.Port
	}
	if os.Getenv("DATABASE_NAME") != "" {
		config.Database = envConfig.Database
	}
	if os.Getenv("DATABASE_USER") != "" {
		config.Username = envConfig.Username
	}
	if os.Getenv("DATABASE_PASSWORD") != "" {
		config.Password = envConfig.Password
	}
	if os.Getenv("DATABASE_PATH") != "" {
		config.Path = envConfig.Path
	}
	if os.Getenv("DATABASE_SSL_MODE") != "" {
		config.SSLMode = envConfig.SSLMode
	}
	
	return config
}
