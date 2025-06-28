// edition-tool is a command-line tool for managing audiobook editions in Hardcover.
// It provides functionality for creating, updating, and managing audiobook editions.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rs/zerolog"
)

func main() {
	// Set up basic logging
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	// Define subcommands
	createCmd := flag.NewFlagSet("create", flag.ExitOnError)
	createInteractive := createCmd.Bool("interactive", false, "Run in interactive mode")
	createPrepopulated := createCmd.Bool("prepopulated", false, "Prepopulate with Hardcover data")
	createFile := createCmd.String("file", "", "Create from JSON file")

	// Check if a subcommand is provided
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "create":
		if err := createCmd.Parse(os.Args[2:]); err != nil {
			logger.Fatal().Err(err).Msg("Error parsing command line arguments")
		}
		if *createInteractive {
			// TODO: Implement interactive mode
			logger.Info().Msg("Interactive edition creation")
		} else if *createPrepopulated {
			// TODO: Implement prepopulated mode
			logger.Info().Msg("Prepopulated edition creation")
		} else if *createFile != "" {
			// TODO: Implement file-based creation
			logger.Info().Str("file", *createFile).Msg("Creating edition from file")
		} else {
			createCmd.Usage()
			os.Exit(1)
		}
	case "help":
		printUsage()
	default:
		logger.Error().Str("command", os.Args[1]).Msg("Unknown command")
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Audiobook Edition Management Tool

Usage:
  edition-tool <command> [flags]

Commands:
  create    Create a new audiobook edition

Flags for create:
  --interactive    Run in interactive mode
  --prepopulated   Prepopulate with Hardcover data
  --file string    Create from JSON file

Examples:
  edition-tool create --interactive
  edition-tool create --prepopulated
  edition-tool create --file edition.json`)
}
