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

func TestRateLimiterConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(5*time.Millisecond, 3, nil)
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
