// +build ignore

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// debugAPIResponse analyzes API responses to help debug unit conversion issues
func debugAPIResponse(endpoint string, body []byte) {
	if !getEnableDebugAPI() {
		return
	}

	debugLog("=== API Response Debug for %s ===", endpoint)

	// Try to parse as JSON and look for common time-related fields
	var jsonData interface{}
	if err := json.Unmarshal(body, &jsonData); err != nil {
		debugLog("Failed to parse JSON response: %v", err)
		return
	}

	// Look for time-related fields in the response
	timeFields := findTimeFields(jsonData, "")
	if len(timeFields) > 0 {
		debugLog("Found time-related fields in %s:", endpoint)
		for _, field := range timeFields {
			debugLog("  %s", field)
		}
	}
}

// findTimeFields recursively searches for time-related fields in JSON data
func findTimeFields(data interface{}, path string) []string {
	var fields []string

	switch v := data.(type) {
	case map[string]interface{}:
		for key, value := range v {
			newPath := key
			if path != "" {
				newPath = path + "." + key
			}

			// Check if this field looks time-related
			if isTimeField(key, value) {
				fields = append(fields, fmt.Sprintf("%s: %v", newPath, value))
			}

			// Recursively check nested objects
			fields = append(fields, findTimeFields(value, newPath)...)
		}
	case []interface{}:
		for i, item := range v {
			newPath := fmt.Sprintf("%s[%d]", path, i)
			fields = append(fields, findTimeFields(item, newPath)...)
		}
	}

	return fields
}

// isTimeField checks if a field looks like it contains time data
func isTimeField(key string, value interface{}) bool {
	lowerKey := strings.ToLower(key)

	// Check if field name suggests time data
	timeKeywords := []string{
		"time", "duration", "progress", "seconds", "minutes", "hours",
		"current", "total", "elapsed", "remaining", "position",
	}

	isTimeKey := false
	for _, keyword := range timeKeywords {
		if strings.Contains(lowerKey, keyword) {
			isTimeKey = true
			break
		}
	}

	if !isTimeKey {
		return false
	}

	// Check if value is numeric (could be time data)
	switch v := value.(type) {
	case float64:
		// Check for suspicious values that might indicate unit conversion issues
		if v > 100 && v < 100000 {
			return true
		}
		return v > 0
	case int:
		return v > 0
	case int64:
		return v > 0
	default:
		return false
	}
}
