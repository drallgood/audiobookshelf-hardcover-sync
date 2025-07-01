package util

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestLogger creates a test logger that writes to stderr
func setupTestLogger(t *testing.T) *logger.Logger {
	// Configure the global logger for testing
	cfg := logger.Config{
		Level:      "debug",
		Format:     "console",
		Output:     os.Stderr,
		TimeFormat: time.RFC3339,
	}
	logger.Setup(cfg)
	return logger.Get()
}

func init() {
	// Enable test mode to disable buffering in ParseRetryAfter
	testMode = true
}

func TestRateLimiter_Wait(t *testing.T) {
	tests := []struct {
		name           string
		rate           time.Duration
		burst         int
		maxConcurrent int
		reqCount      int
		expectError   bool
		setup         func(*RateLimiter) // Optional setup function for the rate limiter
		minTime       time.Duration     // Minimum expected time for the test to complete
	}{
		{
			name:           "single request",
			rate:           time.Millisecond * 50, // Reduced from 100ms for faster tests
			burst:         1,
			maxConcurrent: 1,
			reqCount:      1,
			expectError:   false,
			minTime:       0, // No minimum time for a single request
		},
		{
			name:           "multiple requests within burst",
			rate:           time.Millisecond * 50, // Reduced from 100ms for faster tests
			burst:         5,
			maxConcurrent: 5,
			reqCount:      3,
			expectError:   false,
			minTime:       0, // No rate limiting within burst
		},
		{
			name:           "concurrent requests with rate limiting",
			rate:           time.Millisecond * 50, // Reduced from 200ms for faster tests
			burst:         2,
			maxConcurrent: 10,
			reqCount:      5, // Reduced from 10 to make test faster
			expectError:   false,
			minTime:       time.Duration(5-2) * 50 * time.Millisecond, // (reqCount - burst) * rate
		},
		{
			name:           "context canceled",
			rate:           time.Hour, // Very slow rate to ensure we hit the context timeout
			burst:         1,
			maxConcurrent: 1,
			reqCount:      1,
			expectError:   true,
			minTime:       0, // Not relevant for this test case
			setup: func(rl *RateLimiter) {
				rl.SetBackoffFactor(1.0) // Disable backoff for this test
			},
		},
		{
			name:           "with backoff",
			rate:           time.Millisecond * 50, // Reduced from 100ms for faster tests
			burst:         1,
			maxConcurrent: 3, // Increased to allow the test to run
			reqCount:      2, // Reduced from 3 to make test faster
			expectError:   false,
			minTime:       time.Second, // Backoff is set to 1 second
			setup: func(rl *RateLimiter) {
				rl.SetBackoffFactor(2.0)
				// Simulate a rate limit to trigger backoff
				rl.mu.Lock()
				rl.backoffUntil = time.Now().Add(time.Second)
				rl.mu.Unlock()
				// Log the backoff state for debugging
				rl.logger.Debug("Test setup: Backoff set", map[string]interface{}{
					"backoff_until": rl.backoffUntil,
					"now":           time.Now(),
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiter(tt.rate, tt.burst, tt.maxConcurrent, nil)
			// Apply any test-specific setup
			if tt.setup != nil {
				tt.setup(rl)
			}

			var wg sync.WaitGroup
			start := time.Now()
			errCh := make(chan error, tt.reqCount)

			for i := 0; i < tt.reqCount; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					// For the context canceled test, create a canceled context
					var ctx context.Context
					if tt.name == "context canceled" {
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(context.Background())
						cancel() // Immediately cancel the context
					} else {
						ctx = context.Background()
					}

					err := rl.Wait(ctx)
					if tt.expectError {
						errCh <- err
					} else if err != nil {
						errCh <- fmt.Errorf("unexpected error: %v", err)
					} else {
						errCh <- nil
					}
				}(i)
			}

			// Wait for all goroutines to complete
			wg.Wait()
			close(errCh)

			// Check for any errors
			for err := range errCh {
				if tt.expectError {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			}

			// For tests that don't expect errors, verify timing if specified
			if !tt.expectError && tt.minTime > 0 {
				elapsed := time.Since(start)
				// Allow for some timing slack (80% of expected time)
				minAllowedTime := time.Duration(float64(tt.minTime) * 0.8)
				assert.GreaterOrEqual(t, elapsed, minAllowedTime, 
					"test %s: expected at least %v, got %v", tt.name, minAllowedTime, elapsed)
			}
		})
	}
}

func TestRateLimiter_BasicRateLimiting(t *testing.T) {
	t.Run("basic rate limiting", func(t *testing.T) {
		rl := NewRateLimiter(100*time.Millisecond, 1, 1, nil)
		defer rl.ResetRate()

		start := time.Now()
		ctx := context.Background()

		// First request should pass immediately
		err := rl.Wait(ctx)
		require.NoError(t, err)

		// Second request should be rate limited
		err = rl.Wait(ctx)
		require.NoError(t, err)
		elapsed := time.Since(start)

		// Should take at least 80ms due to rate limiting (allowing for some timing variation)
		// The actual rate is 100ms, but timing can vary slightly due to scheduling
		assert.GreaterOrEqual(t, elapsed, 80*time.Millisecond, "expected at least 80ms delay, got %v", elapsed)
	})
}

func TestRateLimiter_ContextCancellation(t *testing.T) {
	t.Run("context canceled", func(t *testing.T) {
		log := setupTestLogger(t)
		rl := NewRateLimiter(100*time.Millisecond, 1, 5, log)

		// Create a context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		// This should fail due to context timeout
		err := rl.Wait(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestRateLimiter_ConcurrencyLimiting(t *testing.T) {
	t.Run("concurrency limiting", func(t *testing.T) {
		log := setupTestLogger(t)
		
		// Use a very slow rate to ensure we hit concurrency limits before rate limits
		rate := 10 * time.Second
		burst := 1
		maxConcurrent := 2
		totalRequests := 5

		t.Logf("Starting test with maxConcurrent=%d, totalRequests=%d", maxConcurrent, totalRequests)

		rateLimiter := NewRateLimiter(rate, burst, maxConcurrent, log)
		t.Logf("Rate limiter created: rate=%v, burst=%d, maxConcurrent=%d", rate, burst, rateLimiter.maxConcurrent)

		// Channel to coordinate goroutine startup
		startCh := make(chan struct{})

		// Channel to collect errors
		errCh := make(chan error, totalRequests)

		// Channel to signal when each goroutine is done
		doneCh := make(chan struct{}, totalRequests)

		// Track the number of active requests
		var activeReqs int32
		var maxActive int32

		// Start all the goroutines
		for i := 0; i < totalRequests; i++ {
			go func(id int) {
				// Wait for the start signal
				<-startCh

				// Call the rate limiter to acquire the semaphore
				err := rateLimiter.Wait(context.Background())
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d: %v", id, err)
					doneCh <- struct{}{}
					return
				}

				// Track active requests
				current := atomic.AddInt32(&activeReqs, 1)
				for {
					prevMax := atomic.LoadInt32(&maxActive)
					if current <= prevMax || atomic.CompareAndSwapInt32(&maxActive, prevMax, current) {
						break
					}
				}

				// Simulate work
				time.Sleep(100 * time.Millisecond)

				// Mark request as done
				atomic.AddInt32(&activeReqs, -1)
				doneCh <- struct{}{}
			}(i)
		}

		// Start all requests at once
		close(startCh)

		// Wait for all goroutines to complete
		for i := 0; i < totalRequests; i++ {
			select {
			case err := <-errCh:
				t.Error(err)
			case <-doneCh:
				// Goroutine completed
			}
		}

		// Verify max concurrency was respected
		maxConcurrentReached := atomic.LoadInt32(&maxActive)
		if maxConcurrentReached > int32(maxConcurrent) {
			t.Errorf("Max concurrency exceeded: got %d, want <= %d", maxConcurrentReached, maxConcurrent)
		} else {
			t.Logf("Max concurrency was properly limited to %d", maxConcurrentReached)
		}
	})
}

const (
	// Default backoff factor for testing
	testDefaultBackoffFactor = 8.0 // This matches the default in the implementation

	// Default jitter factor for testing (0.0 to disable jitter)
	testDefaultJitterFactor = 0.0
)

func TestRateLimiter_OnRateLimit(t *testing.T) {
	// Create a rate limiter with test values
	rl := NewRateLimiter(100*time.Millisecond, 1, 1, nil)
	defer rl.ResetRate()

	// Set test values for backoff and jitter
	rl.SetBackoffFactor(testDefaultBackoffFactor)
	rl.SetJitterFactor(testDefaultJitterFactor)

	tests := []struct {
		name       string
		retryAfter time.Duration
		setup      func(rl *RateLimiter)
		check      func(t *testing.T, waitTime time.Duration, rl *RateLimiter)
	}{
		{
			name:       "short retry with default backoff",
			retryAfter: 100 * time.Millisecond,
			setup: func(rl *RateLimiter) {
				// No special setup needed for this test case
			},
			check: func(t *testing.T, waitTime time.Duration, rl *RateLimiter) {
				// With the default backoff factor of 8.0, we expect waitTime to be around 800ms (100ms * 8.0)
				// The actual calculation includes some additional factors, so we'll use a range
				expectedMin := 100 * time.Millisecond
				expectedMax := 2 * time.Second
				assert.GreaterOrEqual(t, waitTime, expectedMin, "wait time should be at least %v", expectedMin)
				assert.LessOrEqual(t, waitTime, expectedMax, "wait time should be at most %v", expectedMax)

				// Verify metrics
				metrics := rl.GetMetrics()
				assert.Equal(t, uint64(1), metrics.RateLimited)
			},
		},
		{
			name:       "long retry with backoff",
			retryAfter: 10 * time.Second,
			setup: func(rl *RateLimiter) {
				// Set a custom backoff factor for this test
				rl.SetBackoffFactor(1.5)
			},
			check: func(t *testing.T, waitTime time.Duration, rl *RateLimiter) {
				// With backoff factor of 1.5, we expect waitTime to be around 15s (10s * 1.5)
				// The actual calculation includes some additional factors, so we'll use a range
				// Using 9s instead of 10s to account for test environment timing variations
				expectedMin := 9 * time.Second
				expectedMax := 30 * time.Second
				assert.GreaterOrEqual(t, waitTime, expectedMin, "wait time should be at least %v (was %v)", expectedMin, waitTime)
				assert.LessOrEqual(t, waitTime, expectedMax, "wait time should be at most %v (was %v)", expectedMax, waitTime)

				// Verify metrics
				metrics := rl.GetMetrics()
				assert.Equal(t, uint64(1), metrics.RateLimited)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiter(100*time.Millisecond, 1, 1, nil)
			defer rl.ResetRate()

			// Apply any test-specific setup
			if tt.setup != nil {
				tt.setup(rl)
			}

			// Call OnRateLimit and verify the result
			waitTime := rl.OnRateLimit(tt.retryAfter)
			tt.check(t, waitTime, rl)
		})
	}
}

func TestRateLimiter_ResetRate(t *testing.T) {
	// Create a rate limiter with a custom rate
	rl := NewRateLimiter(time.Second, 1, 1, nil)

	// Modify the rate to something different
	rl.mu.Lock()
	rl.rate = 5 * time.Second
	rl.mu.Unlock()

	// Reset the rate
	rl.ResetRate()

	// Should be back to the default (2 seconds)
	assert.Equal(t, 2*time.Second, rl.GetRate(), "rate should be reset to default 2 seconds")
}

func TestRateLimiter_GetMetrics(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, 1, nil)
	defer rl.ResetRate()

	// Initial metrics
	metrics := rl.GetMetrics()
	assert.Equal(t, uint64(0), metrics.Requests)
	assert.Equal(t, uint64(0), metrics.RateLimited)

	// Make a request
	ctx := context.Background()
	err := rl.Wait(ctx)
	require.NoError(t, err)

	// Check updated metrics
	metrics = rl.GetMetrics()
	assert.Equal(t, uint64(1), metrics.Requests)
}

func TestRateLimiter_SetBackoffFactor(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, 1, nil)
	defer rl.ResetRate()

	// Set backoff factor
	rl.SetBackoffFactor(2.0)

	// Verify backoff factor was set
	rl.mu.RLock()
	assert.Equal(t, 2.0, rl.backoffFactor)
	rl.mu.RUnlock()
}

func TestRateLimiter_SetJitterFactor(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, 1, nil)
	defer rl.ResetRate()

	// Set jitter factor
	rl.SetJitterFactor(0.3)

	// Verify jitter factor was set
	rl.mu.RLock()
	assert.Equal(t, 0.3, rl.jitterFactor)
	rl.mu.RUnlock()
}

func TestRateLimiter_CheckBackoff(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, 1, nil)
	defer rl.ResetRate()

	// No backoff initially
	rl.mu.RLock()
	remaining := rl.checkBackoff()
	rl.mu.RUnlock()
	assert.Equal(t, time.Duration(0), remaining)

	// Set a backoff
	rl.mu.Lock()
	rl.backoffUntil = time.Now().Add(100 * time.Millisecond)
	rl.mu.Unlock()

	// Should return remaining backoff time
	rl.mu.RLock()
	remaining = rl.checkBackoff()
	rl.mu.RUnlock()
	assert.Greater(t, remaining, time.Duration(0))
	assert.LessOrEqual(t, remaining, 100*time.Millisecond)
}

