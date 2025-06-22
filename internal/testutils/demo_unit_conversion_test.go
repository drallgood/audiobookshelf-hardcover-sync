package testutils

import (
	"testing"
)

// TestDemoUnitConversionBuild ensures the demo code compiles without issues
func TestDemoUnitConversionBuild(t *testing.T) {
	// This test ensures the demo code compiles and doesn't have syntax errors
	// The actual demonstration logic is in the init() function with a false condition
	// so it never runs during tests but still gets compiled

	// Test that our unit conversion functions work correctly
	convertedCurrentTime := convertTimeUnits(1800000.0, 3600.0) // 30 min in ms, 1 hour in seconds

	if convertedCurrentTime != 1800.0 {
		t.Errorf("Expected converted currentTime to be 1800.0, got %.1f", convertedCurrentTime)
	}

	// Test that progress calculation works correctly with conversion
	progress := calculateProgressWithConversion(convertedCurrentTime, 0.0, 3600.0)
	expected := 0.5 // 50%

	if progress < expected-0.001 || progress > expected+0.001 {
		t.Errorf("Expected progress to be approximately %.3f, got %.6f", expected, progress)
	}

	t.Logf("✅ Unit conversion demo compiles and works correctly")
	t.Logf("   Converted 1,800,000ms to %.0fs, calculated %.1f%% progress", convertedCurrentTime, progress*100)
}
