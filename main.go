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
	version   = "dev" // Application version
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

// getAudiobookMatchMode returns the behavior when ASIN lookup fails
// Options: "continue" (default), "skip", "fail"
func getAudiobookMatchMode() string {
	mode := strings.ToLower(os.Getenv("AUDIOBOOK_MATCH_MODE"))
	switch mode {
	case "skip", "fail":
		return mode
	default:
		return "continue" // default for backwards compatibility
	}
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
		"/api/me",
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

			// Try /api/me response structure with mediaProgress
			var meResp struct {
				MediaProgress []struct {
					ID            string  `json:"id"`
					LibraryItemId string  `json:"libraryItemId"`
					Progress      float64 `json:"progress"`
					IsFinished    bool    `json:"isFinished"`
					CurrentTime   float64 `json:"currentTime"`
				} `json:"mediaProgress"`
			}
			if err := json.Unmarshal(body, &meResp); err == nil && len(meResp.MediaProgress) > 0 {
				for _, item := range meResp.MediaProgress {
					if item.IsFinished {
						progressData[item.LibraryItemId] = 1.0
						debugLog("Found manually finished book in mediaProgress: %s (isFinished=true)", item.LibraryItemId)
					} else {
						progressData[item.LibraryItemId] = item.Progress
						debugLog("Found progress in mediaProgress: %s progress=%.6f", item.LibraryItemId, item.Progress)
					}
				}
				debugLog("Successfully parsed %d progress items from %s (mediaProgress)", len(progressData), endpoint)
				return progressData, nil
			}

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

				// Extract duration and current time information first
				var currentTime, totalDuration float64

				// Try to get duration from various sources
				if item.Media.Metadata.Duration > 0 {
					totalDuration = item.Media.Metadata.Duration
				} else if item.Media.Duration > 0 {
					totalDuration = item.Media.Duration
				} else if item.UserProgress != nil && item.UserProgress.TotalDuration > 0 {
					totalDuration = item.UserProgress.TotalDuration
				}

				// Determine progress from different possible sources
				progress := item.Progress

				// Get current time position
				if item.UserProgress != nil && item.UserProgress.CurrentTime > 0 {
					currentTime = item.UserProgress.CurrentTime
				} else if totalDuration > 0 && progress > 0 {
					// Calculate current time from progress percentage (initial estimate)
					currentTime = totalDuration * progress
				}

				// 1. Check if we have progress data from the /api/me/progress endpoint
				if userProgress, hasProgress := progressData[item.ID]; hasProgress {
					progress = userProgress
					debugLog("Using progress from /api/me/progress: %.6f for '%s'", progress, title)
				} else if item.UserProgress != nil {
					// 2. Check UserProgress field in the item
					if item.UserProgress.IsFinished {
						progress = 1.0
						debugLog("UserProgress.IsFinished is true, setting progress to 1.0 for '%s'", title)
					} else {
						progress = item.UserProgress.Progress
						debugLog("Using UserProgress.Progress: %.6f for '%s'", progress, title)
					}
				} else if item.IsFinished {
					// 3. Check if item is marked as finished
					progress = 1.0
					debugLog("Item.IsFinished is true, setting progress to 1.0 for '%s'", title)
				}

				// 4. If progress is still 0, try fetching individual item progress
				if progress == 0 {
					indivProgress, isFinished, err := fetchItemProgress(item.ID)
					if err == nil {
						progress = indivProgress
						if isFinished {
							progress = 1.0
						}
						debugLog("Using individual item progress: %.6f (finished: %v) for '%s'", progress, isFinished, title)
					} else {
						debugLog("Failed to fetch individual progress for item '%s': %v", title, err)
					}
				}

				// 5. If progress is still 0, try the enhanced finished book detection
				if progress == 0 {
					debugLog("Trying enhanced finished book detection for '%s' with currentTime=%.2f, totalDuration=%.2f",
						title, currentTime, totalDuration)
					isFinished, detectedProgress := isBookLikelyFinished(item.ID, currentTime, totalDuration)
					if isFinished {
						progress = 1.0
						debugLog("Enhanced detection found '%s' is finished, setting progress to 1.0", title)
					} else if detectedProgress > 0 {
						progress = detectedProgress
						debugLog("Enhanced detection found progress %.6f for '%s'", progress, title)
					}
				}

				debugLog("Item '%s' final progress: %.6f (raw: %.6f, UserProgress: %v, IsFinished: %v)", title, progress, item.Progress, item.UserProgress != nil, item.IsFinished)

				// Recalculate current time with final progress if needed
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
	  books(where: {title: {_eq: $title}, contributions: {author: {name: {_eq: $author}}}}) {
		id
		title
		contributions {
		  author {
			name
		  }
		}
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
				ID            string `json:"id"`
				Title         string `json:"title"`
				Contributions []struct {
					Author struct {
						Name string `json:"name"`
					} `json:"author"`
				} `json:"contributions"`
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
		if strings.EqualFold(book.Title, title) {
			// Check if any of the book's authors match the requested author
			for _, contribution := range book.Contributions {
				if strings.Contains(strings.ToLower(contribution.Author.Name), strings.ToLower(author)) {
					return book.ID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no matching book found for '%s' by '%s'", title, author)
}

// Detect potentially finished books using listening sessions and other indicators
func isBookLikelyFinished(itemID string, currentTime, totalDuration float64) (bool, float64) {
	// If we have valid duration data, check if the book is essentially finished
	if totalDuration > 0 && currentTime > 0 {
		progressRatio := currentTime / totalDuration

		// Consider books with >= 95% completion as finished
		// This accounts for books where users stop a few minutes before the end
		if progressRatio >= 0.95 {
			debugLog("Book %s appears finished based on duration analysis: %.2f%% (%.2fs / %.2fs)",
				itemID, progressRatio*100, currentTime, totalDuration)
			return true, 1.0
		}

		// Return the calculated progress if it's meaningful
		if progressRatio >= 0.01 { // At least 1% progress
			debugLog("Book %s has calculated progress: %.2f%% (%.2fs / %.2fs)",
				itemID, progressRatio*100, currentTime, totalDuration)
			return false, progressRatio
		}
	}

	// Try to fetch listening sessions for this specific item to get more accurate data
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check for item-specific listening sessions
	url := fmt.Sprintf("%s/api/items/%s/listening-sessions", getAudiobookShelfURL(), itemID)
	debugLog("Checking item-specific listening sessions: %s", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, 0
	}
	req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, 0
		}

		// Try to parse sessions response
		var sessionsData struct {
			Sessions []struct {
				CurrentTime   float64 `json:"currentTime"`
				Duration      float64 `json:"duration"`
				TotalDuration float64 `json:"totalDuration"`
				Progress      float64 `json:"progress"`
				UpdatedAt     int64   `json:"updatedAt"`
			} `json:"sessions"`
		}

		if err := json.Unmarshal(body, &sessionsData); err == nil && len(sessionsData.Sessions) > 0 {
			// Find the most recent session
			var latestSession *struct {
				CurrentTime   float64 `json:"currentTime"`
				Duration      float64 `json:"duration"`
				TotalDuration float64 `json:"totalDuration"`
				Progress      float64 `json:"progress"`
				UpdatedAt     int64   `json:"updatedAt"`
			}

			for i := range sessionsData.Sessions {
				session := &sessionsData.Sessions[i]
				if latestSession == nil || session.UpdatedAt > latestSession.UpdatedAt {
					latestSession = session
				}
			}

			if latestSession != nil {
				// Use session's duration info if available
				sessionDuration := latestSession.Duration
				if sessionDuration == 0 && latestSession.TotalDuration > 0 {
					sessionDuration = latestSession.TotalDuration
				}

				if sessionDuration > 0 && latestSession.CurrentTime > 0 {
					sessionProgress := latestSession.CurrentTime / sessionDuration
					debugLog("Latest session for %s: %.2f%% (%.2fs / %.2fs)",
						itemID, sessionProgress*100, latestSession.CurrentTime, sessionDuration)

					// Check if this session indicates completion
					if sessionProgress >= 0.95 {
						debugLog("Book %s appears finished based on latest session data", itemID)
						return true, 1.0
					}

					if sessionProgress >= 0.01 {
						return false, sessionProgress
					}
				}

				// Check direct progress field from session
				if latestSession.Progress >= 0.95 {
					debugLog("Book %s appears finished based on session progress field: %.2f%%",
						itemID, latestSession.Progress*100)
					return true, 1.0
				}

				if latestSession.Progress >= 0.01 {
					debugLog("Book %s has session progress: %.2f%%", itemID, latestSession.Progress*100)
					return false, latestSession.Progress
				}
			}
		}
	}

	return false, 0
}

// checkExistingUserBook checks if the user already has this book in their Hardcover library
// Returns: userBookId (0 if not found), currentStatusId, currentProgressSeconds, error
func checkExistingUserBook(bookId string) (int, int, int, error) {
	query := `
	query CheckUserBook($bookId: Int!) {
	  user_books(where: { book_id: { _eq: $bookId } }, limit: 1) {
		id
		status_id
		book_id
		user_book_reads(order_by: { created_at: desc }, limit: 1) {
		  progress_seconds
		  finished_at
		}
	  }
	}`

	variables := map[string]interface{}{"bookId": toInt(bookId)}
	payload := map[string]interface{}{"query": query, "variables": variables}
	payloadBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, 0, fmt.Errorf("hardcover user_books query error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			UserBooks []struct {
				ID            int `json:"id"`
				StatusID      int `json:"status_id"`
				BookID        int `json:"book_id"`
				UserBookReads []struct {
					ProgressSeconds *int    `json:"progress_seconds"`
					FinishedAt      *string `json:"finished_at"`
				} `json:"user_book_reads"`
			} `json:"user_books"`
		} `json:"data"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0, err
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, 0, err
	}

	// If no user book found, return 0s to indicate we need to create it
	if len(result.Data.UserBooks) == 0 {
		debugLog("No existing user book found for bookId=%s", bookId)
		return 0, 0, 0, nil
	}

	userBook := result.Data.UserBooks[0]
	userBookId := userBook.ID
	currentStatusId := userBook.StatusID
	currentProgressSeconds := 0

	// Get the most recent progress if available
	if len(userBook.UserBookReads) > 0 && userBook.UserBookReads[0].ProgressSeconds != nil {
		currentProgressSeconds = *userBook.UserBookReads[0].ProgressSeconds
	}

	debugLog("Found existing user book: userBookId=%d, statusId=%d, progressSeconds=%d",
		userBookId, currentStatusId, currentProgressSeconds)

	return userBookId, currentStatusId, currentProgressSeconds, nil
}

// checkExistingUserBookRead checks if a user_book_read already exists for the given user_book_id on today's date
// Returns: existingReadId (0 if not found), existingProgressSeconds, error
func checkExistingUserBookRead(userBookID int, targetDate string) (int, int, error) {
	query := `
	query CheckUserBookRead($userBookId: Int!, $targetDate: date!) {
	  user_book_reads(where: { 
		user_book_id: { _eq: $userBookId },
		started_at: { _eq: $targetDate }
	  }, limit: 1) {
		id
		progress_seconds
		started_at
		finished_at
	  }
	}`

	variables := map[string]interface{}{
		"userBookId": userBookID,
		"targetDate": targetDate,
	}
	payload := map[string]interface{}{"query": query, "variables": variables}
	payloadBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("hardcover user_book_reads query error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Data struct {
			UserBookReads []struct {
				ID              int     `json:"id"`
				ProgressSeconds *int    `json:"progress_seconds"`
				StartedAt       *string `json:"started_at"`
				FinishedAt      *string `json:"finished_at"`
			} `json:"user_book_reads"`
		} `json:"data"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, err
	}

	// If no user book read found for this date, return 0s to indicate we need to create it
	if len(result.Data.UserBookReads) == 0 {
		debugLog("No existing user_book_read found for userBookId=%d on date=%s", userBookID, targetDate)
		return 0, 0, nil
	}

	userBookRead := result.Data.UserBookReads[0]
	existingReadId := userBookRead.ID
	existingProgressSeconds := 0

	if userBookRead.ProgressSeconds != nil {
		existingProgressSeconds = *userBookRead.ProgressSeconds
	}

	debugLog("Found existing user_book_read: id=%d, progressSeconds=%d, date=%s",
		existingReadId, existingProgressSeconds, targetDate)

	return existingReadId, existingProgressSeconds, nil
}

