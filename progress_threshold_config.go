package main

import (
"os"
"strconv"
)

// getProgressChangeThreshold returns the threshold for progress change detection
// Default: 0.02 (2% change required to trigger update)
func getProgressChangeThreshold() float64 {
	threshold := os.Getenv("PROGRESS_CHANGE_THRESHOLD")
	if threshold == "" {
		return 0.02 // Default: 2% threshold
	}
	if val, err := strconv.ParseFloat(threshold, 64); err == nil {
		// Validate range - must be between 0 and 1
		if val >= 0 && val <= 1 {
			return val
		}
	}
	return 0.02 // Return default for invalid values
}
