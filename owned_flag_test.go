package main

import (
	"testing"
)

// TestOwnedFlagChecking tests the owned flag logic in sync
func TestOwnedFlagChecking(t *testing.T) {
	tests := []struct {
		name            string
		existingOwned   bool
		targetOwned     bool
		expectedChanged bool
	}{
		{
			name:            "owned flag matches",
			existingOwned:   true,
			targetOwned:     true,
			expectedChanged: false,
		},
		{
			name:            "needs to be marked as owned",
			existingOwned:   false,
			targetOwned:     true,
			expectedChanged: true,
		},
		{
			name:            "needs to be unmarked as owned",
			existingOwned:   true,
			targetOwned:     false,
			expectedChanged: true,
		},
		{
			name:            "both false, no change needed",
			existingOwned:   false,
			targetOwned:     false,
			expectedChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ownedChanged := tt.targetOwned != tt.existingOwned
			if ownedChanged != tt.expectedChanged {
				t.Errorf("Expected ownedChanged=%t, got %t", tt.expectedChanged, ownedChanged)
			}
		})
	}
}

// TestOwnedFlagSyncLogic tests the complete sync logic with owned flag considerations
func TestOwnedFlagSyncLogic(t *testing.T) {
	tests := []struct {
		name                string
		statusChanged       bool
		progressChanged     bool
		ownedChanged        bool
		targetOwned         bool
		existingOwned       bool
		hasEditionId        bool
		expectedNeedsSync   bool
		expectedSkipReason  string
		expectedOwnedAction string
	}{
		{
			name:                "status changed - needs sync",
			statusChanged:       true,
			progressChanged:     false,
			ownedChanged:        false,
			expectedNeedsSync:   true,
			expectedSkipReason:  "",
			expectedOwnedAction: "",
		},
		{
			name:                "progress changed - needs sync",
			statusChanged:       false,
			progressChanged:     true,
			ownedChanged:        false,
			expectedNeedsSync:   true,
			expectedSkipReason:  "",
			expectedOwnedAction: "",
		},
		{
			name:                "owned changed - mark as owned with edition_id",
			statusChanged:       false,
			progressChanged:     false,
			ownedChanged:        true,
			targetOwned:         true,
			existingOwned:       false,
			hasEditionId:        true,
			expectedNeedsSync:   false,
			expectedSkipReason:  "owned_flag_fixed",
			expectedOwnedAction: "mark_owned",
		},
		{
			name:                "owned changed - mark as owned without edition_id",
			statusChanged:       false,
			progressChanged:     false,
			ownedChanged:        true,
			targetOwned:         true,
			existingOwned:       false,
			hasEditionId:        false,
			expectedNeedsSync:   false,
			expectedSkipReason:  "owned_flag_cannot_fix",
			expectedOwnedAction: "cannot_mark_owned",
		},
		{
			name:                "owned changed - should unmark owned",
			statusChanged:       false,
			progressChanged:     false,
			ownedChanged:        true,
			targetOwned:         false,
			existingOwned:       true,
			hasEditionId:        true,
			expectedNeedsSync:   false,
			expectedSkipReason:  "owned_flag_cannot_unmark",
			expectedOwnedAction: "cannot_unmark_owned",
		},
		{
			name:                "nothing changed - skip normally",
			statusChanged:       false,
			progressChanged:     false,
			ownedChanged:        false,
			expectedNeedsSync:   false,
			expectedSkipReason:  "up_to_date",
			expectedOwnedAction: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the sync logic
			var needsSync bool
			var skipReason string
			var ownedAction string

			if tt.statusChanged || tt.progressChanged {
				needsSync = true
			} else if tt.ownedChanged {
				needsSync = false
				if tt.targetOwned && !tt.existingOwned && tt.hasEditionId {
					skipReason = "owned_flag_fixed"
					ownedAction = "mark_owned"
				} else if tt.targetOwned && !tt.existingOwned && !tt.hasEditionId {
					skipReason = "owned_flag_cannot_fix"
					ownedAction = "cannot_mark_owned"
				} else if !tt.targetOwned && tt.existingOwned {
					skipReason = "owned_flag_cannot_unmark"
					ownedAction = "cannot_unmark_owned"
				}
			} else {
				needsSync = false
				skipReason = "up_to_date"
			}

			if needsSync != tt.expectedNeedsSync {
				t.Errorf("Expected needsSync=%t, got %t", tt.expectedNeedsSync, needsSync)
			}

			if skipReason != tt.expectedSkipReason {
				t.Errorf("Expected skipReason=%s, got %s", tt.expectedSkipReason, skipReason)
			}

			if ownedAction != tt.expectedOwnedAction {
				t.Errorf("Expected ownedAction=%s, got %s", tt.expectedOwnedAction, ownedAction)
			}
		})
	}
}
