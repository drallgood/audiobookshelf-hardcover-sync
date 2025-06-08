package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"
)

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

			// Debug the API response for unit conversion issues
			debugAPIResponse(endpoint, body)

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
							// Apply unit conversion before calculating progress
							convertedCurrentTime := convertTimeUnits(session.CurrentTime, session.Duration)
							sessionProgress = convertedCurrentTime / session.Duration
							debugLog("Session progress calculated from currentTime/duration: %.6f (%.2fs / %.2fs) for item %s",
								sessionProgress, convertedCurrentTime, session.Duration, session.LibraryItemID)
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
							// Apply unit conversion before calculating progress
							convertedCurrentTime := convertTimeUnits(session.CurrentTime, session.Duration)
							sessionProgress = convertedCurrentTime / session.Duration
							debugLog("Session progress calculated: %.6f = %.2f / %.2f for item %s",
								sessionProgress, convertedCurrentTime, session.Duration, session.LibraryItemID)
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
										// Apply unit conversion before calculating progress
										convertedCurrentTime := convertTimeUnits(currentTime, duration)
										sessionProgress = convertedCurrentTime / duration
										debugLog("Manual session progress calculated: %.6f = %.2f / %.2f",
											sessionProgress, convertedCurrentTime, duration)
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

			debugLog("Could not parse individual progress from %s", endpoint)
		} else {
			resp.Body.Close()
			debugLog("Individual item endpoint %s returned status %d", endpoint, resp.StatusCode)
		}
	}

	return 0, false, fmt.Errorf("no valid individual progress found for item %s", itemID)
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
					// Apply unit conversion to handle milliseconds vs seconds
					currentTime = convertTimeUnits(currentTime, totalDuration)
				} else if totalDuration > 0 && progress > 0 {
					// Calculate current time from progress percentage (initial estimate)
					currentTime = totalDuration * progress
				}

				// 1. First check UserProgress field in the item (most reliable)
				if item.UserProgress != nil {
					if item.UserProgress.IsFinished {
						progress = 1.0
						debugLog("UserProgress.IsFinished is true, setting progress to 1.0 for '%s'", title)
					} else {
						progress = item.UserProgress.Progress
						debugLog("Using UserProgress.Progress: %.6f for '%s'", progress, title)
					}
				} else if item.IsFinished {
					// 2. Check if item is marked as finished
					progress = 1.0
					debugLog("Item.IsFinished is true, setting progress to 1.0 for '%s'", title)
				}

				// 3. Check if we have progress data from the /api/me/progress endpoint (but validate against UserProgress)
				if userProgress, hasProgress := progressData[item.ID]; hasProgress {
					// If we have UserProgress.CurrentTime, validate the /api/me/progress value
					if item.UserProgress != nil && item.UserProgress.CurrentTime > 0 && totalDuration > 0 {
						calculatedProgressFromCurrentTime := item.UserProgress.CurrentTime / totalDuration
						if math.Abs(calculatedProgressFromCurrentTime-userProgress) > 0.05 { // 5% tolerance
							debugLog("Progress discrepancy between /api/me/progress (%.6f) and UserProgress.CurrentTime-based (%.6f) for '%s', preferring UserProgress",
								userProgress, calculatedProgressFromCurrentTime, title)
							// Keep the UserProgress-based value already set above
						} else {
							progress = userProgress
							debugLog("Using validated progress from /api/me/progress: %.6f for '%s'", progress, title)
						}
					} else {
						// No UserProgress.CurrentTime available, but validate if the progress seems reasonable
						if totalDuration > 0 && userProgress > 0 {
							estimatedCurrentTime := userProgress * totalDuration
							// If the estimated currentTime is very small (< 1 minute) but totalDuration is long (> 30 minutes),
							// this might be a stale or incorrect progress value
							if estimatedCurrentTime < 60 && totalDuration > 1800 { // Less than 1 minute progress on 30+ minute book
								debugLog("Suspicious progress from /api/me/progress (%.6f = %.1fs) for long book '%s' (%.1f hours), trying listening sessions API",
									userProgress, estimatedCurrentTime, title, totalDuration/3600)
								// Try to get correct progress from listening sessions API
								sessionProgress := getProgressFromListeningSessions(item.ID, totalDuration)
								if sessionProgress > 0 {
									progress = sessionProgress
									debugLog("Using progress from listening sessions: %.6f for '%s'", progress, title)
								} else {
									debugLog("No progress found from listening sessions, skipping suspicious /api/me/progress value")
								}
							} else {
								progress = userProgress
								debugLog("Using progress from /api/me/progress: %.6f for '%s'", progress, title)
							}
						} else {
							progress = userProgress
							debugLog("Using progress from /api/me/progress: %.6f for '%s'", progress, title)
						}
					}
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

				// 5. If progress is still 0, try the listening sessions API directly
				if progress == 0 && totalDuration > 0 {
					debugLog("No progress found from other sources, trying listening sessions API for '%s'", title)
					sessionProgress := getProgressFromListeningSessions(item.ID, totalDuration)
					if sessionProgress > 0 {
						progress = sessionProgress
						debugLog("Using progress from listening sessions API: %.6f for '%s'", progress, title)

						// Also calculate currentTime from this progress if we don't have it
						if currentTime == 0 {
							currentTime = progress * totalDuration
							debugLog("Calculated currentTime from listening sessions progress: %.2fs", currentTime)
						}
					}
				}

				// 6. If progress is still 0, try the enhanced finished book detection
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

				// Use current time from UserProgress if available, otherwise calculate from progress
				if item.UserProgress != nil && item.UserProgress.CurrentTime > 0 {
					currentTime = item.UserProgress.CurrentTime
					// Apply unit conversion to handle milliseconds vs seconds
					currentTime = convertTimeUnits(currentTime, totalDuration)
					// Also verify the progress matches the currentTime to avoid inconsistencies
					if totalDuration > 0 {
						calculatedProgress := currentTime / totalDuration
						debugLog("UserProgress currentTime: %.2fs, calculated progress: %.6f, final progress: %.6f",
							currentTime, calculatedProgress, progress)
						// If there's a significant discrepancy, prefer the currentTime-based progress
						if math.Abs(calculatedProgress-progress) > 0.05 { // 5% tolerance
							debugLog("Progress discrepancy detected, using currentTime-based progress: %.6f instead of %.6f",
								calculatedProgress, progress)
							progress = calculatedProgress
						}
					}
				} else if totalDuration > 0 && progress > 0 {
					// Calculate current time from progress percentage only if no UserProgress.CurrentTime
					currentTime = totalDuration * progress
					debugLog("Calculated currentTime from progress: %.2fs = %.2fs * %.6f", currentTime, totalDuration, progress)
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
					Metadata:      item.Media.Metadata, // Include full metadata for enhanced mismatch collection
				})
			}
		}
	}
	debugLog("Total audiobooks found: %d", len(audiobooks))
	return audiobooks, nil
}

