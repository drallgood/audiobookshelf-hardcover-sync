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
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	version   = "dev"
	debugMode = false
)

func debugLog(format string, v ...interface{}) {
	if debugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
}

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// Getter functions for environment variables
func getAudiobookShelfURL() string {
	return os.Getenv("AUDIOBOOKSHELF_URL")
}

func getAudiobookShelfToken() string {
	return os.Getenv("AUDIOBOOKSHELF_TOKEN")
}

func getHardcoverToken() string {
	return os.Getenv("HARDCOVER_TOKEN")
}

func getHardcoverSyncDelay() time.Duration {
	delayStr := os.Getenv("HARDCOVER_SYNC_DELAY_MS")
	if delayStr == "" {
		return 1500 * time.Millisecond // default 1.5s
	}
	delayMs, err := strconv.Atoi(delayStr)
	if err != nil || delayMs < 0 {
		return 1500 * time.Millisecond
	}
	return time.Duration(delayMs) * time.Millisecond
}

// AudiobookShelf API response structures (updated)
type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MediaMetadata struct {
	Title      string `json:"title"`
	AuthorName string `json:"authorName"`
	ISBN       string `json:"isbn,omitempty"`
	ISBN13     string `json:"isbn_13,omitempty"`
	ASIN       string `json:"asin,omitempty"`
}

type Media struct {
	ID       string        `json:"id"`
	Metadata MediaMetadata `json:"metadata"`
}

type Item struct {
	ID        string  `json:"id"`
	MediaType string  `json:"mediaType"`
	Media     Media   `json:"media"`
	Progress  float64 `json:"progress"`
}

type Audiobook struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Author   string  `json:"author"`
	ISBN     string  `json:"isbn,omitempty"`
	Progress float64 `json:"progress"`
}

// Fetch libraries from AudiobookShelf
func fetchLibraries() ([]Library, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := getAudiobookShelfURL() + "/api/libraries"
	debugLog("Fetching libraries from: %s", url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		debugLog("AudiobookShelf libraries error body: %s", string(body))
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
		debugLog("JSON unmarshal error (libraries): %v", err)
		return nil, err
	}
	return result.Libraries, nil
}

