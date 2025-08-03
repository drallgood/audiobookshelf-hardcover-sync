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

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/server"
)

// Package main is the entry point for the Audiobookshelf to Hardcover sync service.
// It provides synchronization between Audiobookshelf and Hardcover, including
// reading progress, book status, and ownership information.
//
// Environment Variables:
//   AUDIOBOOKSHELF_URL      URL to your AudiobookShelf server (legacy single-user mode)
//   AUDIOBOOKSHELF_TOKEN    API token for AudiobookShelf (legacy single-user mode)
//   HARDCOVER_TOKEN         API token for Hardcover (legacy single-user mode)
//   SYNC_INTERVAL           (optional) Go duration string for periodic sync (e.g., "10m", "1h")
//   LOG_LEVEL               (optional) Log level (debug, info, warn, error, fatal, panic)
//   DRY_RUN                 (optional) If set to true, no changes will be made to Hardcover
//   SYNC_WANT_TO_READ       (optional) Sync books with 0% progress as "Want to Read" (default: true)
//   SYNC_OWNED              (optional) Mark synced books as owned in Hardcover (default: true)
//   MINIMUM_PROGRESS_THRESHOLD (optional) Minimum progress threshold for syncing (0.0 to 1.0, default: 0.01)
//   HARDCOVER_RATE_LIMIT    (optional) Maximum number of API requests per second (default: 10)
//   ENCRYPTION_KEY          (optional) Base64-encoded 32-byte key for token encryption (auto-generated if not set)
//   DATA_DIR                (optional) Directory for database and encryption key files (default: ./data)
//
// Endpoints:
//   GET /healthz           # Health check
//   POST/GET /sync         # Trigger a sync (legacy single-user mode)
//   GET /                  # Multi-user web interface
//   GET /api/users         # List all users
//   POST /api/users        # Create a new user
//   PUT /api/users/:id     # Update user configuration
//   DELETE /api/users/:id  # Delete a user
//   POST /api/users/:id/sync/start  # Start sync for a user
//   POST /api/users/:id/sync/cancel # Cancel sync for a user
//   GET /api/users/:id/sync/status  # Get sync status for a user

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

	// Initialize multi-user system
	log.Info("Initializing multi-user system", nil)
	
	// Set up database with config.yaml and environment-based configuration
	// Create database config from config.yaml with environment variable override
	configDB := &database.ConfigDatabase{
		Type:           cfg.Database.Type,
		Host:           cfg.Database.Host,
		Port:           cfg.Database.Port,
		Name:           cfg.Database.Name,
		User:           cfg.Database.User,
		Password:       cfg.Database.Password,
		Path:           cfg.Database.Path,
		SSLMode:        cfg.Database.SSLMode,
	}
	configDB.ConnectionPool.MaxOpenConns = cfg.Database.ConnectionPool.MaxOpenConns
	configDB.ConnectionPool.MaxIdleConns = cfg.Database.ConnectionPool.MaxIdleConns
	configDB.ConnectionPool.ConnMaxLifetime = cfg.Database.ConnectionPool.ConnMaxLifetime
	
	dbConfig := database.NewDatabaseConfigFromConfig(configDB)
	db, err := database.NewDatabase(dbConfig, log)
	if err != nil {
		log.Error("Failed to initialize database", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	defer db.Close()
	
	// Set up encryption
	encryptor, err := crypto.NewEncryptionManager(log)
	if err != nil {
		log.Error("Failed to initialize encryption", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	
	// Set up repository
	repo := database.NewRepository(db, encryptor, log)
	
	// Perform automatic migration from single-user config if needed
	// Use the actual config path that was loaded, not default search paths
	configPath := flags.configFile
	dbPath := database.GetDefaultDatabasePath() // Get default SQLite path for migration
	log.Info("Checking migration from config", map[string]interface{}{
		"config_path": configPath,
		"db_path": dbPath,
	})
	if err := database.AutoMigrate(dbPath, configPath, log); err != nil {
		log.Error("Failed to perform migration", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	
	// Create multi-user service
	multiUserService := multiuser.NewMultiUserService(repo, cfg, log)
	
	// Initialize authentication system
	log.Info("Initializing authentication system", nil)
	// Convert config.yaml auth config to internal auth config with env overrides
	configAuth := &auth.ConfigAuth{
		Enabled: cfg.Authentication.Enabled,
		Session: struct {
			Secret     string `yaml:"secret"`
			CookieName string `yaml:"cookie_name"`
			MaxAge     int    `yaml:"max_age"`
			Secure     bool   `yaml:"secure"`
			HttpOnly   bool   `yaml:"http_only"`
			SameSite   string `yaml:"same_site"`
		}{
			Secret:     cfg.Authentication.Session.Secret,
			CookieName: cfg.Authentication.Session.CookieName,
			MaxAge:     cfg.Authentication.Session.MaxAge,
			Secure:     cfg.Authentication.Session.Secure,
			HttpOnly:   cfg.Authentication.Session.HttpOnly,
			SameSite:   cfg.Authentication.Session.SameSite,
		},
		DefaultAdmin: struct {
			Username string `yaml:"username"`
			Email    string `yaml:"email"`
			Password string `yaml:"password"`
		}{
			Username: cfg.Authentication.DefaultAdmin.Username,
			Email:    cfg.Authentication.DefaultAdmin.Email,
			Password: cfg.Authentication.DefaultAdmin.Password,
		},
		Keycloak: struct {
			Enabled      bool   `yaml:"enabled"`
			Issuer       string `yaml:"issuer"`
			ClientID     string `yaml:"client_id"`
			ClientSecret string `yaml:"client_secret"`
			RedirectURI  string `yaml:"redirect_uri"`
			Scopes       string `yaml:"scopes"`
			RoleClaim    string `yaml:"role_claim"`
		}{
			Enabled:      cfg.Authentication.Keycloak.Enabled,
			Issuer:       cfg.Authentication.Keycloak.Issuer,
			ClientID:     cfg.Authentication.Keycloak.ClientID,
			ClientSecret: cfg.Authentication.Keycloak.ClientSecret,
			RedirectURI:  cfg.Authentication.Keycloak.RedirectURI,
			Scopes:       cfg.Authentication.Keycloak.Scopes,
			RoleClaim:    cfg.Authentication.Keycloak.RoleClaim,
		},
	}
	authConfig := auth.NewAuthConfigFromConfig(configAuth)
	authService, err := auth.NewAuthService(db.GetDB(), authConfig, log)
	if err != nil {
		log.Error("Failed to initialize authentication service", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	
	// Initialize default admin user if authentication is enabled and no users exist
	if authConfig.Enabled {
		if err := authService.InitializeDefaultUser(ctx); err != nil {
			log.Error("Failed to initialize default admin user", map[string]interface{}{
				"error": err.Error(),
			})
			// Don't exit - this is not critical
		}
		log.Info("Authentication system initialized", map[string]interface{}{
			"enabled": authConfig.Enabled,
			"providers": len(authConfig.Providers),
		})
	} else {
		log.Info("Authentication system disabled", nil)
	}
	// Create HTTP server with multi-user and authentication support
	srv := server.New(fmt.Sprintf(":%s", cfg.Server.Port), multiUserService, authService, log)

	// Start the HTTP server
	go func() {
		log.Info("Starting HTTP server", map[string]interface{}{
			"port": cfg.Server.Port,
		})

		// Start the server
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("failed to start server: %w", err)
		}
	}()

	// Start periodic sync for all users if enabled
	if !flags.serverOnly && cfg.App.SyncInterval > 0 {
		syncInterval := cfg.App.SyncInterval
		if flags.syncInterval > 0 {
			syncInterval = flags.syncInterval
		}

		log.Info("Starting periodic sync for all users", map[string]interface{}{
			"interval": syncInterval.String(),
		})

		// Start a ticker for periodic sync
		ticker := time.NewTicker(syncInterval)
		defer ticker.Stop()

		// Start the first sync after a short delay to avoid immediate sync on startup
		// This allows the application to fully initialize before starting the first sync
		initialSyncTicker := time.NewTicker(5 * time.Second)
		defer initialSyncTicker.Stop()

		// Periodic sync
		go func() {
			// Initial sync after delay
			<-initialSyncTicker.C
			users, err := repo.ListUsers()
			if err != nil {
				log.Error("Failed to list users for initial sync", map[string]interface{}{
					"error": err.Error(),
				})
			} else {
				for _, user := range users {
					log.Info("Starting initial sync for user", map[string]interface{}{
						"user_id": user.ID,
					})
					go func(userID string) {
						log.Info("Starting initial sync for user", map[string]interface{}{
							"user_id": userID,
						})
						if err := multiUserService.StartSync(userID); err != nil {
							log.Error("Failed to start sync for user", map[string]interface{}{
								"user_id": userID,
								"error":   err.Error(),
							})
						}
					}(user.ID)
				}
			}
			initialSyncTicker.Stop()

			// Regular periodic syncs
			for {
				select {
				case <-ticker.C:
					users, err := repo.ListUsers()
					if err != nil {
						log.Error("Failed to list users for periodic sync", map[string]interface{}{
							"error": err.Error(),
						})
						continue
					}

					for _, user := range users {
						// Skip if user is already syncing
						if multiUserService.IsUserSyncing(user.ID) {
							log.Debug("Sync already in progress for user, skipping", map[string]interface{}{
								"user_id": user.ID,
							})
							continue
						}
						log.Info("Starting periodic sync for user", map[string]interface{}{
							"user_id": user.ID,
						})
						go multiUserService.StartSync(user.ID)
					}

				case <-ctx.Done():
					return
				}
			}
		}()
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
	fmt.Println("  --config FILE")
	fmt.Println("  \tPath to config file (YAML/JSON)")
	fmt.Println("  \tEnvironment: CONFIG_PATH")

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
