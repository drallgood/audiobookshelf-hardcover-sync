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
	"github.com/rs/zerolog/log"
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
			log.Warn().Err(err).Str("var", envVar).Msg("Failed to set environment variable")
		}
	}
}

// initLogger initializes the logger with the appropriate configuration
func initLogger() {
	logger.Setup(logger.Config{
		Level:  "info",
		Output: os.Stdout,
	})
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

	log.Info().Msg("========================================")
	log.Info().Msg("STARTING ONE-TIME SYNC OPERATION")
	log.Info().Msg("========================================")

	// Load configuration from file if specified, otherwise from environment
	log.Info().Str("config_file", flags.configFile).Msg("Loading configuration...")
	cfg, err := config.Load(flags.configFile)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	// Re-initialize logger with config from file
	logger.Setup(logger.Config{
		Level:      "debug",
		Output:     os.Stdout,
		TimeFormat: time.RFC3339,
	})
	log = logger.Get() // Get the reconfigured logger

	log.Info().
		Str("version", version).
		Msg("Starting one-time sync with debug logging")

	// Log detailed configuration
	log.Info().Msg("========================================")
	log.Info().Msg("CONFIGURATION")
	log.Info().Msg("========================================")

	// Log API configuration
	log.Info().
		Str("audiobookshelf_url", cfg.Audiobookshelf.URL).
		Bool("has_audiobookshelf_token", cfg.Audiobookshelf.Token != "").
		Bool("has_hardcover_token", cfg.Hardcover.Token != "").
		Msg("API Configuration")

	// Log sync settings
	log.Info().
		Float64("minimum_progress_threshold", cfg.App.MinimumProgress).
		Str("audiobook_match_mode", cfg.App.AudiobookMatchMode).
		Bool("sync_want_to_read", cfg.App.SyncWantToRead).
		Bool("sync_owned", cfg.App.SyncOwned).
		Bool("dry_run", cfg.App.DryRun).
		Str("test_book_filter", cfg.App.TestBookFilter).
		Int("test_book_limit", cfg.App.TestBookLimit).
		Msg("Sync Settings")

	// Log paths and cache settings
	log.Info().
		Str("cache_dir", cfg.Paths.CacheDir).
		Str("mismatch_output_dir", cfg.App.MismatchOutputDir).
		Msg("Paths Configuration")

	log.Info().Msg("========================================")

	// Create API clients with detailed logging
	log.Info().Msg("Initializing API clients...")
	log.Debug().
		Str("audiobookshelf_url", cfg.Audiobookshelf.URL).
		Bool("has_audiobookshelf_token", cfg.Audiobookshelf.Token != "").
		Bool("has_hardcover_token", cfg.Hardcover.Token != "").
		Msg("Creating API clients")

	audiobookshelfClient := audiobookshelf.NewClient(cfg.Audiobookshelf.URL, cfg.Audiobookshelf.Token)
	// Get the global logger instance and pass it to the Hardcover client
	logInstance := logger.Get()
	hardcoverClient := hardcover.NewClient(cfg.Hardcover.Token, logInstance)

	log.Debug().
		Str("client_type", "audiobookshelf").
		Str("base_url", cfg.Audiobookshelf.URL).
		Msg("Created Audiobookshelf client")

	log.Debug().
		Str("client_type", "hardcover").
		Bool("has_token", cfg.Hardcover.Token != "").
		Msg("Created Hardcover client")

	// Create sync service with detailed logging
	log.Info().Msg("Initializing sync service...")
	syncService := sync.NewService(
		audiobookshelfClient,
		hardcoverClient,
		cfg,
	)

	log.Debug().
		Bool("dry_run", cfg.App.DryRun).
		Str("match_mode", cfg.App.AudiobookMatchMode).
		Msg("Initialized sync service")

	// Create a context with timeout and cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Add logger to context using zerolog's context
	ctx = log.Logger.WithContext(ctx)

	// Run the sync with detailed logging
	log.Info().Msg("========================================")
	log.Info().Msg("STARTING SYNC OPERATION")
	log.Info().Msg("========================================")

	log.Info().Msg("Sync configuration:")
	log.Info().Str("Audiobookshelf URL", cfg.Audiobookshelf.URL).Msg("  -")
	log.Info().Bool("Has Audiobookshelf Token", cfg.Audiobookshelf.Token != "").Msg("  -")
	log.Info().Bool("Has Hardcover Token", cfg.Hardcover.Token != "").Msg("  -")
	log.Info().Bool("Dry Run", cfg.App.DryRun).Msg("  -")
	log.Info().Str("Audiobook Match Mode", cfg.App.AudiobookMatchMode).Msg("  -")

	startTime := time.Now()
	log.Info().Time("start_time", startTime).Msg("Starting sync operation...")

	// Run the sync
	log.Info().Msg("Initiating sync service...")
	err = syncService.Sync(ctx)

	// Log completion
	duration := time.Since(startTime)
	
	if err != nil {
		log.Error().
			Err(err).
			Dur("duration", duration).
			Msg("Sync operation failed")
		os.Exit(1)
	}

	// Log success
	log.Info().
		Dur("duration", duration).
		Float64("duration_seconds", duration.Seconds()).
		Msg("Sync completed successfully")
	log.Info().Msg("========================================")
}

// startPeriodicSync starts the periodic sync service
// StartPeriodicSync starts a periodic sync service with the specified interval
func StartPeriodicSync(ctx context.Context, syncService *sync.Service, abortCh <-chan struct{}, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Initial sync
		if err := syncService.Sync(ctx); err != nil {
			log.Error().Err(err).Msg("Initial sync failed")
		}

		for {
			select {
			case <-ticker.C:
				if err := syncService.Sync(ctx); err != nil {
					log.Error().Err(err).Msg("Periodic sync failed")
				}
			case <-abortCh:
				return
			}
		}
	}()
}
