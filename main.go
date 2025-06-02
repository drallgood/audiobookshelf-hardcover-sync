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

// Helper function to get minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

func getMinimumProgressThreshold() float64 {
	thresholdStr := os.Getenv("MINIMUM_PROGRESS_THRESHOLD")
	if thresholdStr == "" {
		return 0.01 // default 1%
	}
	threshold, err := strconv.ParseFloat(thresholdStr, 64)
	if err != nil || threshold < 0 || threshold > 1 {
		return 0.01 // default 1% if invalid
	}
	return threshold
}

// AudiobookShelf API response structures (updated)
type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MediaMetadata struct {
	Title      string  `json:"title"`
	AuthorName string  `json:"authorName"`
	ISBN       string  `json:"isbn,omitempty"`
	ISBN13     string  `json:"isbn_13,omitempty"`
	ASIN       string  `json:"asin,omitempty"`
	Duration   float64 `json:"duration,omitempty"` // Total duration in seconds
}

type Media struct {
	ID       string        `json:"id"`
	Metadata MediaMetadata `json:"metadata"`
	Duration float64       `json:"duration,omitempty"` // Backup duration location
}

type UserProgress struct {
	Progress      float64 `json:"progress"`
	CurrentTime   float64 `json:"currentTime"`
	IsFinished    bool    `json:"isFinished"`
	TimeRemaining float64 `json:"timeRemaining"`
	TotalDuration float64 `json:"totalDuration,omitempty"` // Total book duration
}

type Item struct {
	ID           string        `json:"id"`
	MediaType    string        `json:"mediaType"`
	Media        Media         `json:"media"`
	Progress     float64       `json:"progress"`
	UserProgress *UserProgress `json:"userProgress,omitempty"`
	IsFinished   bool          `json:"isFinished"`
}

type Audiobook struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Author        string  `json:"author"`
	ISBN          string  `json:"isbn,omitempty"`
	ISBN10        string  `json:"isbn10,omitempty"`
	ASIN          string  `json:"asin,omitempty"`
	Progress      float64 `json:"progress"`
	CurrentTime   float64 `json:"currentTime,omitempty"`   // Current position in seconds
	TotalDuration float64 `json:"totalDuration,omitempty"` // Total duration in seconds
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

