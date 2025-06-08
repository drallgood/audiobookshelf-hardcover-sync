package main

// convertTimeUnits detects if currentTime is in milliseconds instead of seconds
// and converts it accordingly. This fixes the 1000x multiplication error.
func convertTimeUnits(currentTime, totalDuration float64) float64 {
	if currentTime <= 0 || totalDuration <= 0 {
		return currentTime
	}

	// Calculate the progress ratio as-is
	ratio := currentTime / totalDuration

	// If the ratio is greater than 1 (meaning currentTime > totalDuration),
	// this suggests currentTime might be in milliseconds while totalDuration is in seconds
	if ratio > 1.0 {
		convertedCurrentTime := currentTime / 1000.0
		convertedRatio := convertedCurrentTime / totalDuration

		// If converting to seconds gives us a reasonable ratio (0.001 to 1.1),
		// then currentTime was likely in milliseconds
		if convertedRatio >= 0.001 && convertedRatio <= 1.1 {
			debugLog("Unit conversion detected: currentTime %.2f appears to be milliseconds, converted to %.2f seconds (ratio: %.6f)",
				currentTime, convertedCurrentTime, convertedRatio)
			return convertedCurrentTime
		}
	}

	// If currentTime is suspiciously large (> 1000), check if converting would be appropriate
	if currentTime > 1000 {
		convertedCurrentTime := currentTime / 1000.0
		convertedRatio := convertedCurrentTime / totalDuration

		// Convert if the value is large enough to suggest milliseconds
		// and the conversion doesn't make it unreasonably small
		if convertedRatio >= 0.001 && convertedCurrentTime >= 1.0 {
			debugLog("Unit conversion for large currentTime: %.2f -> %.2f seconds (ratio: %.6f -> %.6f)",
				currentTime, convertedCurrentTime, ratio, convertedRatio)
			return convertedCurrentTime
		}
	}

	// Return original value if no conversion is needed
	return currentTime
}

// convertProgressData applies unit conversion to progress data structures
func convertProgressData(currentTime, duration, totalDuration float64) (float64, float64, float64) {
	// Determine the most reliable duration value
	var referenceDuration float64
	if totalDuration > 0 {
		referenceDuration = totalDuration
	} else if duration > 0 {
		referenceDuration = duration
	}

	// Convert currentTime if needed
	convertedCurrentTime := convertTimeUnits(currentTime, referenceDuration)

	// Apply conversion to duration fields if they seem to be in milliseconds too
	convertedDuration := duration
	convertedTotalDuration := totalDuration

	// Check if duration fields might also be in milliseconds
	// This is less common but possible
	if convertedCurrentTime != currentTime { // We did convert currentTime
		// Check if duration values are also suspiciously large
		if duration > 10000 { // > ~2.8 hours in seconds, might be milliseconds
			convertedDuration = duration / 1000.0
			debugLog("Also converted duration %.2f -> %.2f seconds", duration, convertedDuration)
		}
		if totalDuration > 10000 {
			convertedTotalDuration = totalDuration / 1000.0
			debugLog("Also converted totalDuration %.2f -> %.2f seconds", totalDuration, convertedTotalDuration)
		}
	}

	return convertedCurrentTime, convertedDuration, convertedTotalDuration
}

// calculateProgressWithConversion calculates progress with automatic unit conversion
func calculateProgressWithConversion(currentTime, duration, totalDuration float64) float64 {
	// Apply unit conversion
	convertedCurrentTime, convertedDuration, convertedTotalDuration := convertProgressData(currentTime, duration, totalDuration)

	// Calculate progress using the converted values
	var calculatedProgress float64
	if convertedTotalDuration > 0 && convertedCurrentTime > 0 {
		calculatedProgress = convertedCurrentTime / convertedTotalDuration
	} else if convertedDuration > 0 && convertedCurrentTime > 0 {
		calculatedProgress = convertedCurrentTime / convertedDuration
	}

	// Ensure progress is within reasonable bounds
	if calculatedProgress > 1.0 {
		debugLog("Progress still > 1.0 after conversion (%.6f), capping at 1.0", calculatedProgress)
		calculatedProgress = 1.0
	}

	return calculatedProgress
}
