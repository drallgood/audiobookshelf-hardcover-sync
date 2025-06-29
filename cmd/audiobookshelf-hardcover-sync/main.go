// audiobookshelf-hardcover-sync is the main service for syncing Audiobookshelf with Hardcover.
// It provides both a long-running service with periodic syncs and a one-time sync option.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/server"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

// Package main is the entry point for the Audiobookshelf to Hardcover sync service.
// It provides synchronization between Audiobookshelf and Hardcover, including
// reading progress, book status, and ownership information.
//
// Environment Variables:
//   AUDIOBOOKSHELF_URL      URL to your AudiobookShelf server
//   AUDIOBOOKSHELF_TOKEN    API token for AudiobookShelf
//   HARDCOVER_TOKEN         API token for Hardcover
//   SYNC_INTERVAL           (optional) Go duration string for periodic sync (e.g., "10m", "1h")
//   LOG_LEVEL               (optional) Log level (debug, info, warn, error, fatal, panic)
//   DRY_RUN                 (optional) If set to true, no changes will be made to Hardcover
//   SYNC_WANT_TO_READ       (optional) Sync books with 0% progress as "Want to Read" (default: true)
//   SYNC_OWNED              (optional) Mark synced books as owned in Hardcover (default: true)
//   MINIMUM_PROGRESS_THRESHOLD (optional) Minimum progress threshold for syncing (0.0 to 1.0, default: 0.01)
//   HARDCOVER_RATE_LIMIT    (optional) Maximum number of API requests per second (default: 10)
//
// Endpoints:
//   GET /healthz           # Health check
//   POST/GET /sync         # Trigger a sync

var (
	version = "dev" // Set during build
)

// configFlags is defined in cli.go

func main() {
	// Parse command line flags
	flags := parseFlags()

	// Show help if requested
	if flags.help {
		showHelp()
		return
	}

	// Show version if requested
	if flags.version {
		showVersion()
		return
	}

	// Load configuration first (without initializing logger)
	// We'll use environment variables and command line flags to determine initial log level
	cfg, err := config.Load(flags.configFile)
	if err != nil {
		// If we can't load config, log to stderr with basic formatting
		fmt.Fprintf(os.Stderr, "FATAL: Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize the logger with the configured settings
	logger.Setup(logger.Config{
		Level:      cfg.Logging.Level,
		Format:     logger.ParseLogFormat(cfg.Logging.Format),
		Output:     os.Stdout,
		TimeFormat: time.RFC3339,
	})

	// Get the logger instance
	log := logger.Get()

	// Log application startup
	log.Info("Starting audiobookshelf-hardcover-sync", map[string]interface{}{
		"version":    version,
		"log_level":  cfg.Logging.Level,
		"log_format": cfg.Logging.Format,
	})

	// Log basic configuration info (without sensitive data)
	log.Info("Application configuration", map[string]interface{}{
		"log_level": cfg.Logging.Level,
		"dry_run":   cfg.App.DryRun,
	})

	// Set environment variables from flags if provided
	setEnvFromFlag(flags.audiobookshelfURL, "AUDIOBOOKSHELF_URL")
	setEnvFromFlag(flags.audiobookshelfToken, "AUDIOBOOKSHELF_TOKEN")
	setEnvFromFlag(flags.hardcoverToken, "HARDCOVER_TOKEN")

	// Set test book limit from flags if provided
	if flags.testBookLimit > 0 {
		os.Setenv("TEST_BOOK_LIMIT", strconv.Itoa(flags.testBookLimit))
	}
	
	if flags.syncInterval > 0 {
		os.Setenv("SYNC_INTERVAL", flags.syncInterval.String())
	}
	
	if flags.dryRun {
		os.Setenv("DRY_RUN", "true")
	}
	
	// If one-time sync is requested, run it and exit
	if flags.oneTimeSync {
		RunOneTimeSync(flags)
		return
	}

	// Set up signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize services
	abortCh := make(chan struct{})
	errCh := make(chan error, 1)

	// Create HTTP server with configured port
	srv := server.New(fmt.Sprintf(":%s", cfg.Server.Port))

	// Create API clients
	audiobookshelfClient := audiobookshelf.NewClient(cfg.Audiobookshelf.URL, cfg.Audiobookshelf.Token)
	// Get the global logger instance and pass it to the Hardcover client
	logInstance := logger.Get()
	hardcoverClient := hardcover.NewClient(cfg.Hardcover.Token, logInstance)

	// Create sync service
	syncService := sync.NewService(
		audiobookshelfClient,
		hardcoverClient,
		cfg,
	)

	// Start the HTTP server
	go func() {
		addr := ":" + cfg.Server.Port
		log.Info("Starting HTTP server", map[string]interface{}{
			"addr": addr,
		})
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("failed to start HTTP server: %w", err)
			return
		}
	}()

	// Start periodic sync if enabled and not in server-only mode
	if !flags.serverOnly && cfg.App.SyncInterval > 0 {
		// Use the sync interval from config unless overridden by command line flag
		syncInterval := cfg.App.SyncInterval
		if flags.syncInterval >= 0 {
			syncInterval = flags.syncInterval
		}
		StartPeriodicSync(ctx, syncService, abortCh, syncInterval)
	} else if !flags.serverOnly {
		log.Info("Periodic sync is disabled (set SYNC_INTERVAL to enable)", nil)
	}

	// Wait for shutdown signal or error
	select {
	case <-ctx.Done():
		log.Info("Shutdown signal received", nil)
	case err := <-errCh:
		log.Error("Fatal error occurred", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// Start graceful shutdown
	log.Info("Initiating graceful shutdown...", nil)

	// Cancel any ongoing operations
	stop()

	// Signal any background goroutines to stop
	close(abortCh)

	// Shutdown HTTP server with configured timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	log.Info("Initiating graceful shutdown...", map[string]interface{}{
		"timeout": cfg.Server.ShutdownTimeout.String(),
	})

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("Error during server shutdown", map[string]interface{}{
			"error": err.Error(),
		})
	}

	log.Info("Shutdown completed", nil)
}

