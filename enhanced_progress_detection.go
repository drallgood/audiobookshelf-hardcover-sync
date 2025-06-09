package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Enhanced progress detection that checks additional endpoints for isFinished flags
func fetchUserProgressEnhanced() (map[string]float64, map[string]bool, error) {
	progressData := make(map[string]float64)
	finishedData := make(map[string]bool)

	// Original endpoints plus additional ones that might have isFinished data
	endpoints := []string{
		"/api/me",
		"/api/me/listening-sessions", 
		"/api/sessions",
		"/api/me/progress",
		"/api/me/items-in-progress",
		"/api/me/library-items/progress",
		"/api/me/finished-items",           // New: might contain finished books
		"/api/me/completed-items",          // New: alternative finished endpoint
		"/api/me/reading-history",          // New: might show completed books
		"/api/me/stats",                    // New: statistics might include finished count
	}

	for _, endpoint := range endpoints {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		url := fmt.Sprintf("%s%s", getAudiobookShelfURL(), endpoint)
		debugLog("Trying enhanced progress endpoint: %s", url)
		
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
			debugLog("Enhanced progress endpoint %s response size: %d bytes", endpoint, len(body))

			// Parse the response and extract both progress and isFinished data
			newProgress, newFinished := parseEnhancedProgressResponse(body, endpoint)
			
			// Merge the results
			for itemID, progress := range newProgress {
				progressData[itemID] = progress
			}
			for itemID, finished := range newFinished {
				finishedData[itemID] = finished
			}

			// If we found meaningful data, continue to next endpoint
			if len(newProgress) > 0 || len(newFinished) > 0 {
				debugLog("Found %d progress items and %d finished flags from %s", 
					len(newProgress), len(newFinished), endpoint)
			}
		} else {
			resp.Body.Close()
			cancel()
			debugLog("Endpoint %s returned status %d", endpoint, resp.StatusCode)
		}
	}

	debugLog("Enhanced progress detection found %d items with progress, %d items with finished flags", 
		len(progressData), len(finishedData))
	return progressData, finishedData, nil
}

func parseEnhancedProgressResponse(body []byte, endpoint string) (map[string]float64, map[string]bool) {
	progressData := make(map[string]float64)
	finishedData := make(map[string]bool)

	// Try to parse as generic JSON first
	var jsonData interface{}
	if err := json.Unmarshal(body, &jsonData); err != nil {
		debugLog("Failed to parse JSON from %s: %v", endpoint, err)
		return progressData, finishedData
	}

	// Recursively search for progress and isFinished data
	extractProgressAndFinished(jsonData, "", progressData, finishedData)

	return progressData, finishedData
}

func extractProgressAndFinished(data interface{}, path string, progressMap map[string]float64, finishedMap map[string]bool) {
	switch v := data.(type) {
	case map[string]interface{}:
		// Look for item ID in various field names
		var itemID string
		if id, hasID := v["id"].(string); hasID {
			itemID = id
		} else if libID, hasLibID := v["libraryItemId"].(string); hasLibID {
			itemID = libID
		} else if libItemID, hasLibItemID := v["libraryItemId"].(string); hasLibItemID {
			itemID = libItemID
		}

		// If we have an item ID, look for progress and finished data
		if itemID != "" {
			// Check for isFinished flag
			if isFinished, hasFinished := v["isFinished"].(bool); hasFinished {
				finishedMap[itemID] = isFinished
				debugLog("Found isFinished=%v for item %s at path %s", isFinished, itemID, path)
				
				// If finished, set progress to 1.0
				if isFinished {
					progressMap[itemID] = 1.0
				}
			}

			// Check for progress data
			if progress, hasProgress := v["progress"].(float64); hasProgress {
				progressMap[itemID] = progress
				debugLog("Found progress=%.6f for item %s at path %s", progress, itemID, path)
			}

			// Check for alternative completion indicators
			if status, hasStatus := v["status"].(string); hasStatus {
				if strings.ToLower(status) == "completed" || strings.ToLower(status) == "finished" {
					finishedMap[itemID] = true
					progressMap[itemID] = 1.0
					debugLog("Found completion status='%s' for item %s", status, itemID)
				}
			}

			// Check for progress percentage that indicates completion
			if progress, hasProgress := v["progress"].(float64); hasProgress && progress >= 0.99 {
				finishedMap[itemID] = true
				debugLog("Inferred finished=true for item %s due to progress=%.6f", itemID, progress)
			}
		}

		// Recursively search nested objects
		for key, value := range v {
			newPath := key
			if path != "" {
				newPath = path + "." + key
			}
			extractProgressAndFinished(value, newPath, progressMap, finishedMap)
		}

	case []interface{}:
		// Search array elements
		for i, item := range v {
			newPath := fmt.Sprintf("[%d]", i)
			if path != "" {
				newPath = path + newPath
			}
			extractProgressAndFinished(item, newPath, progressMap, finishedMap)
		}
	}
}

