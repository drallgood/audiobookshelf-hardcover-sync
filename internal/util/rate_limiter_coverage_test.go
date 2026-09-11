package util

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRateLimiter(t *testing.T) {
	tests := []struct {
		name          string
		rate          time.Duration
		burst         int
		maxConcurrent int
		expectPanic   bool
	}{
		{
			name:          "default values",
			rate:          0,
			burst:         0,
			maxConcurrent: 0,
			expectPanic:   false,
		},
		{
			name:          "custom values",
			rate:          time.Second,
			burst:         5,
			maxConcurrent: 10,
			expectPanic:   false,
		},
		{
			name:          "negative rate uses default",
			rate:          -1 * time.Second,
			burst:         5,
			maxConcurrent: 10,
			expectPanic:   false, // Negative rate is handled by using default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectPanic {
				assert.Panics(t, func() {
					NewRateLimiter(tt.rate, tt.burst, tt.maxConcurrent, nil)
				}, "Expected panic for invalid values")
				return
			}

			rl := NewRateLimiter(tt.rate, tt.burst, tt.maxConcurrent, nil)
			defer rl.ResetRate()

			if tt.rate > 0 {
				assert.Equal(t, tt.rate, rl.GetRate())
			} else {
				assert.Equal(t, DefaultRate, rl.GetRate())
			}

			if tt.burst > 0 {
				assert.Greater(t, rl.maxTokens, 0)
			} else {
				assert.Equal(t, DefaultBurst, rl.maxTokens)
			}
		})
	}
}

func TestRateLimiterConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(5*time.Millisecond, 5, 3, nil)
	defer rl.ResetRate()

	const totalRequests = 10
	var wg sync.WaitGroup
	startCh := make(chan struct{})
	errCh := make(chan error, totalRequests)

	for range totalRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			errCh <- rl.Wait(ctx)
		}()
	}

	close(startCh)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	assert.Equal(t, uint64(totalRequests), rl.GetMetrics().Requests)
}
