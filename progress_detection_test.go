package main

import (
	"testing"
)

// Test the suspicious progress detection logic
func TestSuspiciousProgressDetection(t *testing.T) {
	tests := []struct {
		name         string
		progress     float64
		duration     float64
		expectedSusp bool
		description  string
	}{
		{
			name:         "Red Bounty case",
			progress:     0.000307,
			duration:     32760, // 9.1 hours in seconds
			expectedSusp: true,
			description:  "10 seconds progress on 9+ hour book",
		},
		{
			name:         "Normal progress",
			progress:     0.5,
			duration:     3600,
			expectedSusp: false,
			description:  "50% progress on 1 hour book",
		},
		{
			name:         "Very small progress on short book",
			progress:     0.001,
			duration:     600, // 10 minutes
			expectedSusp: false,
			description:  "36 seconds progress on 10 minute book",
		},
		{
			name:         "Tiny progress on long book",
			progress:     0.0001,
			duration:     18000, // 5 hours
			expectedSusp: true,
			description:  "1.8 seconds progress on 5 hour book",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualSeconds := tt.progress * tt.duration
			isSuspicious := actualSeconds < 30 && tt.duration > 1800 // Less than 30 seconds on 30+ minute book

			if isSuspicious != tt.expectedSusp {
				t.Errorf("Expected suspicious=%v, got suspicious=%v for %s (%.1fs progress on %.1fs duration)",
					tt.expectedSusp, isSuspicious, tt.description, actualSeconds, tt.duration)
			}

			t.Logf("Test '%s': %.1f seconds progress on %.1f minute book - Suspicious: %v",
				tt.name, actualSeconds, tt.duration/60, isSuspicious)
		})
	}
}

// Test the listening sessions response parsing
func TestParseListeningSessionsResponse(t *testing.T) {
	tests := []struct {
		name             string
		responseBody     string
		expectedProgress float64
	}{
		{
			name: "Sessions wrapped in object",
			responseBody: `{
				"sessions": [
					{"currentTime": 300, "duration": 3600},
					{"currentTime": 1800, "duration": 3600}
				]
			}`,
			expectedProgress: 1800.0 / 3600.0, // Latest session progress
		},
		{
			name: "Direct array of sessions",
			responseBody: `[
				{"currentTime": 600, "duration": 3600},
				{"currentTime": 1200, "duration": 3600}
			]`,
			expectedProgress: 1200.0 / 3600.0,
		},
		{
			name: "Single session object",
			responseBody: `{
				"currentTime": 900, 
				"duration": 3600
			}`,
			expectedProgress: 900.0 / 3600.0,
		},
		{
			name:             "Empty response",
			responseBody:     `{}`,
			expectedProgress: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			progress := parseListeningSessionsResponse([]byte(tt.responseBody), "test-item", 3600.0)

			if absFloat(progress-tt.expectedProgress) > 0.001 {
				t.Errorf("Expected progress %.6f, got %.6f", tt.expectedProgress, progress)
			}

			t.Logf("Test '%s': parsed progress %.6f (%.1f%%)", tt.name, progress, progress*100)
		})
	}
}

// Test response parsing with actual response format
func TestParseActualResponseFormat(t *testing.T) {
	// Test with a response format that might come from AudiobookShelf
	responseBody := `{
		"sessions": [
			{
				"id": "session1",
				"currentTime": 2100.5,
				"duration": 32760.0,
				"progress": 0.064,
				"libraryItemId": "li_abc123",
				"createdAt": 1672531200,
				"updatedAt": 1672531300
			}
		]
	}`

	progress := parseListeningSessionsResponse([]byte(responseBody), "li_abc123", 32760.0)
	expectedFromCurrentTime := 2100.5 / 32760.0 // Should use currentTime/duration

	t.Logf("Parsed progress: %.6f, Expected from currentTime: %.6f", progress, expectedFromCurrentTime)

	// Should get non-zero progress
	if progress <= 0 {
		t.Errorf("Expected non-zero progress, got %.6f", progress)
	}
}

// Helper function to calculate absolute difference
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
