// Package edition provides a command-line tool for creating new audiobook editions in Hardcover.
// It supports creating editions from scratch or prepopulating data from existing books.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/urfave/cli/v2"
)

// init is intentionally left empty to allow configuration to be loaded first

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// EditionCreatorInput is an alias for edition.EditionInput
type EditionCreatorInput = edition.EditionInput

// EditionCreatorResult is an alias for edition.EditionResult
type EditionCreatorResult = edition.EditionResult

func main() {
	// Parse command line args manually to get config path
	configPath := "config.yaml"
	args := os.Args[1:]
	for i, arg := range args {
		if (arg == "-c" || arg == "--config") && i+1 < len(args) {
			configPath = args[i+1]
			break
		}
	}

	// Load configuration first
	cfg, err := config.Load(configPath)
	if err != nil {
		// If we can't load config, use default logger settings
		logger.Setup(logger.Config{
			Level:      "info",
			Format:     logger.FormatJSON,
			TimeFormat: time.RFC3339,
		})
		logger.Get().Error("Failed to load config, using default logger settings", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		// Initialize logger with config values
		logger.Setup(logger.Config{
			Level:      cfg.Logging.Level,
			Format:     logger.ParseLogFormat(cfg.Logging.Format),
			Output:     os.Stdout,
			TimeFormat: time.RFC3339,
		})
	}

	// Now create and run the CLI app
	app := &cli.App{
		Name:    "edition",
		Usage:   "Create and manage audiobook editions in Hardcover",
		Version: fmt.Sprintf("%s (%s) %s", version, commit, date),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Load configuration from `FILE`",
				Value:   "config.yaml",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Enable dry run mode (no changes will be made)",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Create a new audiobook edition",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "input",
						Aliases:  []string{"i"},
						Usage:    "Input JSON file with edition data",
						Required: true,
					},
				},
				Action: createEdition,
			},
			{
				Name:  "prepopulate",
				Usage: "Generate a prepopulated JSON template for a book",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:     "book-id",
						Usage:    "Hardcover book ID to prepopulate from",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "output",
						Aliases:  []string{"o"},
						Usage:    "Output JSON file",
						Value:    "edition-template.json",
					},
				},
				Action: prepopulateEdition,
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		// Use the logger to ensure consistent format
		logger.Get().Error("Error running application", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
}

func createEdition(c *cli.Context) error {
	// Initialize configuration
	cfg, err := config.LoadFromFile(c.String("config"))
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Get the logger
	log := logger.Get()

	// Load input JSON
	inputFile := c.String("input")
	data, err := os.ReadFile(inputFile)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	var input EditionCreatorInput
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("invalid JSON input: %w", err)
	}

	// Initialize Hardcover client and creator
	hc := hardcover.NewClient(cfg.Hardcover.Token, log)
	// Get Audiobookshelf token from config
	audiobookshelfToken := cfg.Audiobookshelf.Token
	if audiobookshelfToken == "" {
		log.Warn("No Audiobookshelf token found in config, image uploads may fail")
	} else {
		log.Debug("Using Audiobookshelf token from config")
	}
	creator := edition.NewCreator(hc, log, c.Bool("dry-run"), audiobookshelfToken)

	// Create edition
	result, err := creator.CreateEdition(context.Background(), &input)
	if err != nil {
		return fmt.Errorf("failed to create edition: %w", err)
	}

	// Output result
	output, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(output))
	return nil
}

func prepopulateEdition(c *cli.Context) error {
	// Initialize configuration
	cfg, err := config.LoadFromFile(c.String("config"))
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Get the logger
	log := logger.Get()

	// Initialize Hardcover client and creator
	hc := hardcover.NewClient(cfg.Hardcover.Token, log)
	// Get Audiobookshelf token from config
	audiobookshelfToken := cfg.Audiobookshelf.Token
	if audiobookshelfToken == "" {
		log.Warn("No Audiobookshelf token found in config, image uploads may fail")
	} else {
		log.Debug("Using Audiobookshelf token from config")
	}
	creator := edition.NewCreator(hc, log, c.Bool("dry-run"), audiobookshelfToken)

	// Generate prepopulated data
	prepopulated, err := creator.PrepopulateFromBook(context.Background(), c.Int("book-id"))
	if err != nil {
		return fmt.Errorf("failed to prepopulate data: %w", err)
	}

	// Write to output file
	outputFile := c.String("output")
	output, _ := json.MarshalIndent(prepopulated, "", "  ")
	if err := os.WriteFile(outputFile, output, 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	fmt.Printf("Prepopulated data written to %s\n", outputFile)
	return nil
}
