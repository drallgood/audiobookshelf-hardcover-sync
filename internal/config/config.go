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
		Level  string `yaml:"level" env:"LOG_LEVEL"`
		// Format is the log format (json, console)
		Format string `yaml:"format" env:"LOG_FORMAT"`
	} `yaml:"logging"`

	// Audiobookshelf configuration
	Audiobookshelf struct {
		// URL is the base URL of the Audiobookshelf server
		URL   string `yaml:"url" env:"AUDIOBOOKSHELF_URL"`
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
		// Debug enables debug mode
		Debug bool `yaml:"debug" env:"DEBUG"`
		// SyncInterval is the interval between syncs
		SyncInterval time.Duration `yaml:"sync_interval" env:"SYNC_INTERVAL"`
		// MinimumProgress is the minimum progress threshold for syncing (0.0 to 1.0)
		MinimumProgress float64 `yaml:"minimum_progress" env:"MINIMUM_PROGRESS"`
		// AudiobookMatchMode is the mode for matching audiobooks (strict, title-author, title-only)
		AudiobookMatchMode string `yaml:"audiobook_match_mode" env:"AUDIOBOOK_MATCH_MODE"`
		// SyncWantToRead syncs books with 0% progress as "Want to Read"
		SyncWantToRead bool `yaml:"sync_want_to_read" env:"SYNC_WANT_TO_READ"`
		// SyncOwned marks synced books as owned in Hardcover
		SyncOwned bool `yaml:"sync_owned" env:"SYNC_OWNED"`
		// DryRun enables dry run mode (no changes will be made)
		DryRun bool `yaml:"dry_run" env:"DRY_RUN"`
		// TestBookFilter filters books by title for testing
		TestBookFilter string `yaml:"test_book_filter" env:"TEST_BOOK_FILTER"`
		// TestBookLimit limits the number of books to process for testing
		TestBookLimit int `yaml:"test_book_limit" env:"TEST_BOOK_LIMIT"`
	} `yaml:"app"`

	// File paths
	Paths struct {
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

	// Default rate limiting (1500ms between requests, burst of 2, max 3 concurrent)
	cfg.RateLimit.Rate = 1500 * time.Millisecond
	cfg.RateLimit.Burst = 2
	cfg.RateLimit.MaxConcurrent = 3

	// Default logging
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"

	// Default application settings
	cfg.App.Debug = false
	cfg.App.SyncInterval = 1 * time.Hour
	cfg.App.MinimumProgress = 0.01 // 1% minimum progress threshold
	cfg.App.AudiobookMatchMode = "strict"
	cfg.App.SyncWantToRead = true
	cfg.App.SyncOwned = true
	cfg.App.DryRun = false
	cfg.App.TestBookFilter = ""
	cfg.App.TestBookLimit = 0

	// Default paths
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
	if debug, set := os.LookupEnv("DEBUG"); set {
		cfg.App.Debug = strings.ToLower(debug) == "true"
	}
	if logLevel := getEnv("LOG_LEVEL", ""); logLevel != "" {
		cfg.Logging.Level = logLevel
	}
	if syncInterval := getDurationFromEnv("SYNC_INTERVAL", 0); syncInterval > 0 {
		cfg.App.SyncInterval = syncInterval
	}
	if minProgress := getFloat64FromEnv("MINIMUM_PROGRESS_THRESHOLD", 0); minProgress > 0 {
		cfg.App.MinimumProgress = minProgress
	}
	if matchMode := getEnv("AUDIOBOOK_MATCH_MODE", ""); matchMode != "" {
		cfg.App.AudiobookMatchMode = matchMode
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
	fmt.Printf("  debug: %t\n", cfg.App.Debug)
	fmt.Printf("  log_level: %s\n", cfg.Logging.Level)
	fmt.Printf("  log_format: %s\n", cfg.Logging.Format)
	fmt.Printf("  sync_interval: %v\n", cfg.App.SyncInterval)
	fmt.Printf("  minimum_progress: %f\n", cfg.App.MinimumProgress)
	fmt.Printf("  audiobook_match_mode: %s\n", cfg.App.AudiobookMatchMode)
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

// Validate checks that all required configuration is present
func (c *Config) Validate() error {
	var missing []string

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

	// Application settings
	if debug := os.Getenv("DEBUG"); debug != "" {
		if b, err := strconv.ParseBool(debug); err == nil {
			cfg.App.Debug = b
		}
	}
	// Log level is now handled by the Logging.Level field
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		cfg.Logging.Level = logLevel
	}
	if syncInterval := os.Getenv("SYNC_INTERVAL"); syncInterval != "" {
		if d, err := time.ParseDuration(syncInterval); err == nil {
			cfg.App.SyncInterval = d
		}
	}
	if mismatchDir := os.Getenv("MISMATCH_OUTPUT_DIR"); mismatchDir != "" {
		cfg.Paths.MismatchOutputDir = mismatchDir
	}
	if minProgress := os.Getenv("MINIMUM_PROGRESS_THRESHOLD"); minProgress != "" {
		if f, err := strconv.ParseFloat(minProgress, 64); err == nil {
			cfg.App.MinimumProgress = f
		}
	}
	if matchMode := os.Getenv("AUDIOBOOK_MATCH_MODE"); matchMode != "" {
		cfg.App.AudiobookMatchMode = matchMode
	}
	if wantToRead := os.Getenv("SYNC_WANT_TO_READ"); wantToRead != "" {
		if b, err := strconv.ParseBool(wantToRead); err == nil {
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
