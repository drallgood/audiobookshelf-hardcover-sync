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

// init initializes the logger with default values
func init() {
	logger.Setup(logger.Config{
		Level:      "info",
		Format:     logger.FormatJSON,
		TimeFormat: time.RFC3339,
	})
}

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
	// Initialize the logger via init() function
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

// generateExampleJSON generates an example JSON file for creating an edition
func generateExampleJSON(filename string) error {
	example := EditionCreatorInput{
		BookID:        12345,
		Title:         "Example Audiobook",
		Subtitle:      "Unabridged",
		ImageURL:      "https://example.com/cover.jpg",
		ASIN:          "B00XXXYYZZ",
		ISBN10:        "1234567890",
		ISBN13:        "9781234567890",
		AuthorIDs:     []int{1, 2, 3},
		NarratorIDs:   []int{4, 5},
		PublisherID:   10,
		ReleaseDate:   time.Now().Format("2006-01-02"),
		AudioLength:   3600, // 1 hour in seconds
		EditionFormat: "Audible Audio",
		EditionInfo:   "Special edition with bonus content",
		LanguageID:    1, // English
		CountryID:     1, // USA
	}

	data, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal example JSON: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write example file: %w", err)
	}

	fmt.Printf("Example JSON written to %s\n", filename)
	return nil
}
