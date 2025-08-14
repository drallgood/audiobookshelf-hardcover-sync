package main

import (
	"context"
	"flag"
	"os"
	"strconv"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

// boolFlag is a custom flag type that tracks if a boolean flag was explicitly set
type boolFlag struct {
	value bool
	set   bool
}

// String implements the flag.Value interface
func (b *boolFlag) String() string {
	return strconv.FormatBool(b.value)
}

// Set implements the flag.Value interface
func (b *boolFlag) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	b.value = v
	b.set = true
	return nil
}

// IsBoolFlag makes the flag a boolean flag
func (b *boolFlag) IsBoolFlag() bool {
	return true
}

// configFlags holds the application configuration from command-line flags
type configFlags struct {
	configFile          string        // Path to config file
	audiobookshelfURL   string        // Audiobookshelf server URL
	audiobookshelfToken string        // Audiobookshelf API token
	hardcoverToken      string        // Hardcover API token
	syncInterval        time.Duration // Sync interval duration
	dryRun              *boolFlag     // Enable dry-run mode
	testBookFilter      string        // Filter books by title/author (case-insensitive)
	testBookLimit       int           // Limit number of books to process
	help                *boolFlag     // Show help
	version             *boolFlag     // Show version
	oneTimeSync         *boolFlag     // Run sync once and exit
	serverOnly          *boolFlag     // Only run the HTTP server, don't start sync service
}

// parseFlags parses command-line flags and returns the configuration
func parseFlags() *configFlags {
	cfg := configFlags{
		dryRun:      &boolFlag{value: false, set: false},
		help:        &boolFlag{value: false, set: false},
		version:     &boolFlag{value: false, set: false},
		oneTimeSync: &boolFlag{value: false, set: false},
		serverOnly:  &boolFlag{value: false, set: false},
	}

	// Define flags with our custom boolFlag type
	flag.Var(cfg.dryRun, "dry-run", "Run in dry-run mode (no changes will be made)")
	flag.Var(cfg.help, "help", "Show help")
	flag.Var(cfg.version, "version", "Show version")
	flag.Var(cfg.oneTimeSync, "once", "Run sync once and exit")
	flag.Var(cfg.serverOnly, "server-only", "Only run the HTTP server, don't start sync service")

	// String flags need to be pointers to detect if they were set
	configFile := flag.String("config", "", "Path to config file (YAML/JSON)")
	audiobookshelfURL := flag.String("audiobookshelf-url", "", "Audiobookshelf server URL")
	audiobookshelfToken := flag.String("audiobookshelf-token", "", "Audiobookshelf API token")
	hardcoverToken := flag.String("hardcover-token", "", "Hardcover API token")
	syncInterval := flag.Duration("sync-interval", -1, "Sync interval (e.g., 10m, 1h). Defaults to config value if not set")
	testBookFilter := flag.String("test-book-filter", "", "Filter books by title/author (case-insensitive)")
	testBookLimit := flag.Int("test-book-limit", -1, "Limit number of books to process (-1 for no limit)")

	// Parse flags
	flag.Parse()

	// Set environment variables from boolean flags if they were explicitly set
	if cfg.dryRun.set {
		// Use the environment variable name that matches the config struct tag (DRY_RUN)
		os.Setenv("DRY_RUN", strconv.FormatBool(cfg.dryRun.value))
	}
	if cfg.help.set {
		os.Setenv("HELP", strconv.FormatBool(cfg.help.value))
	}
	if cfg.version.set {
		os.Setenv("VERSION", strconv.FormatBool(cfg.version.value))
	}
	if cfg.oneTimeSync.set {
		os.Setenv("ONCE", strconv.FormatBool(cfg.oneTimeSync.value))
	}
	if cfg.serverOnly.set {
		os.Setenv("SERVER_ONLY", strconv.FormatBool(cfg.serverOnly.value))
	}

	// Environment variables for non-boolean flags

	// Only set string values if they were explicitly provided
	if *configFile != "" {
		cfg.configFile = *configFile
	} else {
		// Check for CONFIG_PATH environment variable if no config file was specified via flags
		cfg.configFile = os.Getenv("CONFIG_PATH")
	}

	// Only set environment variables from flags if provided
	setEnvFromFlag(*audiobookshelfURL, "AUDIOBOOKSHELF_URL")
	setEnvFromFlag(*audiobookshelfToken, "AUDIOBOOKSHELF_TOKEN")
	setEnvFromFlag(*hardcoverToken, "HARDCOVER_TOKEN")

	// Explicitly set DRY_RUN environment variable if the flag was set
	if cfg.dryRun.set {
		os.Setenv("DRY_RUN", strconv.FormatBool(cfg.dryRun.value))
		logger.Get().Debug("Set DRY_RUN from CLI flag", map[string]interface{}{
			"dry_run": cfg.dryRun.value,
		})
	}

	if *hardcoverToken != "" {
		cfg.hardcoverToken = *hardcoverToken
		os.Setenv("HARDCOVER_TOKEN", *hardcoverToken)
	}

	// Only set sync interval if it was explicitly provided
	if *syncInterval >= 0 {
		cfg.syncInterval = *syncInterval
		os.Setenv("SYNC_INTERVAL", syncInterval.String())
	}

	// Only set test book filter if it was explicitly provided
	if *testBookFilter != "" {
		cfg.testBookFilter = *testBookFilter
		os.Setenv("TEST_BOOK_FILTER", *testBookFilter)
	}

	// Only set test book limit if it was explicitly provided
	if *testBookLimit >= 0 {
		cfg.testBookLimit = *testBookLimit
		os.Setenv("TEST_BOOK_LIMIT", strconv.Itoa(*testBookLimit))
	}

	return &cfg
}