// checkRecentFinishedRead checks if a user_book_read with status "read" (finished) already exists
// for the given user_book_id within the last 30 days to prevent duplicate finished reads
func checkRecentFinishedRead(userBookID int) (bool, string, error) {
	// Calculate date 30 days ago
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30).Format("2006-01-02")

	query := `
	query CheckRecentFinishedRead($userBookId: Int!, $since: String!) {
	  user_book_reads(
		where: {
		  user_book_id: { _eq: $userBookId }
		  finished_at: { _is_null: false }
		  finished_at: { _gte: $since }
		}
		order_by: { finished_at: desc }
		limit: 1
	  ) {
		id
		finished_at
	  }
	}`

	variables := map[string]interface{}{
		"userBookId": userBookID,
		"since":      thirtyDaysAgo,
	}

	requestBody := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return false, "", fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", "https://hardcover.app/api/graphql", bytes.NewBuffer(jsonBody))
	if err != nil {
		return false, "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("request failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", fmt.Errorf("failed to read response: %v", err)
	}

	debugLog("checkRecentFinishedRead response: %s", string(body))

	var result struct {
		Data struct {
			UserBookReads []struct {
				ID         int    `json:"id"`
				FinishedAt string `json:"finished_at"`
			} `json:"user_book_reads"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return false, "", fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return false, "", fmt.Errorf("GraphQL errors: %v", result.Errors)
	}

	if len(result.Data.UserBookReads) > 0 {
		finishedAt := result.Data.UserBookReads[0].FinishedAt
		debugLog("Found recent finished read for user_book_id %d: finished on %s", userBookID, finishedAt)
		return true, finishedAt, nil
	}

	debugLog("No recent finished read found for user_book_id %d within last 30 days", userBookID)
	return false, "", nil
}

// Sync each finished audiobook to Hardcover
func syncToHardcover(a Audiobook) error {
	var bookId, editionId string

	debugLog("Starting book matching for '%s' by '%s' (ISBN: %s, ASIN: %s)", a.Title, a.Author, a.ISBN, a.ASIN)

	// PRIORITY 1: Try ASIN first since it's most likely to match the actual audiobook edition
	if a.ASIN != "" {
		query := `
		query BookByASIN($asin: String!) {
		  books(where: { editions: { asin: { _eq: $asin }, reading_format_id: { _eq: 2 } } }, limit: 1) {
			id
			title
			editions(where: { asin: { _eq: $asin }, reading_format_id: { _eq: 2 } }) {
			  id
			  asin
			  isbn_13
			  isbn_10
			  reading_format_id
			  audio_seconds
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
						ID              json.Number `json:"id"`
						ASIN            string      `json:"asin"`
						ReadingFormatID *int        `json:"reading_format_id"`
						AudioSeconds    *int        `json:"audio_seconds"`
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
		if len(result.Data.Books) > 0 && len(result.Data.Books[0].Editions) > 0 {
			bookId = result.Data.Books[0].ID.String()
			editionId = result.Data.Books[0].Editions[0].ID.String()
			debugLog("Found audiobook via ASIN: bookId=%s, editionId=%s, format_id=%v, audio_seconds=%v",
				bookId, editionId, result.Data.Books[0].Editions[0].ReadingFormatID, result.Data.Books[0].Editions[0].AudioSeconds)
		} else {
			debugLog("No audiobook edition found for ASIN %s", a.ASIN)
		}
	}

	// PRIORITY 2: Try ISBN/ISBN13 only if ASIN didn't work and ISBN is different from ASIN
	if bookId == "" && a.ISBN != "" && (a.ASIN == "" || a.ISBN != a.ASIN) {
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

	// PRIORITY 3: Fallback to title/author lookup
	if bookId == "" {
		var err error
		bookId, err = lookupHardcoverBookID(a.Title, a.Author)
		if err != nil {
			return fmt.Errorf("could not find Hardcover bookId for '%s' by '%s' (ISBN: %s, ASIN: %s): %v", a.Title, a.Author, a.ISBN, a.ASIN, err)
		}
	}

	// Validate that we have a valid bookId before proceeding
	if bookId == "" {
		return fmt.Errorf("failed to find valid Hardcover bookId for '%s' by '%s' (ISBN: %s, ASIN: %s) - all lookup methods returned empty bookId", a.Title, a.Author, a.ISBN, a.ASIN)
	}

	// SAFETY CHECK: Handle audiobook edition matching behavior
	// This helps prevent syncing audiobook progress to wrong editions (ebook/physical)
	if a.ASIN != "" && editionId == "" {
		matchMode := getAudiobookMatchMode()
		
		switch matchMode {
		case "fail":
			return fmt.Errorf("ASIN lookup failed for audiobook '%s' (ASIN: %s) and AUDIOBOOK_MATCH_MODE=fail. Cannot guarantee correct audiobook edition match", a.Title, a.ASIN)
		case "skip":
			debugLog("SKIPPING: ASIN lookup failed for '%s' (ASIN: %s) and AUDIOBOOK_MATCH_MODE=skip. Avoiding potential wrong edition sync.", a.Title, a.ASIN)
			return nil // Skip this book entirely
		default: // "continue"
			debugLog("WARNING: ASIN lookup failed for '%s' (ASIN: %s), using fallback book matching. Progress may not sync correctly if this isn't the audiobook edition.", a.Title, a.ASIN)
			debugLog("Consider manually checking if bookId %s corresponds to the correct audiobook edition in Hardcover", bookId)
			debugLog("To change this behavior, set AUDIOBOOK_MATCH_MODE=skip or AUDIOBOOK_MATCH_MODE=fail")
		}
	}

	// Step 2: Check if user already has this book and compare status/progress
	existingUserBookId, existingStatusId, existingProgressSeconds, err := checkExistingUserBook(bookId)
	if err != nil {
		return fmt.Errorf("failed to check existing user book for '%s': %v", a.Title, err)
	}

	// Determine the target status for this book
	targetStatusId := 3 // default to read
	if a.Progress < 0.99 {
		targetStatusId = 2 // currently reading
	}

	// Calculate target progress in seconds
	var targetProgressSeconds int
	if a.Progress > 0 {
		if a.CurrentTime > 0 {
			targetProgressSeconds = int(math.Round(a.CurrentTime))
		} else if a.TotalDuration > 0 && a.Progress > 0 {
			targetProgressSeconds = int(math.Round(a.Progress * a.TotalDuration))
		} else {
			// Fallback: use progress percentage * reasonable audiobook duration (10 hours)
			fallbackDuration := 36000.0 // 10 hours in seconds
			targetProgressSeconds = int(math.Round(a.Progress * fallbackDuration))
		}
		// Ensure we have at least 1 second of progress
		if targetProgressSeconds < 1 {
			targetProgressSeconds = 1
		}
	}

	// Check if we need to sync (book doesn't exist OR status/progress has changed)
	var userBookId int
	needsSync := false

	if existingUserBookId == 0 {
		// Book doesn't exist - need to create it
		needsSync = true
		debugLog("Book '%s' not found in user's Hardcover library - will create", a.Title)
	} else {
		// Book exists - check if status or progress has meaningfully changed
		userBookId = existingUserBookId

		statusChanged := existingStatusId != targetStatusId

		// Consider progress changed if the difference is significant (more than 30 seconds or 10%)
		progressThreshold := int(math.Max(30, float64(targetProgressSeconds)*0.1))
		progressChanged := targetProgressSeconds > 0 &&
			(existingProgressSeconds == 0 ||
				math.Abs(float64(targetProgressSeconds-existingProgressSeconds)) > float64(progressThreshold))

		if statusChanged || progressChanged {
			needsSync = true
			debugLog("Book '%s' needs update - status changed: %t (%d->%d), progress changed: %t (%ds->%ds)",
				a.Title, statusChanged, existingStatusId, targetStatusId, progressChanged, existingProgressSeconds, targetProgressSeconds)
		} else {
			debugLog("Book '%s' already up-to-date in Hardcover (status: %d, progress: %ds) - skipping",
				a.Title, existingStatusId, existingProgressSeconds)
			return nil
		}
	}

	// Only proceed with sync if needed
	if !needsSync {
		return nil
	}

	// If book doesn't exist, create it
	if existingUserBookId == 0 {
		userBookInput := map[string]interface{}{
			"book_id":   toInt(bookId),
			"status_id": targetStatusId,
		}
		if editionId != "" {
			userBookInput["edition_id"] = toInt(editionId)
		}
		debugLog("Creating new user book for '%s' by '%s' (Progress: %.6f) with status_id=%d, userBookInput=%+v", a.Title, a.Author, a.Progress, targetStatusId, userBookInput)
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
			if result.Data.InsertUserBook.UserBook.StatusID != targetStatusId {
				debugLog("Warning: Hardcover returned status_id=%d, expected %d", result.Data.InsertUserBook.UserBook.StatusID, targetStatusId)
			}
			break
		}
		if userBookId == 0 {
			return fmt.Errorf("failed to insert user book for '%s'", a.Title)
		}
	}

	// Step 3: Insert user book read (progress) - only if we have meaningful progress
	if targetProgressSeconds > 0 {
		debugLog("Syncing progress for '%s': %d seconds (%.2f%%)", a.Title, targetProgressSeconds, a.Progress*100)

		// Check if a user_book_read already exists for today
		today := time.Now().Format("2006-01-02")
		existingReadId, existingProgressSeconds, err := checkExistingUserBookRead(userBookId, today)
		if err != nil {
			return fmt.Errorf("failed to check existing user book read for '%s': %v", a.Title, err)
		}

		if existingReadId > 0 {
			// Update existing user_book_read if progress has changed
			if existingProgressSeconds != targetProgressSeconds {
				debugLog("Updating existing user_book_read id=%d for '%s': progressSeconds=%d -> %d", existingReadId, a.Title, existingProgressSeconds, targetProgressSeconds)
				updateMutation := `
				mutation UpdateUserBookRead($id: Int!, $object: DatesReadInput!) {
				  update_user_book_read(id: $id, object: $object) {
					id
					error
					user_book_read {
					  id
					  progress_seconds
					  started_at
					  finished_at
					}
				  }
				}`
				variables := map[string]interface{}{
					"id": existingReadId,
					"object": map[string]interface{}{
						"progress_seconds": targetProgressSeconds,
					},
				}
				payload := map[string]interface{}{
					"query":     updateMutation,
					"variables": variables,
				}
				payloadBytes, err := json.Marshal(payload)
				if err != nil {
					return fmt.Errorf("failed to marshal update_user_book_read payload: %v", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
				if err != nil {
					return fmt.Errorf("failed to create request: %v", err)
				}
				req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

				resp, err := httpClient.Do(req)
				if err != nil {
					return fmt.Errorf("http request failed: %v", err)
				}
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("failed to read response: %v", err)
				}

				debugLog("update_user_book_read response: %s", string(body))

				var result struct {
					Data struct {
						UpdateUserBookRead struct {
							ID           int     `json:"id"`
							Error        *string `json:"error"`
							UserBookRead *struct {
								ID              int     `json:"id"`
								ProgressSeconds int     `json:"progress_seconds"`
								StartedAt       string  `json:"started_at"`
								FinishedAt      *string `json:"finished_at"`
							} `json:"user_book_read"`
						} `json:"update_user_book_read"`
					} `json:"data"`
					Errors []struct {
						Message string `json:"message"`
					} `json:"errors"`
				}

				if err := json.Unmarshal(body, &result); err != nil {
					return fmt.Errorf("failed to parse response: %v", err)
				}

				if len(result.Errors) > 0 {
					return fmt.Errorf("graphql errors: %v", result.Errors)
				}

				if result.Data.UpdateUserBookRead.Error != nil {
					return fmt.Errorf("update error: %s", *result.Data.UpdateUserBookRead.Error)
				}

				debugLog("Successfully updated user_book_read with id: %d", result.Data.UpdateUserBookRead.ID)
				if result.Data.UpdateUserBookRead.UserBookRead != nil {
					debugLog("Confirmed progress update: %d seconds", result.Data.UpdateUserBookRead.UserBookRead.ProgressSeconds)
				}
			} else {
				debugLog("No update needed for existing user_book_read id=%d (progress already %d seconds)", existingReadId, existingProgressSeconds)
			}
		} else {
			// Enhanced duplicate prevention: Check if book was recently finished before creating new read entry
			// This prevents spam in Hardcover feed from books that are already marked as "read"
			if a.Progress >= 0.99 { // Only check for finished books
				recentlyFinished, finishDate, err := checkRecentFinishedRead(userBookId)
				if err != nil {
					debugLog("Warning: Failed to check recent finished reads for '%s': %v", a.Title, err)
					// Continue with normal flow even if check fails
				} else if recentlyFinished {
					debugLog("Skipping duplicate read entry for '%s' - already finished on %s (within last 30 days)", a.Title, finishDate)
					return nil
				}
			}

			// Create new user book read entry
			debugLog("Creating new user_book_read for '%s': %d seconds (%.2f%%)", a.Title, targetProgressSeconds, a.Progress*100)

			// Use the enhanced insertUserBookRead function which includes reading_format_id for audiobooks
			// This ensures Hardcover recognizes it as an audiobook and doesn't ignore progress_seconds
			if err := insertUserBookRead(userBookId, targetProgressSeconds, a.Progress >= 0.99); err != nil {
				return fmt.Errorf("failed to sync progress for '%s': %v", a.Title, err)
			}

			debugLog("Successfully synced progress for '%s': %d seconds", a.Title, targetProgressSeconds)
		}
	}
	return nil
}

// Helper to convert string to int safely
func toInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

// insertUserBookRead is a function that uses the insert_user_book_read mutation
// to sync progress to Hardcover
func insertUserBookRead(userBookID int, progressSeconds int, isFinished bool) error {
	// Prepare the input for the mutation
	userBookRead := map[string]interface{}{
		"progress_seconds":  progressSeconds,
		"reading_format_id": 2, // Audiobook format
	}

	// Set dates based on completion status
	now := time.Now().Format("2006-01-02")
	userBookRead["started_at"] = now

	if isFinished {
		userBookRead["finished_at"] = now
	}

	// Use the direct insert_user_book_read mutation which works more reliably
	insertMutation := `
	mutation InsertUserBookRead($user_book_id: Int!, $user_book_read: DatesReadInput!) {
	  insert_user_book_read(user_book_id: $user_book_id, user_book_read: $user_book_read) {
		id
		error
	  }
	}`

	variables := map[string]interface{}{
		"user_book_id":   userBookID,
		"user_book_read": userBookRead,
	}

	payload := map[string]interface{}{
		"query":     insertMutation,
		"variables": variables,
	}

	debugLog("Using insert_user_book_read with variables: %+v", variables)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal insert_user_book_read payload: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.hardcover.app/v1/graphql", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+getHardcoverToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}

	debugLog("insert_user_book_read response: %s", string(body))

	var result struct {
		Data struct {
			InsertUserBookRead struct {
				ID    int     `json:"id"`
				Error *string `json:"error"`
			} `json:"insert_user_book_read"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %v", err)
	}

	if len(result.Errors) > 0 {
		return fmt.Errorf("graphql errors: %v", result.Errors)
	}

	if result.Data.InsertUserBookRead.Error != nil {
		return fmt.Errorf("insert error: %s", *result.Data.InsertUserBookRead.Error)
	}

	debugLog("Successfully inserted user_book_read with id: %d", result.Data.InsertUserBookRead.ID)
	return nil
}

func runSync() {
	books, err := fetchAudiobookShelfStats()
	if err != nil {
		log.Printf("Failed to fetch AudiobookShelf stats: %v", err)
		return
	}

	// Filter books that have meaningful progress or are likely finished
	minProgress := getMinimumProgressThreshold()
	var booksToSync []Audiobook
	for _, book := range books {
		if book.Progress > minProgress { // Only sync books with more than the minimum progress
			booksToSync = append(booksToSync, book)
		} else {
			// For books with low/zero progress, check if they might actually be finished
			// using the enhanced detection methods
			isFinished, actualProgress := isBookLikelyFinished(book.ID, book.CurrentTime, book.TotalDuration)
			if isFinished {
				// Update the book's progress to reflect that it's finished
				book.Progress = 1.0
				booksToSync = append(booksToSync, book)
				debugLog("Book '%s' appears finished despite low reported progress (%.6f) - adding to sync with progress 1.0", book.Title, book.Progress)
			} else if actualProgress > minProgress {
				// Update the book's progress with the better calculated value
				book.Progress = actualProgress
				booksToSync = append(booksToSync, book)
				debugLog("Book '%s' has better calculated progress (%.6f) - adding to sync", book.Title, actualProgress)
			} else {
				debugLog("Skipping book '%s' with progress %.6f (< %.2f%%)", book.Title, book.Progress, minProgress*100)
			}
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
