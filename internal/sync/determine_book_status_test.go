package sync

import (
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
)

// TestDetermineBookStatus tests the determineBookStatus function
func TestDetermineBookStatus(t *testing.T) {
	// Setup logger for testing
	logger.Setup(logger.Config{Level: "debug"})

	// Create a service instance for testing
	svc := &Service{}

	now := time.Now().Unix()

	tests := []struct {
		name        string
		progress    float64
		isFinished  bool
		finishedAt  int64
		expected    string
	}{
		{
			name:       "finished with timestamp",
			progress:   0.8,
			isFinished: true,
			finishedAt: now,
			expected:   "FINISHED",
		},
		{
			name:       "100% progress",
			progress:   1.0,
			isFinished: false,
			finishedAt: 0,
			expected:   "FINISHED",
		},
		{
			name:       "in progress",
			progress:   0.5,
			isFinished: false,
			finishedAt: 0,
			expected:   "IN_PROGRESS",
		},
		{
			name:       "not started",
			progress:   0.0,
			isFinished: false,
			finishedAt: 0,
			expected:   "TO_READ",
		},
		{
			name:       "finished but no timestamp",
			progress:   0.8,
			isFinished: true,
			finishedAt: 0,
			expected:   "IN_PROGRESS", // Should not be considered finished without timestamp
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			status := svc.determineBookStatus(tt.progress, tt.isFinished, tt.finishedAt)

			// Verify the result
			assert.Equal(t, tt.expected, status, "Unexpected status for test case: %s", tt.name)
		})
	}
}
