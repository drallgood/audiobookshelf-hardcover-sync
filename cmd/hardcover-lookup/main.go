// hardcover-lookup is a command-line tool for looking up authors, narrators, and publishers in Hardcover.
//
// Usage:
//   hardcover-lookup [global-flags] <command> [command-flags]
//
// Commands:
//   author     Look up or verify author information
//   narrator   Look up or verify narrator information
//   publisher  Look up or verify publisher information
//   help       Show help for commands
//
// Global Flags:
//   -config string   Path to config file (default: ./config.yaml or environment variables)
//   -json            Output results in JSON format
//   -limit int       Maximum number of results to return (default 5)
//   -h, --help       Show help
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

func main() {
		// Define global flags
	globalFlags := flag.NewFlagSet("global", flag.ExitOnError)
	helpFlag := globalFlags.Bool("h", false, "Show help")
	helpLongFlag := globalFlags.Bool("help", false, "Show help")
	configPath := globalFlags.String("config", "", "Path to config file (default: ./config.yaml or environment variables)")
	jsonOutput := globalFlags.Bool("json", false, "Output results in JSON format")
	limit := globalFlags.Int("limit", 5, "Maximum number of results to return")

	// Parse global flags first
	globalFlags.Parse(os.Args[1:])

	// Show help if no args or help flag
	if len(os.Args) < 2 || *helpFlag || *helpLongFlag {
		printUsage()
		os.Exit(0)
	}

	// The first non-flag argument is the subcommand
	args := globalFlags.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}
	subcommand := args[0]
	subArgs := args[1:]

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		fmt.Fprintln(os.Stderr, "Please set the required configuration via environment variables or a config file.")
		fmt.Fprintln(os.Stderr, "Required environment variables:")
		fmt.Fprintln(os.Stderr, "  - HARDCOVER_TOKEN: Your Hardcover API token")
		fmt.Fprintln(os.Stderr, "Optional environment variables:")
		fmt.Fprintln(os.Stderr, "  - CONFIG_FILE: Path to config file (default: ./config.yaml)")
		os.Exit(1)
	}

	// Set up logger with config
	logger.Setup(logger.Config{
		Level:  cfg.Logging.Level,
		Format: logger.LogFormat(cfg.Logging.Format),
	})
	log := logger.Get()

	// Create context
	ctx := context.Background()

	// Create Hardcover client
	hc := hardcover.NewClient(cfg.Hardcover.Token, log)
	if hc == nil {
		log.Error("Failed to create Hardcover client")
		time.Sleep(100 * time.Millisecond) // Give logger time to flush
		os.Exit(1)
	}

	// Define subcommands
	authorCmd := flag.NewFlagSet("author", flag.ExitOnError)
	authorName := authorCmd.String("name", "", "Author name to look up")
	authorID := authorCmd.String("id", "", "Author ID to verify")
	authorBulk := authorCmd.String("bulk", "", "Comma-separated list of author names to look up")
	authorLimit := authorCmd.Int("limit", *limit, "Maximum number of results to return")
	authorJSON := authorCmd.Bool("json", *jsonOutput, "Output results in JSON format")

	narratorCmd := flag.NewFlagSet("narrator", flag.ExitOnError)
	narratorName := narratorCmd.String("name", "", "Narrator name to look up")
	narratorID := narratorCmd.String("id", "", "Narrator ID to verify")
	narratorBulk := narratorCmd.String("bulk", "", "Comma-separated list of narrator names to look up")
	narratorLimit := narratorCmd.Int("limit", *limit, "Maximum number of results to return")
	narratorJSON := narratorCmd.Bool("json", *jsonOutput, "Output results in JSON format")

	publisherCmd := flag.NewFlagSet("publisher", flag.ExitOnError)
	publisherName := publisherCmd.String("name", "", "Publisher name to look up")
	publisherID := publisherCmd.String("id", "", "Publisher ID to verify")
	publisherBulk := publisherCmd.String("bulk", "", "Comma-separated list of publisher names to look up")
	publisherLimit := publisherCmd.Int("limit", *limit, "Maximum number of results to return")
	publisherJSON := publisherCmd.Bool("json", *jsonOutput, "Output results in JSON format")

	switch subcommand {
	case "author":
		if err := authorCmd.Parse(subArgs); err != nil {
			log.Error(fmt.Sprintf("Error parsing author command flags: %v", err))
			authorCmd.Usage()
			os.Exit(1)
		}
		if *authorName != "" {
			lookupAuthorByName(ctx, hc, *authorName, *authorLimit, *authorJSON)
		} else if *authorID != "" {
			verifyAuthorID(ctx, hc, *authorID, *authorJSON)
		} else if *authorBulk != "" {
			names := strings.Split(*authorBulk, ",")
			for i, name := range names {
				names[i] = strings.TrimSpace(name)
			}
			bulkLookupAuthors(ctx, hc, names, *authorLimit, *authorJSON)
		} else {
			authorCmd.Usage()
			os.Exit(1)
		}

	case "narrator":
		if err := narratorCmd.Parse(subArgs); err != nil {
			log.Error(fmt.Sprintf("Error parsing narrator command flags: %v", err))
			narratorCmd.Usage()
			os.Exit(1)
		}
		if *narratorName != "" {
			lookupNarratorByName(ctx, hc, *narratorName, *narratorLimit, *narratorJSON)
		} else if *narratorID != "" {
			verifyNarratorID(ctx, hc, *narratorID, *narratorJSON)
		} else if *narratorBulk != "" {
			names := strings.Split(*narratorBulk, ",")
			for i, name := range names {
				names[i] = strings.TrimSpace(name)
			}
			bulkLookupNarrators(ctx, hc, names, *narratorLimit, *narratorJSON)
		} else {
			narratorCmd.Usage()
			os.Exit(1)
		}

	case "publisher":
		if err := publisherCmd.Parse(subArgs); err != nil {
			log.Error(fmt.Sprintf("Error parsing publisher command flags: %v", err))
			publisherCmd.Usage()
			os.Exit(1)
		}
		if *publisherName != "" {
			lookupPublisherByName(ctx, hc, *publisherName, *publisherLimit, *publisherJSON)
		} else if *publisherID != "" {
			verifyPublisherID(ctx, hc, *publisherID, *publisherJSON)
		} else if *publisherBulk != "" {
			names := strings.Split(*publisherBulk, ",")
			for i, name := range names {
				names[i] = strings.TrimSpace(name)
			}
			bulkLookupPublishers(ctx, hc, names, *publisherLimit, *publisherJSON)
		} else {
			publisherCmd.Usage()
			os.Exit(1)
		}

	case "help":
		printUsage()

	default:
		log.Error(fmt.Sprintf("Unknown command: %s", os.Args[1]))
		printUsage()
		time.Sleep(100 * time.Millisecond) // Give logger time to flush
		os.Exit(1)
	}
}

