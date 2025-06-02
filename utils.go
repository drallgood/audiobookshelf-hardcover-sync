package main

import (
	"log"
	"strconv"
	"strings"
)

var debugMode = false

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

// Helper to convert string to int safely
func toInt(s string) int {
	if i, err := strconv.Atoi(s); err == nil {
		return i
	}
	return 0
}

// Helper to normalize book titles for better matching
func normalizeTitle(title string) string {
	// Remove common audiobook suffixes
	title = strings.ReplaceAll(title, " (Unabridged)", "")
	title = strings.ReplaceAll(title, " [Unabridged]", "")
	title = strings.ReplaceAll(title, " - Unabridged", "")
	
	// Remove trailing punctuation
	title = strings.TrimRight(title, ".,!?;:")
	
	// Convert to lowercase for comparison
	return strings.ToLower(strings.TrimSpace(title))
}
