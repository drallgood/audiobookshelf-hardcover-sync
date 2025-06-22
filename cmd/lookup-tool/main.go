// lookup-tool is a command-line tool for looking up authors, narrators, and publishers in Hardcover.
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

	// Define subcommands
	authorCmd := flag.NewFlagSet("author", flag.ExitOnError)
	authorName := authorCmd.String("name", "", "Author name to look up")
	authorID := authorCmd.String("id", "", "Author ID to verify")
	authorBulk := authorCmd.String("bulk", "", "Comma-separated list of author names to look up")

	narratorCmd := flag.NewFlagSet("narrator", flag.ExitOnError)
	narratorName := narratorCmd.String("name", "", "Narrator name to look up")
	narratorID := narratorCmd.String("id", "", "Narrator ID to verify")
	narratorBulk := narratorCmd.String("bulk", "", "Comma-separated list of narrator names to look up")

	publisherCmd := flag.NewFlagSet("publisher", flag.ExitOnError)
	publisherName := publisherCmd.String("name", "", "Publisher name to look up")
	publisherID := publisherCmd.String("id", "", "Publisher ID to verify")
	publisherBulk := publisherCmd.String("bulk", "", "Comma-separated list of publisher names to look up")

	// Check if a subcommand is provided
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "author":
		authorCmd.Parse(os.Args[2:])
		if *authorName != "" {
			lookupAuthorByName(*authorName)
		} else if *authorID != "" {
			verifyAuthorID(*authorID)
		} else if *authorBulk != "" {
			names := strings.Split(*authorBulk, ",")
			bulkLookupAuthors(names)
		} else {
			authorCmd.Usage()
			os.Exit(1)
		}

	case "narrator":
		narratorCmd.Parse(os.Args[2:])
		if *narratorName != "" {
			lookupNarratorByName(*narratorName)
		} else if *narratorID != "" {
			verifyNarratorID(*narratorID)
		} else if *narratorBulk != "" {
			names := strings.Split(*narratorBulk, ",")
			bulkLookupNarrators(names)
		} else {
			narratorCmd.Usage()
			os.Exit(1)
		}

	case "publisher":
		publisherCmd.Parse(os.Args[2:])
		if *publisherName != "" {
			lookupPublisherByName(*publisherName)
		} else if *publisherID != "" {
			verifyPublisherID(*publisherID)
		} else if *publisherBulk != "" {
			names := strings.Split(*publisherBulk, ",")
			bulkLookupPublishers(names)
		} else {
			publisherCmd.Usage()
			os.Exit(1)
		}

	case "help":
		printUsage()

	default:
		log.Error().Str("command", os.Args[1]).Msg("Unknown command")
		printUsage()
		os.Exit(1)
	}
}

// TODO: Implement these functions in the internal/api/hardcover package
func lookupAuthorByName(name string)    { log.Info().Str("name", name).Msg("Looking up author") }
func verifyAuthorID(id string)          { log.Info().Str("id", id).Msg("Verifying author ID") }
func bulkLookupAuthors(names []string) { log.Info().Strs("names", names).Msg("Bulk looking up authors") }

func lookupNarratorByName(name string)    { log.Info().Str("name", name).Msg("Looking up narrator") }
func verifyNarratorID(id string)          { log.Info().Str("id", id).Msg("Verifying narrator ID") }
func bulkLookupNarrators(names []string) { log.Info().Strs("names", names).Msg("Bulk looking up narrators") }

func lookupPublisherByName(name string)    { log.Info().Str("name", name).Msg("Looking up publisher") }
func verifyPublisherID(id string)          { log.Info().Str("id", id).Msg("Verifying publisher ID") }
func bulkLookupPublishers(names []string) { log.Info().Strs("names", names).Msg("Bulk looking up publishers") }

func printUsage() {
	fmt.Println(`Hardcover Lookup Tool

Usage:
  lookup-tool <command> [flags]

Commands:
  author     Look up or verify author information
  narrator   Look up or verify narrator information
  publisher  Look up or verify publisher information

Examples:
  # Look up an author by name
  lookup-tool author -name "J.K. Rowling"
  
  # Verify an author ID
  lookup-tool author -id "auth123"
  
  # Bulk look up multiple authors
  lookup-tool author -bulk "Author One,Author Two,Author Three"
  
  # Look up a narrator
  lookup-tool narrator -name "Jim Dale"
  
  # Look up a publisher
  lookup-tool publisher -name "Penguin"`)
}
