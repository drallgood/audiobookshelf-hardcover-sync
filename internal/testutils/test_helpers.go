package testutils

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// formatDuration formats a duration in hours to a human-readable string (e.g., "1h 30m 00s")
func formatDuration(hours float64) string {
	if hours <= 0 {
		return "0h 0m 0s"
	}

	// Round to nearest second to avoid floating point precision issues
	totalSeconds := int(hours*3600 + 0.5)
	h := totalSeconds / 3600
	m := (totalSeconds % 3600) / 60
	s := totalSeconds % 60

	// Use different format strings based on the number of hours
	// to match the expected output in tests
	if h < 10 {
		return fmt.Sprintf("%dh %02dm %02ds", h, m, s)
	}
	return fmt.Sprintf("%02dh %02dm %02ds", h, m, s)
}

// formatReleaseDate formats a date string to a consistent format
// It handles various input formats and falls back to the publishedYear if needed
func formatReleaseDate(publishedDate, publishedYear string) string {
	// If no date is provided, use the year if available
	if publishedDate == "" {
		if publishedYear != "" {
			return publishedYear
		}
		return ""
	}

	// Try parsing different date formats
	formats := []string{
		"2006-01-02",      // YYYY-MM-DD
		"2006/01/02",      // YYYY/MM/DD
		"January 2, 2006", // Full month name
		"Jan 2, 2006",     // Abbreviated month
		"2 January 2006",  // Day first
		"2006-01",         // Year-month only
		"January 2006",    // Month year only
	}

	for _, layout := range formats {
		t, err := time.Parse(layout, publishedDate)
		if err != nil {
			continue
		}

		// Determine the output format based on the input format
		hasDay := strings.Contains(layout, "2") &&
			(strings.Contains(layout, "02") ||
				strings.Contains(layout, "2,") ||
				strings.HasPrefix(layout, "2 ") ||
				strings.Contains(layout, " 2 "))

		if hasDay {
			// Full date with day
			return t.Format("Jan 2, 2006")
		} else if strings.Contains(layout, "2006-01") || strings.Contains(layout, "January 2006") {
			// Month-year format
			return t.Format("Jan 2006")
		}
	}

	// If we get here and have a publishedYear, use that
	if publishedYear != "" {
		return publishedYear
	}

	// If it's just a 4-digit year, return as-is
	if matched, _ := regexp.MatchString(`^\d{4}$`, publishedDate); matched {
		return publishedDate
	}

	// As a last resort, return the original string
	return publishedDate
}

// isHardcoverAssetURL checks if a given URL is a Hardcover asset URL
// Returns (isAsset, skipUpload, error)
func isHardcoverAssetURL(imageURL string) (bool, bool, error) {
	if imageURL == "" {
		return false, false, nil
	}

	// Parse the URL to check the hostname
	u, err := url.Parse(imageURL)
	if err != nil {
		return false, false, fmt.Errorf("error parsing URL: %v", err)
	}

	// Check if the hostname is exactly assets.hardcover.app
	// Other subdomains like cdn.assets.hardcover.app should not be considered as asset URLs
	if u.Hostname() == "assets.hardcover.app" {
		return true, true, nil
	}

	return false, false, nil
}

// SearchAPIResponse represents a response from a search API
// This is a simplified version for testing purposes
// SearchAPIResponse represents the response from the search API
type SearchAPIResponse struct {
	Data struct {
		Search struct {
			IDs   []json.Number `json:"ids"`
			Error *string       `json:"error"`
		} `json:"search"`
	} `json:"data"`
}