// Fetch items for a library with progress
func fetchLibraryItems(libraryID string) ([]Item, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Try different include parameters to get progress data
	url := fmt.Sprintf("%s/api/libraries/%s/items?include=progress&minified=0", getAudiobookShelfURL(), libraryID)
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
	debugLog("Raw API response (first 1000 chars): %s", string(body)[:min(1000, len(body))])
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

// Fetch user progress data from AudiobookShelf
func fetchUserProgress() (map[string]float64, error) {
	progressData := make(map[string]float64)

	// Try the authenticated user's session data endpoint first
	endpoints := []string{
		"/api/me/listening-sessions",
		"/api/sessions",
		"/api/me/progress",
		"/api/me/items-in-progress",
		"/api/me/library-items/progress",
	}

	for _, endpoint := range endpoints {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		url := fmt.Sprintf("%s%s", getAudiobookShelfURL(), endpoint)
		debugLog("Trying progress endpoint: %s", url)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
		resp, err := httpClient.Do(req)
		if err != nil {
			cancel()
			debugLog("HTTP error for %s: %v", endpoint, err)
			continue
		}

		if resp.StatusCode == 200 {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			if err != nil {
				continue
			}
			debugLog("Progress endpoint %s response: %s", endpoint, string(body))

			// Try different response structures
			var itemsInProgress []struct {
				ID         string  `json:"id"`
				Progress   float64 `json:"progress"`
				IsFinished bool    `json:"isFinished"`
			}
			if err := json.Unmarshal(body, &itemsInProgress); err == nil && len(itemsInProgress) > 0 {
				for _, item := range itemsInProgress {
					if item.IsFinished {
						progressData[item.ID] = 1.0
					} else {
						progressData[item.ID] = item.Progress
					}
				}
				debugLog("Successfully parsed %d progress items from %s", len(progressData), endpoint)
				return progressData, nil
			}

			// Try libraryItems structure
			var libItemsResp struct {
				LibraryItems []struct {
					ID         string  `json:"id"`
					Progress   float64 `json:"progress"`
					IsFinished bool    `json:"isFinished"`
				} `json:"libraryItems"`
			}
			if err := json.Unmarshal(body, &libItemsResp); err == nil && len(libItemsResp.LibraryItems) > 0 {
				for _, item := range libItemsResp.LibraryItems {
					if item.IsFinished {
						progressData[item.ID] = 1.0
					} else {
						progressData[item.ID] = item.Progress
					}
				}
				debugLog("Successfully parsed %d progress items from %s (libraryItems)", len(progressData), endpoint)
				return progressData, nil
			}

			// Try sessions structure (listening sessions)
			var sessionsResp struct {
				Sessions []struct {
					ID              string                 `json:"id"`
					LibraryItemID   string                 `json:"libraryItemId"`
					EpisodeID       string                 `json:"episodeId"`
					MediaPlayer     string                 `json:"mediaPlayer"`
					DeviceInfo      map[string]interface{} `json:"deviceInfo"`
					Date            string                 `json:"date"`
					DayOfYear       int                    `json:"dayOfYear"`
					Duration        float64                `json:"duration"`
					PlaybackSession map[string]interface{} `json:"playbackSession"`
					Progress        float64                `json:"progress"`
					CurrentTime     float64                `json:"currentTime"`
					TotalDuration   float64                `json:"totalDuration"`
					TimeListening   float64                `json:"timeListening"`
					StartTime       float64                `json:"startTime"`
					CreatedAt       int64                  `json:"createdAt"`
					UpdatedAt       int64                  `json:"updatedAt"`
				} `json:"sessions"`
			}
			if err := json.Unmarshal(body, &sessionsResp); err == nil && len(sessionsResp.Sessions) > 0 {
				debugLog("Successfully parsed %d listening sessions from %s", len(sessionsResp.Sessions), endpoint)
				
				// Build progress map from the latest session for each item
				latestProgress := make(map[string]float64)
				latestCurrentTime := make(map[string]float64)
				latestTotalDuration := make(map[string]float64)
				
				for i, session := range sessionsResp.Sessions {
					debugLog("Session %d: LibraryItemID=%s, Duration=%.2f, CurrentTime=%.2f, Progress=%.6f", 
						i, session.LibraryItemID, session.Duration, session.CurrentTime, session.Progress)
						
					if session.LibraryItemID != "" {
						var sessionProgress float64
						
						// Calculate progress from currentTime and duration
						if session.Duration > 0 && session.CurrentTime > 0 {
							sessionProgress = session.CurrentTime / session.Duration
							debugLog("Session progress calculated from currentTime/duration: %.6f (%.2fs / %.2fs) for item %s", 
								sessionProgress, session.CurrentTime, session.Duration, session.LibraryItemID)
						} else if session.Progress > 0 {
							// Fallback to direct progress field if available
							sessionProgress = session.Progress
							debugLog("Session progress from direct field: %.6f for item %s", sessionProgress, session.LibraryItemID)
						} else {
							debugLog("No valid progress data found for session %d (item %s)", i, session.LibraryItemID)
						}
						
						// Keep the most recent progress for each item
						if existing, exists := latestProgress[session.LibraryItemID]; !exists || sessionProgress > existing {
							latestProgress[session.LibraryItemID] = sessionProgress
							latestCurrentTime[session.LibraryItemID] = session.CurrentTime
							latestTotalDuration[session.LibraryItemID] = session.Duration
							debugLog("Updated latest progress for item %s: %.6f", session.LibraryItemID, sessionProgress)
						}
					}
				}
				
				for itemID, progress := range latestProgress {
					progressData[itemID] = progress
					debugLog("Final session progress for item %s: %.6f (%.2fs / %.2fs)", 
						itemID, progress, latestCurrentTime[itemID], latestTotalDuration[itemID])
				}
				debugLog("Successfully parsed %d progress items from %s (sessions)", len(progressData), endpoint)
				return progressData, nil
			}

			// Try bare sessions array or generic JSON structure
			var sessions []struct {
				ID              string                 `json:"id"`
				LibraryItemID   string                 `json:"libraryItemId"`
				EpisodeID       string                 `json:"episodeId"`
				MediaPlayer     string                 `json:"mediaPlayer"`
				DeviceInfo      map[string]interface{} `json:"deviceInfo"`
				Date            string                 `json:"date"`
				DayOfYear       int                    `json:"dayOfYear"`
				Duration        float64                `json:"duration"`
				PlaybackSession map[string]interface{} `json:"playbackSession"`
				Progress        float64                `json:"progress"`
				CurrentTime     float64                `json:"currentTime"`
				TotalDuration   float64                `json:"totalDuration"`
				TimeListening   float64                `json:"timeListening"`
				StartTime       float64                `json:"startTime"`
				CreatedAt       int64                  `json:"createdAt"`
				UpdatedAt       int64                  `json:"updatedAt"`
			}
			if err := json.Unmarshal(body, &sessions); err == nil && len(sessions) > 0 {
				debugLog("Successfully parsed %d sessions from %s (bare array)", len(sessions), endpoint)
				
				// Build progress map from the latest session for each item
				latestProgress := make(map[string]float64)
				for i, session := range sessions {
					debugLog("Session %d: LibraryItemID=%s, Duration=%.2f, CurrentTime=%.2f, Progress=%.6f", 
						i, session.LibraryItemID, session.Duration, session.CurrentTime, session.Progress)
						
					if session.LibraryItemID != "" {
						var sessionProgress float64
						
						// Calculate progress from currentTime and duration
						if session.Duration > 0 && session.CurrentTime > 0 {
							sessionProgress = session.CurrentTime / session.Duration
							debugLog("Session progress calculated: %.6f = %.2f / %.2f for item %s", 
								sessionProgress, session.CurrentTime, session.Duration, session.LibraryItemID)
						} else if session.Progress > 0 {
							sessionProgress = session.Progress
							debugLog("Session progress from direct field: %.6f for item %s", sessionProgress, session.LibraryItemID)
						}
						
						// Keep the most recent progress for each item
						if existing, exists := latestProgress[session.LibraryItemID]; !exists || sessionProgress > existing {
							latestProgress[session.LibraryItemID] = sessionProgress
							debugLog("Updated latest progress for item %s: %.6f", session.LibraryItemID, sessionProgress)
						}
					}
				}
				
				for itemID, progress := range latestProgress {
					progressData[itemID] = progress
				}
				debugLog("Successfully parsed %d progress items from %s (sessions array)", len(progressData), endpoint)
				return progressData, nil
			}
			
			// Try generic JSON parsing to understand the structure
			var genericData map[string]interface{}
			if err := json.Unmarshal(body, &genericData); err == nil {
				debugLog("Successfully parsed generic JSON structure from %s", endpoint)
				debugLog("Top-level keys in response: %v", func() []string {
					keys := make([]string, 0, len(genericData))
					for k := range genericData {
						keys = append(keys, k)
					}
					return keys
				}())
				
				// Check if there's session data at different levels
				if sessionsData, ok := genericData["sessions"]; ok {
					debugLog("Found 'sessions' key in response")
					if sessionArray, ok := sessionsData.([]interface{}); ok && len(sessionArray) > 0 {
						debugLog("Sessions is an array with %d items", len(sessionArray))
						if firstSession, ok := sessionArray[0].(map[string]interface{}); ok {
							debugLog("First session keys: %v", func() []string {
								keys := make([]string, 0, len(firstSession))
								for k := range firstSession {
									keys = append(keys, k)
								}
								return keys
							}())
							
							// Extract progress manually
							if libraryItemID, hasID := firstSession["libraryItemId"].(string); hasID && libraryItemID != "" {
								var sessionProgress float64
								
								if currentTime, hasTime := firstSession["currentTime"].(float64); hasTime {
									if duration, hasDuration := firstSession["duration"].(float64); hasDuration && duration > 0 {
										sessionProgress = currentTime / duration
										debugLog("Manual session progress calculated: %.6f = %.2f / %.2f", 
											sessionProgress, currentTime, duration)
									}
								}
								
								if progress, hasProgress := firstSession["progress"].(float64); hasProgress && sessionProgress == 0 {
									sessionProgress = progress
									debugLog("Manual session progress from direct field: %.6f", sessionProgress)
								}
								
								if sessionProgress > 0 {
									progressData[libraryItemID] = sessionProgress
									debugLog("Successfully extracted manual progress for item %s: %.6f", libraryItemID, sessionProgress)
									return progressData, nil
								}
							}
						}
					}
				}
			}

			debugLog("Could not parse response from %s", endpoint)
		} else {
			resp.Body.Close()
			cancel()
			debugLog("Endpoint %s returned status %d", endpoint, resp.StatusCode)
		}
	}

	debugLog("No progress endpoints returned usable data, returning empty map")
	return progressData, nil
}

// Fetch progress for a specific library item
func fetchItemProgress(itemID string) (float64, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try multiple possible endpoints for individual item progress
	endpoints := []string{
		fmt.Sprintf("/api/me/library-item/%s", itemID),
		fmt.Sprintf("/api/items/%s", itemID),
		fmt.Sprintf("/api/me/item/%s", itemID),
	}

	for _, endpoint := range endpoints {
		url := fmt.Sprintf("%s%s", getAudiobookShelfURL(), endpoint)
		debugLog("Trying individual item endpoint: %s", url)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode == 200 {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				continue
			}

			debugLog("Individual item response from %s: %s", endpoint, string(body))

			// Try to parse progress from different response structures
			var itemResp struct {
				UserMediaProgress struct {
					Progress   float64 `json:"progress"`
					IsFinished bool    `json:"isFinished"`
				} `json:"userMediaProgress"`
				Progress   float64 `json:"progress"`
				IsFinished bool    `json:"isFinished"`
			}

			if err := json.Unmarshal(body, &itemResp); err == nil {
				// Check userMediaProgress first
				if itemResp.UserMediaProgress.Progress > 0 || itemResp.UserMediaProgress.IsFinished {
					if itemResp.UserMediaProgress.IsFinished {
						return 1.0, true, nil
					}
					return itemResp.UserMediaProgress.Progress, true, nil
				}
				// Check top-level progress
				if itemResp.Progress > 0 || itemResp.IsFinished {
					if itemResp.IsFinished {
						return 1.0, true, nil
					}
					return itemResp.Progress, true, nil
				}
			}
		} else {
			resp.Body.Close()
			debugLog("Endpoint %s returned status %d", endpoint, resp.StatusCode)
		}
	}

	return 0, false, fmt.Errorf("no valid individual item endpoints found")
}

