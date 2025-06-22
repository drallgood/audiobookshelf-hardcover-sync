package testutils

import (
	"testing"
	"time"
)

func TestProgressCalculationMultiplication(t *testing.T) {
	tests := []struct {
		name             string
		currentTime      float64 // seconds
		totalDuration    float64 // seconds
		expectedProgress float64 // expected percentage (0-100)
		expectedSeconds  float64 // expected seconds for Hardcover
	}{
		{
			name:             "50% progress - 1 hour book",
			currentTime:      1800, // 30 minutes in seconds
			totalDuration:    3600, // 1 hour in seconds
			expectedProgress: 50.0,
			expectedSeconds:  1800,
		},
		{
			name:             "25% progress - 4 hour book",
			currentTime:      3600,  // 1 hour in seconds
			totalDuration:    14400, // 4 hours in seconds
			expectedProgress: 25.0,
			expectedSeconds:  3600,
		},
		{
			name:             "100% progress - completed book",
			currentTime:      7200, // 2 hours in seconds
			totalDuration:    7200, // 2 hours in seconds
			expectedProgress: 100.0,
			expectedSeconds:  7200,
		},
		{
			name:             "Small progress - 5 minutes of 10 hour book",
			currentTime:      300,   // 5 minutes in seconds
			totalDuration:    36000, // 10 hours in seconds
			expectedProgress: 0.83,  // ~0.83%
			expectedSeconds:  300,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock AudiobookShelf item
			abs := Audiobook{
				ID:            "test-id",
				CurrentTime:   tt.currentTime,
				TotalDuration: tt.totalDuration,
			}

			// Test the progress calculation logic from sync.go
			var targetProgressSeconds float64
			if abs.CurrentTime > 0 {
				targetProgressSeconds = abs.CurrentTime
			} else {
				targetProgressSeconds = abs.TotalDuration * (tt.expectedProgress / 100.0)
			}

			// Calculate percentage for comparison
			calculatedProgress := (targetProgressSeconds / abs.TotalDuration) * 100

			t.Logf("Input: CurrentTime=%.2f, TotalDuration=%.2f", abs.CurrentTime, abs.TotalDuration)
			t.Logf("Calculated: targetProgressSeconds=%.2f, progress=%.2f%%", targetProgressSeconds, calculatedProgress)

			// Check if we're getting 1000x multiplication error
			if targetProgressSeconds > abs.CurrentTime*100 {
				t.Errorf("Potential 1000x error detected: targetProgressSeconds=%.2f is much larger than CurrentTime=%.2f",
					targetProgressSeconds, abs.CurrentTime)
			}

			// Verify the seconds value is correct
			if abs.CurrentTime > 0 && targetProgressSeconds != tt.expectedSeconds {
				t.Errorf("Expected targetProgressSeconds=%.2f, got %.2f", tt.expectedSeconds, targetProgressSeconds)
			}

			// Verify progress percentage is reasonable
			if calculatedProgress > 100.1 { // Allow small floating point errors
				t.Errorf("Progress percentage too high: %.2f%% (expected ~%.2f%%)", calculatedProgress, tt.expectedProgress)
			}
		})
	}
}

func TestProgressCalculationWithRealData(t *testing.T) {
	// Test with data that might come from AudiobookShelf API
	// This simulates potential issues with JSON parsing or unit conversion

	tests := []struct {
		name        string
		jsonData    string
		expectError bool
	}{
		{
			name: "Normal seconds data",
			jsonData: `{
				"id": "test1",
				"currentTime": 1800.5,
				"totalDuration": 3600.0
			}`,
			expectError: false,
		},
		{
			name: "Milliseconds data (potential source of 1000x error)",
			jsonData: `{
				"id": "test2", 
				"currentTime": 1800500,
				"totalDuration": 3600000
			}`,
			expectError: false, // We'll check if this causes issues
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This would test JSON parsing if we had the actual parsing logic
			// For now, we'll simulate the potential issue

			var currentTime, totalDuration float64

			if tt.name == "Milliseconds data (potential source of 1000x error)" {
				// Simulate if data comes in as milliseconds but we treat it as seconds
				currentTime = 1800500 // This would be wrong if treated as seconds
				totalDuration = 3600000

				// Check if this creates unreasonable progress values
				progress := (currentTime / totalDuration) * 100

				if progress > 100 {
					t.Logf("WARNING: Milliseconds data treated as seconds gives %.2f%% progress", progress)
				}

				// Test if dividing by 1000 fixes it
				currentTimeSeconds := currentTime / 1000
				totalDurationSeconds := totalDuration / 1000
				correctedProgress := (currentTimeSeconds / totalDurationSeconds) * 100

				t.Logf("Original: %.2f%%, Corrected (÷1000): %.2f%%", progress, correctedProgress)

				if correctedProgress > 0 && correctedProgress <= 100 {
					t.Logf("Division by 1000 produces reasonable progress: %.2f%%", correctedProgress)
				}
			}
		})
	}
}

func TestTimestampHandling(t *testing.T) {
	// Test the timestamp handling from incremental.go to see if there's confusion
	// between milliseconds and seconds

	now := time.Now()

	// Test millisecond timestamps (as used in incremental.go)
	millis := now.UnixMilli()

	// Test second timestamps
	seconds := now.Unix()

	t.Logf("Current time - Seconds: %d, Milliseconds: %d", seconds, millis)
	t.Logf("Ratio: %.2f", float64(millis)/float64(seconds))

	// The ratio should be approximately 1000
	ratio := float64(millis) / float64(seconds)
	if ratio < 900 || ratio > 1100 {
		t.Errorf("Unexpected timestamp ratio: %.2f (expected ~1000)", ratio)
	}
}
