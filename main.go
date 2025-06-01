package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
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

// AudiobookShelf API response structures (simplified)
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

// Fetch audiobooks with progress from AudiobookShelf
func fetchAudiobookShelfStats() ([]Audiobook, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := audiobookshelfURL + "/api/audiobooks"
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
		return nil, fmt.Errorf("AudiobookShelf API error: %s", resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Audiobooks []Audiobook `json:"audiobooks"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result.Audiobooks, nil
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
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("Hardcover API error: %s - %s", resp.Status, string(body))
	}
	return nil
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
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	server := &http.Server{Addr: ":8080"}
	go func() {
		log.Printf("Health endpoint running on :8080/healthz")
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

	books, err := fetchAudiobookShelfStats()
	if err != nil {
		log.Fatalf("Failed to fetch AudiobookShelf stats: %v", err)
	}
	for _, book := range books {
		if err := syncToHardcover(book); err != nil {
			log.Printf("Failed to sync book '%s': %v", book.Title, err)
		} else {
			log.Printf("Synced book: %s", book.Title)
		}
	}

	<-shutdown
	log.Println("Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	log.Println("Shutdown complete.")
}