// setEnvFromFlag sets an environment variable if the flag value is not empty
func setEnvFromFlag(value, envVar string) {
	if value != "" {
		if err := os.Setenv(envVar, value); err != nil {
			logger.Get().Warn("Failed to set environment variable", map[string]interface{}{
				"error": err.Error(),
				"var":   envVar,
			})
		}
	}
}

// runOneTimeSync performs a single sync operation and exits
// RunOneTimeSync performs a one-time sync operation with the given flags
func RunOneTimeSync(flags *configFlags) {
	// Initialize logger with debug level for one-time sync
	logger.Setup(logger.Config{
		Level:      "debug",
		Format:     logger.FormatJSON, // Default to JSON format initially
		Output:     os.Stdout,
		TimeFormat: time.RFC3339,
	})
	log := logger.Get()

	log.Info("========================================", nil)
	log.Info("STARTING ONE-TIME SYNC OPERATION")
	log.Info("========================================")

	// Load configuration from file if specified, otherwise from environment
	log.Info("Loading configuration...", map[string]interface{}{
		"config_file": flags.configFile,
	})
	cfg, err := config.Load(flags.configFile)
	if err != nil {
		log.Error("Failed to load configuration", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	// Re-initialize logger with config from file
	logger.Setup(logger.Config{
		Level:      "debug",
		Format:     logger.ParseLogFormat(cfg.Logging.Format),
		Output:     os.Stdout,
		TimeFormat: time.RFC3339,
	})
	log = logger.Get() // Get the reconfigured logger

	log.Info("Starting one-time sync with debug logging", map[string]interface{}{
		"version": version,
	})

	// Log detailed configuration
	log.Info("========================================", nil)
	log.Info("CONFIGURATION", nil)
	log.Info("========================================", nil)

	// Log API configuration
	log.Info("API Configuration", map[string]interface{}{
		"audiobookshelf_url":       cfg.Audiobookshelf.URL,
		"has_audiobookshelf_token": cfg.Audiobookshelf.Token != "",
		"has_hardcover_token":      cfg.Hardcover.Token != "",
	})

	// Log sync settings
	log.Info("Sync Settings", map[string]interface{}{
		"minimum_progress_threshold": cfg.Sync.MinimumProgress,
		"sync_want_to_read":          cfg.Sync.SyncWantToRead,
		"sync_owned":                 cfg.Sync.SyncOwned,
		"dry_run":                    cfg.Sync.DryRun,
		"test_book_filter":           cfg.Sync.TestBookFilter,
		"test_book_limit":            cfg.Sync.TestBookLimit,
	})

	// Check for deprecated config values and log warnings
	if cfg.App.SyncWantToRead {
		log.Warn("DEPRECATED: 'app.sync_want_to_read' is deprecated. Please use 'sync.sync_want_to_read' instead.", nil)
	}
	if cfg.App.SyncOwned {
		log.Warn("DEPRECATED: 'app.sync_owned' is deprecated. Please use 'sync.sync_owned' instead.", nil)
	}
	// Log paths and cache settings
	log.Info("Paths Configuration", map[string]interface{}{
		"cache_dir":           cfg.Paths.CacheDir,
		"mismatch_output_dir": cfg.Paths.MismatchOutputDir,
	})

	log.Info("========================================")

	// Create API clients with detailed logging
	log.Info("Initializing API clients...")
	log.Debug("Creating API clients", map[string]interface{}{
		"audiobookshelf_url":       cfg.Audiobookshelf.URL,
		"has_audiobookshelf_token": cfg.Audiobookshelf.Token != "",
		"has_hardcover_token":      cfg.Hardcover.Token != "",
	})

	audiobookshelfClient := audiobookshelf.NewClient(cfg.Audiobookshelf.URL, cfg.Audiobookshelf.Token)
	// Get the global logger instance and pass it to the Hardcover client
	logInstance := logger.Get()
	hardcoverClient := hardcover.NewClient(cfg.Hardcover.Token, logInstance)

	log.Debug("Created Audiobookshelf client", map[string]interface{}{
		"client_type": "audiobookshelf",
		"base_url":    cfg.Audiobookshelf.URL,
	})

	log.Debug("Created Hardcover client", map[string]interface{}{
		"client_type": "hardcover",
		"has_token":   cfg.Hardcover.Token != "",
	})

	// Create sync service with detailed logging
	log.Info("Initializing sync service...", nil)
	syncService, err := sync.NewService(
		audiobookshelfClient,
		hardcoverClient,
		cfg,
	)
	if err != nil {
		log.Error("Failed to initialize sync service", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	log.Debug("Initialized sync service", map[string]interface{}{
		"dry_run": cfg.Sync.DryRun,
	})

	// Create a context with timeout and cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Add logger to context using zerolog's context
	ctx = log.Logger.WithContext(ctx)

	// Run the sync with detailed logging
	log.Info("========================================", map[string]interface{}{})
	log.Info("STARTING SYNC OPERATION", map[string]interface{}{})
	log.Info("========================================", map[string]interface{}{})

	log.Info("Sync configuration:", map[string]interface{}{
		"audiobookshelf_url":       cfg.Audiobookshelf.URL,
		"has_audiobookshelf_token": cfg.Audiobookshelf.Token != "",
		"has_hardcover_token":      cfg.Hardcover.Token != "",
		"dry_run":                  cfg.Sync.DryRun,
	})

	startTime := time.Now()
	log.Info("Starting sync operation...", map[string]interface{}{
		"start_time": startTime,
	})

	// Run the sync
	log.Info("Initiating sync service...", map[string]interface{}{
		"client_type": "sync",
	})
	err = syncService.Sync(ctx)

	// Log completion
	duration := time.Since(startTime)

	if err != nil {
		logger.Get().Error("Sync operation failed", map[string]interface{}{
			"error":    err.Error(),
			"duration": duration.String(),
		})
		os.Exit(1)
	}

	// Log success
	logger.Get().Info("Sync completed successfully", map[string]interface{}{
		"duration":         duration.String(),
		"duration_seconds": duration.Seconds(),
	})
	log.Info("========================================")
}

// startPeriodicSync starts the periodic sync service
// StartPeriodicSync starts a periodic sync service with the specified interval
// Note: The interval is assumed to be valid (positive) as it should have been validated by config
func StartPeriodicSync(ctx context.Context, syncService *sync.Service, abortCh <-chan struct{}, interval time.Duration) {
	log := logger.Get()

	log.Info("Starting periodic sync service", map[string]interface{}{
		"interval": interval.String(),
	})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Initial sync
		if err := syncService.Sync(ctx); err != nil {
			log.Error("Initial sync failed", map[string]interface{}{
				"error": err.Error(),
			})
		}

		for {
			select {
			case <-ticker.C:
				if err := syncService.Sync(ctx); err != nil {
					log.Error("Periodic sync failed", map[string]interface{}{
						"error": err.Error(),
					})
				}
			case <-abortCh:
				log.Info("Received shutdown signal, stopping periodic sync", nil)
				return
			}
		}
	}()
}
