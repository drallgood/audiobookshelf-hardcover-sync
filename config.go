package main

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// Cache for current user to avoid repeated API calls
var cachedCurrentUser string

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
	delayMs := os.Getenv("HARDCOVER_SYNC_DELAY_MS")
	if delayMs == "" {
		return 1000 * time.Millisecond // Default 1 second
	}
	if ms, err := strconv.Atoi(delayMs); err == nil {
		return time.Duration(ms) * time.Millisecond
	}
	return 1000 * time.Millisecond
}

func getMinimumProgressThreshold() float64 {
	threshold := os.Getenv("MINIMUM_PROGRESS_THRESHOLD")
	if threshold == "" {
		return 0.01 // Default: minimum progress threshold
	}
	if val, err := strconv.ParseFloat(threshold, 64); err == nil {
		// Validate range - must be between 0 and 1
		if val >= 0 && val <= 1 {
			return val
		}
	}
	return 0.01 // Return default for invalid values
}

// getAudiobookMatchMode returns the behavior when ASIN lookup fails
// Options: "continue" (default), "skip", "fail"
func getAudiobookMatchMode() string {
	mode := strings.ToLower(os.Getenv("AUDIOBOOK_MATCH_MODE"))
	switch mode {
	case "skip", "fail":
		return mode
	default:
		return "continue"
	}
}

// getSyncWantToRead returns whether to sync books with 0% progress as "Want to read"
// Default: true (sync unstarted books as "Want to Read")
func getSyncWantToRead() bool {
	val := strings.ToLower(os.Getenv("SYNC_WANT_TO_READ"))
	// Default to true unless explicitly disabled
	return val != "false" && val != "0" && val != "no"
}

// getSyncOwned returns whether to mark synced books as "owned" in Hardcover
// Default: true (mark synced books as owned)
func getSyncOwned() bool {
	val := strings.ToLower(os.Getenv("SYNC_OWNED"))
	// Default to true unless explicitly disabled
	return val != "false" && val != "0" && val != "no"
}

// getDryRun returns whether to run in dry run mode (no actual API calls for modifications)
// Default: false (perform actual API calls)
func getDryRun() bool {
	val := strings.ToLower(os.Getenv("DRY_RUN"))
	return val == "true" || val == "1" || val == "yes"
}
