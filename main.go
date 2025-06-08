// audiobookshelf-hardcover-sync
//
// Syncs Audiobookshelf to Hardcover.
//
// Features:
// - Periodic sync (set SYNC_INTERVAL, e.g. "10m", "1h")
// - Manual sync via HTTP POST/GET to /sync
// - Health check at /healthz
// - Configurable via environment variables
//
// Environment Variables:
//   AUDIOBOOKSHELF_URL      URL to your AudiobookShelf server
//   AUDIOBOOKSHELF_TOKEN    API token for AudiobookShelf
//   HARDCOVER_TOKEN         API token for Hardcover
//   SYNC_INTERVAL           (optional) Go duration string for periodic sync
//   HARDCOVER_SYNC_DELAY_MS (optional) Delay between Hardcover syncs in milliseconds
//
// Usage:
//   ./main                       # Runs initial sync, then waits for /sync or SYNC_INTERVAL
//   ./main --health-check        # Health check mode for Docker
//   ./main --version             # Print version
//   ./main --create-edition      # Interactive edition creation tool
//   ./main --create-edition-json FILE # Create edition from JSON file
//   ./main --generate-example FILE.json # Generate example JSON file for batch creation
//   ./main --lookup-author       # Search for author IDs by name
//   ./main --lookup-narrator     # Search for narrator IDs by name
//   ./main --lookup-publisher    # Search for publisher IDs by name
//   ./main --bulk-lookup-authors "Name1,Name2" # Search for multiple authors
//   ./main --bulk-lookup-narrators "Name1,Name2" # Search for multiple narrators
//   ./main --bulk-lookup-publishers "Name1,Name2" # Search for multiple publishers
//   ./main --verify-author-id ID # Verify and get details for a specific author ID
//   ./main --verify-narrator-id ID # Verify and get details for a specific narrator ID
//   ./main --verify-publisher-id ID # Verify and get details for a specific publisher ID
//   ./main --upload-image "url:description" # Upload image from URL to Hardcover
//
// Endpoints:
//   GET /healthz           # Health check
//   POST/GET /sync         # Trigger a sync

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var (
	version = "v1.5.0" // Application version
)