// Detect potentially finished books using listening sessions and other indicators
func isBookLikelyFinished(itemID string, currentTime, totalDuration float64) (bool, float64) {
	// Calculate initial progress ratio
	var progressRatio float64
	if totalDuration > 0 && currentTime > 0 {
		progressRatio = currentTime / totalDuration
		debugLog("Calculated progress ratio for enhanced detection: %.6f (%.2fs / %.2fs)", progressRatio, currentTime, totalDuration)

		// If we're already within the last 5% or 5 minutes of the book, consider it finished
		remainingTime := totalDuration - currentTime
		if progressRatio >= 0.95 || remainingTime <= 300 { // 5 minutes
			debugLog("Book likely finished based on progress ratio %.6f or remaining time %.2fs", progressRatio, remainingTime)
			return true, 1.0
		}

		// If progress is reasonable but not complete, return it
		if progressRatio > 0.01 { // At least 1% progress
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

		debugLog("Item-specific listening sessions response: %s", string(body))

		// Try to parse listening sessions response
		var sessionData struct {
			Sessions []struct {
				CurrentTime   float64 `json:"currentTime"`
				Duration      float64 `json:"duration"`
				TotalDuration float64 `json:"totalDuration"`
				TimeRemaining float64 `json:"timeRemaining"`
				Progress      float64 `json:"progress"`
			} `json:"sessions"`
		}

		if err := json.Unmarshal(body, &sessionData); err == nil && len(sessionData.Sessions) > 0 {
			// Get the most recent session
			session := sessionData.Sessions[len(sessionData.Sessions)-1]
			debugLog("Latest session: CurrentTime=%.2f, Duration=%.2f, TotalDuration=%.2f, Progress=%.6f",
				session.CurrentTime, session.Duration, session.TotalDuration, session.Progress)

			// Use session data to determine if finished
			var sessionDuration float64
			if session.TotalDuration > 0 {
				sessionDuration = session.TotalDuration
			} else if session.Duration > 0 {
				sessionDuration = session.Duration
			} else if totalDuration > 0 {
				sessionDuration = totalDuration
			}

			if sessionDuration > 0 && session.CurrentTime > 0 {
				sessionProgress := session.CurrentTime / sessionDuration
				remainingTime := sessionDuration - session.CurrentTime

				debugLog("Session-based progress: %.6f, remaining: %.2fs", sessionProgress, remainingTime)

				// Consider finished if within last 5% or 5 minutes
				if sessionProgress >= 0.95 || remainingTime <= 300 {
					return true, 1.0
				}

				// Return calculated progress if it's meaningful
				if sessionProgress > 0.01 {
					return false, sessionProgress
				}
			}

			// Also check direct progress field
			if session.Progress >= 0.95 {
				return true, 1.0
			} else if session.Progress > 0.01 {
				return false, session.Progress
			}
		}
	}

	return false, progressRatio
}

// fetchRecentListeningSessions fetches listening sessions that were updated after the given timestamp
// This enables incremental sync by only processing sessions that have changed since the last sync
func fetchRecentListeningSessions(sinceTimestamp int64) ([]string, error) {
	debugLog("Fetching listening sessions updated since timestamp: %d", sinceTimestamp)

	endpoints := []string{
		"/api/me/listening-sessions",
		"/api/sessions",
		"/api/me/sessions",
	}

	var updatedItemIds []string
	itemIdSet := make(map[string]bool) // Use set to avoid duplicates

	for _, endpoint := range endpoints {
		url := getAudiobookShelfURL() + endpoint
		debugLog("Trying incremental session endpoint: %s", endpoint)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			cancel()
			continue
		}

		req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
		req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

		resp, err := httpClient.Do(req)
		if err != nil {
			cancel()
			continue
		}

		if resp.StatusCode == 200 {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()

			if err != nil {
				continue
			}

			// Try sessions response structure
			var sessionsResp struct {
				Sessions []struct {
					ID            string `json:"id"`
					LibraryItemID string `json:"libraryItemId"`
					CreatedAt     int64  `json:"createdAt"`
					UpdatedAt     int64  `json:"updatedAt"`
				} `json:"sessions"`
			}

			if err := json.Unmarshal(body, &sessionsResp); err == nil && len(sessionsResp.Sessions) > 0 {
				debugLog("Found %d sessions from %s", len(sessionsResp.Sessions), endpoint)

				for _, session := range sessionsResp.Sessions {
					// Check if session was updated after our timestamp threshold
					if session.UpdatedAt > sinceTimestamp || session.CreatedAt > sinceTimestamp {
						if session.LibraryItemID != "" && !itemIdSet[session.LibraryItemID] {
							updatedItemIds = append(updatedItemIds, session.LibraryItemID)
							itemIdSet[session.LibraryItemID] = true
							debugLog("Found updated session for item %s (UpdatedAt: %d, CreatedAt: %d)",
								session.LibraryItemID, session.UpdatedAt, session.CreatedAt)
						}
					}
				}

				debugLog("Found %d unique updated items from sessions", len(updatedItemIds))
				return updatedItemIds, nil
			}

			// Try bare sessions array
			var sessions []struct {
				ID            string `json:"id"`
				LibraryItemID string `json:"libraryItemId"`
				CreatedAt     int64  `json:"createdAt"`
				UpdatedAt     int64  `json:"updatedAt"`
			}

			if err := json.Unmarshal(body, &sessions); err == nil && len(sessions) > 0 {
				debugLog("Found %d sessions from %s (bare array)", len(sessions), endpoint)

				for _, session := range sessions {
					if session.UpdatedAt > sinceTimestamp || session.CreatedAt > sinceTimestamp {
						if session.LibraryItemID != "" && !itemIdSet[session.LibraryItemID] {
							updatedItemIds = append(updatedItemIds, session.LibraryItemID)
							itemIdSet[session.LibraryItemID] = true
							debugLog("Found updated session for item %s (UpdatedAt: %d, CreatedAt: %d)",
								session.LibraryItemID, session.UpdatedAt, session.CreatedAt)
						}
					}
				}

				debugLog("Found %d unique updated items from sessions", len(updatedItemIds))
				return updatedItemIds, nil
			}
		} else {
			resp.Body.Close()
			cancel()
			debugLog("Endpoint %s returned status %d", endpoint, resp.StatusCode)
		}
	}

	debugLog("No recent listening sessions found or API not available")
	return updatedItemIds, nil
}

