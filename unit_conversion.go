package main

// convertTimeUnits detects if currentTime is in milliseconds instead of seconds
// and converts it accordingly. This fixes the 1000x multiplication error.
func convertTimeUnits(currentTime, totalDuration float64) float64 {
	if currentTime <= 0 || totalDuration <= 0 {
		return currentTime
	}

	// Calculate the progress ratio as-is
	ratio := currentTime / totalDuration

	// Check for data quality issues first
	// If currentTime is reasonable for audiobooks (> 1 minute) but totalDuration is very small (< 10 minutes),
	// this suggests corrupted data rather than unit conversion issue
	if currentTime > 60 && totalDuration < 600 && ratio > 50 {
		debugLog("Data quality issue detected: currentTime=%.2f seconds (%.1f hours) but totalDuration=%.2f seconds (%.1f minutes). Ratio=%.1f suggests corrupted data, not unit mismatch.",
			currentTime, currentTime/3600, totalDuration, totalDuration/60, ratio)
		debugLog("Keeping original values - this may need manual review")
		return currentTime
	}

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

	// If currentTime is suspiciously large compared to a reasonable audiobook duration,
	// check if converting would be appropriate. Most audiobooks are under 50 hours (180,000 seconds).
	// Values over 180,000 seconds are likely in milliseconds.
	if currentTime > 180000 { // > 50 hours, likely milliseconds
		convertedCurrentTime := currentTime / 1000.0
		convertedRatio := convertedCurrentTime / totalDuration

		// Convert if the value would be reasonable after conversion
		// Allow higher ratios for extremely large values that are clearly milliseconds
		if convertedRatio >= 0.001 && convertedCurrentTime >= 1.0 {
			debugLog("Unit conversion for extremely large currentTime: %.2f -> %.2f seconds (ratio: %.6f -> %.6f)",
				currentTime, convertedCurrentTime, ratio, convertedRatio)
			return convertedCurrentTime
		}
	}

	// Additional check: if ratio suggests currentTime is in different units than totalDuration
	// Only convert if the ratio is extremely high (suggesting milliseconds vs seconds)
	if ratio > 100.0 { // currentTime is 100x larger than totalDuration, likely unit mismatch
		convertedCurrentTime := currentTime / 1000.0
		convertedRatio := convertedCurrentTime / totalDuration

		if convertedRatio >= 0.001 && convertedRatio <= 1.1 {
			debugLog("Unit conversion for ratio mismatch: %.2f -> %.2f seconds (ratio: %.6f -> %.6f)",
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
		// If currentTime was converted, check if duration fields should also be converted
		// Use a more aggressive approach: if the ratio suggests unit mismatch, convert duration too
		
		// Check if duration values are also extremely large (> 50 hours)
		if duration > 180000 { // > 50 hours in seconds, might be milliseconds
			convertedDuration = duration / 1000.0
			debugLog("Also converted duration %.2f -> %.2f seconds", duration, convertedDuration)
		}
		if totalDuration > 180000 {
			convertedTotalDuration = totalDuration / 1000.0
			debugLog("Also converted totalDuration %.2f -> %.2f seconds", totalDuration, convertedTotalDuration)
		}
		
		// Additional check: if currentTime needed conversion but totalDuration is small,
		// check if totalDuration should also be converted to maintain unit consistency
		if totalDuration > 0 && totalDuration <= 180000 {
			// Check if converting totalDuration would make the ratio more reasonable
			testConvertedTotal := totalDuration / 1000.0
			newRatio := convertedCurrentTime / testConvertedTotal
			originalRatio := currentTime / totalDuration
			
			// If the original ratio was very high (> 50) and converting totalDuration 
			// makes the ratio more reasonable (< 10), then convert totalDuration too
			if originalRatio > 50.0 && newRatio > 0.1 && newRatio < 10.0 && testConvertedTotal > 0.1 {
				convertedTotalDuration = testConvertedTotal
				debugLog("Unit consistency conversion: totalDuration %.2f -> %.2f seconds (ratio: %.2f -> %.2f)",
					totalDuration, convertedTotalDuration, originalRatio, newRatio)
			}
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
