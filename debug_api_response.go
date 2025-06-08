package main

import (
	"encoding/json"
	"fmt"
)

// debugAPIResponse logs detailed information about API response values
// to help identify unit conversion issues (milliseconds vs seconds)
func debugAPIResponse(endpoint string, body []byte) {
	if !debugMode {
		return
	}

	debugLog("=== API Response Debug for %s ===", endpoint)

	// Try to parse as generic JSON to inspect raw values
	var genericData map[string]interface{}
	if err := json.Unmarshal(body, &genericData); err == nil {
		debugLog("Raw JSON structure keys: %v", getMapKeys(genericData))

		// Look for mediaProgress data
		if mediaProgress, ok := genericData["mediaProgress"].([]interface{}); ok {
			debugLog("Found mediaProgress array with %d items", len(mediaProgress))
			for i, item := range mediaProgress {
				if itemMap, ok := item.(map[string]interface{}); ok {
					debugAPIProgressItem(fmt.Sprintf("mediaProgress[%d]", i), itemMap)
				}
			}
		}

		// Look for sessions data
		if sessions, ok := genericData["sessions"].([]interface{}); ok {
			debugLog("Found sessions array with %d items", len(sessions))
			for i, session := range sessions {
				if sessionMap, ok := session.(map[string]interface{}); ok {
					debugAPIProgressItem(fmt.Sprintf("sessions[%d]", i), sessionMap)
				}
			}
		}

		// Look for direct session data (single item responses)
		if _, hasCurrentTime := genericData["currentTime"]; hasCurrentTime {
			debugAPIProgressItem("direct_session", genericData)
		}
	}

	debugLog("=== End API Response Debug ===")
}

// debugAPIProgressItem logs progress-related fields from an API response item
func debugAPIProgressItem(itemName string, item map[string]interface{}) {
	var currentTime, duration, totalDuration, progress float64
	var hasCurrentTime, hasDuration, hasTotalDuration, hasProgress bool

	if ct, ok := item["currentTime"].(float64); ok {
		currentTime = ct
		hasCurrentTime = true
	}
	if d, ok := item["duration"].(float64); ok {
		duration = d
		hasDuration = true
	}
	if td, ok := item["totalDuration"].(float64); ok {
		totalDuration = td
		hasTotalDuration = true
	}
	if p, ok := item["progress"].(float64); ok {
		progress = p
		hasProgress = true
	}

	debugLog("Item %s:", itemName)
	if hasCurrentTime {
		debugLog("  currentTime: %.2f (%.2f minutes, %.2f hours)",
			currentTime, currentTime/60, currentTime/3600)

		// Check if this might be in milliseconds
		if currentTime > 1000 {
			debugLog("  -> Potential milliseconds: %.2f seconds (%.2f minutes)",
				currentTime/1000, currentTime/60000)
		}
	}
	if hasDuration {
		debugLog("  duration: %.2f (%.2f minutes, %.2f hours)",
			duration, duration/60, duration/3600)
	}
	if hasTotalDuration {
		debugLog("  totalDuration: %.2f (%.2f minutes, %.2f hours)",
			totalDuration, totalDuration/60, totalDuration/3600)
	}
	if hasProgress {
		debugLog("  progress: %.6f (%.2f%%)", progress, progress*100)
	}

	// Calculate progress ratio if we have the data
	if hasCurrentTime && (hasDuration || hasTotalDuration) {
		var calcDuration float64
		if hasTotalDuration && totalDuration > 0 {
			calcDuration = totalDuration
		} else if hasDuration && duration > 0 {
			calcDuration = duration
		}

		if calcDuration > 0 {
			ratio := currentTime / calcDuration
			debugLog("  calculated progress: %.6f (%.2f%%) from currentTime/duration",
				ratio, ratio*100)

			// Check if treating currentTime as milliseconds would make more sense
			if currentTime > 1000 {
				ratioFromMs := (currentTime / 1000) / calcDuration
				debugLog("  if currentTime were milliseconds: %.6f (%.2f%%)",
					ratioFromMs, ratioFromMs*100)

				// Flag if the millisecond interpretation seems more reasonable
				if ratioFromMs > 0.001 && ratioFromMs < 1.1 && (ratio > 1.1 || ratio < 0.001) {
					debugLog("  *** UNIT MISMATCH DETECTED: currentTime appears to be in milliseconds ***")
				}
			}
		}
	}
}

// Helper function to get keys from a map
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