// isLocalAudiobookShelfURL checks if a URL points to a local Audiobookshelf instance
func isLocalAudiobookShelfURL(urlStr string) bool {
	if urlStr == "" {
		return false
	}

	// Check for common localhost/loopback addresses, common local network prefixes, and .local domains
	localPatterns := []string{
		`^https?://localhost(?:\:\d+)?/`,
		`^https?://127\.\d+\.\d+\.\d+(?:\:\d+)?/`,
		`^https?://192\.168\.\d+\.\d+(?:\:\d+)?/`,
		`^https?://10\.\d+\.\d+\.\d+(?:\:\d+)?/`,
		`^https?://172\.(?:1[6-9]|2[0-9]|3[0-1])\.\d+\.\d+(?:\:\d+)?/`,
		`^https?://[^/]+\.local(?:\:\d+)?/`,
	}

	for _, pattern := range localPatterns {
		matched, _ := regexp.MatchString(pattern, urlStr)
		if matched {
			return true
		}
	}

	return false
}

// PublisherSearchResult represents a publisher search result
type PublisherSearchResult struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	BookCount     int    `json:"book_count,omitempty"`
	Website       string `json:"website,omitempty"`
	EditionsCount int    `json:"editions_count,omitempty"`
	IsCanonical   bool   `json:"is_canonical,omitempty"`
}

// fetchAudiobookShelfStats fetches statistics from the Audiobookshelf server
func fetchAudiobookShelfStats() (map[string]interface{}, error) {
	// This is a stub implementation for testing
	return map[string]interface{}{
		"libraries": 1,
		"books":     10,
		"authors":   5,
	}, nil
}

// fetchLibraryItems fetches items from a library
func fetchLibraryItems(libraryID string) ([]interface{}, error) {
	// This is a stub implementation for testing
	return []interface{}{}, nil
}

// fetchLibraries fetches all libraries from the Audiobookshelf server
func fetchLibraries() ([]interface{}, error) {
	// This is a stub implementation for testing
	return []interface{}{}, nil
}

// syncToHardcover syncs items to Hardcover
func syncToHardcover(items []interface{}) error {
	if len(items) == 0 {
		return nil
	}

	// Check for required HARDCOVER_TOKEN
	token := os.Getenv("HARDCOVER_TOKEN")
	if token == "" {
		return fmt.Errorf("HARDCOVER_TOKEN environment variable is not set")
	}

	// Check if the first item is an Audiobook
	if book, ok := items[0].(Audiobook); ok {
		// For testing purposes, return an error if progress is less than 1.0
		if book.Progress < 1.0 {
			return fmt.Errorf("book not finished (progress: %.2f)", book.Progress)
		}
	}

	// This is a stub implementation for testing
	return nil
}

// runSync runs the sync process
func runSync() error {
	// This is a stub implementation for testing
	return nil
}

// getMinimumProgressThreshold returns the minimum progress threshold for syncing
func getMinimumProgressThreshold() float64 {
	envVal := os.Getenv("MINIMUM_PROGRESS_THRESHOLD")
	if envVal == "" {
		// Default to 0.01 if not set
		return 0.01
	}

	// Parse the environment variable as a float64
	threshold, err := strconv.ParseFloat(envVal, 64)
	if err != nil {
		// Return default on parse error
		return 0.01
	}

	// Return default if threshold is outside valid range [0.0, 1.0]
	if threshold < 0.0 || threshold > 1.0 {
		return 0.01
	}

	return threshold
}

// fetchUserProgress fetches the user's progress from Audiobookshelf
func fetchUserProgress() (map[string]interface{}, error) {
	// This is a stub implementation for testing
	return map[string]interface{}{}, nil
}

// getHardcoverToken gets the Hardcover API token
// UNUSED: Kept for future testing
// func getHardcoverToken() string {
// 	return os.Getenv("HARDCOVER_API_TOKEN")
// }

// uploadImageToHardcover uploads an image to Hardcover
func uploadImageToHardcover(imageURL string, bookID int) (int, error) {
	// This is a stub implementation for testing
	return 999999, nil
}

// executeImageMutation executes an image mutation in Hardcover
func executeImageMutation(payload map[string]interface{}) (int, error) {
	// This is a stub implementation for testing
	// In dry run mode, return a fake ID
	if os.Getenv("DRY_RUN") == "true" {
		return 888888, nil
	}
	return 0, fmt.Errorf("not implemented")
}

