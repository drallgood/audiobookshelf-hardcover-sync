package config

import (
	"fmt"
	"os"
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
		// EnableWebUI enables the web UI for multi-user mode (default: false)
		EnableWebUI bool `yaml:"enable_web_ui" env:"ENABLE_WEB_UI"`
	} `yaml:"server"`

	// Sync configuration
	Sync struct {
		// Enable incremental sync (only process changed books)
		Incremental bool `yaml:"incremental" env:"SYNC_INCREMENTAL"`
		// Path to store sync state (default: ./data/sync_state.json)
		StateFile string `yaml:"state_file" env:"SYNC_STATE_FILE"`
		// Minimum change in progress (seconds) to trigger an update (default: 60)
		MinChangeThreshold int `yaml:"min_change_threshold" env:"SYNC_MIN_CHANGE_THRESHOLD"`
		// Sync interval (default: 1h)
		SyncInterval time.Duration `yaml:"sync_interval" env:"SYNC_SYNC_INTERVAL"`
		// Minimum progress threshold (0.0-1.0) to sync a book (default: 0.0)
		MinimumProgress float64 `yaml:"minimum_progress" env:"SYNC_MINIMUM_PROGRESS"`
		// Sync books with 0% progress as "Want to Read" in Hardcover
		SyncWantToRead bool `yaml:"sync_want_to_read" env:"SYNC_WANT_TO_READ"`
		// Process unread books (0% progress) for mismatches and "want to read" status
		ProcessUnreadBooks bool `yaml:"process_unread_books" env:"PROCESS_UNREAD_BOOKS"`
		// Mark synced books as owned in Hardcover
		SyncOwned bool `yaml:"sync_owned" env:"SYNC_OWNED"`
		// Dry run mode - log actions without making changes
		DryRun bool `yaml:"dry_run" env:"DRY_RUN"`
		// Single-user mode - only sync books for the specified user
		SingleUserMode bool `yaml:"single_user_mode" env:"SYNC_SINGLE_USER_MODE"`
		// Single-user mode username
		SingleUserUsername string `yaml:"single_user_username" env:"SYNC_SINGLE_USER_USERNAME"`
		// Test book filter for debugging (regex pattern)
		TestBookFilter string `yaml:"test_book_filter" env:"TEST_BOOK_FILTER"`
		// Test book limit for debugging (0 = no limit)
		TestBookLimit int `yaml:"test_book_limit" env:"TEST_BOOK_LIMIT"`
		// Library filtering configuration
		Libraries struct {
			// Include only these libraries (empty = all)
			Include []string `yaml:"include" env:"SYNC_LIBRARIES_INCLUDE"`
			// Exclude these libraries (empty = none)
			Exclude []string `yaml:"exclude" env:"SYNC_LIBRARIES_EXCLUDE"`
		} `yaml:"libraries"`
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
		// BaseURL is the base URL for the Hardcover GraphQL API
		BaseURL string `yaml:"base_url" env:"HARDCOVER_BASE_URL"`
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
	cfg.Server.EnableWebUI = false // Web UI is disabled by default for backward compatibility

	// Default sync configuration
	cfg.Sync.Incremental = true
	cfg.Sync.StateFile = "./data/sync_state.json"
	cfg.Sync.MinChangeThreshold = 60 // 1 minute
	cfg.Sync.SyncInterval = time.Hour
	cfg.Sync.MinimumProgress = 0.0
	cfg.Sync.SyncWantToRead = true
	cfg.Sync.ProcessUnreadBooks = true
	cfg.Sync.SyncOwned = false
	cfg.Sync.DryRun = false
	cfg.Sync.SingleUserMode = false
	cfg.Sync.TestBookFilter = ""
	cfg.Sync.TestBookLimit = 0

	// Database defaults
	cfg.Database.Type = "sqlite"
	cfg.Database.Host = "localhost"
	cfg.Database.Port = 5432
	cfg.Database.Name = "audiobookshelf_hardcover_sync"
	cfg.Database.User = "postgres"
	cfg.Database.Password = ""
	cfg.Database.Path = "./data/audiobookshelf-hardcover-sync.db"
	cfg.Database.SSLMode = "disable"
	cfg.Database.ConnectionPool.MaxOpenConns = 10
	cfg.Database.ConnectionPool.MaxIdleConns = 5
	cfg.Database.ConnectionPool.ConnMaxLifetime = 30 // minutes

	// Authentication defaults
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

	// Default Hardcover settings
	// Official GraphQL endpoint, can be overridden via HARDCOVER_BASE_URL or config
	cfg.Hardcover.BaseURL = "https://api.hardcover.app/v1/graphql"

    return cfg
}