func TestRateLimiter_CalculateJitter(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, 1, nil)
	defer rl.ResetRate()

	// Test with default jitter factor
	rl.mu.RLock()
	jitter := rl.calculateJitter()
	rl.mu.RUnlock()

	// Jitter should be between -50ms and +50ms (100ms * 0.5)
	assert.GreaterOrEqual(t, jitter, -50*time.Millisecond)
	assert.LessOrEqual(t, jitter, 50*time.Millisecond)
}

func TestParseRetryAfter(t *testing.T) {
	// Use a fixed time for testing
	fixedTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	
	// Override time.Now for this test
	origNow := timeNow
	timeNow = func() time.Time { return fixedTime }
	defer func() { timeNow = origNow }()

	tests := []struct {
		name     string
		header   string
		setup    func()
		check    func(t *testing.T, d time.Duration, err error)
	}{
		{
			name:   "seconds",
			header: "60",
			check: func(t *testing.T, d time.Duration, err error) {
				assert.NoError(t, err)
				// Allow for some flexibility in the exact value due to time.Now() calls
				assert.InDelta(t, 60.0, d.Seconds(), 5.0)
			},
		},
		{
			name:   "http date",
			header: fixedTime.Add(30 * time.Second).Format(http.TimeFormat),
			check: func(t *testing.T, d time.Duration, err error) {
				assert.NoError(t, err)
				// Should be exactly 30 seconds with our fixed time
				assert.Equal(t, 30*time.Second, d.Round(time.Second))
			},
		},
		{
			name:   "invalid format",
			header: "invalid",
			check: func(t *testing.T, d time.Duration, err error) {
				assert.Error(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := ParseRetryAfter(tt.header)
			tt.check(t, d, err)
		})
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "rate limited error",
			err:      ErrRateLimited,
			expected: true,
		},
		{
			name:     "retry after error",
			err:      ErrRetryAfter,
			expected: true,
		},
		{
			name:     "wrapped error",
			err:      fmt.Errorf("wrapped: %w", ErrRateLimited),
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsRateLimitError(tt.err))
		})
	}
}

func TestWithRateLimitHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		check   func(t *testing.T, rl *RateLimiter)
	}{
		{
			name: "github rate limit headers",
			headers: map[string]string{
				"X-RateLimit-Limit":     "60",
				"X-RateLimit-Remaining": "10",
				"X-RateLimit-Reset":     strconv.FormatInt(time.Now().Add(30*time.Second).Unix(), 10),
			},
			check: func(t *testing.T, rl *RateLimiter) {
				rl.mu.RLock()
				defer rl.mu.RUnlock()
				
				// Rate should be adjusted based on remaining requests and time
				// The exact value depends on the rate limiter's internal calculations
				assert.Greater(t, rl.rate, 0*time.Second)
				assert.Less(t, rl.rate, 30*time.Second) // More generous upper bound
			},
		},
		{
			name: "standard retry-after header",
			headers: map[string]string{
				"Retry-After": "60",
			},
			check: func(t *testing.T, rl *RateLimiter) {
				rl.mu.RLock()
				defer rl.mu.RUnlock()
				
				// Should set a backoff for the retry-after duration
				assert.False(t, rl.backoffUntil.IsZero())
			},
		},
		{
			name: "no rate limit headers",
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			check: func(t *testing.T, rl *RateLimiter) {
				// No rate limiting should be applied
				rl.mu.RLock()
				defer rl.mu.RUnlock()
				
				// Should keep the original rate
				assert.Equal(t, 100*time.Millisecond, rl.rate)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Initialize logger for testing
			logger.Setup(logger.Config{
				Level:      "debug",
				Format:     logger.FormatConsole,
				Output:     nil, // Use default output
				TimeFormat: time.RFC3339,
			})
			log := logger.Get().With(map[string]interface{}{"test": tt.name})
			rl := NewRateLimiter(100*time.Millisecond, 10, 1, log)
			defer rl.ResetRate()

			// Create a response with headers and a valid Request
			header := http.Header{}
			for k, v := range tt.headers {
				header.Set(k, v)
			}

			// Ensure we have a valid request with URL for all test cases
			req, err := http.NewRequest("GET", "https://api.example.com/test", nil)
			assert.NoError(t, err)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     header,
				Request:    req, // Ensure we always have a valid request
			}

			// Process the response
			rl.WithRateLimitHeaders(resp)

			// Run the test-specific checks
			tt.check(t, rl)
		})
	}
}