// executeEditionMutation executes an edition mutation in Hardcover
func executeEditionMutation(payload map[string]interface{}) (int, error) {
	// This is a stub implementation for testing
	// In dry run mode, return a fake ID
	if os.Getenv("DRY_RUN") == "true" {
		return 777777, nil
	}
	return 0, fmt.Errorf("not implemented")
}

// createEditionCommand creates a new edition in Hardcover
// UNUSED: Kept for future testing
// func createEditionCommand() error {
// 	// Implementation would go here
// 	return nil
// }

// createEditionWithPrepopulation creates a new edition with prepopulated data
// UNUSED: Kept for future testing
// func createEditionWithPrepopulation() error {
// 	// Implementation would go here
// 	return nil
// }

// createEditionFromJSON creates a new edition from a JSON file
// UNUSED: Kept for future testing
// func createEditionFromJSON(filename string) error {
// 	// Implementation would go here
// 	return nil
// }

// generatePrepopulatedTemplate generates a prepopulated template for a new edition
// UNUSED: Kept for future testing
// func generatePrepopulatedTemplate() error {
// 	// Implementation would go here
// 	return nil
// }

// enhanceExistingTemplate enhances an existing template with additional data
// UNUSED: Kept for future testing
// func enhanceExistingTemplate() error {
// 	// Implementation would go here
// 	return nil
// }

// lookupAuthorIDCommand looks up an author ID by name
// UNUSED: Kept for future testing
// func lookupAuthorIDCommand(name string) error {
// 	// Implementation would go here
// 	return nil
// }

// lookupNarratorIDCommand looks up a narrator ID by name
// UNUSED: Kept for future testing
// func lookupNarratorIDCommand(name string) error {
// 	// Implementation would go here
// 	return nil
// }

// lookupPublisherIDCommand looks up a publisher ID by name
// UNUSED: Kept for future testing
// func lookupPublisherIDCommand(name string) error {
// 	// Implementation would go here
// 	return nil
// }

// verifyIDCommand verifies an ID by type and value
// UNUSED: Kept for future testing
// func verifyIDCommand(idType, id string) error {
// 	// Implementation would go here
// 	return nil
// }

// bulkLookupAuthorsCommand performs a bulk lookup of authors
// UNUSED: Kept for future testing
// func bulkLookupAuthorsCommand(filename string) error {
// 	// Implementation would go here
// 	return nil
// }

// bulkLookupNarratorsCommand performs a bulk lookup of narrators
// UNUSED: Kept for future testing
// func bulkLookupNarratorsCommand(filename string) error {
// 	// Implementation would go here
// 	return nil
// }

// bulkLookupPublishersCommand performs a bulk lookup of publishers
// UNUSED: Kept for future testing
// func bulkLookupPublishersCommand(filename string) error {
// 	// Implementation would go here
// 	return nil
// }

// uploadImageCommand handles the upload of an image
// UNUSED: Kept for future testing
// func uploadImageCommand(imagePath string) error {
// 	// Implementation would go here
// 	return nil
// }

// getSyncWantToRead gets the sync want to read setting
// Returns whether to sync books with 0% progress as "Want to read"
// Default: true (sync unstarted books as "Want to Read")
func getSyncWantToRead() bool {
	val := strings.ToLower(os.Getenv("SYNC_WANT_TO_READ"))
	// Default to true unless explicitly disabled
	return val != "false" && val != "0" && val != "no"
}

// getSyncOwned returns whether to mark synced books as "owned" in Hardcover
// Default: true (mark synced books as owned)
// This matches the legacy implementation in internal/legacy/config.go
func getSyncOwned() bool {
	val := strings.ToLower(os.Getenv("SYNC_OWNED"))
	// Default to true unless explicitly disabled
	return val != "false" && val != "0" && val != "no"
}

// checkExistingUserBook checks if a user book exists in the database
// This is a stub implementation that always returns false, nil
func checkExistingUserBook(userID, bookID string) (bool, error) {
	// This is a stub implementation that doesn't make any HTTP requests
	// In a real implementation, this would check if the user has the book in their library
	return false, nil
}
