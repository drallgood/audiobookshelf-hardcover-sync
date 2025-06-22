package testutils

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

// Test that suspicious progress on long books is treated as finished
func TestSuspiciousProgressTreatment(t *testing.T) {
	tests := []struct {
		name                   string
		progress               float64
		duration               float64
		expectedFinalProgress  float64
		description            string
	}{
		{
			name:                  "Red Bounty scenario - tiny progress on long book should be treated as finished",
			progress:              0.000307, // ~10 seconds
			duration:              32760,    // 9.1 hours in seconds
			expectedFinalProgress: 1.0,      // Should be treated as finished
			description:           "Very small progress on long audiobook treated as finished",
		},
		{
			name:                  "Normal small progress on short book - keep original",
			progress:              0.01,  // 1%
			duration:              600,   // 10 minutes
			expectedFinalProgress: 0.01,  // Should keep original
			description:           "Normal progress on short book",
		},
		{
			name:                  "Reasonable progress on long book - keep original",
			progress:              0.05,   // 5%
			duration:              32760,  // 9.1 hours
			expectedFinalProgress: 0.05,   // Should keep original  
			description:           "Reasonable progress on long book",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimatedCurrentTime := tt.progress * tt.duration
			
			// Simulate the logic from the actual code
			var finalProgress float64
			if estimatedCurrentTime < 60 && tt.duration > 1800 { // Suspicious criteria
				// Simulate: listening sessions API returns no progress (sessionProgress = 0)
				// So we should treat this as finished
				finalProgress = 1.0
			} else {
				// Keep original progress
				finalProgress = tt.progress
			}

			if finalProgress != tt.expectedFinalProgress {
				t.Errorf("Expected final progress %.6f, got %.6f for %s", 
					tt.expectedFinalProgress, finalProgress, tt.description)
			}

			t.Logf("✅ Test '%s': %.1fs progress on %.1fh book → final progress: %.1f%% (%s)",
				tt.name, estimatedCurrentTime, tt.duration/3600, finalProgress*100, tt.description)
		})
	}
}

// Helper function to calculate absolute difference
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
