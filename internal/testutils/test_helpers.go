package testutils

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// StateVersion is the current version of the sync state file format
const StateVersion = "1.0"

// SyncState represents the state of the sync process
// It's used to implement incremental sync functionality
type SyncState struct {
	Version            string `json:"version"`
	LastSyncTimestamp  int64  `json:"last_sync_timestamp,omitempty"`
	LastFullSync       int64  `json:"last_full_sync,omitempty"`
	LastSyncItemCount  int    `json:"last_sync_item_count,omitempty"`
	LastSyncBookCount  int    `json:"last_sync_book_count,omitempty"`
	LastSyncError      string `json:"last_sync_error,omitempty"`
	LastSyncSuccess    bool   `json:"last_sync_success"`
	LastSyncDurationMs int64  `json:"last_sync_duration_ms,omitempty"`
}

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

// debugMode indicates if debug mode is enabled
var debugMode = true

// debugLog logs a debug message if debug mode is enabled
func debugLog(format string, v ...interface{}) {
	if debugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
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

// loadSyncState loads the sync state from the state file
func loadSyncState() (*SyncState, error) {
	stateFile := getStateFilePath()
	if stateFile == "" {
		return &SyncState{Version: StateVersion}, nil
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &SyncState{Version: StateVersion}, nil
		}
		return nil, fmt.Errorf("error reading state file: %v", err)
	}

	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("error parsing state file: %v", err)
	}

	// Ensure we have a version
	if state.Version == "" {
		state.Version = StateVersion
	}

	return &state, nil
}

// saveSyncState saves the sync state to the state file
func saveSyncState(state *SyncState) error {
	if state == nil {
		return fmt.Errorf("cannot save nil state")
	}

	// Ensure we have a version
	if state.Version == "" {
		state.Version = StateVersion
	}

	stateFile := getStateFilePath()
	if stateFile == "" {
		return fmt.Errorf("no state file path configured")
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(stateFile), 0755); err != nil {
		return fmt.Errorf("error creating state directory: %v", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling state: %v", err)
	}

	tempFile := stateFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0600); err != nil {
		return fmt.Errorf("error writing state file: %v", err)
	}

	// Rename the temp file to the final name (atomic operation)
	if err := os.Rename(tempFile, stateFile); err != nil {
		return fmt.Errorf("error renaming state file: %v", err)
	}

	return nil
}

// getStateFilePath returns the path to the state file
// It checks the SYNC_STATE_FILE environment variable first, then falls back to a default location
func getStateFilePath() string {
	if path := os.Getenv("SYNC_STATE_FILE"); path != "" {
		return path
	}

	// Default to a file in the user's config directory
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}

	return filepath.Join(configDir, "audiobookshelf-hardcover-sync", "sync_state.json")
}

// getTimestampThreshold returns a timestamp threshold for incremental sync
// It returns the current time minus the given duration in milliseconds
func getTimestampThreshold(duration time.Duration) int64 {
	return time.Now().Add(-duration).UnixMilli()
}

// getIncrementalSyncMode determines if we should do a full or incremental sync
func getIncrementalSyncMode() bool {
	envVal := os.Getenv("INCREMENTAL_SYNC_MODE")
	if envVal == "" {
		// Default to true if not set
		return true
	}
	// Parse the environment variable as a boolean
	result, _ := strconv.ParseBool(envVal)
	return result
}

// convertMismatchToEditionInput converts a BookMismatch to an EditionCreatorInput
func convertMismatchToEditionInput(mismatch BookMismatch) EditionCreatorInput {
	authors := []string{}
	if mismatch.Author != "" {
		authors = append(authors, mismatch.Author)
	}
	
	// Safely handle nil CoverURL
	imageURL := ""
	if mismatch.CoverURL != nil {
		imageURL = *mismatch.CoverURL
	}
	
	return EditionCreatorInput{
		Title:     mismatch.Title,
		Authors:   authors,
		ISBN10:    mismatch.ISBN,
		ASIN:      mismatch.ASIN,
		ImageURL:  imageURL,
	}
}

// shouldPerformFullSync determines if a full sync should be performed
// based on the current state and configuration
func shouldPerformFullSync(state *SyncState, forceFullSync bool) bool {
	now := time.Now()

	// Force full sync if explicitly requested
	if forceFullSync {
		debugLog("Full sync forced by parameter")
		return true
	}

	// Force full sync if we've never done one
	if state == nil || state.LastFullSync == 0 {
		debugLog("No previous full sync found, performing full sync")
		return true
	}

	// Check if last sync was successful
	if !state.LastSyncSuccess {
		debugLog("Last sync was not successful, performing full sync")
		return true
	}

	// Check FORCE_FULL_SYNC environment variable
	if os.Getenv("FORCE_FULL_SYNC") == "true" {
		debugLog("FORCE_FULL_SYNC environment variable set, performing full sync")
		return true
	}

	// Check if it's been more than 7 days since last full sync
	lastFullSync := time.Unix(state.LastFullSync/1000, 0)
	daysSinceFullSync := now.Sub(lastFullSync).Hours() / 24

	if daysSinceFullSync >= 7 {
		debugLog("Last full sync was %.1f days ago, performing full sync", daysSinceFullSync)
		return true
	}

	debugLog("Using incremental sync (last full sync: %.1f days ago)", daysSinceFullSync)
	return false
}

// DefaultStateFile is the default path to the state file
const DefaultStateFile = "./sync_state.json"