// RunOneTimeSync is defined in cli.go

func showHelp() {
	fmt.Println("Audiobookshelf to Hardcover Sync")
	fmt.Println("\nUsage:")
	fmt.Println("  audiobookshelf-hardcover-sync [flags]")

	fmt.Println("\nRequired Configuration (can be provided via flags or environment variables):")
	fmt.Println("  --audiobookshelf-url URL")
	fmt.Println("  \tAudiobookshelf server URL")
	fmt.Println("  \tEnvironment: AUDIOBOOKSHELF_URL")

	fmt.Println("  --audiobookshelf-token TOKEN")
	fmt.Println("  \tAudiobookshelf API token")
	fmt.Println("  \tEnvironment: AUDIOBOOKSHELF_TOKEN")

	fmt.Println("  --hardcover-token TOKEN")
	fmt.Println("  \tHardcover API token")
	fmt.Println("  \tEnvironment: HARDCOVER_TOKEN")

	fmt.Println("\nOptional Configuration:")
	fmt.Println("  --sync-interval DURATION")
	fmt.Println("  \tInterval between syncs (e.g., 1h, 30m, 0 to disable)")
	fmt.Println("  \tEnvironment: SYNC_INTERVAL (duration string, e.g., 1h30m)")

	fmt.Println("  --dry-run")
	fmt.Println("  \tRun in dry-run mode (no changes will be made)")
	fmt.Println("  \tEnvironment: DRY_RUN (true/false)")

	fmt.Println("\nOther Options:")
	fmt.Println("  -h, --help")
	fmt.Println("  \tShow this help message")

	fmt.Println("  -v, --version")
	fmt.Println("  \tShow version information")

	fmt.Println("\nAdditional environment variables:")
	fmt.Println("  LOG_LEVEL")
	fmt.Println("  \tSet the log level (debug, info, warn, error, fatal, panic)")

	fmt.Println("  MINIMUM_PROGRESS_THRESHOLD")
	fmt.Println("  \tMinimum progress threshold for syncing (0.0 to 1.0, default: 0.01)")

	fmt.Println("  SYNC_WANT_TO_READ")
	fmt.Println("  \tSync books with 0% progress as \"Want to Read\" (true/false, default: true)")

	fmt.Println("  SYNC_OWNED")
	fmt.Println("  \tMark synced books as owned in Hardcover (true/false, default: true)")

	fmt.Println("\nExample:")
	fmt.Println(`  audiobookshelf-hardcover-sync \
    --audiobookshelf-url https://audiobookshelf.example.com \
    --audiobookshelf-token your-audiobookshelf-token \
    --hardcover-token your-hardcover-token \
    --sync-interval 1h`)
}

func showVersion() {
	fmt.Printf("audiobookshelf-hardcover-sync version %s\n", version)
}