// Fetch items for a library
func fetchLibraryItems(libraryID string) ([]Item, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/api/libraries/%s/items", getAudiobookShelfURL(), libraryID)
	debugLog("Fetching items from: %s", url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		debugLog("Error creating request: %v", err)
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
	resp, err := httpClient.Do(req)
	if err != nil {
		debugLog("HTTP request error: %v", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		debugLog("AudiobookShelf items error body: %s", string(body))
		return nil, fmt.Errorf("AudiobookShelf API error: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		debugLog("Error reading response body: %v", err)
		return nil, err
	}
	var result struct {
		Results []Item `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		detail := string(body)
		debugLog("JSON unmarshal error (results): %v, body: %s", err, detail)
		return nil, err
	}
	debugLog("Fetched %d items for library %s", len(result.Results), libraryID)
	if len(result.Results) == 0 {
		debugLog("No items found for library %s", libraryID)
	}
	return result.Results, nil
}

// Fetch audiobooks with progress from all libraries
func fetchAudiobookShelfStats() ([]Audiobook, error) {
	debugLog("Fetching AudiobookShelf stats using new API...")
	libs, err := fetchLibraries()
	if err != nil {
		debugLog("Error fetching libraries: %v", err)
		return nil, err
	}
	var audiobooks []Audiobook
	for _, lib := range libs {
		items, err := fetchLibraryItems(lib.ID)
		if err != nil {
			debugLog("Failed to fetch items for library %s: %v", lib.Name, err)
			continue
		}
		debugLog("Processing %d items for library %s", len(items), lib.Name)
		for i, item := range items {
			if i < 5 { // Log first 5 item types for debug
				detail := item.MediaType
				debugLog("Item %d mediaType: %s", i, detail)
			}
			if strings.EqualFold(item.MediaType, "book") {
				title := item.Media.Metadata.Title
				author := item.Media.Metadata.AuthorName
				isbn := item.Media.Metadata.ISBN
				if isbn == "" {
					isbn = item.Media.Metadata.ISBN13
				}
				// If no ISBN, try ASIN as ISBN-10 fallback
				if isbn == "" && item.Media.Metadata.ASIN != "" {
					isbn = item.Media.Metadata.ASIN
				}
				audiobooks = append(audiobooks, Audiobook{
					ID:       item.ID,
					Title:    title,
					Author:   author,
					ISBN:     isbn,
					Progress: item.Progress,
				})
			}
		}
	}
	debugLog("Total audiobooks found: %d", len(audiobooks))
	return audiobooks, nil
}

// Helper to normalize book titles for better matching
func normalizeTitle(title string) string {
	title = strings.ToLower(title)
	title = strings.TrimSpace(title)
	// Remove common audiobook suffixes
	suffixes := []string{"(unabridged)", "(abridged)", "(a novel)", "(novel)", "(audio book)", "(audiobook)", "(audio)"}
	for _, s := range suffixes {
		if strings.HasSuffix(title, s) {
			title = strings.TrimSpace(strings.TrimSuffix(title, s))
		}
	}
	// Remove trailing punctuation
	title = strings.TrimRight(title, ".:;,- ")
	return title
}

// Lookup Hardcover bookId by title and author using the books(filter: {title, author}) GraphQL query
func lookupHardcoverBookID(title, author string) (string, error) {
	// Try original title first
	id, err := lookupHardcoverBookIDRaw(title, author)
	if err == nil {
		return id, nil
	}
	// Fallback: try normalized title
	normTitle := normalizeTitle(title)
	if normTitle != title {
		debugLog("Retrying Hardcover lookup with normalized title: '%s'", normTitle)
		id, err2 := lookupHardcoverBookIDRaw(normTitle, author)
		if err2 == nil {
			return id, nil
		}
	}
	return "", err
}

// Raw lookup (no normalization)
func lookupHardcoverBookIDRaw(title, author string) (string, error) {
	query := `
	query BooksByTitleAuthor($title: String!, $author: String!) {
	  books(filter: {title: $title, author: $author}) {
		id
		title
		author
	  }
	}`
	variables := map[string]interface{}{
		"title":  title,
		"author": author,
	}
	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}
	payloadBytes, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("hardcover books query error: %s - %s", resp.Status, string(body))
	}
	var result struct {
		Data struct {
			Books []struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Author string `json:"author"`
			} `json:"books"`
		} `json:"data"`
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	for _, book := range result.Data.Books {
		if strings.EqualFold(book.Title, title) && strings.EqualFold(book.Author, author) {
			return book.ID, nil
		}
	}
	return "", fmt.Errorf("no matching book found for '%s' by '%s'", title, author)
}

// Sync each finished audiobook to Hardcover
func syncToHardcover(a Audiobook) error {
	// Step 1: Lookup Hardcover book and edition by ISBN (preferred) or title/author
	var bookId, editionId string
	if a.ISBN != "" {
		// Query for book and edition by ISBN
		query := `
		query BookByISBN($isbn: String!) {
		  books(where: { editions: { isbn_13: { _eq: $isbn } } }, limit: 1) {
			id
			title
			editions(where: { isbn_13: { _eq: $isbn } }) {
			  id
			  isbn_13
			  isbn_10
			}
		  }
		}`
		variables := map[string]interface{}{"isbn": a.ISBN}
		payload := map[string]interface{}{"query": query, "variables": variables}
		payloadBytes, _ := json.Marshal(payload)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("hardcover books query error: %s - %s", resp.Status, string(body))
		}
		var result struct {
			Data struct {
				Books []struct {
					ID       string `json:"id"`
					Editions []struct {
						ID string `json:"id"`
					} `json:"editions"`
				} `json:"books"`
			} `json:"data"`
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}
		if len(result.Data.Books) > 0 {
			bookId = result.Data.Books[0].ID
			if len(result.Data.Books[0].Editions) > 0 {
				editionId = result.Data.Books[0].Editions[0].ID
			}
		}
	}
	if bookId == "" {
		// fallback: lookup by title/author
		var err error
		bookId, err = lookupHardcoverBookID(a.Title, a.Author)
		if err != nil {
			return fmt.Errorf("could not find Hardcover bookId for '%s' by '%s' (ISBN: %s): %v", a.Title, a.Author, a.ISBN, err)
		}
	}
	// Step 2: Insert user book
	statusId := 3 // default to read
	if a.Progress < 1.0 {
		statusId = 2 // currently reading
	}
	userBookInput := map[string]interface{}{
		"book_id":   toInt(bookId),
		"status_id": statusId,
	}
	if editionId != "" {
		userBookInput["edition_id"] = toInt(editionId)
	}
	insertUserBookMutation := `
	mutation InsertUserBook($object: UserBookCreateInput!) {
	  insert_user_book(object: $object) {
		id
		user_book { id }
		error
	  }
	}`
	variables := map[string]interface{}{"object": userBookInput}
	payload := map[string]interface{}{"query": insertUserBookMutation, "variables": variables}
	payloadBytes, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var userBookId int
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode == 429 {
			if retry := resp.Header.Get("Retry-After"); retry != "" {
				if sec, err := strconv.Atoi(retry); err == nil && sec > 0 {
					time.Sleep(time.Duration(sec) * time.Second)
					continue
				}
			}
			time.Sleep(3 * time.Second)
			continue
		}
		if resp.StatusCode != 200 {
			continue
		}
		var result struct {
			Data struct {
				InsertUserBook struct {
					ID       int `json:"id"`
					UserBook struct {
						ID int `json:"id"`
					} `json:"user_book"`
					Error *string `json:"error"`
				} `json:"insert_user_book"`
			} `json:"data"`
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}
		if result.Data.InsertUserBook.Error != nil {
			return fmt.Errorf("insert_user_book error: %s", *result.Data.InsertUserBook.Error)
		}
		userBookId = result.Data.InsertUserBook.UserBook.ID
		break
	}
	if userBookId == 0 {
		return fmt.Errorf("failed to insert user book for '%s'", a.Title)
	}
	// Step 3: Insert user book read (progress)
	if a.Progress > 0 && a.Progress < 1.0 {
		// Assume total duration is not available, so use progress_seconds as seconds listened
		progressSeconds := int(math.Round(a.Progress * 3600)) // fallback: 1hr book, scale as needed
		insertUserBookReadMutation := `
		mutation InsertUserBookRead($user_book_id: Int!, $user_book_read: DatesReadInput!) {
		  insert_user_book_read(user_book_id: $user_book_id, user_book_read: $user_book_read) {
			id
			user_book_read { id, progress_seconds }
			error
		  }
		}`
		variables := map[string]interface{}{
			"user_book_id":   userBookId,
			"user_book_read": map[string]interface{}{"progress_seconds": progressSeconds},
		}
		payload := map[string]interface{}{"query": insertUserBookReadMutation, "variables": variables}
		payloadBytes, _ := json.Marshal(payload)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for attempt := 0; attempt < 3; attempt++ {
			req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
			if err != nil {
				continue
			}
			req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")
			resp, err := httpClient.Do(req)
			if err != nil {
				continue
			}
			defer resp.Body.Close()
			if resp.StatusCode == 429 {
				if retry := resp.Header.Get("Retry-After"); retry != "" {
					if sec, err := strconv.Atoi(retry); err == nil && sec > 0 {
						time.Sleep(time.Duration(sec) * time.Second)
						continue
					}
				}
				time.Sleep(3 * time.Second)
				continue
			}
			if resp.StatusCode != 200 {
				continue
			}
			var result struct {
				Data struct {
					InsertUserBookRead struct {
						ID           int `json:"id"`
						UserBookRead struct {
							ID              int `json:"id"`
							ProgressSeconds int `json:"progress_seconds"`
						} `json:"user_book_read"`
						Error *string `json:"error"`
					} `json:"insert_user_book_read"`
				} `json:"data"`
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(body, &result); err != nil {
				continue
			}
			if result.Data.InsertUserBookRead.Error != nil {
				return fmt.Errorf("insert_user_book_read error: %s", *result.Data.InsertUserBookRead.Error)
			}
			break
		}
	}
	return nil
}

// Helper to convert string to int safely
func toInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

func runSync() {
	books, err := fetchAudiobookShelfStats()
	if err != nil {
		log.Printf("Failed to fetch AudiobookShelf stats: %v", err)
		return
	}
	delay := getHardcoverSyncDelay()
	for i, book := range books {
		if i > 0 {
			time.Sleep(delay)
		}
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
	verbose := flag.Bool("v", false, "Enable verbose debug logging")
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