// getProgressFromListeningSessions attempts to get accurate progress from the listening sessions API
// when other progress sources are unreliable or missing
func getProgressFromListeningSessions(itemID string, totalDuration float64) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try multiple possible endpoints for item-specific listening sessions
	endpoints := []string{
		fmt.Sprintf("/api/items/%s/listening-sessions", itemID),
		fmt.Sprintf("/api/library-items/%s/listening-sessions", itemID),
		fmt.Sprintf("/api/me/library-item/%s/listening-sessions", itemID),
		fmt.Sprintf("/api/me/item/%s/listening-sessions", itemID),
		fmt.Sprintf("/api/items/%s/sessions", itemID),
		fmt.Sprintf("/api/library-items/%s/sessions", itemID),
	}

	for _, endpoint := range endpoints {
		url := getAudiobookShelfURL() + endpoint
		debugLog("Trying listening sessions endpoint: %s", url)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			debugLog("Error creating request for %s: %v", endpoint, err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
		resp, err := httpClient.Do(req)
		if err != nil {
			debugLog("Error fetching from %s: %v", endpoint, err)
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			debugLog("Endpoint %s returned status %d", endpoint, resp.StatusCode)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			debugLog("Error reading response from %s: %v", endpoint, err)
			continue
		}

		debugLog("Response from %s: %s", endpoint, string(body))

		// Debug the API response for unit conversion issues
		debugAPIResponse(endpoint, body)

		// Try to get progress from this endpoint
		progress := parseListeningSessionsResponse(body, itemID, totalDuration)
		if progress > 0 {
			debugLog("Successfully got progress %.6f from endpoint %s", progress, endpoint)
			return progress
		}
	}

	debugLog("No meaningful progress found from any listening sessions endpoint for item %s", itemID)
	return 0
}

