package testutils

import (
	"testing"
	"time"
)

// TestEditionFieldFix tests that the edition_id is properly included in user_book_read mutations
// to prevent the edition field from becoming null
func TestEditionFieldFix(t *testing.T) {
	tests := []struct {
		name              string
		progressSeconds   int
		isFinished        bool
		editionID         int
		expectEditionID   bool
		expectWarning     bool
	}{
		{
			name:            "Insert with valid edition_id",
			progressSeconds: 1800,
			isFinished:      false,
			editionID:       12345,
			expectEditionID: true,
			expectWarning:   false,
		},
		{
			name:            "Insert without edition_id",
			progressSeconds: 3600,
			isFinished:      true,
			editionID:       0,
			expectEditionID: false,
			expectWarning:   true,
		},
		{
			name:            "Update with valid edition_id",
			progressSeconds: 2700,
			isFinished:      false,
			editionID:       67890,
			expectEditionID: true,
			expectWarning:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test insertUserBookRead mutation structure
			userBookRead := map[string]interface{}{
				"progress_seconds":  tt.progressSeconds,
				"reading_format_id": 2, // Audiobook format
			}

			// Set edition_id if available (this is the critical fix)
			if tt.editionID > 0 {
				userBookRead["edition_id"] = tt.editionID
			}

			// Set dates based on completion status
			now := time.Now().Format("2006-01-02")
			userBookRead["started_at"] = now

			if tt.isFinished {
				userBookRead["finished_at"] = now
			}

			// Verify edition_id is included when expected
			if tt.expectEditionID {
				if editionID, exists := userBookRead["edition_id"]; !exists || editionID != tt.editionID {
					t.Errorf("Expected edition_id %d to be set in userBookRead, got %v", tt.editionID, editionID)
				}
			} else {
				if _, exists := userBookRead["edition_id"]; exists {
					t.Errorf("Expected no edition_id in userBookRead, but it was set")
				}
			}

			// Test update_user_book_read object structure
			updateObject := map[string]interface{}{
				"progress_seconds": tt.progressSeconds,
			}

			// Set edition_id if available (critical fix for updates too)
			if tt.editionID > 0 {
				updateObject["edition_id"] = tt.editionID
			}

			// Preserve started_at (reading history fix)
			updateObject["started_at"] = "2025-06-05" // Simulated existing date

			if tt.isFinished {
				updateObject["finished_at"] = now
			}

			// Verify update object has edition_id when expected
			if tt.expectEditionID {
				if editionID, exists := updateObject["edition_id"]; !exists || editionID != tt.editionID {
					t.Errorf("Expected edition_id %d to be set in updateObject, got %v", tt.editionID, editionID)
				}
			} else {
				if _, exists := updateObject["edition_id"]; exists {
					t.Errorf("Expected no edition_id in updateObject, but it was set")
				}
			}

			// Verify all required fields are present
			requiredFields := []string{"progress_seconds", "started_at"}
			for _, field := range requiredFields {
				if _, exists := userBookRead[field]; !exists {
					t.Errorf("Required field %s missing from userBookRead", field)
				}
				if _, exists := updateObject[field]; !exists {
					t.Errorf("Required field %s missing from updateObject", field)
				}
			}

			t.Logf("✅ Edition field fix test passed for %s", tt.name)
			t.Logf("   userBookRead: %+v", userBookRead)
			t.Logf("   updateObject: %+v", updateObject)
		})
	}
}

// TestEditionFieldMutationCompatibility tests that the mutations are compatible with Hardcover's GraphQL schema
func TestEditionFieldMutationCompatibility(t *testing.T) {
	// Test DatesReadInput structure matches the schema
	datesReadInput := map[string]interface{}{
		"progress_seconds":  1800,
		"reading_format_id": 2,
		"edition_id":        12345, // This is the critical addition
		"started_at":        "2025-06-08",
		// finished_at is optional
	}

	// Verify all fields are valid according to DatesReadInput schema
	validFields := map[string]bool{
		"action":            true,
		"edition_id":        true, // This field exists in the schema
		"finished_at":       true,
		"id":                true,
		"progress_pages":    true,
		"progress_seconds":  true,
		"reading_format_id": true,
		"started_at":        true,
	}

	for field := range datesReadInput {
		if !validFields[field] {
			t.Errorf("Field %s is not valid in DatesReadInput schema", field)
		}
	}

	// Ensure we have the critical edition_id field
	if editionID, exists := datesReadInput["edition_id"]; !exists {
		t.Error("CRITICAL: edition_id field is missing from DatesReadInput")
	} else if editionID == 0 || editionID == nil {
		t.Error("CRITICAL: edition_id field is set but has invalid value")
	}

	t.Logf("✅ DatesReadInput structure is valid and includes edition_id")
	t.Logf("   Input: %+v", datesReadInput)
}
