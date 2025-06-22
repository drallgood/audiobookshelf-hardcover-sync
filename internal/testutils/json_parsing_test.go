package testutils

import (
	"encoding/json"
	"testing"
)

func TestSearchAPIResponseJSONParsing(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		expected []int
	}{
		{
			name:     "String IDs from API",
			jsonData: `{"data": {"search": {"ids": ["123", "456", "789"], "error": null}}}`,
			expected: []int{123, 456, 789},
		},
		{
			name:     "Integer IDs from API",
			jsonData: `{"data": {"search": {"ids": [123, 456, 789], "error": null}}}`,
			expected: []int{123, 456, 789},
		},
		{
			name:     "Mixed string and integer IDs",
			jsonData: `{"data": {"search": {"ids": ["123", 456, "789"], "error": null}}}`,
			expected: []int{123, 456, 789},
		},
		{
			name:     "Empty IDs array",
			jsonData: `{"data": {"search": {"ids": [], "error": null}}}`,
			expected: []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response SearchAPIResponse
			err := json.Unmarshal([]byte(tt.jsonData), &response)
			if err != nil {
				t.Fatalf("Failed to unmarshal JSON: %v", err)
			}

			// Convert json.Number to int using the same logic as searchPersonIDs
			var ids []int
			for _, jsonNum := range response.Data.Search.IDs {
				id, err := jsonNum.Int64()
				if err != nil {
					t.Fatalf("Failed to convert ID to integer: %v", err)
				}
				ids = append(ids, int(id))
			}

			// Compare results
			if len(ids) != len(tt.expected) {
				t.Fatalf("Expected %d IDs, got %d", len(tt.expected), len(ids))
			}

			for i, expected := range tt.expected {
				if ids[i] != expected {
					t.Errorf("Expected ID %d at index %d, got %d", expected, i, ids[i])
				}
			}

			t.Logf("✅ Successfully parsed %d IDs: %v", len(ids), ids)
		})
	}
}

func TestSearchPersonIDsJSONConversion(t *testing.T) {
	// Test the conversion logic used in searchPersonIDs function
	testCases := []struct {
		name      string
		jsonValue string
		expected  int
		shouldErr bool
	}{
		{"String number", `"123"`, 123, false},
		{"Integer", `456`, 456, false},
		{"Large number", `"9876543210"`, 9876543210, false},
		{"Invalid string", `"abc"`, 0, true},
		{"Negative number", `"-123"`, -123, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var num json.Number
			err := json.Unmarshal([]byte(tc.jsonValue), &num)
			if err != nil {
				if tc.shouldErr {
					t.Logf("✅ Expected error for %s: %v", tc.name, err)
					return
				}
				t.Fatalf("Failed to unmarshal json.Number: %v", err)
			}

			id, err := num.Int64()
			if tc.shouldErr {
				if err == nil {
					t.Errorf("Expected error for %s, but got none", tc.name)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error for %s: %v", tc.name, err)
				return
			}

			if int(id) != tc.expected {
				t.Errorf("Expected %d, got %d", tc.expected, int(id))
			}

			t.Logf("✅ Successfully converted %s to %d", tc.jsonValue, int(id))
		})
	}
}