func Load(configPath string) (*Config, error) {
	// Start with default configuration
	cfg := DefaultConfig()

	// Note: Debug logging removed to prevent early logger initialization
	// which would override the format specified in the config file
	
	// Note: Debug logging removed to prevent early logger initialization

	// Load from file if path is provided
	if configPath != "" {
		// Check if file exists
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			// Config file does not exist, using defaults
		} else {
			// Read the config file
			data, err := os.ReadFile(configPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}

			// Create a temporary config to load the file into
			fileCfg := &Config{}

			// Unmarshal the config file
			if err := yaml.Unmarshal(data, fileCfg); err != nil {
				return nil, fmt.Errorf("failed to parse config file: %w", err)
			}

			// Merge the config from file into our config
			mergeConfigs(cfg, fileCfg)
		}
	}

	// Load from environment variables
	loadFromEnv(cfg)

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Log the final configuration
	fmt.Println("Final configuration after validation and migration:")
	fmt.Printf("Server:\n  port: %s\n  shutdown_timeout: %s\n  enable_web_ui: %v\n", 
		cfg.Server.Port, cfg.Server.ShutdownTimeout, cfg.Server.EnableWebUI)
	fmt.Printf("Audiobookshelf:\n  url: %s\n  has_token: %v\n", 
		cfg.Audiobookshelf.URL, cfg.Audiobookshelf.Token != "")
	fmt.Printf("Hardcover:\n  has_token: %v\n  base_url: %s\n", cfg.Hardcover.Token != "", cfg.Hardcover.BaseURL)
	fmt.Printf("Sync:\n  incremental: %v\n  state_file: %s\n  min_change_threshold: %d\n  sync_interval: %s\n  minimum_progress: %f\n  sync_want_to_read: %v\n  process_unread_books: %v\n  sync_owned: %v\n  dry_run: %v\n  single_user_mode: %v\n  single_user_username: %s\n  test_book_filter: %s\n  test_book_limit: %d\n",
		cfg.Sync.Incremental, cfg.Sync.StateFile, cfg.Sync.MinChangeThreshold, 
		cfg.Sync.SyncInterval, cfg.Sync.MinimumProgress, cfg.Sync.SyncWantToRead,
		cfg.Sync.ProcessUnreadBooks, cfg.Sync.SyncOwned, cfg.Sync.DryRun,
		cfg.Sync.SingleUserMode, cfg.Sync.SingleUserUsername, cfg.Sync.TestBookFilter,
		cfg.Sync.TestBookLimit)
	fmt.Printf("Rate Limiting:\n  rate: %s\n  burst: %d\n  max_concurrent: %d\n",
		cfg.RateLimit.Rate, cfg.RateLimit.Burst, cfg.RateLimit.MaxConcurrent)
	fmt.Printf("Logging:\n  level: %s\n  format: %s\n", 
		cfg.Logging.Level, cfg.Logging.Format)
	fmt.Printf("Database:\n  type: %s\n  path: %s\n", 
		cfg.Database.Type, cfg.Database.Path)
	fmt.Printf("Authentication:\n  enabled: %v\n  session_cookie_name: %s\n  session_max_age: %d\n  session_secure: %v\n  session_http_only: %v\n  session_same_site: %s\n  default_admin_username: %s\n  default_admin_email: %s\n  has_default_admin_password: %v\n  keycloak_enabled: %v\n  keycloak_issuer: %s\n  keycloak_client_id: %s\n",
		cfg.Authentication.Enabled, 
		cfg.Authentication.Session.CookieName,
		cfg.Authentication.Session.MaxAge,
		cfg.Authentication.Session.Secure,
		cfg.Authentication.Session.HttpOnly,
		cfg.Authentication.Session.SameSite,
		cfg.Authentication.DefaultAdmin.Username,
		cfg.Authentication.DefaultAdmin.Email,
		cfg.Authentication.DefaultAdmin.Password != "",
		cfg.Authentication.Keycloak.Enabled,
		cfg.Authentication.Keycloak.Issuer,
		cfg.Authentication.Keycloak.ClientID)
	fmt.Printf("Paths:\n  data_dir: %s\n  cache_dir: %s\n  mismatch_output_dir: %s\n",
		cfg.Paths.DataDir, cfg.Paths.CacheDir, cfg.Paths.MismatchOutputDir)

	fmt.Println("Configuration loaded successfully")
	return cfg, nil
}

