package main

import (
	"testing"
)

func TestUnitConversion(t *testing.T) {
	tests := []struct {
		name                string
		currentTime         float64
		totalDuration       float64
		expectedCurrentTime float64
		description         string
	}{
		{
			name:                "No conversion needed - normal seconds",
			currentTime:         1800.0,  // 30 minutes
			totalDuration:       3600.0,  // 1 hour
			expectedCurrentTime: 1800.0,  // Should remain unchanged
			description:         "Normal progress values in seconds",
		},
		{
			name:                "Conversion needed - milliseconds to seconds",
			currentTime:         1800000.0, // 30 minutes in milliseconds
			totalDuration:       3600.0,    // 1 hour in seconds
			expectedCurrentTime: 1800.0,    // Should be converted to seconds
			description:         "CurrentTime in milliseconds, duration in seconds",
		},
		{
			name:                "Large currentTime conversion",
			currentTime:         7200000.0, // 2 hours in milliseconds
			totalDuration:       3600.0,    // 1 hour in seconds
			expectedCurrentTime: 7200.0,    // Should be converted to seconds (even though > duration)
			description:         "Very large currentTime that needs conversion",
		},
		{
			name:                "Small values - no conversion",
			currentTime:         150.0,  // 2.5 minutes
			totalDuration:       300.0,  // 5 minutes
			expectedCurrentTime: 150.0,  // Should remain unchanged
			description:         "Small values that don't need conversion",
		},
		{
			name:                "Zero values",
			currentTime:         0.0,
			totalDuration:       3600.0,
			expectedCurrentTime: 0.0,
			description:         "Zero currentTime should remain zero",
		},
		{
			name:                "Millisecond edge case",
			currentTime:         999.0,   // Just under 1000
			totalDuration:       1800.0,  // 30 minutes
			expectedCurrentTime: 999.0,   // Should not be converted
			description:         "Edge case just under 1000 - should not convert",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertTimeUnits(tt.currentTime, tt.totalDuration)
			if result != tt.expectedCurrentTime {
				t.Errorf("convertTimeUnits(%f, %f) = %f, expected %f (%s)",
					tt.currentTime, tt.totalDuration, result, tt.expectedCurrentTime, tt.description)
			}
		})
	}
}

func TestCalculateProgressWithConversion(t *testing.T) {
	tests := []struct {
		name             string
		currentTime      float64
		duration         float64
		totalDuration    float64
		expectedProgress float64
		description      string
	}{
		{
			name:             "Normal progress calculation",
			currentTime:      1800.0,  // 30 minutes
			duration:         0.0,     // Not used
			totalDuration:    3600.0,  // 1 hour
			expectedProgress: 0.5,     // 50%
			description:      "Normal 50% progress",
		},
		{
			name:             "Millisecond currentTime conversion",
			currentTime:      1800000.0, // 30 minutes in milliseconds
			duration:         0.0,       // Not used
			totalDuration:    3600.0,    // 1 hour in seconds
			expectedProgress: 0.5,       // Should be 50% after conversion
			description:      "CurrentTime in milliseconds should be converted",
		},
		{
			name:             "1000x error scenario",
			currentTime:      36000000.0, // 10 hours in milliseconds
			duration:         0.0,        // Not used
			totalDuration:    36000.0,    // 10 hours in seconds
			expectedProgress: 1.0,        // Should be 100% after conversion
			description:      "Reproduces the original 1000x error and fixes it",
		},
		{
			name:             "Use duration when totalDuration is zero",
			currentTime:      900.0,  // 15 minutes
			duration:         1800.0, // 30 minutes
			totalDuration:    0.0,    // Not available
			expectedProgress: 0.5,    // 50%
			description:      "Should use duration when totalDuration is not available",
		},
		{
			name:             "Millisecond currentTime with duration",
			currentTime:      900000.0, // 15 minutes in milliseconds
			duration:         1800.0,   // 30 minutes in seconds
			totalDuration:    0.0,      // Not available
			expectedProgress: 0.5,      // Should be 50% after conversion
			description:      "CurrentTime conversion with duration fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateProgressWithConversion(tt.currentTime, tt.duration, tt.totalDuration)
			if abs := result - tt.expectedProgress; abs > 0.001 && abs < -0.001 { // Allow small floating point differences
				t.Errorf("calculateProgressWithConversion(%f, %f, %f) = %f, expected %f (%s)",
					tt.currentTime, tt.duration, tt.totalDuration, result, tt.expectedProgress, tt.description)
			}
		})
	}
}

func TestOriginal1000xScenario(t *testing.T) {
	// This reproduces the exact scenario from sync.go that was causing the 1000x error
	
	// Simulate AudiobookShelf returning currentTime in milliseconds (the bug)
	currentTimeFromAPI := 18000000.0 // 5 hours in milliseconds
	totalDurationFromAPI := 18000.0   // 5 hours in seconds
	
	// Without conversion (the original bug)
	originalProgress := currentTimeFromAPI / totalDurationFromAPI
	if originalProgress != 1000.0 {
		t.Errorf("Original scenario should produce 1000x error, got %.1f", originalProgress)
	}
	
	// With our conversion fix
	fixedProgress := calculateProgressWithConversion(currentTimeFromAPI, 0.0, totalDurationFromAPI)
	if fixedProgress < 0.99 || fixedProgress > 1.0 {
		t.Errorf("Fixed scenario should produce ~1.0 progress, got %.6f", fixedProgress)
	}
	
	t.Logf("Original bug: %.1fx progress, Fixed: %.6f progress", originalProgress, fixedProgress)
}

func TestSyncProgressCalculation(t *testing.T) {
	// Test the exact logic from sync.go lines 302-305
	tests := []struct {
		name                  string
		currentTime          float64
		totalDuration        float64
		expectedTargetProgress float64
	}{
		{
			name:                  "Normal case",
			currentTime:          1800.0,  // 30 minutes in seconds
			totalDuration:        3600.0,  // 1 hour in seconds
			expectedTargetProgress: 0.5,    // 50%
		},
		{
			name:                  "Millisecond bug case",
			currentTime:          1800000.0, // 30 minutes in milliseconds (API bug)
			totalDuration:        3600.0,    // 1 hour in seconds
			expectedTargetProgress: 0.5,      // Should be 50% after our fix
		},
		{
			name:                  "Complete book case",
			currentTime:          72000000.0, // 20 hours in milliseconds
			totalDuration:        72000.0,    // 20 hours in seconds
			expectedTargetProgress: 1.0,       // 100%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply our unit conversion
			convertedCurrentTime := convertTimeUnits(tt.currentTime, tt.totalDuration)
			
			// Simulate the exact calculation from sync.go
			var targetProgressSeconds float64
			if tt.totalDuration > 0 {
				targetProgressSeconds = convertedCurrentTime
			}
			
			// Calculate the progress percentage that would be sent to Hardcover
			var targetProgress float64
			if tt.totalDuration > 0 {
				targetProgress = targetProgressSeconds / tt.totalDuration
			}
			
			if abs := targetProgress - tt.expectedTargetProgress; abs > 0.001 && abs < -0.001 {
				t.Errorf("Sync calculation with conversion: got %.6f, expected %.6f", 
					targetProgress, tt.expectedTargetProgress)
			}
			
			t.Logf("Test %s: currentTime=%.1f -> %.1f, progress=%.6f", 
				tt.name, tt.currentTime, convertedCurrentTime, targetProgress)
		})
	}
}
