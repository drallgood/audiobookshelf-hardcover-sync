package util

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRateLimiter(t *testing.T) {
	tests := []struct {
		name          string
		rate          time.Duration
		maxConcurrent int
	}{
		{
			name:          "default values",
			rate:          0,
			maxConcurrent: 0,
		},
		{
			name:          "custom values",
			rate:          time.Second,
			maxConcurrent: 10,
		},
		{
			name:          "negative rate uses default",
			rate:          -1 * time.Second,
			maxConcurrent: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiter(tt.rate, tt.maxConcurrent, nil)
			defer rl.ResetRate()

			if tt.rate > 0 {
				assert.Equal(t, tt.rate, rl.GetRate())
			} else {
				assert.Equal(t, DefaultRate, rl.GetRate())
			}
		})
	}
}

func TestRateLimiterAcquireBlocksUntilRelease(t *testing.T) {
	rl := NewRateLimiter(time.Nanosecond, 1, nil)
	firstRelease, err := rl.Acquire(context.Background())
	require.NoError(t, err)
	released := false
	t.Cleanup(func() {
		if !released {
			firstRelease()
		}
	})

	secondResult := make(chan error, 1)
	go func() {
		secondRelease, err := rl.Acquire(context.Background())
		if err == nil {
			secondRelease()
		}
		secondResult <- err
	}()

	select {
	case err := <-secondResult:
		t.Fatalf("second admission completed while first permit was held: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	firstRelease()
	released = true
	select {
	case err := <-secondResult:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("second admission did not complete after first permit was released")
	}
}