// Validate checks that all required configuration is present and valid
func (c *Config) Validate() error {
	var missing []string

	// When web UI is disabled (single-user mode), require tokens
	// When web UI is enabled, tokens can be configured via the web UI
	if !c.Server.EnableWebUI {
		// Single-user mode - require tokens
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
			fmt.Printf("Required configuration fields are missing for single-user mode: %s\n", strings.Join(missing, ", "))

			// Log the current configuration state (without sensitive data)
			fmt.Printf("Current configuration state:\n")
			fmt.Printf("  audiobookshelf_url: %s\n", c.Audiobookshelf.URL)
			fmt.Printf("  has_audiobookshelf_token: %t\n", c.Audiobookshelf.Token != "")
			fmt.Printf("  has_hardcover_token: %t\n", c.Hardcover.Token != "")
			fmt.Printf("  web_ui_enabled: %t\n", c.Server.EnableWebUI)

			return &ConfigError{
				Field: strings.Join(missing, ", "),
				Msg:   "required configuration values are missing for single-user mode",
			}
		}
	} else {
		// Web UI mode - only require URL, tokens can be configured via web UI
		if c.Audiobookshelf.URL == "" {
			missing = append(missing, "AUDIOBOOKSHELF_URL")
		}

		if len(missing) > 0 {
			fmt.Printf("Required configuration fields are missing for web UI mode: %s\n", strings.Join(missing, ", "))

			// Log the current configuration state (without sensitive data)
			fmt.Printf("Current configuration state:\n")
			fmt.Printf("  audiobookshelf_url: %s\n", c.Audiobookshelf.URL)
			fmt.Printf("  has_audiobookshelf_token: %t\n", c.Audiobookshelf.Token != "")
			fmt.Printf("  has_hardcover_token: %t\n", c.Hardcover.Token != "")
			fmt.Printf("  web_ui_enabled: %t\n", c.Server.EnableWebUI)

			return &ConfigError{
				Field: strings.Join(missing, ", "),
				Msg:   "required configuration values are missing for web UI mode",
			}
		}
	}

	// Validate sync settings
	if c.Sync.SyncInterval <= 0 {
		// Set a default sync interval if invalid
		c.Sync.SyncInterval = 1 * time.Hour
		fmt.Printf("Warning: Invalid sync interval, using default: %s\n", c.Sync.SyncInterval)
	}

	// Validate minimum progress is between 0 and 1
	if c.Sync.MinimumProgress < 0 || c.Sync.MinimumProgress > 1 {
		c.Sync.MinimumProgress = 0.0
		fmt.Printf("Warning: Invalid minimum progress, using default: %.2f\n", c.Sync.MinimumProgress)
	}

	// Note: Logger initialization deferred to prevent early initialization with JSON format
	// Check for deprecated app-level settings, migrate them to sync section, and log warnings
	var deprecatedFields []string
	
	// Check if any app.* fields are set and need migration
	appFieldsSet := false

	// SyncInterval
	if c.App.SyncInterval > 0 {
		deprecatedFields = append(deprecatedFields, "app.sync_interval (use sync.sync_interval)")
		c.Sync.SyncInterval = c.App.SyncInterval
		appFieldsSet = true
	}

	// MinimumProgress
	if c.App.MinimumProgress > 0 {
		deprecatedFields = append(deprecatedFields, "app.minimum_progress (use sync.minimum_progress)")
		c.Sync.MinimumProgress = c.App.MinimumProgress
		appFieldsSet = true
	}

	// Migration logic for deprecated app.* fields to sync.* fields
	// We migrate deprecated values if they appear to be set in the config file
	// This provides backward compatibility for users still using the old app section
	
	// For non-zero values (durations, floats), we can detect if they were set
	// For boolean values, we use a simpler approach: if any app boolean is set to true,
	// or if the entire app section has values, we migrate all app booleans
	
	// Check if any app values are set (indicating the app section is being used)
	appSectionInUse := (c.App.SyncInterval > 0 || c.App.MinimumProgress > 0 || 
		c.App.SyncWantToRead || c.App.SyncOwned || c.App.DryRun ||
		c.App.TestBookFilter != "" || c.App.TestBookLimit > 0)

	// Migrate boolean fields if the app section appears to be in use
	if appSectionInUse {
		// SyncWantToRead
		if c.App.SyncWantToRead || c.App.SyncOwned || c.App.DryRun {
			if c.App.SyncWantToRead {
				deprecatedFields = append(deprecatedFields, "app.sync_want_to_read (use sync.sync_want_to_read)")
				c.Sync.SyncWantToRead = c.App.SyncWantToRead
			}
		}

		// SyncOwned
		if c.App.SyncOwned {
			deprecatedFields = append(deprecatedFields, "app.sync_owned (use sync.sync_owned)")
			c.Sync.SyncOwned = c.App.SyncOwned
		}

		// DryRun
		if c.App.DryRun {
			deprecatedFields = append(deprecatedFields, "app.dry_run (use sync.dry_run)")
			c.Sync.DryRun = c.App.DryRun
		}

		if len(deprecatedFields) > 0 {
			appFieldsSet = true
		}
	}

	// Note: Deprecation warnings and sync configuration logging deferred to prevent early logger initialization
	// These will be logged by the main application after the logger is properly configured
	
	// Store deprecation info for later logging (after logger is configured)
	if appFieldsSet && len(deprecatedFields) > 0 {
		// Deprecation warnings will be logged by main application
		fmt.Printf("Warning: Deprecated configuration fields found: %v\n", deprecatedFields)
	}
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

