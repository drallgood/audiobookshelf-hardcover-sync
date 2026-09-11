package util

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// testLogRecorder captures log messages for testing
type testLogRecorder struct {
	msgs  []string
	mutex sync.Mutex
}

// testLogWriter is an io.Writer that captures log messages
type testLogWriter struct {
	recorder *testLogRecorder
}

// Write implements io.Writer interface
func (w *testLogWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	// Remove trailing newline if present
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	w.recorder.mutex.Lock()
	defer w.recorder.mutex.Unlock()
	w.recorder.msgs = append(w.recorder.msgs, msg)
	return len(p), nil
}

// newTestLogger creates a new logger for testing
func newTestLogger() (*logger.Logger, *testLogRecorder) {
	recorder := &testLogRecorder{
		msgs: make([]string, 0),
	}
	
	// Create a buffer to capture log output
	buf := &testLogWriter{recorder: recorder}
	
	// Reset the global logger for testing
	logger.ResetForTesting()
	
	// Configure the global logger with our test configuration
	logger.Setup(logger.Config{
		Level:      "debug",
		Format:     "json",
		Output:     buf,
		TimeFormat: time.RFC3339,
	})
	
	// Get the global logger instance
	log := logger.Get()
	
	return log, recorder
}

// hasMessage checks if a message with the given level and content was logged
func (r *testLogRecorder) hasMessage(level, msg string) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	for _, m := range r.msgs {
		// Parse the JSON log message
		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(m), &logEntry); err != nil {
			continue
		}

		// Check if the level and message match
		if logEntry["level"] == level && strings.Contains(logEntry["message"].(string), msg) {
			fmt.Printf("Found matching message: %s\n", m)
			return true
		}
	}
	return false
}

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
	log, _ := newTestLogger()
	rl := NewRateLimiter(5*time.Millisecond, 5, 3, log)
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

func TestRateLimiterOnRateLimit(t *testing.T) {
	// Create a test logger
	log, _ := newTestLogger()
	rl := NewRateLimiter(100*time.Millisecond, 1, 1, log)
	defer rl.ResetRate()

	// First request should succeed
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := rl.Wait(ctx)
	require.NoError(t, err)

	// Trigger rate limiting with a shorter backoff for testing
	retryAfter := rl.OnRateLimit(100 * time.Millisecond)
	assert.Greater(t, retryAfter, time.Duration(0), "Expected positive retry after duration")

	// Next wait should be delayed due to rate limiting
	start := time.Now()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	err = rl.Wait(ctx2)
	elapsed := time.Since(start)

	assert.NoError(t, err, "Wait should complete without error")
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "Expected delay after rate limiting")
}

func TestRateLimiterResetRate(t *testing.T) {
	rl := NewRateLimiter(time.Second, 1, 1, nil)
	defer rl.ResetRate()

	// Change rate and burst
	rl.SetBackoffFactor(2.0)
	rl.ResetRate()

	// Verify reset preserves the configured base rate.
	assert.Equal(t, time.Second, rl.GetRate())
	assert.Equal(t, DefaultBackoffFactor, rl.backoffFactor)
}

func TestRateLimiterGetMetrics(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 5, 10, nil)
	defer rl.ResetRate()

	// Take some tokens
	err := rl.Wait(context.Background())
	require.NoError(t, err)

	// Get metrics
	metrics := rl.GetMetrics()

	// Verify metrics
	assert.Greater(t, metrics.Requests, uint64(0))
	assert.Equal(t, "100ms", metrics.CurrentRate)
}

func TestRateLimiterWithRateLimitHeaders(t *testing.T) {
	tests := []struct {
		name          string
		headers       map[string]string
		expectBackoff bool
	}{
		{
			name: "rate limit headers",
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Limit":     "100",
				"Retry-After":           "1",
			},
			expectBackoff: true,
		},
		{
			name: "no rate limit",
			headers: map[string]string{
				"X-RateLimit-Remaining": "10",
				"X-RateLimit-Limit":     "100",
			},
			expectBackoff: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new test logger for each test case to isolate them
			log, logRecorder := newTestLogger()
			rl := NewRateLimiter(100*time.Millisecond, 5, 10, log)
			defer rl.ResetRate()

			resp := &http.Response{
				Header: make(http.Header),
			}

			// Set headers
			for k, v := range tt.headers {
				resp.Header.Set(k, v)
			}

			// Process headers
			rl.WithRateLimitHeaders(resp)

			// Debug: Print all logged messages
			for _, msg := range logRecorder.msgs {
				t.Logf("Logged message: %s", msg)
			}

			// Verify behavior
			if tt.expectBackoff {
				// Check for the exact log message that's being generated
				assert.True(t, logRecorder.hasMessage("warn", "Rate limit error with retry-after header"), 
					"Expected rate limit warning")
			} else {
				// For the no rate limit case, we should not see the rate limit error message
				assert.False(t, logRecorder.hasMessage("warn", "Rate limit error with retry-after header"), 
					"Unexpected rate limit warning")
			}
		})
	}
}
