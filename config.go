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
		return 0.0 // Default: sync all books with any progress > 0
	}
	if val, err := strconv.ParseFloat(threshold, 64); err == nil {
		return val
	}
	return 0.0
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