// loadFromEnv loads configuration from environment variables
func loadFromEnv(cfg *Config) {
	// Debug logging removed to prevent early logger initialization
	
	// Track if values were explicitly set via environment variables
	dryRunSet := false
	
	// Handle deprecated app.* environment variables first
	if val := os.Getenv("TEST_BOOK_FILTER"); val != "" && cfg.Sync.TestBookFilter == "" {
		// Only set from deprecated env var if not already set in sync section
		cfg.Sync.TestBookFilter = val
		fmt.Printf("WARNING: 'TEST_BOOK_FILTER' environment variable is deprecated. Use 'SYNC_TEST_BOOK_FILTER' instead.\n")
	}
	
	if val := os.Getenv("TEST_BOOK_LIMIT"); val != "" && cfg.Sync.TestBookLimit == 0 {
		if limit, err := strconv.Atoi(val); err == nil && limit > 0 {
			// Only set from deprecated env var if not already set in sync section
			cfg.Sync.TestBookLimit = limit
			fmt.Printf("WARNING: 'TEST_BOOK_LIMIT' environment variable is deprecated. Use 'SYNC_TEST_BOOK_LIMIT' instead.\n")
		}
	}
	
	// Load sync settings from environment variables - only if not already set in config
	if val := os.Getenv("SYNC_TEST_BOOK_FILTER"); val != "" {
		cfg.Sync.TestBookFilter = val
	}
	
	if val := os.Getenv("SYNC_TEST_BOOK_LIMIT"); val != "" {
		if limit, err := strconv.Atoi(val); err == nil && limit > 0 {
			cfg.Sync.TestBookLimit = limit
		}
	}
	
	// Handle dry run from environment variables
	if val := os.Getenv("DRY_RUN"); val != "" {
		if dryRun, err := strconv.ParseBool(val); err == nil {
			// Setting dry_run from DRY_RUN environment variable
			cfg.Sync.DryRun = dryRun
			dryRunSet = true
		}
	}
	
	// Only override with SYNC_DRY_RUN if DRY_RUN wasn't set
	if val := os.Getenv("SYNC_DRY_RUN"); val != "" && !dryRunSet {
		if dryRun, err := strconv.ParseBool(val); err == nil {
			// Setting dry_run from SYNC_DRY_RUN environment variable
			cfg.Sync.DryRun = dryRun
		}
	}
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
	if baseURL := os.Getenv("HARDCOVER_BASE_URL"); baseURL != "" {
		cfg.Hardcover.BaseURL = strings.TrimSuffix(baseURL, "/")
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
	if enableWebUI := os.Getenv("ENABLE_WEB_UI"); enableWebUI != "" {
		cfg.Server.EnableWebUI = strings.ToLower(enableWebUI) == "true"
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
			cfg.Sync.SyncInterval = d
		}
	}
	if mismatchDir := os.Getenv("MISMATCH_OUTPUT_DIR"); mismatchDir != "" {
		cfg.Paths.MismatchOutputDir = mismatchDir
	}
	if minProgress := os.Getenv("MINIMUM_PROGRESS"); minProgress != "" {
		if f, err := strconv.ParseFloat(minProgress, 64); err == nil {
			cfg.Sync.MinimumProgress = f
		}
	}
	if val := getEnv("SYNC_WANT_TO_READ", ""); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			cfg.Sync.SyncWantToRead = b
		}
	}
	if val := getEnv("PROCESS_UNREAD_BOOKS", ""); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			cfg.Sync.ProcessUnreadBooks = b
		}
	}
	if syncOwned := os.Getenv("SYNC_OWNED"); syncOwned != "" {
		if b, err := strconv.ParseBool(syncOwned); err == nil {
			cfg.Sync.SyncOwned = b
		}
	}
	if dryRun := os.Getenv("DRY_RUN"); dryRun != "" {
		if b, err := strconv.ParseBool(dryRun); err == nil {
			cfg.Sync.DryRun = b
		}
	}
	if testBookFilter := os.Getenv("TEST_BOOK_FILTER"); testBookFilter != "" {
		cfg.Sync.TestBookFilter = testBookFilter
	}
	if testBookLimit := os.Getenv("TEST_BOOK_LIMIT"); testBookLimit != "" {
		if i, err := strconv.Atoi(testBookLimit); err == nil {
			cfg.Sync.TestBookLimit = i
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

	// Handle deprecated app.* fields first
	if src.App.TestBookFilter != "" && dst.Sync.TestBookFilter == "" {
		dst.Sync.TestBookFilter = src.App.TestBookFilter
		// Log deprecation warning
		fmt.Printf("WARNING: 'app.test_book_filter' is deprecated and will be removed in a future version. Use 'sync.test_book_filter' instead.\n")
	}
	if src.App.TestBookLimit != 0 && dst.Sync.TestBookLimit == 0 {
		dst.Sync.TestBookLimit = src.App.TestBookLimit
		// Log deprecation warning
		fmt.Printf("WARNING: 'app.test_book_limit' is deprecated and will be removed in a future version. Use 'sync.test_book_limit' instead.\n")
	}

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
				fieldType := dstField.Type().Field(j)

				if !dstFieldField.CanSet() {
					continue
				}

				switch dstFieldField.Kind() {
				case reflect.String:
					if srcFieldField.String() != "" {
						// Merging string field
						dstFieldField.SetString(srcFieldField.String())
					}
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					if srcFieldField.Int() != 0 {
						// Merging int field
						dstFieldField.SetInt(srcFieldField.Int())
					}
				case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
					if srcFieldField.Uint() != 0 {
						// Merging uint field
						dstFieldField.SetUint(srcFieldField.Uint())
					}
				case reflect.Float32, reflect.Float64:
					if srcFieldField.Float() != 0 {
						// Merging float field
						dstFieldField.SetFloat(srcFieldField.Float())
					}
				case reflect.Bool:
					// Check if the field is explicitly set in the source config
					// by looking for the 'yaml' tag and checking if it's present in the source
					fieldTag := fieldType.Tag.Get("yaml")
					if fieldTag == "-" {
						continue // Skip fields marked with yaml:"-"
					}

					// Always set boolean values from config file, whether true or false
					// but only if the field is present in the source config
					if srcFieldField.IsValid() && srcFieldField.CanInterface() {
						// Merging bool field
						dstFieldField.SetBool(srcFieldField.Bool())
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
			// Always set boolean values from config file, whether true or false
			if srcField.IsValid() && srcField.CanInterface() {
				dstField.SetBool(srcField.Bool())
			}
		}
	}
}



// getEnv returns the value of an environment variable or a default value
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
