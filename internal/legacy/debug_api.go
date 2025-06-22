// +build ignore

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

// Test function to directly inspect AudiobookShelf API responses
func debugAudiobookShelfAPI() {
	fmt.Printf("=== AudiobookShelf API Debug Tool ===
")

	// The item ID from the log for "Red Bounty"
	itemID := "b66baeff-9987-4ccc-b1e7-3e0df592fe48"

	// Test various endpoints
	endpoints := []string{
		"/api/me",
		"/api/me/progress",
		"/api/me/listening-sessions",
		fmt.Sprintf("/api/items/%s", itemID),
		fmt.Sprintf("/api/items/%s/listening-sessions", itemID),
		fmt.Sprintf("/api/me/library-item/%s", itemID),
		fmt.Sprintf("/api/me/library-item/%s/listening-sessions", itemID),
		"/api/sessions",
	}

	for _, endpoint := range endpoints {
		fmt.Printf("
=== Testing %s ===
", endpoint)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		url := getAudiobookShelfURL() + endpoint

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			fmt.Printf("Error creating request: %v
", err)
			cancel()
			continue
		}

		req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
		resp, err := httpClient.Do(req)
		cancel()

		if err != nil {
			fmt.Printf("HTTP Error: %v
", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			fmt.Printf("Read Error: %v
", err)
			continue
		}

		fmt.Printf("Status: %d
", resp.StatusCode)

		if resp.StatusCode == 200 {
			// Try to format as JSON for readability
			var jsonData interface{}
			if err := json.Unmarshal(body, &jsonData); err == nil {
				prettyJSON, _ := json.MarshalIndent(jsonData, "", "  ")
				maxLen := len(prettyJSON)
				if maxLen > 1000 {
					maxLen = 1000
				}
				fmt.Printf("Response (first 1000 chars):
%s
", string(prettyJSON)[:maxLen])
			} else {
				maxLen := len(body)
				if maxLen > 500 {
					maxLen = 500
				}
				fmt.Printf("Response (raw, first 500 chars):
%s
", string(body)[:maxLen])
			}
		} else {
			maxLen := len(body)
			if maxLen > 200 {
				maxLen = 200
			}
			fmt.Printf("Error response: %s
", string(body)[:maxLen])
		}
	}
}

// Enhanced function to explore endpoints for missing isFinished flags
func exploreFinishedFlagEndpoints() {
	fmt.Printf("=== Exploring AudiobookShelf Endpoints for isFinished Flags ===
")

	// First get some finished books to test with
	audiobooks, err := fetchAudiobookShelfStats()
	if err != nil {
		fmt.Printf("Error fetching audiobooks: %v
", err)
		return
	}

	// Find books with high progress (likely finished)
	var testBooks []Audiobook
	for _, book := range audiobooks {
		if book.Progress >= 0.95 {
			testBooks = append(testBooks, book)
			if len(testBooks) >= 2 { // Test with 2 books
				break
			}
		}
	}

	if len(testBooks) == 0 {
		fmt.Printf("No books with progress >= 95%% found for testing
")
		return
	}

	fmt.Printf("Testing with %d high-progress books:
", len(testBooks))
	for i, book := range testBooks {
		fmt.Printf("  %d. '%s' - Progress: %.2f%%
", i+1, book.Title, book.Progress*100)
	}

	// Test comprehensive list of endpoints
	testEndpointVariations(testBooks[0])

	// Test user-wide endpoints
	testUserWideEndpoints()
}

func testEndpointVariations(book Audiobook) {
	fmt.Printf("
=== Testing Item-Specific Endpoints for '%s' ===
", book.Title)

	// Comprehensive list of endpoint patterns to try
	endpointPatterns := []string{
		"/api/items/%s",
		"/api/items/%s/progress",
		"/api/items/%s/status",
		"/api/items/%s/complete",
		"/api/items/%s/finished",
		"/api/items/%s/user-progress",
		"/api/me/item/%s",
		"/api/me/items/%s",
		"/api/me/library-item/%s",
		"/api/me/library-items/%s",
		"/api/me/library-item/%s/progress",
		"/api/me/library-item/%s/status",
		"/api/library-items/%s",
		"/api/library-items/%s/progress",
		"/api/library-items/%s/status",
		"/api/library-items/%s/user-progress",
		"/api/items/%s/listening-sessions",
		"/api/items/%s/sessions",
		"/api/me/library-item/%s/listening-sessions",
		"/api/me/library-item/%s/sessions",
		"/api/library-items/%s/listening-sessions",
		"/api/library-items/%s/sessions",
	}

	finishedFlagsFound := 0
	for _, pattern := range endpointPatterns {
		endpoint := fmt.Sprintf(pattern, book.ID)
		hasFinished := testSingleEndpointForFinished(endpoint)
		if hasFinished {
			finishedFlagsFound++
		}
	}

	fmt.Printf("Summary: Found isFinished flags in %d/%d item-specific endpoints
",
		finishedFlagsFound, len(endpointPatterns))
}

func testUserWideEndpoints() {
	fmt.Printf("
=== Testing User-Wide Endpoints ===
")

	userEndpoints := []string{
		"/api/me",
		"/api/me/progress",
		"/api/me/items-in-progress",
		"/api/me/library-items/progress",
		"/api/me/finished-items",
		"/api/me/completed-items",
		"/api/me/items-finished",
		"/api/me/items-completed",
		"/api/me/reading-history",
		"/api/me/listening-history",
		"/api/me/stats",
		"/api/me/statistics",
		"/api/me/media-status",
		"/api/me/book-status",
		"/api/me/progress-summary",
		"/api/me/completion-status",
		"/api/me/listening-sessions",
		"/api/me/sessions",
		"/api/sessions",
		"/api/progress",
		"/api/finished-items",
		"/api/completed-items",
		"/api/reading-history",
		"/api/listening-history",
	}

	finishedFlagsFound := 0
	for _, endpoint := range userEndpoints {
		hasFinished := testSingleEndpointForFinished(endpoint)
		if hasFinished {
			finishedFlagsFound++
		}
	}

	fmt.Printf("Summary: Found isFinished flags in %d/%d user-wide endpoints
",
		finishedFlagsFound, len(userEndpoints))
}

func testSingleEndpointForFinished(endpoint string) bool {
	fmt.Printf("
Testing: %s
", endpoint)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	url := getAudiobookShelfURL() + endpoint
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		fmt.Printf("  ❌ Request error: %v
", err)
		return false
	}

	req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
	req.Header.Set("User-Agent", "AudiobookShelfSyncScript/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("  ❌ HTTP error: %v
", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("  ❌ Status: %d
", resp.StatusCode)
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("  ❌ Read error: %v
", err)
		return false
	}

	fmt.Printf("  ✅ Status: 200, Size: %d bytes
", len(body))

	// Search for isFinished flags
	isFinishedCount := countIsFinishedOccurrences(body)
	if isFinishedCount > 0 {
		fmt.Printf("  🎯 Found %d isFinished flag(s)
", isFinishedCount)

		// Show a preview of where isFinished appears
		previewFinishedContext(body)
		return true
	} else {
		fmt.Printf("  ⚪ No isFinished flags found
")

		// Look for alternative completion indicators
		alternativeCount := countAlternativeCompletionFields(body)
		if alternativeCount > 0 {
			fmt.Printf("  💡 Found %d alternative completion indicator(s)
", alternativeCount)
		}
		return false
	}
}

func countIsFinishedOccurrences(data []byte) int {
	content := string(data)
	count := 0
	searchPos := 0

	for {
		pos := strings.Index(content[searchPos:], "\"isFinished\"")
		if pos == -1 {
			break
		}
		count++
		searchPos += pos + 1
	}

	return count
}

func countAlternativeCompletionFields(data []byte) int {
	content := string(data)
	alternatives := []string{
		"\"completed\"", "\"complete\"", "\"finished\"",
		"\"status\"", "\"state\"", "\"progressStatus\"",
		"\"readingStatus\"", "\"progress\":1", "\"progress\":0.99",
	}

	count := 0
	for _, alt := range alternatives {
		if strings.Contains(content, alt) {
			count++
		}
	}

	return count
}

func previewFinishedContext(data []byte) {
	content := string(data)
	pos := strings.Index(content, "\"isFinished\"")
	if pos == -1 {
		return
	}

	// Show context around the isFinished field
	start := pos - 50
	if start < 0 {
		start = 0
	}
	end := pos + 100
	if end > len(content) {
		end = len(content)
	}

	context := content[start:end]
	fmt.Printf("  📝 Context: ...%s...
", context)
}

// Function to run comprehensive endpoint testing
func runComprehensiveEndpointTest() {
	exploreFinishedFlagEndpoints()
}
