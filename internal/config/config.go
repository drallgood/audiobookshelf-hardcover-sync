package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the application
type Config struct {
	// Server configuration
	Server struct {
		Port            string        `yaml:"port" env:"PORT"`
		ShutdownTimeout time.Duration `yaml:"shutdown_timeout" env:"SHUTDOWN_TIMEOUT"`
	} `yaml:"server"`

	// Sync configuration
	Sync struct {
		// Enable incremental sync (only process changed books)
		Incremental bool `yaml:"incremental" env:"SYNC_INCREMENTAL"`
		// Path to store sync state (default: ./data/sync_state.json)
		StateFile string `yaml:"state_file" env:"SYNC_STATE_FILE"`
		// Minimum change in progress (seconds) to trigger an update (default: 60)
		MinChangeThreshold int `yaml:"min_change_threshold" env:"SYNC_MIN_CHANGE_THRESHOLD"`
		// Library filtering configuration
		Libraries struct {
			// Include only these libraries (by name or ID). If specified, only these libraries will be synced.
			Include []string `yaml:"include" env:"SYNC_LIBRARIES_INCLUDE"`
			// Exclude these libraries (by name or ID) from sync. Include takes precedence over exclude.
			Exclude []string `yaml:"exclude" env:"SYNC_LIBRARIES_EXCLUDE"`
		} `yaml:"libraries"`
		// SyncInterval is the interval between syncs (e.g., "1h", "30m")
		SyncInterval time.Duration `yaml:"sync_interval" env:"SYNC_INTERVAL"`
		// MinimumProgress is the minimum progress threshold for syncing (0.0 to 1.0)
		MinimumProgress float64 `yaml:"minimum_progress" env:"MINIMUM_PROGRESS"`
		// SyncWantToRead syncs books with 0% progress as "Want to Read"
		SyncWantToRead bool `yaml:"sync_want_to_read" env:"SYNC_WANT_TO_READ"`
		// SyncOwned marks synced books as owned in Hardcover
		SyncOwned bool `yaml:"sync_owned" env:"SYNC_OWNED"`
		// DryRun enables dry run mode (no changes will be made)
		DryRun bool `yaml:"dry_run" env:"DRY_RUN"`
	} `yaml:"sync"`

	// Rate limiting configuration
	RateLimit struct {
		// Rate is the minimum time between requests (e.g., 2s for 1 request per 2 seconds)
		Rate time.Duration `yaml:"rate" env:"RATE_LIMIT_RATE"`
		// Burst is the maximum number of requests that can be made in a burst
		Burst int `yaml:"burst" env:"RATE_LIMIT_BURST"`
		// MaxConcurrent is the maximum number of concurrent requests
		MaxConcurrent int `yaml:"max_concurrent" env:"RATE_LIMIT_MAX_CONCURRENT"`
	} `yaml:"rate_limit"`

	// Logging configuration
	Logging struct {
		// Level is the minimum log level (debug, info, warn, error, fatal, panic)
		Level string `yaml:"level" env:"LOG_LEVEL"`
		// Format is the log format (json, console)
		Format string `yaml:"format" env:"LOG_FORMAT"`
	} `yaml:"logging"`

	// Audiobookshelf configuration
	Audiobookshelf struct {
		// URL is the base URL of the Audiobookshelf server
		URL string `yaml:"url" env:"AUDIOBOOKSHELF_URL"`
		// Token is the API token for Audiobookshelf
		Token string `yaml:"token" env:"AUDIOBOOKSHELF_TOKEN"`
	} `yaml:"audiobookshelf"`

	// Hardcover configuration
	Hardcover struct {
		// Token is the API token for Hardcover
		Token string `yaml:"token" env:"HARDCOVER_TOKEN"`
	} `yaml:"hardcover"`

	// Application settings
	App struct {
		// TestBookFilter filters books by title for testing
		TestBookFilter string `yaml:"test_book_filter" env:"TEST_BOOK_FILTER"`
		// TestBookLimit limits the number of books to process for testing
		TestBookLimit int `yaml:"test_book_limit" env:"TEST_BOOK_LIMIT"`
		
		// Deprecated: Moved to Sync section
		SyncInterval time.Duration `yaml:"sync_interval,omitempty" env:"-"`
		// Deprecated: Moved to Sync section
		MinimumProgress float64 `yaml:"minimum_progress,omitempty" env:"-"`
		// Deprecated: Moved to Sync section
		SyncWantToRead bool `yaml:"sync_want_to_read,omitempty" env:"-"`
		// Deprecated: Moved to Sync section
		SyncOwned bool `yaml:"sync_owned,omitempty" env:"-"`
		// Deprecated: Moved to Sync section
		DryRun bool `yaml:"dry_run,omitempty" env:"-"`
	} `yaml:"app"`

	// Database configuration
	Database struct {
		// Type specifies the database type (sqlite, postgresql, mysql, mariadb)
		Type string `yaml:"type" env:"DATABASE_TYPE"`
		// Host is the database server hostname (for non-SQLite databases)
		Host string `yaml:"host" env:"DATABASE_HOST"`
		// Port is the database server port (for non-SQLite databases)
		Port int `yaml:"port" env:"DATABASE_PORT"`
		// Name is the database name (for non-SQLite databases)
		Name string `yaml:"name" env:"DATABASE_NAME"`
		// User is the database username (for non-SQLite databases)
		User string `yaml:"user" env:"DATABASE_USER"`
		// Password is the database password (for non-SQLite databases)
		Password string `yaml:"password" env:"DATABASE_PASSWORD"`
		// Path is the SQLite database file path (for SQLite only)
		Path string `yaml:"path" env:"DATABASE_PATH"`
		// SSLMode specifies the SSL mode for PostgreSQL connections
		SSLMode string `yaml:"ssl_mode" env:"DATABASE_SSL_MODE"`
		// Connection pool settings (for non-SQLite databases)
		ConnectionPool struct {
			// MaxOpenConns is the maximum number of open connections
			MaxOpenConns int `yaml:"max_open_conns" env:"DATABASE_MAX_OPEN_CONNS"`
			// MaxIdleConns is the maximum number of idle connections
			MaxIdleConns int `yaml:"max_idle_conns" env:"DATABASE_MAX_IDLE_CONNS"`
			// ConnMaxLifetime is the connection lifetime in minutes
			ConnMaxLifetime int `yaml:"conn_max_lifetime" env:"DATABASE_CONN_MAX_LIFETIME"`
		} `yaml:"connection_pool"`
	} `yaml:"database"`

	// Authentication configuration
	Authentication struct {
		// Enable authentication system
		Enabled bool `yaml:"enabled" env:"AUTH_ENABLED"`
		// Session configuration
		Session struct {
			// Secret for signing cookies (auto-generated if empty)
			Secret string `yaml:"secret" env:"AUTH_SESSION_SECRET"`
			// Cookie name for sessions
			CookieName string `yaml:"cookie_name" env:"AUTH_COOKIE_NAME"`
			// Session max age in seconds
			MaxAge int `yaml:"max_age" env:"AUTH_SESSION_MAX_AGE"`
			// Secure cookie (HTTPS only)
			Secure bool `yaml:"secure" env:"AUTH_SESSION_SECURE"`
			// HTTP only cookie (no JavaScript access)
			HttpOnly bool `yaml:"http_only" env:"AUTH_SESSION_HTTP_ONLY"`
			// SameSite cookie policy
			SameSite string `yaml:"same_site" env:"AUTH_SESSION_SAME_SITE"`
		} `yaml:"session"`
		// Default admin user configuration
		DefaultAdmin struct {
			// Default admin username
			Username string `yaml:"username" env:"AUTH_DEFAULT_ADMIN_USERNAME"`
			// Default admin email
			Email string `yaml:"email" env:"AUTH_DEFAULT_ADMIN_EMAIL"`
			// Default admin password
			Password string `yaml:"password" env:"AUTH_DEFAULT_ADMIN_PASSWORD"`
		} `yaml:"default_admin"`
		// Keycloak/OIDC configuration
		Keycloak struct {
			// Enable Keycloak/OIDC authentication
			Enabled bool `yaml:"enabled" env:"KEYCLOAK_ENABLED"`
			// OIDC issuer URL
			Issuer string `yaml:"issuer" env:"KEYCLOAK_ISSUER"`
			// OIDC client ID
			ClientID string `yaml:"client_id" env:"KEYCLOAK_CLIENT_ID"`
			// OIDC client secret
			ClientSecret string `yaml:"client_secret" env:"KEYCLOAK_CLIENT_SECRET"`
			// OIDC redirect URI
			RedirectURI string `yaml:"redirect_uri" env:"KEYCLOAK_REDIRECT_URI"`
			// OIDC scopes
			Scopes string `yaml:"scopes" env:"KEYCLOAK_SCOPES"`
			// Role claim name
			RoleClaim string `yaml:"role_claim" env:"KEYCLOAK_ROLE_CLAIM"`
		} `yaml:"keycloak"`
	} `yaml:"authentication"`

	// File paths
	Paths struct {
		// DataDir is the base directory for all application data (database, encryption keys, etc.)
		DataDir string `yaml:"data_dir" env:"DATA_DIR"`
		// CacheDir is the directory for cache files
		CacheDir string `yaml:"cache_dir" env:"CACHE_DIR"`
		// MismatchOutputDir is the directory where mismatch JSON files will be saved
		MismatchOutputDir string `yaml:"mismatch_output_dir" env:"MISMATCH_OUTPUT_DIR"`
	} `yaml:"paths"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	cfg := &Config{}

	// Set default values
	cfg.Server.Port = "8080"
	cfg.Server.ShutdownTimeout = 30 * time.Second

	// Default sync configuration
	cfg.Sync.Incremental = false
	cfg.Sync.StateFile = "./data/sync_state.json"
	cfg.Sync.MinChangeThreshold = 60 // 60 seconds

	// Default rate limiting (1500ms between requests, burst of 2, max 3 concurrent)
	cfg.RateLimit.Rate = 1500 * time.Millisecond
	cfg.RateLimit.Burst = 2
	cfg.RateLimit.MaxConcurrent = 3

	// Default logging
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"

	// Default application settings
	cfg.App.TestBookFilter = ""
	cfg.App.TestBookLimit = 0

	// Default database configuration (SQLite)
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = "" // Will use GetDefaultDatabasePath() if empty
	cfg.Database.Host = "localhost"
	cfg.Database.Port = 5432 // Default PostgreSQL port
	cfg.Database.Name = "audiobookshelf_sync"
	cfg.Database.User = "sync_user"
	cfg.Database.Password = ""
	cfg.Database.SSLMode = "prefer"
	cfg.Database.ConnectionPool.MaxOpenConns = 25
	cfg.Database.ConnectionPool.MaxIdleConns = 5
	cfg.Database.ConnectionPool.ConnMaxLifetime = 60 // minutes

	// Default authentication configuration
	cfg.Authentication.Enabled = false
	cfg.Authentication.Session.Secret = "" // Auto-generated if empty
	cfg.Authentication.Session.CookieName = "audiobookshelf-sync-session"
	cfg.Authentication.Session.MaxAge = 86400 // 24 hours
	cfg.Authentication.Session.Secure = false // Set to true for HTTPS
	cfg.Authentication.Session.HttpOnly = true
	cfg.Authentication.Session.SameSite = "Lax"
	cfg.Authentication.DefaultAdmin.Username = "admin"
	cfg.Authentication.DefaultAdmin.Email = "admin@localhost"
	cfg.Authentication.DefaultAdmin.Password = "" // Must be set if auth is enabled
	cfg.Authentication.Keycloak.Enabled = false
	cfg.Authentication.Keycloak.Issuer = ""
	cfg.Authentication.Keycloak.ClientID = ""
	cfg.Authentication.Keycloak.ClientSecret = ""
	cfg.Authentication.Keycloak.RedirectURI = ""
	cfg.Authentication.Keycloak.Scopes = "openid profile email"
	cfg.Authentication.Keycloak.RoleClaim = "realm_access.roles"

	// Default paths
	cfg.Paths.DataDir = "./data"
	cfg.Paths.CacheDir = "./cache"
	cfg.Paths.MismatchOutputDir = "./mismatches"

	return cfg
}

// Load loads configuration from a file (if specified) and environment variables.
// Configuration priority: 1) Command line flags, 2) Environment variables, 3) Config file, 4) Defaults
func Load(configFile string) (*Config, error) {
	fmt.Printf("Loading configuration from %s...\n", configFile)
	cfg := DefaultConfig()

	// Load configuration from file first (if specified)
	if configFile != "" {
		fmt.Printf("Loading configuration from file: %s\n", configFile)

		// Get absolute path to config file
		absConfigFile, err := filepath.Abs(configFile)
		if err != nil {
			fmt.Printf("Warning: Failed to get absolute path for config file %s: %v\n", configFile, err)
		} else {
			configFile = absConfigFile
		}

		// Check if file exists
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			fmt.Println("No config file specified, using default configuration")
			return &Config{}, nil
		} else {
			// Read the config file
			data, err := os.ReadFile(configFile)
			if err != nil {
				fmt.Printf("Failed to read config file: %v\n", err)
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}

			// Create a temporary config to load the file into
			fileCfg := &Config{}

			// Unmarshal the config file
			if err := yaml.Unmarshal(data, fileCfg); err != nil {
				fmt.Printf("Failed to parse config file: %v\n", err)
				return nil, fmt.Errorf("failed to parse config file: %w", err)
			}

			// Merge the config from file into our config (only non-zero values)
			mergeConfigs(cfg, fileCfg)
			fmt.Println("Successfully loaded configuration from file")
		}
	} else {
		fmt.Println("No config file specified, using environment variables and defaults")
	}

	// Then load from environment variables (overrides config file)
	loadFromEnv(cfg)

	// Then load from individual environment variables (highest priority)
	// Server configuration
	if port := getEnv("PORT", ""); port != "" {
		cfg.Server.Port = port
	}
	if timeout := getDurationFromEnv("SHUTDOWN_TIMEOUT", 0); timeout > 0 {
		cfg.Server.ShutdownTimeout = timeout
	}

	// Application settings
	if logLevel := getEnv("LOG_LEVEL", ""); logLevel != "" {
		cfg.Logging.Level = logLevel
	}
	if syncInterval := getDurationFromEnv("SYNC_INTERVAL", 0); syncInterval > 0 {
		cfg.App.SyncInterval = syncInterval
	}
	if minProgress := getFloat64FromEnv("MINIMUM_PROGRESS", 0); minProgress > 0 {
		cfg.App.MinimumProgress = minProgress
	}
	if syncWantToRead, set := os.LookupEnv("SYNC_WANT_TO_READ"); set {
		cfg.App.SyncWantToRead = strings.ToLower(syncWantToRead) == "true"
	}
	if syncOwned, set := os.LookupEnv("SYNC_OWNED"); set {
		cfg.App.SyncOwned = strings.ToLower(syncOwned) == "true"
	}
	if dryRun, set := os.LookupEnv("DRY_RUN"); set {
		cfg.App.DryRun = strings.ToLower(dryRun) == "true"
	}
	if testBookFilter := getEnv("TEST_BOOK_FILTER", ""); testBookFilter != "" {
		cfg.App.TestBookFilter = testBookFilter
	}
	if testBookLimit := getIntFromEnv("TEST_BOOK_LIMIT", 0); testBookLimit > 0 {
		cfg.App.TestBookLimit = testBookLimit
	}

	// Rate limiting configuration
	if rate := getDurationFromEnv("RATE_LIMIT_RATE", 0); rate > 0 {
		cfg.RateLimit.Rate = rate
	}
	if burst := getIntFromEnv("RATE_LIMIT_BURST", 0); burst > 0 {
		cfg.RateLimit.Burst = burst
	}
	if maxConcurrent := getIntFromEnv("RATE_LIMIT_MAX_CONCURRENT", 0); maxConcurrent > 0 {
		cfg.RateLimit.MaxConcurrent = maxConcurrent
	}

	// Log the final configuration (without sensitive data)
	fmt.Println("Final configuration:")
	fmt.Printf("  audiobookshelf_url: %s\n", cfg.Audiobookshelf.URL)
	fmt.Printf("  has_audiobookshelf_token: %t\n", cfg.Audiobookshelf.Token != "")
	fmt.Printf("  has_hardcover_token: %t\n", cfg.Hardcover.Token != "")
	fmt.Printf("  sync_interval: %v\n", cfg.App.SyncInterval)
	fmt.Printf("  dry_run: %t\n", cfg.App.DryRun)
	fmt.Printf("  test_book_filter: %s\n", cfg.App.TestBookFilter)
	fmt.Printf("  test_book_limit: %d\n", cfg.App.TestBookLimit)

	fmt.Println("Loaded application settings:")
	fmt.Printf("  log_level: %s\n", cfg.Logging.Level)
	fmt.Printf("  log_format: %s\n", cfg.Logging.Format)
	fmt.Printf("  sync_interval: %v\n", cfg.App.SyncInterval)
	fmt.Printf("  minimum_progress: %f\n", cfg.App.MinimumProgress)
	fmt.Printf("  sync_want_to_read: %t\n", cfg.App.SyncWantToRead)
	fmt.Printf("  sync_owned: %t\n", cfg.App.SyncOwned)
	fmt.Printf("  dry_run: %t\n", cfg.App.DryRun)
	fmt.Printf("  test_book_filter: %s\n", cfg.App.TestBookFilter)

	fmt.Println("Loaded file paths:")
	fmt.Printf("  cache_dir: %s\n", cfg.Paths.CacheDir)

	// Validate required configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	fmt.Println("Configuration loaded successfully")
	return cfg, nil
}

// Validate checks that all required configuration is present and valid
func (c *Config) Validate() error {
	var missing []string

	// Check required fields
	if c.Audiobookshelf.URL == "" {
		missing = append(missing, "AUDIOBOOKSHELF_URL")
	}
	if c.Audiobookshelf.Token == "" {
		missing = append(missing, "AUDIOBOOKSHELF_TOKEN")
	}
	if c.Hardcover.Token == "" {
		missing = append(missing, "HARDCOVER_TOKEN")
	}

	if len(missing) > 0 {
		fmt.Printf("Required configuration fields are missing: %s\n", strings.Join(missing, ", "))

		// Log the current configuration state (without sensitive data)
		fmt.Printf("Current configuration state:\n")
		fmt.Printf("  audiobookshelf_url: %s\n", c.Audiobookshelf.URL)
		fmt.Printf("  has_audiobookshelf_token: %t\n", c.Audiobookshelf.Token != "")
		fmt.Printf("  has_hardcover_token: %t\n", c.Hardcover.Token != "")

		return &ConfigError{
			Field: strings.Join(missing, ", "),
			Msg:   "required configuration values are missing",
		}
	}

	// Validate sync interval is positive
	if c.App.SyncInterval <= 0 {
		// Set a default sync interval if invalid
		c.App.SyncInterval = 1 * time.Hour
		fmt.Printf("Warning: Invalid sync interval, using default: %s\n", c.App.SyncInterval)
	}

	fmt.Println("Configuration validation passed")
	return nil
}

// ConfigError represents a configuration error
type ConfigError struct {
	Field string
	Msg   string
}

func (e *ConfigError) Error() string {
	return "config error: " + e.Field + " " + e.Msg
}

// Helper functions for environment variable parsing
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// parseCommaSeparatedList parses a comma-separated string into a slice of trimmed strings
func parseCommaSeparatedList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func getIntFromEnv(key string, fallback int) int {
	if value, exists := os.LookupEnv(key); exists {
		i, err := strconv.Atoi(value)
		if err != nil {
			fmt.Printf("Warning: Failed to parse int from env var %s: %v\n", key, err)
			return fallback
		}
		return i
	}
	return fallback
}

// loadFromEnv loads configuration from environment variables
func loadFromEnv(cfg *Config) {
	// Audiobookshelf configuration
	if url := os.Getenv("AUDIOBOOKSHELF_URL"); url != "" {
		cfg.Audiobookshelf.URL = strings.TrimSuffix(url, "/")
	}
	if token := os.Getenv("AUDIOBOOKSHELF_TOKEN"); token != "" {
		cfg.Audiobookshelf.Token = token
	}

	// Hardcover configuration
	if token := os.Getenv("HARDCOVER_TOKEN"); token != "" {
		cfg.Hardcover.Token = token
	}

	// Server configuration
	if port := os.Getenv("PORT"); port != "" {
		cfg.Server.Port = port
	}
	if timeout := os.Getenv("SHUTDOWN_TIMEOUT"); timeout != "" {
		if d, err := time.ParseDuration(timeout); err == nil {
			cfg.Server.ShutdownTimeout = d
		}
	}

	// Application settings - Log level is handled by the Logging.Level field
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		cfg.Logging.Level = logLevel
	}
	// Handle log format (json or console)
	if logFormat := os.Getenv("LOG_FORMAT"); logFormat != "" {
		cfg.Logging.Format = logFormat
	}
	if syncInterval := os.Getenv("SYNC_INTERVAL"); syncInterval != "" {
		if d, err := time.ParseDuration(syncInterval); err == nil {
			cfg.App.SyncInterval = d
		}
	}
	if mismatchDir := os.Getenv("MISMATCH_OUTPUT_DIR"); mismatchDir != "" {
		cfg.Paths.MismatchOutputDir = mismatchDir
	}
	if minProgress := os.Getenv("MINIMUM_PROGRESS"); minProgress != "" {
		if f, err := strconv.ParseFloat(minProgress, 64); err == nil {
			cfg.App.MinimumProgress = f
		}
	}
	if syncWantToRead := os.Getenv("SYNC_WANT_TO_READ"); syncWantToRead != "" {
		if b, err := strconv.ParseBool(syncWantToRead); err == nil {
			cfg.App.SyncWantToRead = b
		}
	}
	if syncOwned := os.Getenv("SYNC_OWNED"); syncOwned != "" {
		if b, err := strconv.ParseBool(syncOwned); err == nil {
			cfg.App.SyncOwned = b
		}
	}
	if dryRun := os.Getenv("DRY_RUN"); dryRun != "" {
		if b, err := strconv.ParseBool(dryRun); err == nil {
			cfg.App.DryRun = b
		}
	}
	if testBookFilter := os.Getenv("TEST_BOOK_FILTER"); testBookFilter != "" {
		cfg.App.TestBookFilter = testBookFilter
	}
	if testBookLimit := os.Getenv("TEST_BOOK_LIMIT"); testBookLimit != "" {
		if i, err := strconv.Atoi(testBookLimit); err == nil {
			cfg.App.TestBookLimit = i
		}
	}

	// Sync configuration
	if syncIncremental := os.Getenv("SYNC_INCREMENTAL"); syncIncremental != "" {
		if b, err := strconv.ParseBool(syncIncremental); err == nil {
			cfg.Sync.Incremental = b
		}
	}
	if syncStateFile := os.Getenv("SYNC_STATE_FILE"); syncStateFile != "" {
		cfg.Sync.StateFile = syncStateFile
	}
	if syncMinChangeThreshold := os.Getenv("SYNC_MIN_CHANGE_THRESHOLD"); syncMinChangeThreshold != "" {
		if i, err := strconv.Atoi(syncMinChangeThreshold); err == nil {
			cfg.Sync.MinChangeThreshold = i
		}
	}
	// Library filtering from environment variables
	if librariesInclude := os.Getenv("SYNC_LIBRARIES_INCLUDE"); librariesInclude != "" {
		cfg.Sync.Libraries.Include = parseCommaSeparatedList(librariesInclude)
	}
	if librariesExclude := os.Getenv("SYNC_LIBRARIES_EXCLUDE"); librariesExclude != "" {
		cfg.Sync.Libraries.Exclude = parseCommaSeparatedList(librariesExclude)
	}

	// File paths
	cfg.Paths.CacheDir = getEnv("CACHE_DIR", cfg.Paths.CacheDir)
	cfg.Paths.MismatchOutputDir = getEnv("MISMATCH_OUTPUT_DIR", cfg.Paths.MismatchOutputDir)
}

// mergeConfigs merges non-zero values from src into dst
func mergeConfigs(dst, src *Config) {
	dstVal := reflect.ValueOf(dst).Elem()
	srcVal := reflect.ValueOf(src).Elem()

	for i := 0; i < dstVal.NumField(); i++ {
		dstField := dstVal.Field(i)
		srcField := srcVal.Field(i)

		// Skip unexported fields
		if !dstField.CanSet() {
			continue
		}

		switch dstField.Kind() {
		case reflect.Struct:
			// For nested structs, recursively merge each field
			for j := 0; j < dstField.NumField(); j++ {
				dstFieldField := dstField.Field(j)
				srcFieldField := srcField.Field(j)

				if !dstFieldField.CanSet() {
					continue
				}

				switch dstFieldField.Kind() {
				case reflect.String:
					if srcFieldField.String() != "" {
						dstFieldField.SetString(srcFieldField.String())
					}
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					if srcFieldField.Int() != 0 {
						dstFieldField.SetInt(srcFieldField.Int())
					}
				case reflect.Float32, reflect.Float64:
					if srcFieldField.Float() != 0 {
						dstFieldField.SetFloat(srcFieldField.Float())
					}
				case reflect.Bool:
					if srcFieldField.Bool() {
						dstFieldField.SetBool(true)
					}
				case reflect.Struct:
					// Handle nested structs recursively
					if dstFieldField.CanAddr() && srcFieldField.CanAddr() {
						dstNested := dstFieldField.Addr().Interface()
						srcNested := srcFieldField.Addr().Interface()
						mergeNestedConfigs(dstNested, srcNested)
					}
				}
			}
		case reflect.String:
			// Only overwrite if source has a non-zero value
			if srcField.String() != "" {
				dstField.SetString(srcField.String())
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			// Only overwrite if source has a non-zero value
			if srcField.Int() != 0 {
				dstField.SetInt(srcField.Int())
			}
		case reflect.Float32, reflect.Float64:
			// Only overwrite if source has a non-zero value
			if srcField.Float() != 0 {
				dstField.SetFloat(srcField.Float())
			}
		case reflect.Bool:
			// Only overwrite if source is true
			if srcField.Bool() {
				dstField.SetBool(true)
			}
		}
	}
}

// mergeNestedConfigs merges nested config structs
func mergeNestedConfigs(dst, src interface{}) {
	dstVal := reflect.ValueOf(dst).Elem()
	srcVal := reflect.ValueOf(src).Elem()

	for i := 0; i < dstVal.NumField(); i++ {
		dstField := dstVal.Field(i)
		srcField := srcVal.Field(i)

		if !dstField.CanSet() {
			continue
		}

		switch dstField.Kind() {
		case reflect.String:
			if srcField.String() != "" {
				dstField.SetString(srcField.String())
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if srcField.Int() != 0 {
				dstField.SetInt(srcField.Int())
			}
		case reflect.Float32, reflect.Float64:
			if srcField.Float() != 0 {
				dstField.SetFloat(srcField.Float())
			}
		case reflect.Bool:
			if srcField.Bool() {
				dstField.SetBool(true)
			}
		case reflect.Struct:
			if dstField.CanAddr() && srcField.CanAddr() {
				dstNested := dstField.Addr().Interface()
				srcNested := srcField.Addr().Interface()
				mergeNestedConfigs(dstNested, srcNested)
			}
		}
	}
}

// getDurationFromEnv reads a duration from an environment variable or returns a default value
func getDurationFromEnv(key string, fallback time.Duration) time.Duration {
	if value, exists := os.LookupEnv(key); exists {
		d, err := time.ParseDuration(value)
		if err != nil {
			fmt.Printf("Warning: Failed to parse duration from env var %s: %v\n", key, err)
			return fallback
		}
		return d
	}
	return fallback
}

// getFloat64FromEnv reads a float64 from an environment variable or returns a default value
func getFloat64FromEnv(key string, fallback float64) float64 {
	if value, exists := os.LookupEnv(key); exists {
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			fmt.Printf("Warning: Failed to parse float64 from env var %s: %v\n", key, err)
			return fallback
		}
		return f
	}
	return fallback
}
