package main

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
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

// Helper to format duration from decimal hours to "Xh Ym Zs" format
func formatDuration(hours float64) string {
	if hours <= 0 {
		return "0h 0m 0s"
	}

	// Round to nearest second to avoid floating point precision issues
	totalSeconds := int(hours*3600 + 0.5)
	h := totalSeconds / 3600
	m := (totalSeconds % 3600) / 60
	s := totalSeconds % 60

	return fmt.Sprintf("%dh %02dm %02ds", h, m, s)
}

// Helper to format release date for better readability
func formatReleaseDate(publishedDate, publishedYear string) string {
	// Prefer publishedDate over publishedYear
	dateStr := publishedDate
	if dateStr == "" {
		dateStr = publishedYear
	}

	if dateStr == "" {
		return ""
	}

	// Try to parse common date formats and reformat them
	formats := []string{
		"2006-01-02",      // YYYY-MM-DD
		"2006/01/02",      // YYYY/MM/DD
		"01/02/2006",      // MM/DD/YYYY
		"02/01/2006",      // DD/MM/YYYY
		"January 2, 2006", // Month DD, YYYY
		"Jan 2, 2006",     // Mon DD, YYYY
		"2 January 2006",  // DD Month YYYY
		"2 Jan 2006",      // DD Mon YYYY
		"2006-01",         // YYYY-MM
		"01/2006",         // MM/YYYY
		"January 2006",    // Month YYYY
		"Jan 2006",        // Mon YYYY
	}

	// Try parsing as a full date first
	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			// Check if this is a full date format (includes day) vs partial (month/year only)
			hasDay := strings.Contains(format, "-02") || strings.Contains(format, "/02") ||
				strings.Contains(format, " 2,") || format == "2 January 2006" || format == "2 Jan 2006"

			if hasDay {
				return t.Format("Jan 2, 2006")
			} else {
				return t.Format("Jan 2006")
			}
		}
	}

	// If it's just a 4-digit year, return as-is
	if matched, _ := regexp.MatchString(`^\d{4}$`, dateStr); matched {
		return dateStr
	}

	// If no format matches, return the original string
	return dateStr
}


