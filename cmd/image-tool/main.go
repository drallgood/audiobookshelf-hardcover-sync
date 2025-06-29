// image-tool is a command-line tool for managing book and edition cover images in Hardcover.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

func main() {
	// Check if help is requested
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		printUsage()
		return
	}

	// Check for the upload subcommand
	if len(os.Args) > 1 && os.Args[1] == "upload" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}

	// Define command-line flags
	var (
		imageURL    = flag.String("url", "", "URL of the image to upload (required)")
		bookID      = flag.String("book", "", "Hardcover book ID to attach the image to (mutually exclusive with -edition)")
		editionID   = flag.String("edition", "", "Hardcover edition ID to attach the image to (mutually exclusive with -book)")
		descFlag    = flag.String("desc", "", "Optional description for the image (alias for -description)")
		description = flag.String("description", "", "Optional description for the image (alias for -desc)")
		configFile  = flag.String("config", "", "Path to config file (default: config.yaml in current directory or /etc/audiobookshelf-hardcover-sync/)")
	)

	// Parse flags
	flag.Parse()

	// Use description from either flag (description flag takes precedence if both are provided)
	imageDescription := *description
	if imageDescription == "" {
		imageDescription = *descFlag
	}

	// Customize flag usage output
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Upload a cover image to a book or edition in Hardcover\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [command] [flags]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  upload    Upload a cover image to a book or edition")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nEnvironment variables:")
		fmt.Fprintln(os.Stderr, "  HARDCOVER_TOKEN  Authentication token for Hardcover API (required)")
	}

	// Load configuration
	cfg, err := loadConfig(*configFile)
	if err != nil {
		// Initialize a basic logger for config loading errors
		logger.Setup(logger.Config{
			Level:      "info",
			Format:     logger.FormatConsole,
			TimeFormat: time.RFC3339,
		})
		logger.Get().Error("Failed to load configuration", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	// Set up logging from config
	logFormat := logger.FormatJSON
	if cfg.Logging.Format == "console" {
		logFormat = logger.FormatConsole
	}

	logger.Setup(logger.Config{
		Level:      cfg.Logging.Level,
		Format:     logFormat,
		TimeFormat: time.RFC3339,
	})

	log := logger.Get()

	// Validate required flags
	if (*bookID == "" && *editionID == "") || *imageURL == "" {
		log.Error("Either --book or --edition is required, and --url is required", nil)
		flag.Usage()
		os.Exit(1)
	}

	if *bookID != "" && *editionID != "" {
		log.Error("Only one of --book or --edition can be specified", nil)
		flag.Usage()
		os.Exit(1)
	}

	// Execute the upload with config
	if *bookID != "" {
		uploadBookImage(*imageURL, *bookID, imageDescription, cfg)
	} else {
		// Validate edition ID is a number but keep it as string for the API
		if _, err := strconv.Atoi(*editionID); err != nil {
			log.Error("Invalid edition ID format - must be a number", map[string]interface{}{
				"error": err.Error(),
			})
			os.Exit(1)
		}
		uploadEditionImage(*imageURL, *editionID, imageDescription, cfg)
	}
}

// loadConfig loads configuration from file and environment variables
func loadConfig(configPath string) (*config.Config, error) {
	// If no config file specified, try default locations
	if configPath == "" {
		// Try current directory
		if _, err := os.Stat("config.yaml"); err == nil {
			configPath = "config.yaml"
		} else {
			// Try system config directory
			systemConfig := "/etc/audiobookshelf-hardcover-sync/config.yaml"
			if _, err := os.Stat(systemConfig); err == nil {
				configPath = systemConfig
			}
		}
	}

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return cfg, nil
}

// uploadBookImage handles the image upload to a book in Hardcover
func uploadBookImage(imageURL, bookID, description string, cfg *config.Config) {
	// Create a logger instance with relevant fields
	log := logger.Get().WithFields(map[string]interface{}{
		"url":         imageURL,
		"bookID":      bookID,
		"description": description,
	})

	log.Info("Starting book image upload to Hardcover", nil)

	// Get the API token from config
	token := cfg.Hardcover.Token
	if token == "" {
		log.Error("Hardcover token is required in configuration", nil)
		os.Exit(1)
	}

	// Create a new client with configuration
	hcCfg := hardcover.DefaultClientConfig()
	// Use a default timeout if not specified in config
	timeout := 30 * time.Second
	if cfg.Server.ShutdownTimeout > 0 {
		timeout = cfg.Server.ShutdownTimeout
	}
	client := hardcover.NewClientWithConfig(hcCfg, token, logger.Get())

	// Create a context with timeout from config
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Create a creator instance
	creator := edition.NewCreator(client, logger.Get(), false, cfg.Audiobookshelf.Token)

	// Convert bookID to int (assuming it's a valid number)
	bookIDInt, err := strconv.Atoi(bookID)
	if err != nil {
		log.Error("Invalid book ID format - must be a number", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	// Upload the book image using the creator
	log.Info("Uploading book cover image...", nil)
	err = creator.UploadEditionImage(ctx, bookIDInt, imageURL, description)
	if err != nil {
		log.Error("Failed to upload book cover image", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	log.Info("Successfully uploaded book cover image to Hardcover", map[string]interface{}{
		"bookID":   bookID,
		"imageURL": imageURL,
	})
}

// uploadEditionImage handles the image upload to an edition in Hardcover
func uploadEditionImage(imageURL string, editionID string, description string, cfg *config.Config) {
	// Create a logger instance with relevant fields
	log := logger.Get().WithFields(map[string]interface{}{
		"url":         imageURL,
		"editionID":   editionID,
		"description": description,
	})

	log.Info("Starting edition image upload to Hardcover", nil)

	// Get the API token from config
	token := cfg.Hardcover.Token
	if token == "" {
		log.Error("Hardcover token is required in configuration", nil)
		os.Exit(1)
	}

	// Create a new client with configuration
	hcCfg := hardcover.DefaultClientConfig()
	// Use a default timeout if not specified in config
	timeout := 30 * time.Second
	if cfg.Server.ShutdownTimeout > 0 {
		timeout = cfg.Server.ShutdownTimeout
	}

	// Create a context with timeout from config
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Create a new client and creator
	client := hardcover.NewClientWithConfig(hcCfg, token, logger.Get())
	creator := edition.NewCreator(client, logger.Get(), false, cfg.Audiobookshelf.Token)

	// Convert editionID to int
	editionIDInt, err := strconv.Atoi(editionID)
	if err != nil {
		log.Error("Invalid edition ID format", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	// Upload the edition image using the creator
	log.Info("Uploading edition cover image...", nil)
	err = creator.UploadEditionImage(ctx, editionIDInt, imageURL, description)
	if err != nil {
		log.Error("Failed to upload edition cover image", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	log.Info("Successfully uploaded edition cover image to Hardcover", map[string]interface{}{
		"editionID": editionID,
		"imageURL":  imageURL,
	})
}

func printUsage() {
	fmt.Println(`Hardcover Image Tool

Usage:
  image-tool [command] [flags]
  image-tool [flags]

Commands:
  upload    Upload a cover image to a book or edition

Flags:
  -book string        Hardcover book ID (mutually exclusive with -edition)
  -config string      Path to config file (default: config.yaml in current directory or /etc/audiobookshelf-hardcover-sync/)
  -desc string        Optional description for the image (alias for -description)
  -description string  Optional description for the image (alias for -desc)
  -edition string     Hardcover edition ID (mutually exclusive with -book)
  -url string         URL of the image to upload (required)

Examples:
  # Upload a cover image to a book with a description
  image-tool upload -url https://example.com/cover.jpg -book 123 -desc "Cover art"
  
  # Upload a cover image to an edition
  image-tool upload -url https://example.com/edition-cover.jpg -edition 456 -desc "Special edition cover"
  
  # Legacy format (without upload command)
  image-tool -url https://example.com/cover.jpg -book 123`)
}
