package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
)

func main() {
	// Initialize logger with console format for better readability
	loggerConfig := logger.Config{
		Level:  "debug",
		Format: logger.FormatConsole,
	}
	logger.Setup(loggerConfig)
	log := logger.Get()

	log.Info("Starting mismatch test", nil)

	// Create a test mismatch with ASIN
	metadata := mismatch.MediaMetadata{
		Title:         "Project Hail Mary",
		AuthorName:    "Andy Weir",
		NarratorName:  "Ray Porter",
		Publisher:     "Audible Studios",
		PublishedYear: "2021",
		ASIN:          "B08GB58KD5", // Real ASIN for Project Hail Mary
		CoverURL:      "https://example.com/cover.jpg",
		Duration:      840.5, // Duration in minutes
	}

	// Add the mismatch with metadata
	mismatch.AddWithMetadata(
		metadata,
		"123",                  // Book ID
		"456",                  // Edition ID
		"Test mismatch reason", // Reason
		metadata.Duration,      // Duration
		"abs-789",              // Audiobookshelf ID
		nil,                    // No Hardcover client needed
	)

	// Create output directory
	outputDir := filepath.Join(os.TempDir(), "mismatch-test")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Error("Failed to create output directory", map[string]interface{}{
			"error": err.Error(),
			"path":  outputDir,
		})
		os.Exit(1)
	}

	// Create a minimal config
	cfg := &config.Config{}
	cfg.Paths.MismatchOutputDir = outputDir

	// Export mismatches to files
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := mismatch.SaveToFile(ctx, nil, outputDir, cfg); err != nil {
		log.Error("Failed to save mismatches", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	// Print output location
	fmt.Printf("Mismatches exported to: %s\n", outputDir)
	files, _ := os.ReadDir(outputDir)
	for _, file := range files {
		fmt.Printf("- %s\n", file.Name())
	}
}