// lookupAuthorByName looks up an author by name
func lookupAuthorByName(ctx context.Context, hc *hardcover.Client, name string, limit int, jsonOutput bool) {
	log := logger.Get()
	authors, err := hc.SearchPeople(ctx, name, "author", limit)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to lookup author: %v (name: %s)", err, name))
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(authors)
	} else {
		fmt.Printf("Found %d authors matching '%s':\n", len(authors), name)
		for i, a := range authors {
			fmt.Printf("%d. ID: %s, Name: %s\n", i+1, a.ID, a.Name)
		}
	}
}

// verifyAuthorID verifies an author by ID
func verifyAuthorID(ctx context.Context, hc *hardcover.Client, id string, jsonOutput bool) {
	log := logger.Get()
	author, err := hc.GetPersonByID(ctx, id)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to verify author ID: %v (id: %s)", err, id))
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(author)
	} else {
		fmt.Printf("Author found:\nID: %s\nName: %s\n", author.ID, author.Name)
	}
}

// bulkLookupAuthors looks up multiple authors by name
func bulkLookupAuthors(ctx context.Context, hc *hardcover.Client, names []string, limit int, jsonOutput bool) {
	log := logger.Get()
	results := make(map[string]interface{})

	for _, name := range names {
		authors, err := hc.SearchPeople(ctx, name, "author", limit)
		if err != nil {
			log.Error("Failed to lookup author", map[string]interface{}{
				"error": err,
				"name":  name,
			})
			continue
		}

		if jsonOutput {
			results[name] = authors
		} else {
			fmt.Printf("\nResults for '%s':\n", name)
			for i, a := range authors {
				fmt.Printf("%d. ID: %s, Name: %s\n", i+1, a.ID, a.Name)
			}
		}
	}

	if jsonOutput {
		printJSON(results)
	}
}

// lookupNarratorByName looks up a narrator by name
func lookupNarratorByName(ctx context.Context, hc *hardcover.Client, name string, limit int, jsonOutput bool) {
	log := logger.Get()
	narrators, err := hc.SearchPeople(ctx, name, "narrator", limit)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to lookup narrator: %v (name: %s)", err, name))
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(narrators)
	} else {
		fmt.Printf("Found %d narrators matching '%s':\n", len(narrators), name)
		for i, n := range narrators {
			fmt.Printf("%d. ID: %s, Name: %s\n", i+1, n.ID, n.Name)
		}
	}
}

// verifyNarratorID verifies a narrator by ID
func verifyNarratorID(ctx context.Context, hc *hardcover.Client, id string, jsonOutput bool) {
	log := logger.Get()
	narrator, err := hc.GetPersonByID(ctx, id)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to verify narrator ID: %v (id: %s)", err, id))
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(narrator)
	} else {
		fmt.Printf("Narrator found:\nID: %s\nName: %s\n", narrator.ID, narrator.Name)
	}
}