// parseListeningSessionsResponse parses different possible response formats from listening sessions endpoints
func parseListeningSessionsResponse(body []byte, itemID string, totalDuration float64) float64 {
	// Try multiple possible response structures

	// Format 1: Sessions wrapped in an object
	var sessionData struct {
		Sessions []struct {
			CurrentTime   float64 `json:"currentTime"`
			Duration      float64 `json:"duration"`
			TotalDuration float64 `json:"totalDuration"`
			Progress      float64 `json:"progress"`
			CreatedAt     int64   `json:"createdAt"`
			UpdatedAt     int64   `json:"updatedAt"`
		} `json:"sessions"`
	}

	if err := json.Unmarshal(body, &sessionData); err == nil && len(sessionData.Sessions) > 0 {
		debugLog("Found %d sessions in wrapped format", len(sessionData.Sessions))
		return extractProgressFromSessions(sessionData.Sessions, totalDuration)
	}

	// Format 2: Direct array of sessions
	var sessions []struct {
		CurrentTime   float64 `json:"currentTime"`
		Duration      float64 `json:"duration"`
		TotalDuration float64 `json:"totalDuration"`
		Progress      float64 `json:"progress"`
		CreatedAt     int64   `json:"createdAt"`
		UpdatedAt     int64   `json:"updatedAt"`
	}

	if err := json.Unmarshal(body, &sessions); err == nil && len(sessions) > 0 {
		debugLog("Found %d sessions in direct array format", len(sessions))
		return extractProgressFromSessions(sessions, totalDuration)
	}

	// Format 3: Single session object (not array)
	var singleSession struct {
		CurrentTime   float64 `json:"currentTime"`
		Duration      float64 `json:"duration"`
		TotalDuration float64 `json:"totalDuration"`
		Progress      float64 `json:"progress"`
		CreatedAt     int64   `json:"createdAt"`
		UpdatedAt     int64   `json:"updatedAt"`
	}

	if err := json.Unmarshal(body, &singleSession); err == nil && (singleSession.CurrentTime > 0 || singleSession.Progress > 0) {
		debugLog("Found single session object")
		return extractProgressFromSingleSession(singleSession, totalDuration)
	}

	// Format 4: Try to parse as generic JSON and look for any progress data
	var genericData map[string]interface{}
	if err := json.Unmarshal(body, &genericData); err == nil {
		debugLog("Parsing as generic JSON, keys: %v", getKeys(genericData))
		return extractProgressFromGeneric(genericData, totalDuration)
	}

	debugLog("Could not parse listening sessions response in any known format")
	return 0
}