// Fetch audiobooks with progress from all libraries
func fetchAudiobookShelfStats() ([]Audiobook, error) {
	debugLog("Fetching AudiobookShelf stats using new API...")

	// First, try to get user progress data
	progressData, err := fetchUserProgress()
	if err != nil {
		debugLog("Error fetching user progress: %v, continuing with item-level progress", err)
		progressData = make(map[string]float64)
	}

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
				asin := item.Media.Metadata.ASIN
				isbn10 := ""
				if isbn == "" {
					isbn = item.Media.Metadata.ISBN13
				}
				if item.Media.Metadata.ISBN != "" && len(item.Media.Metadata.ISBN) == 10 {
					isbn10 = item.Media.Metadata.ISBN
				}

				// Determine progress from different possible sources
				progress := item.Progress

				// 1. Check if we have progress data from the /api/me/progress endpoint
				if userProgress, hasProgress := progressData[item.ID]; hasProgress {
					progress = userProgress
					debugLog("Using progress from /api/me/progress: %.6f for '%s'", progress, title)
				} else if item.UserProgress != nil {
					// 2. Check UserProgress field in the item
					progress = item.UserProgress.Progress
					debugLog("Using UserProgress.Progress: %.6f for '%s'", progress, title)
				} else if item.IsFinished {
					// 3. Check if item is marked as finished
					progress = 1.0
					debugLog("Item marked as finished, setting progress to 1.0 for '%s'", title)
				}

				// 4. If progress is still 0, try fetching individual item progress
				if progress == 0 {
					indivProgress, isFinished, err := fetchItemProgress(item.ID)
					if err == nil {
						progress = indivProgress
						if isFinished {
							progress = 1.0
						}
						debugLog("Using individual item progress: %.6f for '%s'", progress, title)
					} else {
						debugLog("Failed to fetch individual progress for item '%s': %v", title, err)
					}
				}

				debugLog("Item '%s' final progress: %.6f (raw: %.6f, UserProgress: %v, IsFinished: %v)", title, progress, item.Progress, item.UserProgress != nil, item.IsFinished)

				// Extract duration and current time information
				var currentTime, totalDuration float64

				// Try to get duration from various sources
				if item.Media.Metadata.Duration > 0 {
					totalDuration = item.Media.Metadata.Duration
				} else if item.Media.Duration > 0 {
					totalDuration = item.Media.Duration
				} else if item.UserProgress != nil && item.UserProgress.TotalDuration > 0 {
					totalDuration = item.UserProgress.TotalDuration
				}

				// Get current time position
				if item.UserProgress != nil && item.UserProgress.CurrentTime > 0 {
					currentTime = item.UserProgress.CurrentTime
				} else if totalDuration > 0 && progress > 0 {
					// Calculate current time from progress percentage
					currentTime = totalDuration * progress
				}

				debugLog("Duration info for '%s': currentTime=%.2fs, totalDuration=%.2fs (%.2f hours)",
					title, currentTime, totalDuration, totalDuration/3600)

				audiobooks = append(audiobooks, Audiobook{
					ID:            item.ID,
					Title:         title,
					Author:        author,
					ISBN:          isbn,
					ISBN10:        isbn10,
					ASIN:          asin,
					Progress:      progress,
					CurrentTime:   currentTime,
					TotalDuration: totalDuration,
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
	var bookId, editionId string
	// 1. Try ISBN/ISBN13 if present and not an ASIN
	if a.ISBN != "" && (a.ASIN == "" || a.ISBN != a.ASIN) {
		// Query for book and edition by ISBN/ISBN13
		query := `
		query BookByISBN($isbn: String!) {
		  books(where: { editions: { isbn_13: { _eq: $isbn } } }, limit: 1) {
			id
			title
			editions(where: { isbn_13: { _eq: $isbn } }) {
			  id
			  isbn_13
			  isbn_10
			  asin
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
					ID       json.Number `json:"id"`
					Editions []struct {
						ID     json.Number `json:"id"`
						ISBN10 string      `json:"isbn_10"`
						ASIN   string      `json:"asin"`
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
			bookId = result.Data.Books[0].ID.String()
			if len(result.Data.Books[0].Editions) > 0 {
				editionId = result.Data.Books[0].Editions[0].ID.String()
			}
		}
	}
	// 1b. Try ISBN10 if present and not already tried
	if bookId == "" && a.ISBN10 != "" {
		query := `
		query BookByISBN10($isbn10: String!) {
		  books(where: { editions: { isbn_10: { _eq: $isbn10 } } }, limit: 1) {
			id
			title
			editions(where: { isbn_10: { _eq: $isbn10 } }) {
			  id
			  isbn_13
			  isbn_10
			  asin
			}
		  }
		}`
		variables := map[string]interface{}{"isbn10": a.ISBN10}
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
					ID       json.Number `json:"id"`
					Editions []struct {
						ID     json.Number `json:"id"`
						ISBN10 string      `json:"isbn_10"`
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
			bookId = result.Data.Books[0].ID.String()
			if len(result.Data.Books[0].Editions) > 0 {
				editionId = result.Data.Books[0].Editions[0].ID.String()
			}
		}
	}
	// 2. Try ASIN if present
	if bookId == "" && a.ASIN != "" {
		query := `
		query BookByASIN($asin: String!) {
		  books(where: { editions: { asin: { _eq: $asin } } }, limit: 1) {
			id
			title
			editions(where: { asin: { _eq: $asin } }) {
			  id
			  asin
			  isbn_13
			  isbn_10
			}
		  }
		}`
		variables := map[string]interface{}{"asin": a.ASIN}
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
					ID       json.Number `json:"id"`
					Editions []struct {
						ID   json.Number `json:"id"`
						ASIN string      `json:"asin"`
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
			bookId = result.Data.Books[0].ID.String()
			if len(result.Data.Books[0].Editions) > 0 {
				editionId = result.Data.Books[0].Editions[0].ID.String()
			}
		}
	}
	// 3. Fallback: lookup by title/author
	if bookId == "" {
		var err error
		bookId, err = lookupHardcoverBookID(a.Title, a.Author)
		if err != nil {
			return fmt.Errorf("could not find Hardcover bookId for '%s' by '%s' (ISBN: %s, ASIN: %s): %v", a.Title, a.Author, a.ISBN, a.ASIN, err)
		}
	}
	// Step 2: Insert or update user book
	statusId := 3 // default to read
	if a.Progress < 0.99 {
		statusId = 2 // currently reading
	}
	userBookInput := map[string]interface{}{
		"book_id":   toInt(bookId),
		"status_id": statusId,
	}
	if editionId != "" {
		userBookInput["edition_id"] = toInt(editionId)
	}
	debugLog("Syncing book '%s' by '%s' (Progress: %.6f) with status_id=%d, userBookInput=%+v", a.Title, a.Author, a.Progress, statusId, userBookInput)
	insertUserBookMutation := `
	mutation InsertUserBook($object: UserBookCreateInput!) {
	  insert_user_book(object: $object) {
		id
		user_book { id status_id }
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
						ID       int `json:"id"`
						StatusID int `json:"status_id"`
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
			debugLog("insert_user_book error: %s", *result.Data.InsertUserBook.Error)
			return fmt.Errorf("insert_user_book error: %s", *result.Data.InsertUserBook.Error)
		}
		userBookId = result.Data.InsertUserBook.UserBook.ID
		debugLog("insert_user_book: id=%d, status_id=%d", userBookId, result.Data.InsertUserBook.UserBook.StatusID)
		if result.Data.InsertUserBook.UserBook.StatusID != statusId {
			debugLog("Warning: Hardcover returned status_id=%d, expected %d", result.Data.InsertUserBook.UserBook.StatusID, statusId)
		}
		break
	}
	if userBookId == 0 {
		return fmt.Errorf("failed to insert user book for '%s'", a.Title)
	}
	// Step 3: Insert user book read (progress)
	if a.Progress > 0 {
		var progressSeconds int

		// Use actual current time if available
		if a.CurrentTime > 0 {
			progressSeconds = int(math.Round(a.CurrentTime))
			debugLog("Using actual current time: %.2f seconds for '%s'", a.CurrentTime, a.Title)
		} else if a.TotalDuration > 0 && a.Progress > 0 {
			// Calculate progress seconds from total duration and progress percentage
			progressSeconds = int(math.Round(a.Progress * a.TotalDuration))
			debugLog("Calculated progress from duration: %.2f%% of %.2fs = %d seconds for '%s'",
				a.Progress*100, a.TotalDuration, progressSeconds, a.Title)
		} else {
			// Fallback: use progress percentage * reasonable audiobook duration (10 hours)
			fallbackDuration := 36000.0 // 10 hours in seconds
			progressSeconds = int(math.Round(a.Progress * fallbackDuration))
			debugLog("Using fallback duration calculation: %.2f%% of %.2fs = %d seconds for '%s'",
				a.Progress*100, fallbackDuration, progressSeconds, a.Title)
		}

		// Ensure we have at least 1 second of progress
		if progressSeconds < 1 {
			progressSeconds = 1
		}

		debugLog("Syncing progress for '%s': %d seconds (%.2f%%)", a.Title, progressSeconds, a.Progress*100)
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

	// Filter books that have meaningful progress
	minProgress := getMinimumProgressThreshold()
	var booksToSync []Audiobook
	for _, book := range books {
		if book.Progress > minProgress { // Only sync books with more than the minimum progress
			booksToSync = append(booksToSync, book)
		} else {
			debugLog("Skipping book '%s' with progress %.6f (< %.2f%%)", book.Title, book.Progress, minProgress*100)
		}
	}

	log.Printf("Found %d books with progress to sync (out of %d total books)", len(booksToSync), len(books))

	if len(booksToSync) == 0 {
		log.Printf("No books with progress found to sync")
		return
	}

	delay := getHardcoverSyncDelay()
	for i, book := range booksToSync {
		if i > 0 {
			time.Sleep(delay)
		}
		if err := syncToHardcover(book); err != nil {
			log.Printf("Failed to sync book '%s' (Progress: %.2f%%): %v", book.Title, book.Progress*100, err)
		} else {
			log.Printf("Synced book: %s (Progress: %.2f%%)", book.Title, book.Progress*100)
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
