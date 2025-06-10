package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// AudibleMetadata represents the metadata returned from Audible API
type AudibleMetadata struct {
	ASIN        string    `json:"asin"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle,omitempty"`
	Publisher   string    `json:"publisher,omitempty"`
	ReleaseDate time.Time `json:"release_date"`
	Authors     []string  `json:"authors,omitempty"`
	Narrators   []string  `json:"narrators,omitempty"`
	Duration    int       `json:"duration_seconds,omitempty"`
	Language    string    `json:"language,omitempty"`
	ImageURL    string    `json:"image_url,omitempty"`
}

// AudibleAPIResponse represents the structure of responses from Audible API
type AudibleAPIResponse struct {
	Product struct {
		ASIN     string `json:"asin"`
		Title    string `json:"title"`
		Subtitle string `json:"subtitle"`
		Authors  []struct {
			Name string `json:"name"`
		} `json:"authors"`
		Narrators []struct {
			Name string `json:"name"`
		} `json:"narrators"`
		Publisher struct {
			Name string `json:"name"`
		} `json:"publisher"`
		ReleaseDate string `json:"release_date"`
		Runtime     struct {
			LengthMs int `json:"length_mins"`
		} `json:"runtime"`
		ProductImages struct {
			Large string `json:"500"`
		} `json:"product_images"`
		Language struct {
			Name string `json:"name"`
		} `json:"language"`
	} `json:"product"`
}

// getAudibleMetadata fetches metadata for a book from Audible using ASIN
func getAudibleMetadata(asin string) (*AudibleMetadata, error) {
	if asin == "" {
		return nil, fmt.Errorf("ASIN is required")
	}

	// Validate ASIN format (typically 10 characters, alphanumeric)
	if !isValidASIN(asin) {
		return nil, fmt.Errorf("invalid ASIN format: %s", asin)
	}

	debugLog("Fetching Audible metadata for ASIN: %s", asin)

	// Use Audible's product API - this is a simplified version
	// In reality, you might need to use web scraping or unofficial APIs
	// since Audible doesn't have a public API
	url := fmt.Sprintf("https://api.audible.com/1.0/catalog/products/%s", asin)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Set headers that might be required for Audible API
	req.Header.Set("User-Agent", "AudiobookShelf-Hardcover-Sync/1.0")
	req.Header.Set("Accept", "application/json")

	// Add authentication if configured
	if token := getAudibleAPIToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: getAudibleAPITimeout()}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from Audible: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// If API fails, try fallback methods
		debugLog("Audible API returned status %d for ASIN %s, trying fallback", resp.StatusCode, asin)
		return getAudibleMetadataFallback(asin)
	}

	var apiResp AudibleAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Audible API response: %v", err)
	}

	return convertAudibleResponse(&apiResp), nil
}

// getAudibleMetadataFallback attempts to get metadata using web scraping or alternative methods
func getAudibleMetadataFallback(asin string) (*AudibleMetadata, error) {
	debugLog("Using fallback method for ASIN: %s", asin)

	// This is a placeholder for alternative methods like web scraping
	// In a real implementation, you might:
	// 1. Scrape Audible.com product pages
	// 2. Use third-party APIs
	// 3. Parse existing book metadata more intelligently
	
	// For now, return minimal metadata
	return &AudibleMetadata{
		ASIN: asin,
	}, fmt.Errorf("audible API fallback not implemented")
}

// convertAudibleResponse converts the API response to our internal metadata format
func convertAudibleResponse(resp *AudibleAPIResponse) *AudibleMetadata {
	metadata := &AudibleMetadata{
		ASIN:      resp.Product.ASIN,
		Title:     resp.Product.Title,
		Subtitle:  resp.Product.Subtitle,
		Publisher: resp.Product.Publisher.Name,
		Language:  resp.Product.Language.Name,
		ImageURL:  resp.Product.ProductImages.Large,
	}

	// Convert authors
	for _, author := range resp.Product.Authors {
		metadata.Authors = append(metadata.Authors, author.Name)
	}

	// Convert narrators
	for _, narrator := range resp.Product.Narrators {
		metadata.Narrators = append(metadata.Narrators, narrator.Name)
	}

	// Parse release date
	if resp.Product.ReleaseDate != "" {
		if t, err := parseAudibleDate(resp.Product.ReleaseDate); err == nil {
			metadata.ReleaseDate = t
		}
	}

	// Convert duration (minutes to seconds)
	if resp.Product.Runtime.LengthMs > 0 {
		metadata.Duration = resp.Product.Runtime.LengthMs * 60
	}

	return metadata
}

