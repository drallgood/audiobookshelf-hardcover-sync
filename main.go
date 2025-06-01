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
//
// Usage:
//   ./main                  # Runs initial sync, then waits for /sync or SYNC_INTERVAL
//   ./main --health-check   # Health check mode for Docker
//   ./main --version        # Print version
//
// Endpoints:
//   GET /healthz           # Health check
//   POST/GET /sync         # Trigger a sync

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Configurable environment variables
var (
	audiobookshelfURL   = os.Getenv("AUDIOBOOKSHELF_URL") // e.g. "https://abs.example.com"
	audiobookshelfToken = os.Getenv("AUDIOBOOKSHELF_TOKEN")
	hardcoverURL        = "https://api.hardcover.app/graphql"
	hardcoverToken      = os.Getenv("HARDCOVER_TOKEN")
)

var version = "dev"

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// AudiobookShelf API response structures (updated)
type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Item struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Author   string  `json:"author"`
	Type     string  `json:"type"`
	Progress float64 `json:"progress"`
}

type Audiobook struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Author   string  `json:"author"`
	Progress float64 `json:"progress"`
}

// Hardcover GraphQL mutation for updating reading stats
const hardcoverMutation = `
mutation AddBookToShelf($input: AddBookToShelfInput!) {
  addBookToShelf(input: $input) {
    bookShelfEntry {
      id
      statusId
      finishedAt
    }
  }
}
`

// Fetch libraries from AudiobookShelf
func fetchLibraries() ([]Library, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := audiobookshelfURL + "/api/libraries"
	log.Printf("Fetching libraries from: %s", url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+audiobookshelfToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("AudiobookShelf libraries error body: %s", string(body))
		return nil, fmt.Errorf("AudiobookShelf API error: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Libraries []Library `json:"libraries"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("JSON unmarshal error (libraries): %v", err)
		return nil, err
	}
	return result.Libraries, nil
}

// Fetch items for a library
func fetchLibraryItems(libraryID string) ([]Item, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/api/libraries/%s/items", audiobookshelfURL, libraryID)
	log.Printf("Fetching items from: %s", url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+audiobookshelfToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("AudiobookShelf items error body: %s", string(body))
		return nil, fmt.Errorf("AudiobookShelf API error: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Items []Item `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("JSON unmarshal error (items): %v", err)
		return nil, err
	}
	return result.Items, nil
}

// Fetch audiobooks with progress from all libraries
func fetchAudiobookShelfStats() ([]Audiobook, error) {
	log.Printf("Fetching AudiobookShelf stats using new API...")
	libs, err := fetchLibraries()
	if err != nil {
		return nil, err
	}
	var audiobooks []Audiobook
	for _, lib := range libs {
		items, err := fetchLibraryItems(lib.ID)
		if err != nil {
			log.Printf("Failed to fetch items for library %s: %v", lib.Name, err)
			continue
		}
		for _, item := range items {
			if item.Type == "audiobook" {
				audiobooks = append(audiobooks, Audiobook{
					ID:       item.ID,
					Title:    item.Title,
					Author:   item.Author,
					Progress: item.Progress,
				})
			}
		}
	}
	return audiobooks, nil
}

// Sync each finished audiobook to Hardcover
func syncToHardcover(a Audiobook) error {
	// Only sync fully finished books
	if a.Progress < 1.0 {
		return nil
	}
	// Prepare mutation variables (you may need to look up book IDs in Hardcover)
	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"title":    a.Title,
			"author":   a.Author,
			"statusId": 3, // 3 = read
			// Add other fields as needed
		},
	}
	payload := map[string]interface{}{
		"query":     hardcoverMutation,
		"variables": variables,
	}
	payloadBytes, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", hardcoverURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+hardcoverToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("hardcover API error: %s - %s", resp.Status, string(body))
	}
	return nil
}

func runSync() {
	books, err := fetchAudiobookShelfStats()
	if err != nil {
		log.Printf("Failed to fetch AudiobookShelf stats: %v", err)
		return
	}
	for _, book := range books {
		if err := syncToHardcover(book); err != nil {
			log.Printf("Failed to sync book '%s': %v", book.Title, err)
		} else {
			log.Printf("Synced book: %s", book.Title)
		}
	}
}

func main() {
	healthCheck := flag.Bool("health-check", false, "Run health check and exit")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
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