func main() {
	// Configure timezone from environment variable if set
	if tz := os.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			time.Local = loc
			log.Printf("Timezone set to: %s", tz)
		} else {
			log.Printf("Warning: Failed to load timezone %s: %v", tz, err)
		}
	}

	healthCheck := flag.Bool("health-check", false, "Run health check and exit")
	showVersion := flag.Bool("version", false, "Show version and exit")
	verbose := flag.Bool("v", false, "Enable verbose debug logging")
	createEdition := flag.Bool("create-edition", false, "Interactive edition creation tool")
	createEditionPrepopulated := flag.Bool("create-edition-prepopulated", false, "Interactive edition creation with Hardcover data prepopulation")
	createEditionJSON := flag.String("create-edition-json", "", "Create edition from JSON file")
	generateExample := flag.String("generate-example", "", "Generate example JSON file for batch edition creation")
	generatePrepopulated := flag.String("generate-prepopulated", "", "Generate prepopulated JSON template from Hardcover book ID (format: bookid:filename.json)")
	enhanceTemplate := flag.String("enhance-template", "", "Enhance existing JSON template with prepopulated data (format: filename.json:bookid)")
	lookupAuthor := flag.Bool("lookup-author", false, "Search for author IDs by name")
	lookupNarrator := flag.Bool("lookup-narrator", false, "Search for narrator IDs by name")
	lookupPublisher := flag.Bool("lookup-publisher", false, "Search for publisher IDs by name")
	verifyAuthorID := flag.String("verify-author-id", "", "Verify and get details for a specific author ID")
	verifyNarratorID := flag.String("verify-narrator-id", "", "Verify and get details for a specific narrator ID")
	verifyPublisherID := flag.String("verify-publisher-id", "", "Verify and get details for a specific publisher ID")
	bulkLookupAuthors := flag.String("bulk-lookup-authors", "", "Search for multiple authors by comma-separated names")
	bulkLookupNarrators := flag.String("bulk-lookup-narrators", "", "Search for multiple narrators by comma-separated names")
	bulkLookupPublishers := flag.String("bulk-lookup-publishers", "", "Search for multiple publishers by comma-separated names")
	uploadImage := flag.String("upload-image", "", "Upload image from URL to Hardcover (format: url:bookID:description)")
	debugAPI := flag.Bool("debug-api", false, "Debug AudiobookShelf API endpoints")
	flag.Parse()

	// Enable debug mode if -v flag or DEBUG_MODE env var is set
	if *verbose || os.Getenv("DEBUG_MODE") == "true" {
		debugMode = true
		log.Printf("Verbose debug logging enabled (flag or DEBUG_MODE)")
	}

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	if *debugAPI {
		debugAudiobookShelfAPI()
		os.Exit(0)
	}

	// Handle edition creation commands
	if *createEdition {
		if err := createEditionCommand(); err != nil {
			log.Fatalf("Edition creation failed: %v", err)
		}
		os.Exit(0)
	}

	if *createEditionPrepopulated {
		if err := createEditionWithPrepopulation(); err != nil {
			log.Fatalf("Prepopulated edition creation failed: %v", err)
		}
		os.Exit(0)
	}

	if *createEditionJSON != "" {
		if err := createEditionFromJSON(*createEditionJSON); err != nil {
			log.Fatalf("Edition creation from JSON failed: %v", err)
		}
		os.Exit(0)
	}

	if *generateExample != "" {
		if err := generateExampleJSON(*generateExample); err != nil {
			log.Fatalf("Failed to generate example JSON: %v", err)
		}
		os.Exit(0)
	}

	if *generatePrepopulated != "" {
		// Parse format: bookid:filename.json
		parts := strings.Split(*generatePrepopulated, ":")
		if len(parts) != 2 {
			log.Fatalf("Invalid format for --generate-prepopulated. Expected: bookid:filename.json")
		}
		bookID, filename := parts[0], parts[1]
		if err := generatePrepopulatedTemplate(bookID, filename); err != nil {
			log.Fatalf("Failed to generate prepopulated template: %v", err)
		}
		os.Exit(0)
	}

	if *enhanceTemplate != "" {
		// Parse format: filename.json:bookid
		parts := strings.Split(*enhanceTemplate, ":")
		if len(parts) != 2 {
			log.Fatalf("Invalid format for --enhance-template. Expected: filename.json:bookid")
		}
		filename, bookID := parts[0], parts[1]
		if err := enhanceExistingTemplate(filename, bookID); err != nil {
			log.Fatalf("Failed to enhance template: %v", err)
		}
		os.Exit(0)
	}

	// Handle ID lookup commands
	if *lookupAuthor {
		if err := lookupAuthorIDCommand(); err != nil {
			log.Fatalf("Author ID lookup failed: %v", err)
		}
		os.Exit(0)
	}

	if *lookupNarrator {
		if err := lookupNarratorIDCommand(); err != nil {
			log.Fatalf("Narrator ID lookup failed: %v", err)
		}
		os.Exit(0)
	}

	if *lookupPublisher {
		if err := lookupPublisherIDCommand(); err != nil {
			log.Fatalf("Publisher ID lookup failed: %v", err)
		}
		os.Exit(0)
	}

	if *verifyAuthorID != "" {
		if err := verifyIDCommand("author", *verifyAuthorID); err != nil {
			log.Fatalf("Author ID verification failed: %v", err)
		}
		os.Exit(0)
	}

	if *verifyNarratorID != "" {
		if err := verifyIDCommand("narrator", *verifyNarratorID); err != nil {
			log.Fatalf("Narrator ID verification failed: %v", err)
		}
		os.Exit(0)
	}

	if *verifyPublisherID != "" {
		if err := verifyIDCommand("publisher", *verifyPublisherID); err != nil {
			log.Fatalf("Publisher ID verification failed: %v", err)
		}
		os.Exit(0)
	}

	// Handle bulk lookup commands
	if *bulkLookupAuthors != "" {
		if err := bulkLookupAuthorsCommand(*bulkLookupAuthors); err != nil {
			log.Fatalf("Bulk author lookup failed: %v", err)
		}
		os.Exit(0)
	}

	if *bulkLookupNarrators != "" {
		if err := bulkLookupNarratorsCommand(*bulkLookupNarrators); err != nil {
			log.Fatalf("Bulk narrator lookup failed: %v", err)
		}
		os.Exit(0)
	}

	if *bulkLookupPublishers != "" {
		if err := bulkLookupPublishersCommand(*bulkLookupPublishers); err != nil {
			log.Fatalf("Bulk publisher lookup failed: %v", err)
		}
		os.Exit(0)
	}

	// Handle image upload command
	if *uploadImage != "" {
		if err := uploadImageCommand(*uploadImage); err != nil {
			log.Fatalf("Image upload failed: %v", err)
		}
		os.Exit(0)
	}

	if *healthCheck {
		// Simple health check: check env vars
		required := []string{"AUDIOBOOKSHELF_URL", "AUDIOBOOKSHELF_TOKEN", "HARDCOVER_TOKEN"}
		for _, v := range required {
			if os.Getenv(v) == "" {
				fmt.Printf("Missing required env var: %s\n", v)
				os.Exit(1)
			}
		}
		fmt.Println("ok")
		os.Exit(0)
	}

	// Health endpoint for liveness/readiness
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	http.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		go runSync()
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("sync triggered"))
	})

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	server := &http.Server{Addr: ":8080"}
	go func() {
		log.Printf("Health and sync endpoints running on :8080/healthz and :8080/sync")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Health endpoint error: %v", err)
		}
	}()

	log.Printf("audiobookshelf-hardcover-sync version %s starting", version)

	required := []string{"AUDIOBOOKSHELF_URL", "AUDIOBOOKSHELF_TOKEN", "HARDCOVER_TOKEN"}
	for _, v := range required {
		if os.Getenv(v) == "" {
			log.Fatalf("Missing required env var: %s", v)
		}
	}

	runSync() // Initial sync at startup

	syncInterval := os.Getenv("SYNC_INTERVAL")
	if syncInterval != "" {
		dur, err := time.ParseDuration(syncInterval)
		if err != nil {
			log.Fatalf("Invalid SYNC_INTERVAL: %v", err)
		}
		ticker := time.NewTicker(dur)
		defer ticker.Stop()
		go func() {
			for range ticker.C {
				log.Printf("Periodic sync triggered by SYNC_INTERVAL=%s", syncInterval)
				runSync()
			}
		}()
	}

	<-shutdown
	log.Println("Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	log.Println("Shutdown complete.")
}