// SearchAPIResponse represents a response from a search API
// This is a simplified version for testing purposes
// SearchAPIResponse represents the response from the search API
type SearchAPIResponse struct {
	Data struct {
		Search struct {
			IDs   []json.Number `json:"ids"`
			Error *string      `json:"error"`
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
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	BookCount      int    `json:"book_count,omitempty"`
	Website        string `json:"website,omitempty"`
	EditionsCount  int    `json:"editions_count,omitempty"`
	IsCanonical    bool   `json:"is_canonical,omitempty"`
}

// BookMismatch represents a book that couldn't be matched in Hardcover
type BookMismatch struct {
	Title           string  `json:"title"`
	Author          string  `json:"author"`
	ISBN            string  `json:"isbn,omitempty"`
	ASIN            string  `json:"asin,omitempty"`
	Reason          string  `json:"reason"`
	Timestamp       int64   `json:"timestamp"`
	Attempts        int     `json:"attempts"`
	LastTried       int64   `json:"last_tried,omitempty"`
	Metadata        string  `json:"metadata,omitempty"`
	EditionID       *int    `json:"edition_id,omitempty"`
	CanonicalID     *int    `json:"canonical_id,omitempty"`
	CoverURL        *string `json:"cover_url,omitempty"`
	Duration        float64 `json:"duration,omitempty"`
	DurationSeconds int     `json:"duration_seconds,omitempty"`
}

// MediaMetadata represents metadata for media files
type MediaMetadata struct {
	Title       string `json:"title"`
	Author      string `json:"author"`
	Description string `json:"description,omitempty"`
	Duration    int    `json:"duration,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Bitrate     int    `json:"bitrate,omitempty"`
	Format      string `json:"format,omitempty"`
	SampleRate  int    `json:"sample_rate,omitempty"`
	Channels    int    `json:"channels,omitempty"`
}

// addBookMismatchWithMetadata adds a book mismatch with additional metadata
func addBookMismatchWithMetadata(book *Audiobook, reason string, metadata *MediaMetadata) {
	// This is a stub implementation for testing
	// In a real implementation, this would add the book to a list of mismatches
}

// bookMismatches is a slice of BookMismatch objects
var bookMismatches []BookMismatch

// clearMismatches clears the list of book mismatches
func clearMismatches() {
	bookMismatches = []BookMismatch{}
}

// saveMismatchesJSONFile saves the list of book mismatches to a JSON file
func saveMismatchesJSONFile(filename string) error {
	// This is a stub implementation for testing
	// In a real implementation, this would save the mismatches to a file
	return nil
}

// getAudiobookShelfURL returns the base URL for the Audiobookshelf server
func getAudiobookShelfURL() string {
	// This is a stub implementation for testing
	return "https://example.com/audiobookshelf"
}

// exportMismatchesJSON exports the list of book mismatches as JSON
func exportMismatchesJSON() ([]byte, error) {
	// This is a stub implementation for testing
	return json.Marshal(bookMismatches)
}

// sanitizeFilename sanitizes a string to be used as a filename
func sanitizeFilename(name string) string {
	// Remove invalid characters
	invalidChars := regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
	sanitized := invalidChars.ReplaceAllString(name, "_")

	// Limit length
	if len(sanitized) > 100 {
		sanitized = sanitized[:100]
	}

	return sanitized
}

// createEditionReadyMismatchFiles creates files for editions that are ready to be created
func createEditionReadyMismatchFiles() error {
	// This is a stub implementation for testing
	return nil
}

// debugAudiobookShelfAPI enables or disables debug mode for the Audiobookshelf API
func debugAudiobookShelfAPI(enable bool) {
	debugMode = enable
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
func getHardcoverToken() string {
	// This is a stub implementation for testing
	return "test-hardcover-token"
}

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
func createEditionCommand() error {
	// This is a stub implementation for testing
	return nil
}

// createEditionWithPrepopulation creates a new edition with prepopulated data
func createEditionWithPrepopulation() error {
	// This is a stub implementation for testing
	return nil
}

// createEditionFromJSON creates a new edition from a JSON file
func createEditionFromJSON(filename string) error {
	// This is a stub implementation for testing
	return nil
}

// generatePrepopulatedTemplate generates a prepopulated template for a new edition
func generatePrepopulatedTemplate() error {
	// This is a stub implementation for testing
	return nil
}

// enhanceExistingTemplate enhances an existing template with additional data
func enhanceExistingTemplate() error {
	// This is a stub implementation for testing
	return nil
}

// lookupAuthorIDCommand looks up an author ID by name
func lookupAuthorIDCommand(name string) error {
	// This is a stub implementation for testing
	return nil
}

// lookupNarratorIDCommand looks up a narrator ID by name
func lookupNarratorIDCommand(name string) error {
	// This is a stub implementation for testing
	return nil
}

// lookupPublisherIDCommand looks up a publisher ID by name
func lookupPublisherIDCommand(name string) error {
	// This is a stub implementation for testing
	return nil
}

// verifyIDCommand verifies an ID by type and value
func verifyIDCommand(idType, id string) error {
	// This is a stub implementation for testing
	return nil
}

// bulkLookupAuthorsCommand performs a bulk lookup of authors
func bulkLookupAuthorsCommand(filename string) error {
	// This is a stub implementation for testing
	return nil
}

// bulkLookupNarratorsCommand performs a bulk lookup of narrators
func bulkLookupNarratorsCommand(filename string) error {
	// This is a stub implementation for testing
	return nil
}

// bulkLookupPublishersCommand performs a bulk lookup of publishers
func bulkLookupPublishersCommand(filename string) error {
	// This is a stub implementation for testing
	return nil
}

// uploadImageCommand handles the upload of an image
func uploadImageCommand(imagePath string) error {
	// This is a stub implementation for testing
	return nil
}

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
