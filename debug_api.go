package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Test function to directly inspect AudiobookShelf API responses
func debugAudiobookShelfAPI() {
	fmt.Printf("=== AudiobookShelf API Debug Tool ===\n")

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
		fmt.Printf("\n=== Testing %s ===\n", endpoint)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		url := getAudiobookShelfURL() + endpoint

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			fmt.Printf("Error creating request: %v\n", err)
			cancel()
			continue
		}

		req.Header.Set("Authorization", "Bearer "+getAudiobookShelfToken())
		resp, err := httpClient.Do(req)
		cancel()

		if err != nil {
			fmt.Printf("HTTP Error: %v\n", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			fmt.Printf("Read Error: %v\n", err)
			continue
		}

		fmt.Printf("Status: %d\n", resp.StatusCode)

		if resp.StatusCode == 200 {
			// Try to format as JSON for readability
			var jsonData interface{}
			if err := json.Unmarshal(body, &jsonData); err == nil {
				prettyJSON, _ := json.MarshalIndent(jsonData, "", "  ")
				maxLen := len(prettyJSON)
				if maxLen > 1000 {
					maxLen = 1000
				}
				fmt.Printf("Response (first 1000 chars):\n%s\n", string(prettyJSON)[:maxLen])
			} else {
				maxLen := len(body)
				if maxLen > 500 {
					maxLen = 500
				}
				fmt.Printf("Response (raw, first 500 chars):\n%s\n", string(body)[:maxLen])
			}
		} else {
			maxLen := len(body)
			if maxLen > 200 {
				maxLen = 200
			}
			fmt.Printf("Error response: %s\n", string(body)[:maxLen])
		}
	}
}