// bulkLookupNarrators looks up multiple narrators by name
func bulkLookupNarrators(ctx context.Context, hc *hardcover.Client, names []string, limit int, jsonOutput bool) {
	log := logger.Get()
	results := make(map[string]interface{})

	for _, name := range names {
		narrators, err := hc.SearchPeople(ctx, name, "narrator", limit)
		if err != nil {
			log.Error("Failed to lookup narrator", map[string]interface{}{
				"error": err,
				"name":  name,
			})
			continue
		}

		if jsonOutput {
			results[name] = narrators
		} else {
			fmt.Printf("\nResults for '%s':\n", name)
			for i, n := range narrators {
				fmt.Printf("%d. ID: %s, Name: %s\n", i+1, n.ID, n.Name)
			}
		}
	}

	if jsonOutput {
		printJSON(results)
	}
}

// lookupPublisherByName looks up a publisher by name
func lookupPublisherByName(ctx context.Context, hc *hardcover.Client, name string, limit int, jsonOutput bool) {
	log := logger.Get()
	publishers, err := hc.SearchPublishers(ctx, name, limit)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to lookup publisher: %v (name: %s)", err, name))
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(publishers)
	} else {
		fmt.Printf("Found %d publishers matching '%s':\n", len(publishers), name)
		for i, p := range publishers {
			fmt.Printf("%d. ID: %s, Name: %s\n", i+1, p.ID, p.Name)
		}
	}
}

// verifyPublisherID verifies a publisher by ID
func verifyPublisherID(ctx context.Context, hc *hardcover.Client, id string, jsonOutput bool) {
	log := logger.Get()
	// Since we don't have a direct GetPublisherByID method, we'll use SearchPublishers
	// with a filter on the ID field
	publishers, err := hc.SearchPublishers(ctx, "", 10) // Empty name to get all publishers
	if err != nil {
		log.Error(fmt.Sprintf("Failed to fetch publishers: %v", err))
		os.Exit(1)
	}

	// Find the publisher with the matching ID
	var foundPublisher *models.Publisher
	for i := range publishers {
		if publishers[i].ID == id {
			foundPublisher = &publishers[i]
			break
		}
	}

	if foundPublisher == nil {
		log.Error(fmt.Sprintf("Publisher not found with ID: %s", id))
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(foundPublisher)
	} else {
		fmt.Printf("Publisher found:\nID: %s\nName: %s\n", foundPublisher.ID, foundPublisher.Name)
	}
}

// bulkLookupPublishers looks up multiple publishers by name
func bulkLookupPublishers(ctx context.Context, hc *hardcover.Client, names []string, limit int, jsonOutput bool) {
	log := logger.Get()
	results := make(map[string]interface{})

	for _, name := range names {
		publishers, err := hc.SearchPublishers(ctx, name, limit)
		if err != nil {
			log.Error("Failed to lookup publisher", map[string]interface{}{
				"error": err,
				"name":  name,
			})
			continue
		}

		if jsonOutput {
			results[name] = publishers
		} else {
			fmt.Printf("\nResults for '%s':\n", name)
			for i, p := range publishers {
				fmt.Printf("%d. ID: %s, Name: %s\n", i+1, p.ID, p.Name)
			}
		}
	}

	if jsonOutput {
		printJSON(results)
	}
}

// printJSON prints the given value as JSON
func printJSON(v interface{}) {
	log := logger.Get()
	err := json.NewEncoder(os.Stdout).Encode(v)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to encode JSON: %v", err))
		os.Exit(1)
	}
}

// printUsage prints the usage information
func printUsage() {
	fmt.Printf(`Hardcover Lookup Tool

A command-line tool for looking up authors, narrators, and publishers in Hardcover.

Usage:
  hardcover-lookup [global-flags] <command> [command-flags]

Global Flags:
  -config string   Path to config file (default: ./config.yaml or environment variables)
  -json            Output results in JSON format
  -limit int       Maximum number of results to return (default 5)
  -h, --help       Show this help message

Commands:
  author       Look up or verify author information
    -name      Search for an author by name
    -id        Verify an author by ID
    -bulk      Bulk look up multiple authors (comma-separated)
    -limit     Maximum results to return (default 5)

  narrator     Look up or verify narrator information
    -name      Search for a narrator by name
    -id        Verify a narrator by ID
    -bulk      Bulk look up multiple narrators (comma-separated)
    -limit     Maximum results to return (default 5)

  publisher    Look up or verify publisher information
    -name      Search for a publisher by name
    -id        Verify a publisher by ID
    -bulk      Bulk look up multiple publishers (comma-separated)
    -limit     Maximum results to return (default 5)

Examples:
  # Look up an author by name
  hardcover-lookup author -name "J.K. Rowling"
  
  # Verify an author ID with JSON output
  hardcover-lookup -json author -id "auth123"
  
  # Bulk look up multiple narrators with a custom limit
  hardcover-lookup narrator -bulk "Jim Dale,Stephen Fry" -limit 10
  
  # Look up a publisher with a custom config file
  hardcover-lookup -config ./my-config.yaml publisher -name "Penguin"

Configuration:
  The tool can be configured via environment variables or a YAML config file.
  Required environment variables:
    - HARDCOVER_TOKEN: Your Hardcover API token
  
  Optional environment variables:
    - CONFIG_FILE: Path to config file (default: ./config.yaml)
    - LOG_LEVEL: Log level (debug, info, warn, error, fatal)
    - LOG_FORMAT: Log format (json, text)
`)
}
