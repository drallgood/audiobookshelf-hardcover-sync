package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/pflag"
)

// Config holds all configuration for the application
type Config struct {
	App      AppConfig      `koanf:"app"`
	Logging  LoggingConfig  `koanf:"logging"`
	HTTP     HTTPConfig     `koanf:"http"`
	Audiobookshelf AudiobookshelfConfig `koanf:"audiobookshelf"`
	Hardcover    HardcoverConfig    `koanf:"hardcover"`
	Cache    CacheConfig    `koanf:"cache"`
	Metrics  MetricsConfig  `koanf:"metrics"`
	Concurrency ConcurrencyConfig `koanf:"concurrency"`
}

// AppConfig contains application-level configuration
type AppConfig struct {
	Name        string        `koanf:"name"`
	Environment string        `koanf:"env"`
	Debug       bool          `koanf:"debug"`
	SyncInterval time.Duration `koanf:"sync_interval"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
	Output string `koanf:"output"`
}

// HTTPConfig contains HTTP client configuration
type HTTPConfig struct {
	Timeout             time.Duration `koanf:"timeout"`
	MaxIdleConns        int           `koanf:"max_idle_conns"`
	IdleConnTimeout     time.Duration `koanf:"idle_conn_timeout"`
	DisableCompression  bool          `koanf:"disable_compression"`
	MaxRetries          int           `koanf:"max_retries"`
	RetryWaitMin        time.Duration `koanf:"retry_wait_min"`
	RetryWaitMax        time.Duration `koanf:"retry_wait_max"`
	InsecureSkipVerify  bool          `koanf:"insecure_skip_verify"`
}

// AudiobookshelfConfig contains Audiobookshelf API configuration
type AudiobookshelfConfig struct {
	URL      string `koanf:"url"`
	APIKey   string `koanf:"api_key"`
	UserID   string `koanf:"user_id"`
}

// HardcoverConfig contains Hardcover API configuration
type HardcoverConfig struct {
	URL      string `koanf:"url"`
	APIKey   string `koanf:"api_key"`
	Username string `koanf:"username"`
}

// CacheConfig contains cache configuration
type CacheConfig struct {
	Enabled bool          `koanf:"enabled"`
	TTL     time.Duration `koanf:"ttl"`
	Path    string        `koanf:"path"`
}

// ConcurrencyConfig contains concurrency settings
type ConcurrencyConfig struct {
	// MaxWorkers is the maximum number of concurrent workers for book processing
	MaxWorkers int `koanf:"max_workers"`
	// QueueSize is the size of the task queue for worker pool
	QueueSize int `koanf:"queue_size"`
}

// MetricsConfig contains metrics configuration
type MetricsConfig struct {
	Enabled   bool   `koanf:"enabled"`
	Path      string `koanf:"path"`
	Namespace string `koanf:"namespace"`
}

// DefaultConfig returns a configuration with default values
func DefaultConfig() *Config {
	return &Config{
		App: AppConfig{
			Name:        "audiobookshelf-hardcover-sync",
			Environment: "development",
			Debug:       false,
			SyncInterval: 15 * time.Minute,
		},
		Concurrency: ConcurrencyConfig{
			MaxWorkers: 5,
			QueueSize:  100,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		HTTP: HTTPConfig{
			Timeout:            30 * time.Second,
			MaxIdleConns:       100,
			IdleConnTimeout:    90 * time.Second,
			DisableCompression: false,
			MaxRetries:         3,
			RetryWaitMin:       1 * time.Second,
			RetryWaitMax:       5 * time.Second,
			InsecureSkipVerify: false,
		},
		Audiobookshelf: AudiobookshelfConfig{
			URL: "http://localhost:1337",
		},
		Hardcover: HardcoverConfig{
			URL: "https://api.hardcover.app/v1",
		},
		Cache: CacheConfig{
			Enabled: true,
			TTL:     24 * time.Hour,
			Path:    "./cache",
		},
		Metrics: MetricsConfig{
			Enabled:   true,
			Path:      "/metrics",
			Namespace: "abs_hardcover",
		},
	}
}

// Load loads configuration from file and environment variables
func Load(configPath string) (*Config, error) {
	k := koanf.New(".")
	
	// Load default values
	defaultCfg := DefaultConfig()
	
	// Convert default config to map[string]interface{} for koanf
	var defaultMap map[string]interface{}
	if err := mapstructure.Decode(defaultCfg, &defaultMap); err != nil {
		return nil, fmt.Errorf("error decoding default config: %w", err)
	}
	
	// Load defaults
	for key, value := range defaultMap {
		k.Set(key, value)
	}

	// Load configuration from file if specified
	if configPath != "" {
		if err := k.Load(file.Provider(configPath), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("error loading config file: %w", err)
		}
	}

	// Load environment variables
	if err := k.Load(env.Provider("ABS_", ".", func(s string) string {
		return strings.ReplaceAll(strings.ToLower(
			strings.TrimPrefix(s, "ABS_")), "__", ".")
	}), nil); err != nil {
		return nil, fmt.Errorf("error loading environment variables: %w", err)
	}

	// Unmarshal the configuration
	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Ensure cache directory exists
	if cfg.Cache.Enabled {
		if err := os.MkdirAll(cfg.Cache.Path, 0755); err != nil {
			return nil, fmt.Errorf("error creating cache directory: %w", err)
		}
	}

	return &cfg, nil
}

// Save saves the configuration to a file
func (c *Config) Save(path string) error {
	k := koanf.New(".")
	
	// Convert config to map[string]interface{} for koanf
	var configMap map[string]interface{}
	if err := mapstructure.Decode(c, &configMap); err != nil {
		return fmt.Errorf("error decoding config: %w", err)
	}
	
	// Set values in koanf
	for key, value := range configMap {
		k.Set(key, value)
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating config directory: %w", err)
	}

	// Write config to file
	out, err := k.Marshal(yaml.Parser())
	if err != nil {
		return fmt.Errorf("error marshaling config: %w", err)
	}

	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}

	return nil
}

// BindFlags binds command line flags to the configuration
func (c *Config) BindFlags(fs *pflag.FlagSet) {
	// App flags
	fs.String("app.name", c.App.Name, "Application name")
	fs.String("app.env", c.App.Environment, "Application environment (development, production, etc.)")
	fs.Bool("app.debug", c.App.Debug, "Enable debug mode")
	fs.Duration("app.sync_interval", c.App.SyncInterval, "Interval between syncs")

	// Logging flags
	fs.String("logging.level", c.Logging.Level, "Log level (debug, info, warn, error, fatal)")
	fs.String("logging.format", c.Logging.Format, "Log format (json, text)")
	fs.String("logging.output", c.Logging.Output, "Log output (stdout, stderr, file path)")

	// HTTP flags
	fs.Duration("http.timeout", c.HTTP.Timeout, "HTTP client timeout")
	fs.Int("http.max_idle_conns", c.HTTP.MaxIdleConns, "Maximum number of idle connections")
	fs.Duration("http.idle_conn_timeout", c.HTTP.IdleConnTimeout, "Idle connection timeout")
	fs.Bool("http.disable_compression", c.HTTP.DisableCompression, "Disable HTTP compression")
	fs.Int("http.max_retries", c.HTTP.MaxRetries, "Maximum number of retries for failed requests")
	fs.Duration("http.retry_wait_min", c.HTTP.RetryWaitMin, "Minimum time to wait between retries")
	fs.Duration("http.retry_wait_max", c.HTTP.RetryWaitMax, "Maximum time to wait between retries")

	// Audiobookshelf flags
	fs.String("audiobookshelf.url", c.Audiobookshelf.URL, "Audiobookshelf server URL")
	fs.String("audiobookshelf.api_key", c.Audiobookshelf.APIKey, "Audiobookshelf API key")
	fs.String("audiobookshelf.user_id", c.Audiobookshelf.UserID, "Audiobookshelf user ID")

	// Hardcover flags
	fs.String("hardcover.url", c.Hardcover.URL, "Hardcover API URL")
	fs.String("hardcover.api_key", c.Hardcover.APIKey, "Hardcover API key")
	fs.String("hardcover.username", c.Hardcover.Username, "Hardcover username")

	// Cache flags
	fs.Bool("cache.enabled", c.Cache.Enabled, "Enable caching")
	fs.Duration("cache.ttl", c.Cache.TTL, "Cache TTL")
	fs.String("cache.path", c.Cache.Path, "Cache directory path")

	// Metrics flags
	fs.Bool("metrics.enabled", c.Metrics.Enabled, "Enable metrics collection")
	fs.String("metrics.path", c.Metrics.Path, "Metrics HTTP endpoint path")
	fs.String("metrics.namespace", c.Metrics.Namespace, "Metrics namespace")
}

// LoadFromFlags loads configuration from command line flags
func (c *Config) LoadFromFlags(fs *pflag.FlagSet) error {
	if fs == nil {
		return nil
	}

	// Create a new koanf instance for flag parsing
	k := koanf.New(".")

	// Use posflag provider to parse flags
	if err := k.Load(posflag.Provider(fs, ".", k), nil); err != nil {
		return fmt.Errorf("failed to load flags: %w", err)
	}

	// Unmarshal the parsed flags into the config
	return k.Unmarshal("", c)
}

// GetConcurrentWorkers returns the number of concurrent workers to use
func (c *Config) GetConcurrentWorkers() int {
	if c.Concurrency.MaxWorkers <= 0 {
		// Default to number of CPUs - 1, but at least 1
		return max(1, runtime.NumCPU()-1)
	}
	return c.Concurrency.MaxWorkers
}