// Enhanced item-specific progress detection that checks for isFinished flags
func fetchItemProgressEnhanced(itemID string) (float64, bool, error) {
	// Extended list of endpoints to check for item-specific data
	endpoints := []string{
		fmt.Sprintf("/api/me/library-item/%s", itemID),
		fmt.Sprintf("/api/items/%s", itemID),
		fmt.Sprintf("/api/me/item/%s", itemID),
		fmt.Sprintf("/api/items/%s/progress", itemID),
		fmt.Sprintf("/api/items/%s/status", itemID),
		fmt.Sprintf("/api/items/%s/complete", itemID),
		fmt.Sprintf("/api/items/%s/finished", itemID),
		fmt.Sprintf("/api/library-items/%s", itemID),
		fmt.Sprintf("/api/library-items/%s/progress", itemID),
		fmt.Sprintf("/api/me/library-item/%s/progress", itemID),
	}

	for _, endpoint := range endpoints {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		url := fmt.Sprintf("%s%s", getAudiobookShelfURL(), endpoint)
		debugLog("Trying enhanced individual item endpoint: %s", url)
		
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
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

			debugLog("Enhanced individual item response from %s: %d bytes", endpoint, len(body))

			// Parse and look for progress/finished data
			progress, isFinished, found := parseItemProgressResponse(body, endpoint)
			if found {
				return progress, isFinished, nil
			}
		} else {
			resp.Body.Close()
			cancel()
			debugLog("Enhanced individual item endpoint %s returned status %d", endpoint, resp.StatusCode)
		}
	}

	return 0, false, fmt.Errorf("no valid enhanced progress found for item %s", itemID)
}

func parseItemProgressResponse(body []byte, endpoint string) (float64, bool, bool) {
	// Try to parse as JSON
	var jsonData map[string]interface{}
	if err := json.Unmarshal(body, &jsonData); err != nil {
		debugLog("Failed to parse item response from %s: %v", endpoint, err)
		return 0, false, false
	}

	// Look for isFinished flag first (most reliable)
	if isFinished, hasFinished := jsonData["isFinished"].(bool); hasFinished {
		if isFinished {
			debugLog("Found isFinished=true in item response from %s", endpoint)
			return 1.0, true, true
		}
	}

	// Check userMediaProgress structure
	if userProgress, hasUserProgress := jsonData["userMediaProgress"].(map[string]interface{}); hasUserProgress {
		if isFinished, hasFinished := userProgress["isFinished"].(bool); hasFinished && isFinished {
			debugLog("Found userMediaProgress.isFinished=true from %s", endpoint)
			return 1.0, true, true
		}
		if progress, hasProgress := userProgress["progress"].(float64); hasProgress {
			isFinished := progress >= 0.99
			debugLog("Found userMediaProgress.progress=%.6f from %s (finished: %v)", progress, endpoint, isFinished)
			return progress, isFinished, true
		}
	}

	// Check userProgress structure
	if userProgress, hasUserProgress := jsonData["userProgress"].(map[string]interface{}); hasUserProgress {
		if isFinished, hasFinished := userProgress["isFinished"].(bool); hasFinished && isFinished {
			debugLog("Found userProgress.isFinished=true from %s", endpoint)
			return 1.0, true, true
		}
		if progress, hasProgress := userProgress["progress"].(float64); hasProgress {
			isFinished := progress >= 0.99
			debugLog("Found userProgress.progress=%.6f from %s (finished: %v)", progress, endpoint, isFinished)
			return progress, isFinished, true
		}
	}

	// Check top-level progress
	if progress, hasProgress := jsonData["progress"].(float64); hasProgress {
		isFinished := progress >= 0.99
		debugLog("Found top-level progress=%.6f from %s (finished: %v)", progress, endpoint, isFinished)
		return progress, isFinished, true
	}

	// Check for status indicators
	if status, hasStatus := jsonData["status"].(string); hasStatus {
		if strings.ToLower(status) == "completed" || strings.ToLower(status) == "finished" {
			debugLog("Found completion status='%s' from %s", status, endpoint)
			return 1.0, true, true
		}
	}

	return 0, false, false
}