// parseAudibleDate parses various date formats that Audible might return
func parseAudibleDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("empty date string")
	}

	// Common Audible date formats
	formats := []string{
		"2006-01-02",           // YYYY-MM-DD
		"2006/01/02",           // YYYY/MM/DD
		"01-02-2006",           // MM-DD-YYYY
		"01/02/2006",           // MM/DD/YYYY
		"January 2, 2006",      // Month DD, YYYY
		"Jan 2, 2006",          // Mon DD, YYYY
		"2 January 2006",       // DD Month YYYY
		"2 Jan 2006",           // DD Mon YYYY
		"2006-01-02T15:04:05Z", // ISO 8601
		"2006-01-02 15:04:05",  // YYYY-MM-DD HH:MM:SS
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse date: %s", dateStr)
}

// isValidASIN validates that the ASIN follows the expected format
func isValidASIN(asin string) bool {
	// ASIN is typically 10 characters: letters and numbers
	// Examples: B01234567X, 1234567890
	// Must be uppercase letters and numbers only
	matched, _ := regexp.MatchString(`^[A-Z0-9]{10}$`, asin)
	return matched
}

// formatAudibleDate formats a time.Time to the standard release date format
func formatAudibleDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02") // YYYY-MM-DD format for Hardcover
}

// isDateMoreSpecific compares two date strings and returns true if the first is more specific
func isDateMoreSpecific(newDate, existingDate string) bool {
	if existingDate == "" {
		return newDate != ""
	}
	
	// Count components in each date
	newComponents := countDateComponents(newDate)
	existingComponents := countDateComponents(existingDate)
	
	return newComponents > existingComponents
}

// countDateComponents counts the number of date components (year, month, day)
func countDateComponents(dateStr string) int {
	if dateStr == "" {
		return 0
	}
	
	// Try to parse and determine specificity
	formats := []struct {
		format     string
		components int
	}{
		{"2006-01-02", 3},      // YYYY-MM-DD (full date)
		{"Jan 2, 2006", 3},     // Month DD, YYYY (full date)
		{"January 2, 2006", 3}, // Month DD, YYYY (full date)
		{"2 Jan 2006", 3},      // DD Mon YYYY (full date)
		{"2 January 2006", 3},  // DD Month YYYY (full date)
		{"2006-01", 2},         // YYYY-MM (year-month)
		{"Jan 2006", 2},        // Mon YYYY (year-month)
		{"January 2006", 2},    // Month YYYY (year-month)
		{"2006", 1},            // YYYY (year only)
	}
	
	for _, f := range formats {
		if _, err := time.Parse(f.format, dateStr); err == nil {
			return f.components
		}
	}
	
	// If we can't parse it, try to guess based on separators
	separators := strings.Count(dateStr, "-") + strings.Count(dateStr, "/") + strings.Count(dateStr, " ")
	if separators >= 2 {
		return 3 // Likely full date
	} else if separators == 1 {
		return 2 // Likely year-month
	}
	
	return 1 // Likely year only
}

// isAudibleImageBetter determines if an Audible image URL is better than the existing one
func isAudibleImageBetter(audibleURL, existingURL string) bool {
	if audibleURL == "" {
		return false
	}
	if existingURL == "" {
		return true
	}
	
	// Audible images are generally high quality, prefer them if:
	// 1. The existing URL is not from Audible
	// 2. The Audible URL indicates higher resolution
	
	audibleIsHighRes := strings.Contains(audibleURL, "500") || strings.Contains(audibleURL, "1000") || 
					   strings.Contains(audibleURL, "large") || strings.Contains(audibleURL, "._SL")
	existingIsAudible := strings.Contains(existingURL, "audible.com") || strings.Contains(existingURL, "audible-")
	
	// If existing is not from Audible and new one is high-res Audible, prefer Audible
	if !existingIsAudible && audibleIsHighRes {
		return true
	}
	
	// Otherwise, keep existing to avoid unnecessary changes
	return false
}
