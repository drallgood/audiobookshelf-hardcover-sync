package testutils

import (
	"testing"
)

func TestUnitConversionFix(t *testing.T) {
	tests := []struct {
		name          string
		currentTime   float64
		totalDuration float64
		expectedTime  float64
		description   string
	}{
		{
			name:          "Normal audiobook progress - no conversion",
			currentTime:   42671.61, // ~11.9 hours in seconds
			totalDuration: 42672.0,  // ~11.9 hours in seconds
			expectedTime:  42671.61, // Should NOT be converted
			description:   "Normal audiobook duration should not be converted",
		},
		{
			name:          "Short audiobook progress - no conversion",
			currentTime:   1800.0, // 30 minutes in seconds
			totalDuration: 3600.0, // 1 hour in seconds
			expectedTime:  1800.0, // Should NOT be converted
			description:   "Short audiobook should not be converted",
		},
		{
			name:          "Long audiobook progress - no conversion",
			currentTime:   72000.0, // 20 hours in seconds
			totalDuration: 86400.0, // 24 hours in seconds
			expectedTime:  72000.0, // Should NOT be converted
			description:   "Long but reasonable audiobook should not be converted",
		},
		{
			name:          "Extremely long value - should convert",
			currentTime:   200000000.0, // ~55,555 hours in milliseconds
			totalDuration: 72000.0,     // 20 hours in seconds
			expectedTime:  200000.0,    // Should be converted to ~55.5 hours
			description:   "Extremely large value should be converted from milliseconds",
		},
		{
			name:          "Ratio-based conversion",
			currentTime:   7200000.0, // 2 hours in milliseconds
			totalDuration: 7200.0,    // 2 hours in seconds
			expectedTime:  7200.0,    // Should be converted (ratio = 1000)
			description:   "High ratio should trigger conversion",
		},
		{
			name:          "Edge case - 50 hours exactly",
			currentTime:   180000.0, // Exactly 50 hours
			totalDuration: 180000.0, // Exactly 50 hours
			expectedTime:  180000.0, // Should NOT be converted (threshold is exclusive)
			description:   "Exactly 50 hours should not be converted",
		},
		{
			name:          "Just over threshold - data quality issue",
			currentTime:   180001.0, // Just over 50 hours
			totalDuration: 180.0,    // 3 minutes in seconds
			expectedTime:  180001.0, // Should NOT be converted (data quality issue detected)
			description:   "Data quality issue: 50+ hours progress on 3-minute book should not be converted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertTimeUnits(tt.currentTime, tt.totalDuration)

			// Use a small tolerance for floating point comparison
			tolerance := 0.01
			if abs(result-tt.expectedTime) > tolerance {
				t.Errorf("%s: convertTimeUnits(%.2f, %.2f) = %.2f, expected %.2f",
					tt.description, tt.currentTime, tt.totalDuration, result, tt.expectedTime)
			}
		})
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestProgressCalculationWithFixedConversion(t *testing.T) {
	// Test the specific case from the log
	currentTime := 42671.61
	totalDuration := 42672.0

	result := convertTimeUnits(currentTime, totalDuration)

	// Should NOT be converted since it's a reasonable audiobook duration
	if result != currentTime {
		t.Errorf("Allegiant case: convertTimeUnits(%.2f, %.2f) = %.2f, expected %.2f (no conversion)",
			currentTime, totalDuration, result, currentTime)
	}

	// Test progress calculation
	progress := calculateProgressWithConversion(currentTime, 0, totalDuration)
	expectedProgress := currentTime / totalDuration // Should be very close to 1.0

	tolerance := 0.001
	if abs(progress-expectedProgress) > tolerance {
		t.Errorf("Progress calculation: got %.6f, expected %.6f", progress, expectedProgress)
	}
}