// Helper function to extract progress from session array
func extractProgressFromSessions(sessions interface{}, totalDuration float64) float64 {
	// Use reflection to handle both specific struct and interface{} arrays
	sessionsValue := reflect.ValueOf(sessions)
	if sessionsValue.Kind() != reflect.Slice {
		return 0
	}

	sessionCount := sessionsValue.Len()
	if sessionCount == 0 {
		return 0
	}

	// Get the most recent session (last in array)
	lastSession := sessionsValue.Index(sessionCount - 1)
	return extractProgressFromSessionValue(lastSession, totalDuration)
}

// Helper function to extract progress from a single session
func extractProgressFromSingleSession(session interface{}, totalDuration float64) float64 {
	return extractProgressFromSessionValue(reflect.ValueOf(session), totalDuration)
}

// Helper function to extract progress from a session using reflection
func extractProgressFromSessionValue(sessionValue reflect.Value, totalDuration float64) float64 {
	var currentTime, duration, progress float64

	// Extract fields using reflection or direct access
	if sessionValue.Kind() == reflect.Struct {
		if field := sessionValue.FieldByName("CurrentTime"); field.IsValid() && field.CanFloat() {
			currentTime = field.Float()
		}
		if field := sessionValue.FieldByName("Duration"); field.IsValid() && field.CanFloat() {
			duration = field.Float()
		}
		if field := sessionValue.FieldByName("TotalDuration"); field.IsValid() && field.CanFloat() {
			totalDur := field.Float()
			if totalDur > 0 {
				duration = totalDur
			}
		}
		if field := sessionValue.FieldByName("Progress"); field.IsValid() && field.CanFloat() {
			progress = field.Float()
		}
	}

	debugLog("Session data: CurrentTime=%.2f, Duration=%.2f, Progress=%.6f, TotalDuration=%.2f",
		currentTime, duration, progress, totalDuration)

	// Apply unit conversion to handle milliseconds vs seconds
	currentTime, duration, totalDuration = convertProgressData(currentTime, duration, totalDuration)

	// Calculate progress using the converted values
	return calculateProgressWithConversion(currentTime, duration, totalDuration)
}

// Helper function to extract progress from generic JSON
func extractProgressFromGeneric(data map[string]interface{}, totalDuration float64) float64 {
	// Look for sessions at various levels
	if sessions, ok := data["sessions"]; ok {
		if sessionSlice, ok := sessions.([]interface{}); ok && len(sessionSlice) > 0 {
			if lastSession, ok := sessionSlice[len(sessionSlice)-1].(map[string]interface{}); ok {
				return extractProgressFromGenericSession(lastSession, totalDuration)
			}
		}
	}

	// Look for direct session data
	return extractProgressFromGenericSession(data, totalDuration)
}

// Helper function to extract progress from a generic session map
func extractProgressFromGenericSession(session map[string]interface{}, totalDuration float64) float64 {
	var currentTime, duration, progress float64

	if ct, ok := session["currentTime"].(float64); ok {
		currentTime = ct
	}
	if d, ok := session["duration"].(float64); ok {
		duration = d
	}
	if td, ok := session["totalDuration"].(float64); ok && td > 0 {
		duration = td
	}
	if p, ok := session["progress"].(float64); ok {
		progress = p
	}

	debugLog("Generic session: CurrentTime=%.2f, Duration=%.2f, Progress=%.6f", currentTime, duration, progress)

	// Apply unit conversion to handle milliseconds vs seconds
	currentTime, duration, _ = convertProgressData(currentTime, duration, totalDuration)

	// Calculate progress using the converted values
	return calculateProgressWithConversion(currentTime, duration, totalDuration)
}

// Helper function to get keys from map
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
