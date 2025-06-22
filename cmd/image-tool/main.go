// image-tool is a command-line tool for managing book cover images in Hardcover.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Set up basic logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Define flags for the upload command
	uploadCmd := flag.NewFlagSet("upload", flag.ExitOnError)
	imageURL := uploadCmd.String("url", "", "URL of the image to upload (required)")
	bookID := uploadCmd.String("book-id", "", "Hardcover book ID (required)")
	description := uploadCmd.String("description", "", "Image description (optional)")

	// Check if a subcommand is provided
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "upload":
		uploadCmd.Parse(os.Args[2:])
		if *imageURL == "" || *bookID == "" {
			log.Error().Msg("Both --url and --book-id are required")
			uploadCmd.Usage()
			os.Exit(1)
		}
		uploadImage(*imageURL, *bookID, *description)

	case "help":
		printUsage()

	// Handle the legacy format: --upload-image "url:bookID:description"
	case "--upload-image":
		if len(os.Args) < 3 {
			log.Error().Msg("Missing argument for --upload-image")
			printUsage()
			os.Exit(1)
		}
		parts := strings.SplitN(os.Args[2], ":", 3)
		if len(parts) < 2 {
			log.Error().Msg("Invalid format. Expected: url:bookID[:description]")
			os.Exit(1)
		}
		desc := ""
		if len(parts) > 2 {
			desc = parts[2]
		}
		uploadImage(parts[0], parts[1], desc)

	default:
		log.Error().Str("command", os.Args[1]).Msg("Unknown command")
		printUsage()
		os.Exit(1)
	}
}

// uploadImage handles the image upload to Hardcover
func uploadImage(imageURL, bookID, description string) {
	log.Info().
		Str("url", imageURL).
		Str("bookID", bookID).
		Str("description", description).
		Msg("Uploading image to Hardcover")
	
	// TODO: Implement actual image upload logic
	// This will call the appropriate function from the internal/api/hardcover package
}

func printUsage() {
	fmt.Println(`Hardcover Image Tool

Usage:
  image-tool <command> [flags]

Commands:
  upload     Upload an image to a book

Flags for upload:
  --url string         URL of the image to upload (required)
  --book-id string     Hardcover book ID (required)
  --description string Image description (optional)

Examples:
  # Upload a cover image with a description
  image-tool upload --url https://example.com/cover.jpg --book-id book123 --description "Cover art"
  
  # Legacy format (deprecated but supported)
  image-tool --upload-image "https://example.com/cover.jpg:book123:Cover art"
  
  # Minimal usage with required parameters only
  image-tool upload --url https://example.com/cover.jpg --book-id book123`)
}
