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

// configFlags holds the application configuration from command-line flags
type configFlags struct {
	configFile         string        // Path to config file
	audiobookshelfURL   string        // Audiobookshelf server URL
	audiobookshelfToken string        // Audiobookshelf API token
	hardcoverToken      string        // Hardcover API token
	syncInterval        time.Duration // Sync interval duration
	dryRun              bool          // Enable dry-run mode
	testBookFilter      string        // Filter books by title/author (case-insensitive)
	testBookLimit       int           // Limit number of books to process
	help                bool          // Show help
	version             bool          // Show version
	oneTimeSync         bool          // Run sync once and exit
	serverOnly          bool          // Only run the HTTP server, don't start sync service
}

// parseFlags parses command-line flags and returns the configuration
func parseFlags() *configFlags {
	var cfg configFlags

	// Define flags
	flag.StringVar(&cfg.configFile, "config", "", "Path to config file (YAML/JSON)")
	flag.StringVar(&cfg.audiobookshelfURL, "audiobookshelf-url", "", "Audiobookshelf server URL")
	flag.StringVar(&cfg.audiobookshelfToken, "audiobookshelf-token", "", "Audiobookshelf API token")
	flag.StringVar(&cfg.hardcoverToken, "hardcover-token", "", "Hardcover API token")
	flag.DurationVar(&cfg.syncInterval, "sync-interval", 0, "Sync interval (e.g., 10m, 1h)")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "Run in dry-run mode (no changes will be made)")
	flag.StringVar(&cfg.testBookFilter, "test-book-filter", "", "Filter books by title/author (case-insensitive)")
	flag.IntVar(&cfg.testBookLimit, "test-book-limit", 0, "Limit number of books to process (0 for no limit)")
	flag.BoolVar(&cfg.help, "help", false, "Show help")
	flag.BoolVar(&cfg.version, "version", false, "Show version")
	flag.BoolVar(&cfg.oneTimeSync, "once", false, "Run sync once and exit")
	flag.BoolVar(&cfg.serverOnly, "server-only", false, "Only run the HTTP server, don't start sync service")

	// Parse flags
	flag.Parse()

	// Set environment variables from flags if they're provided
	setEnvFromFlag(cfg.audiobookshelfURL, "AUDIOBOOKSHELF_URL")
	setEnvFromFlag(cfg.audiobookshelfToken, "AUDIOBOOKSHELF_TOKEN")
	setEnvFromFlag(cfg.hardcoverToken, "HARDCOVER_TOKEN")

	if cfg.syncInterval > 0 {
		os.Setenv("SYNC_INTERVAL", cfg.syncInterval.String())
	}

	if cfg.dryRun {
		os.Setenv("DRY_RUN", "true")
	}

	if cfg.testBookFilter != "" {
		os.Setenv("TEST_BOOK_FILTER", cfg.testBookFilter)
	}

	if cfg.testBookLimit > 0 {
		os.Setenv("TEST_BOOK_LIMIT", strconv.Itoa(cfg.testBookLimit))
	}

	return &cfg
}

// setEnvFromFlag sets an environment variable if the flag value is not empty
func setEnvFromFlag(value, envVar string) {
	if value != "" {
		if err := os.Setenv(envVar, value); err != nil {
			logger.Get().Warn("Failed to set environment variable", map[string]interface{}{
				"error": err.Error(),
				"var":    envVar,
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
		"minimum_progress_threshold": cfg.App.MinimumProgress,
		"audiobook_match_mode":       cfg.App.AudiobookMatchMode,
		"sync_want_to_read":          cfg.App.SyncWantToRead,
		"sync_owned":                 cfg.App.SyncOwned,
		"dry_run":                    cfg.App.DryRun,
		"test_book_filter":           cfg.App.TestBookFilter,
		"test_book_limit":            cfg.App.TestBookLimit,
	})

	// Log paths and cache settings
	log.Info("Paths Configuration", map[string]interface{}{
		"cache_dir":           cfg.Paths.CacheDir,
		"mismatch_output_dir": cfg.App.MismatchOutputDir,
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
		"base_url":   cfg.Audiobookshelf.URL,
	})

	log.Debug("Created Hardcover client", map[string]interface{}{
		"client_type": "hardcover",
		"has_token":   cfg.Hardcover.Token != "",
	})

	// Create sync service with detailed logging
	log.Info("Initializing sync service...", nil)
	syncService := sync.NewService(
		audiobookshelfClient,
		hardcoverClient,
		cfg,
	)

	log.Debug("Initialized sync service", map[string]interface{}{
		"dry_run":     cfg.App.DryRun,
		"match_mode": cfg.App.AudiobookMatchMode,
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
		"audiobookshelf_url":     cfg.Audiobookshelf.URL,
		"has_audiobookshelf_token": cfg.Audiobookshelf.Token != "",
		"has_hardcover_token":   cfg.Hardcover.Token != "",
		"dry_run":               cfg.App.DryRun,
		"audiobook_match_mode":   cfg.App.AudiobookMatchMode,
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
		"duration":          duration.String(),
		"duration_seconds": duration.Seconds(),
	})
	log.Info("========================================")
}

// startPeriodicSync starts the periodic sync service
// StartPeriodicSync starts a periodic sync service with the specified interval
func StartPeriodicSync(ctx context.Context, syncService *sync.Service, abortCh <-chan struct{}, interval time.Duration) {
	log := logger.Get()

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
				return
			}
		}
	}()
}