// Enhanced audiobook stats fetching that uses the improved progress detection
func fetchAudiobookShelfStatsEnhanced() ([]Audiobook, error) {
	debugLog("Fetching AudiobookShelf stats using enhanced API detection...")

	// Get enhanced progress data with finished flags
	progressData, finishedData, err := fetchUserProgressEnhanced()
	if err != nil {
		debugLog("Error fetching enhanced user progress: %v, continuing with item-level progress", err)
		progressData = make(map[string]float64)
		finishedData = make(map[string]bool)
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
			if i < 5 {
				debugLog("Item %d mediaType: %s", i, item.MediaType)
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

				// Extract duration information
				var currentTime, totalDuration float64
				if item.Media.Metadata.Duration > 0 {
					totalDuration = item.Media.Metadata.Duration
				} else if item.Media.Duration > 0 {
					totalDuration = item.Media.Duration
				} else if item.UserProgress != nil && item.UserProgress.TotalDuration > 0 {
					totalDuration = item.UserProgress.TotalDuration
				}

				// Determine progress using enhanced detection
				progress := item.Progress

				// 1. Check if we have enhanced finished data
				if isFinished, hasFinished := finishedData[item.ID]; hasFinished && isFinished {
					progress = 1.0
					debugLog("Enhanced detection: book '%s' is finished", title)
				} else if userProgress, hasProgress := progressData[item.ID]; hasProgress {
					// 2. Use enhanced progress data if available
					progress = userProgress
					debugLog("Using enhanced progress: %.6f for '%s'", progress, title)
				} else if item.UserProgress != nil {
					// 3. Fall back to UserProgress
					if item.UserProgress.IsFinished {
						progress = 1.0
						debugLog("UserProgress.IsFinished is true for '%s'", title)
					} else {
						progress = item.UserProgress.Progress
						debugLog("Using UserProgress.Progress: %.6f for '%s'", progress, title)
					}
				} else if item.IsFinished {
					// 4. Check item-level finished flag
					progress = 1.0
					debugLog("Item.IsFinished is true for '%s'", title)
				}

				// 5. If still no progress, try enhanced item-specific detection
				if progress == 0 {
					enhancedProgress, enhancedFinished, err := fetchItemProgressEnhanced(item.ID)
					if err == nil && (enhancedProgress > 0 || enhancedFinished) {
						progress = enhancedProgress
						if enhancedFinished {
							progress = 1.0
						}
						debugLog("Using enhanced item progress: %.6f (finished: %v) for '%s'", 
							progress, enhancedFinished, title)
					}
				}

				// Calculate current time
				if item.UserProgress != nil && item.UserProgress.CurrentTime > 0 {
					currentTime = item.UserProgress.CurrentTime
					currentTime = convertTimeUnits(currentTime, totalDuration)
				} else if totalDuration > 0 && progress > 0 {
					currentTime = totalDuration * progress
				}

				debugLog("Enhanced stats for '%s': progress=%.6f, currentTime=%.2fs, totalDuration=%.2fs", 
					title, progress, currentTime, totalDuration)

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
					Metadata:      item.Media.Metadata,
				})
			}
		}
	}

	debugLog("Enhanced stats found %d audiobooks", len(audiobooks))
	return audiobooks, nil
}
