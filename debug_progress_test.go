package main

import (
	"fmt"
	"math"
	"testing"
)

func TestActualProgressCalculationDebugging(t *testing.T) {
	// This test reproduces the exact logic from sync.go lines 302-305
	// to understand where the 1000x multiplication might be happening
	
	tests := []struct {
		name          string
		progress      float64
		currentTime   float64 // seconds
		totalDuration float64 // seconds
		expectedSecs  int
	}{
		{
			name:          "Normal case - 50% progress with currentTime",
			progress:      0.5,
			currentTime:   1800,  // 30 minutes
			totalDuration: 3600,  // 1 hour
			expectedSecs:  1800,
		},
		{
			name:          "Normal case - 25% progress without currentTime",
			progress:      0.25,
			currentTime:   0,     // No currentTime available
			totalDuration: 14400, // 4 hours
			expectedSecs:  3600,  // 25% of 4 hours = 1 hour
		},
		{
			name:          "Edge case - very small progress",
			progress:      0.001, // 0.1%
			currentTime:   0,
			totalDuration: 36000, // 10 hours
			expectedSecs:  36,    // 0.1% of 10 hours = 36 seconds
		},
		{
			name:          "Potential bug case - currentTime in milliseconds",
			progress:      0.5,
			currentTime:   1800000, // 30 minutes in MILLISECONDS (potential bug source)
			totalDuration: 3600,    // 1 hour in seconds
			expectedSecs:  1800000, // Would be wrong if currentTime is in milliseconds
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reproduce the exact logic from sync.go
			var targetProgressSeconds int
			if tt.progress > 0 {
				if tt.currentTime > 0 {
					targetProgressSeconds = int(math.Round(tt.currentTime))
				} else if tt.totalDuration > 0 && tt.progress > 0 {
					targetProgressSeconds = int(math.Round(tt.progress * tt.totalDuration))
				} else {
					// Fallback: use progress percentage * reasonable audiobook duration (10 hours)
					fallbackDuration := 36000.0 // 10 hours in seconds
					targetProgressSeconds = int(math.Round(tt.progress * fallbackDuration))
				}
				// Ensure we have at least 1 second of progress
				if targetProgressSeconds < 1 {
					targetProgressSeconds = 1
				}
			}

			t.Logf("Test: %s", tt.name)
			t.Logf("Input: progress=%.6f, currentTime=%.2f, totalDuration=%.2f", 
				tt.progress, tt.currentTime, tt.totalDuration)
			t.Logf("Output: targetProgressSeconds=%d", targetProgressSeconds)
			
			// Check for potential 1000x error
			if tt.name == "Potential bug case - currentTime in milliseconds" {
				if targetProgressSeconds > 100000 { // Much larger than expected
					t.Logf("FOUND 1000x ERROR: targetProgressSeconds=%d (expected ~%d)", 
						targetProgressSeconds, tt.expectedSecs/1000)
					t.Logf("This suggests currentTime is coming in as milliseconds but treated as seconds")
					
					// Test the fix
					correctedSeconds := int(math.Round(tt.currentTime / 1000))
					t.Logf("Corrected (÷1000): %d seconds", correctedSeconds)
				}
			}
			
			// For normal cases, verify they're reasonable
			if tt.name != "Potential bug case - currentTime in milliseconds" {
				if targetProgressSeconds != tt.expectedSecs {
					t.Errorf("Expected %d seconds, got %d", tt.expectedSecs, targetProgressSeconds)
				}
			}
		})
	}
}

func TestProgressToSecondsCalculation(t *testing.T) {
	// Test the specific calculation used in sync.go when currentTime is 0
	// This tests: targetProgressSeconds = int(math.Round(a.Progress * a.TotalDuration))
	
	tests := []struct {
		progress      float64
		totalDuration float64
		expectedSecs  int
	}{
		{0.5, 3600, 1800},     // 50% of 1 hour = 30 minutes
		{0.25, 14400, 3600},   // 25% of 4 hours = 1 hour  
		{0.001, 36000, 36},    // 0.1% of 10 hours = 36 seconds
		{1.0, 7200, 7200},     // 100% of 2 hours = 2 hours
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("Case_%d", i+1), func(t *testing.T) {
			result := int(math.Round(tt.progress * tt.totalDuration))
			
			t.Logf("progress=%.6f * totalDuration=%.2f = %.2f seconds → %d seconds", 
				tt.progress, tt.totalDuration, tt.progress * tt.totalDuration, result)
			
			if result != tt.expectedSecs {
				t.Errorf("Expected %d seconds, got %d", tt.expectedSecs, result)
			}
			
			// Check for unreasonable values that might indicate unit confusion
			if result > 100000 { // More than ~27 hours
				t.Errorf("Unreasonably large progress: %d seconds (%.2f hours)", result, float64(result)/3600)
			}
		})
	}
}
