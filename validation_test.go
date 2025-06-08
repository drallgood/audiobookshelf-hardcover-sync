package main

import (
	"testing"
)

// TestValidateUnitConversionIntegration validates that our unit conversion fix
// integrates correctly with the main sync logic without breaking existing functionality
func TestValidateUnitConversionIntegration(t *testing.T) {
	tests := []struct {
		name             string
		currentTime      float64
		totalDuration    float64
		expectedResult   float64
		expectCorrection bool
	}{
		{
			name:             "Normal seconds - no conversion needed",
			currentTime:      1800.0, // 30 minutes in seconds
			totalDuration:    3600.0, // 1 hour in seconds
			expectedResult:   0.5,    // 50% progress
			expectCorrection: false,
		},
		{
			name:             "Milliseconds - same units, no conversion needed",
			currentTime:      1800000.0, // 30 minutes in milliseconds
			totalDuration:    3600000.0, // 1 hour in milliseconds
			expectedResult:   0.5,       // 50% progress (no conversion needed when both in same units)
			expectCorrection: false,     // No conversion needed when units match
		},
		{
			name:             "Mixed units - currentTime in ms, totalDuration in seconds (1000x error scenario)",
			currentTime:      1800000.0, // 30 minutes in milliseconds
			totalDuration:    3600.0,    // 1 hour in seconds
			expectedResult:   0.5,       // 50% progress (corrected)
			expectCorrection: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use our unit conversion function
			convertedCurrentTime := convertTimeUnits(tt.currentTime, tt.totalDuration)

			// Calculate progress
			var progress float64
			if tt.totalDuration > 0 {
				progress = convertedCurrentTime / tt.totalDuration
			}

			// Validate the result
			if progress != tt.expectedResult {
				t.Errorf("Expected progress %.2f, got %.2f", tt.expectedResult, progress)
			}

			// Check if conversion was applied when expected
			conversionApplied := (convertedCurrentTime != tt.currentTime)
			if conversionApplied != tt.expectCorrection {
				t.Errorf("Expected conversion applied: %v, got: %v", tt.expectCorrection, conversionApplied)
			}

			t.Logf("Input: currentTime=%.2f, totalDuration=%.2f", tt.currentTime, tt.totalDuration)
			t.Logf("Converted: currentTime=%.2f", convertedCurrentTime)
			t.Logf("Progress: %.2f%% (%.4f)", progress*100, progress)
		})
	}
}

// TestProgressBoundaryValidation ensures our fix handles edge cases correctly
func TestProgressBoundaryValidation(t *testing.T) {
	tests := []struct {
		name          string
		currentTime   float64
		totalDuration float64
		expectValid   bool
	}{
		{
			name:          "Zero progress",
			currentTime:   0,
			totalDuration: 3600,
			expectValid:   true,
		},
		{
			name:          "Complete progress (100%)",
			currentTime:   3600,
			totalDuration: 3600,
			expectValid:   true,
		},
		{
			name:          "Complete progress in milliseconds (should be converted)",
			currentTime:   3600000,
			totalDuration: 3600000,
			expectValid:   true,
		},
		{
			name:          "Invalid: progress > 100% without conversion (indicates millisecond data)",
			currentTime:   3600000, // milliseconds
			totalDuration: 3600,    // seconds
			expectValid:   true,    // Should be valid after conversion
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			progress := calculateProgressWithConversion(tt.currentTime, tt.totalDuration, 0)

			// Progress should be between 0 and 1 after conversion
			isValid := progress >= 0 && progress <= 1

			if isValid != tt.expectValid {
				t.Errorf("Expected valid: %v, got valid: %v (progress: %.4f)", tt.expectValid, isValid, progress)
			}

			t.Logf("Progress: %.2f%% (%.4f)", progress*100, progress)
		})
	}
}
