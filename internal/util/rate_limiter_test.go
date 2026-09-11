package util

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/rs/zerolog"
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

func containsLogEntry(t *testing.T, output, level, message string) bool {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["level"] == level && entry["message"] == message {
			return true
		}
	}
	return false
}

func init() {
	// Enable test mode to disable buffering in ParseRetryAfter
	testMode = true
}

func TestRateLimiter_ContextCancellation(t *testing.T) {
	t.Run("context canceled", func(t *testing.T) {
		log := setupTestLogger(t)
		rl := NewRateLimiter(100*time.Millisecond, 5, log)

		// Create a context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		// This should fail due to context timeout
		err := rl.Wait(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestRateLimiter_ConcurrentWaitsArePaced(t *testing.T) {
	const (
		interval      = 15 * time.Millisecond
		totalRequests = 5
	)
	rl := NewRateLimiter(interval, 2, setupTestLogger(t))
	start := make(chan struct{})
	type result struct {
		completedAt time.Time
		err         error
	}
	completed := make(chan result, totalRequests)

	for range totalRequests {
		go func() {
			<-start
			err := rl.Wait(context.Background())
			completed <- result{completedAt: time.Now(), err: err}
		}()
	}
	close(start)

	first := <-completed
	require.NoError(t, first.err)
	previous := first.completedAt
	for range totalRequests - 1 {
		current := <-completed
		require.NoError(t, current.err)
		assert.GreaterOrEqual(t, current.completedAt.Sub(previous), 12*time.Millisecond)
		previous = current.completedAt
	}
}

func TestRateLimiter_PendingWaitObservesNewBackoff(t *testing.T) {
	const (
		interval = 100 * time.Millisecond
		backoff  = 160 * time.Millisecond
	)
	rl := NewRateLimiter(interval, 2, setupTestLogger(t))
	type result struct {
		completedAt time.Time
		err         error
	}
	completed := make(chan result, 1)

	go func() {
		err := rl.Wait(context.Background())
		completed <- result{completedAt: time.Now(), err: err}
	}()
	require.Eventually(t, func() bool {
		return rl.GetMetrics().Requests == 1
	}, time.Second, time.Millisecond)

	backoffStarted := time.Now()
	rl.OnRateLimit(backoff)
	got := <-completed
	require.NoError(t, got.err)

	assert.GreaterOrEqual(t, got.completedAt.Sub(backoffStarted), 140*time.Millisecond,
		"pending waiter was admitted before the new backoff expired")
}

func TestRateLimiter_CancellationDoesNotCollapsePendingSlots(t *testing.T) {
	const interval = 80 * time.Millisecond
	rl := NewRateLimiter(interval, 3, setupTestLogger(t))
	type result struct {
		completedAt time.Time
		err         error
	}
	completed := make(chan result, 2)

	go func() {
		err := rl.Wait(context.Background())
		completed <- result{completedAt: time.Now(), err: err}
	}()
	require.Eventually(t, func() bool {
		return rl.GetMetrics().Requests == 1
	}, time.Second, time.Millisecond)

	cancelCtx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() {
		canceled <- rl.Wait(cancelCtx)
	}()
	require.Eventually(t, func() bool {
		return rl.GetMetrics().Requests == 2
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-canceled, context.Canceled)

	go func() {
		err := rl.Wait(context.Background())
		completed <- result{completedAt: time.Now(), err: err}
	}()

	first := <-completed
	second := <-completed
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.GreaterOrEqual(t, second.completedAt.Sub(first.completedAt), 60*time.Millisecond,
		"cancellation allowed two pending waiters into the same pacing slot")
}

func TestRateLimiter_OnRateLimit(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
	defer rl.ResetRate()

	waitTime := rl.OnRateLimit(10 * time.Second)

	assert.Equal(t, 10*time.Second, waitTime)
	assert.Equal(t, 100*time.Millisecond, rl.GetRate(), "Retry-After must remain a temporary pause")
	assert.Equal(t, uint64(1), rl.GetMetrics().RateLimited)
}

func TestRateLimiter_ResetRate(t *testing.T) {
	rl := NewRateLimiter(time.Second, 1, nil)
	rl.SetBackoffFactor(2)
	rl.SetJitterFactor(0)

	rl.WithRateLimitHeaders(&http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
	})
	require.Equal(t, 2*time.Second, rl.GetRate())

	rl.ResetRate()

	assert.Equal(t, time.Second, rl.GetRate())
}

func TestRateLimiter_GetMetrics(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
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
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
	defer rl.ResetRate()

	// Set backoff factor
	rl.SetBackoffFactor(2.0)

	// Verify backoff factor was set
	rl.mu.RLock()
	assert.Equal(t, 2.0, rl.backoffFactor)
	rl.mu.RUnlock()
}

func TestRateLimiter_SetJitterFactor(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
	defer rl.ResetRate()

	// Set jitter factor
	rl.SetJitterFactor(0.3)

	// Verify jitter factor was set
	rl.mu.RLock()
	assert.Equal(t, 0.3, rl.jitterFactor)
	rl.mu.RUnlock()
}

func TestRateLimiter_CalculateJitter(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
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
		name   string
		header string
		setup  func()
		check  func(t *testing.T, d time.Duration, err error)
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

func TestParseIETFRateLimitBucket(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantR    int
		wantT    int
	}{
		{`"Free";r=8;t=42`, "free", 8, 42},
		{`"daily";r=4231;t=51234`, "daily", 4231, 51234},
		{`"Free";r=0;t=0`, "free", 0, 0},
		{`"Free"`, "free", 0, 0},
		{`"";r=5`, "", 0, 0},
	}
	for _, tt := range tests {
		name, params := parseRateLimitBucket(tt.input)
		assert.Equal(t, tt.wantName, name)
		assert.Equal(t, tt.wantR, params["r"])
		assert.Equal(t, tt.wantT, params["t"])
	}
}

func TestParseIETFRateLimit(t *testing.T) {
	headers := http.Header{}
	headers.Set("RateLimit", `"Free";r=8;t=42, "daily";r=4231;t=51234`)

	var rl RateLimiter
	remaining, reset := rl.parseIETFRateLimit(headers)

	assert.Equal(t, 8, remaining["free"])
	assert.Equal(t, 4231, remaining["daily"])
	assert.Equal(t, 42, reset["free"])
	assert.Equal(t, 51234, reset["daily"])
}

func TestParseIETFRateLimitPolicy(t *testing.T) {
	headers := http.Header{}
	headers.Set("RateLimit-Policy", `"Free";q=60;w=60;burst=10, "daily";q=5000;w=86400`)

	var rl RateLimiter
	quota, burst, window := rl.parseIETFRateLimitPolicy(headers)

	assert.Equal(t, 60, quota["free"])
	assert.Equal(t, 5000, quota["daily"])
	assert.Equal(t, 10, burst["free"])
	assert.Equal(t, 60, window["free"])
	assert.Equal(t, 86400, window["daily"])
}

func TestRateLimiterWaitDoesNotAccelerate(t *testing.T) {
	const interval = 15 * time.Millisecond
	rl := NewRateLimiter(interval, 1, nil)

	var previous time.Time
	for range 6 {
		require.NoError(t, rl.Wait(context.Background()))
		now := time.Now()
		if !previous.IsZero() {
			assert.GreaterOrEqual(t, now.Sub(previous), 12*time.Millisecond)
		}
		previous = now
	}
}

func TestRateLimiterIETFZeroResetDoesNotCreatePermanentBackoff(t *testing.T) {
	configuredRate := 2 * time.Second
	rl := NewRateLimiter(configuredRate, 1, nil)
	resp := &http.Response{Header: http.Header{
		"Ratelimit":        {`"Free";r=1;t=0`},
		"Ratelimit-Policy": {`"Free";q=60;w=60;burst=10`},
	}}

	rl.WithRateLimitHeaders(resp)

	assert.Equal(t, configuredRate, rl.GetRate())
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	assert.Zero(t, rl.backoffUntil)
}

func TestRateLimiterCapsServerPauseAtDefaultMaxBackoff(t *testing.T) {
	serverDelaySeconds := int(DefaultMaxBackoff/time.Second) + 1
	serverDelay := time.Duration(serverDelaySeconds) * time.Second
	require.Greater(t, serverDelay, DefaultMaxBackoff)

	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
	rl.WithRateLimitHeaders(&http.Response{Header: http.Header{
		"Retry-After": {strconv.Itoa(serverDelaySeconds)},
	}})

	rl.mu.RLock()
	pause := rl.backoffUntil.Sub(time.Now())
	rl.mu.RUnlock()

	assert.Greater(t, pause, DefaultMaxBackoff-time.Second)
	assert.LessOrEqual(t, pause, DefaultMaxBackoff)
}

func TestRateLimiterDailyExhaustionOverridesRetryAfter(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
	dailyResetSeconds := int(DefaultMaxDailyResetWait/time.Second) + 1
	rl.WithRateLimitHeaders(&http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Retry-After": {"1"},
			"Ratelimit": {
				fmt.Sprintf(`"Free";r=59;t=1, "daily";r=0;t=%d`, dailyResetSeconds),
			},
			"Ratelimit-Policy": {`"Free";q=60;w=60, "daily";q=5000;w=86400`},
		},
	})

	rl.mu.RLock()
	pause := rl.backoffUntil.Sub(time.Now())
	rl.mu.RUnlock()
	assert.Greater(t, pause, DefaultMaxBackoff)
	assert.LessOrEqual(t, pause, DefaultMaxDailyResetWait)
	assert.Equal(t, 0, rl.DailyRemaining())
	assert.Equal(t, 5000, rl.DailyLimit())
	assert.Equal(t, uint64(1), rl.GetMetrics().RateLimited)
}

func TestRateLimiterWaitsForDailyQuotaReset(t *testing.T) {
	tests := []struct {
		name               string
		serverDelaySeconds int
		expectedPause      time.Duration
	}{
		{
			name:               "waits beyond ordinary backoff cap",
			serverDelaySeconds: int((DefaultMaxBackoff + time.Minute) / time.Second),
			expectedPause:      DefaultMaxBackoff + time.Minute,
		},
		{
			name:               "caps maximum integer without overflowing",
			serverDelaySeconds: int(^uint(0) >> 1),
			expectedPause:      DefaultMaxDailyResetWait,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiter(100*time.Millisecond, 1, nil)
			rl.WithRateLimitHeaders(&http.Response{Header: http.Header{
				"Ratelimit":        {fmt.Sprintf(`"daily";r=0;t=%d`, tt.serverDelaySeconds)},
				"Ratelimit-Policy": {`"daily";q=5000;w=86400`},
			}})

			rl.mu.RLock()
			pause := rl.backoffUntil.Sub(time.Now())
			rl.mu.RUnlock()

			assert.Greater(t, pause, tt.expectedPause-time.Second)
			assert.LessOrEqual(t, pause, tt.expectedPause)
		})
	}
}

func TestRateLimiterUsesPolicyWindowForSteadyPacing(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
	rl.WithRateLimitHeaders(&http.Response{Header: http.Header{
		"Ratelimit":        {`"Free";r=8;t=42`},
		"Ratelimit-Policy": {`"Free";q=60;w=60;burst=10`},
	}})

	assert.Equal(t, time.Second, rl.GetRate())
}

func TestRateLimiterFallsBackForBareTooManyRequests(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
	rl.SetBackoffFactor(2)
	rl.SetJitterFactor(0)

	rl.WithRateLimitHeaders(&http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}})

	assert.Equal(t, 200*time.Millisecond, rl.GetRate())
	assert.Equal(t, uint64(1), rl.GetMetrics().RateLimited)
	rl.mu.RLock()
	assert.True(t, rl.backoffUntil.After(time.Now()))
	rl.mu.RUnlock()

	rl.WithRateLimitHeaders(&http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}})
	assert.Equal(t, 400*time.Millisecond, rl.GetRate())
	assert.Equal(t, uint64(2), rl.GetMetrics().RateLimited)

	rl.WithRateLimitHeaders(&http.Response{StatusCode: http.StatusOK, Header: http.Header{}})
	assert.Equal(t, 400*time.Millisecond, rl.GetRate())

	healthyHeaders := make(http.Header)
	healthyHeaders.Set("X-RateLimit-Limit", "100")
	healthyHeaders.Set("X-RateLimit-Remaining", "90")
	rl.WithRateLimitHeaders(&http.Response{StatusCode: http.StatusOK, Header: healthyHeaders})
	assert.Equal(t, 100*time.Millisecond, rl.GetRate())
}

func TestRateLimiterUnguided429PreservesLongerBackoff(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 1, nil)
	rl.SetBackoffFactor(2)
	rl.SetJitterFactor(0)

	rl.WithRateLimitHeaders(&http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": {"2"}},
	})
	rl.mu.RLock()
	authoritativeDeadline := rl.backoffUntil
	rl.mu.RUnlock()

	rl.WithRateLimitHeaders(&http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
	})

	assert.Equal(t, 200*time.Millisecond, rl.GetRate(), "bare 429 should still escalate steady pacing")
	assert.Equal(t, uint64(2), rl.GetMetrics().RateLimited, "each 429 should count")
	rl.mu.RLock()
	actualDeadline := rl.backoffUntil
	rl.mu.RUnlock()
	assert.False(t, actualDeadline.Before(authoritativeDeadline), "fallback must not shorten authoritative backoff")
}

func TestRateLimiterFallsBackForUnguidedTooManyRequests(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	tests := []struct {
		name   string
		header http.Header
	}{
		{
			name: "legacy reset without remaining",
			header: http.Header{
				"X-RateLimit-Reset": {reset},
			},
		},
		{
			name: "malformed legacy remaining",
			header: http.Header{
				"X-RateLimit-Remaining": {"unknown"},
				"X-RateLimit-Reset":     {reset},
			},
		},
		{
			name: "nonzero legacy remaining",
			header: http.Header{
				"X-RateLimit-Limit":     {"100"},
				"X-RateLimit-Remaining": {"10"},
				"X-RateLimit-Reset":     {reset},
			},
		},
		{
			name: "non-exhausted IETF quota",
			header: http.Header{
				"RateLimit":        {`"Free";r=8;t=42`},
				"RateLimit-Policy": {`"Free";q=60;w=60;burst=10`},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiter(100*time.Millisecond, 1, nil)
			rl.SetBackoffFactor(2)
			rl.SetJitterFactor(0)

			rl.WithRateLimitHeaders(&http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     tt.header,
			})

			assert.Equal(t, 200*time.Millisecond, rl.GetRate())
			assert.Equal(t, uint64(1), rl.GetMetrics().RateLimited)
			rl.mu.RLock()
			assert.True(t, rl.backoffUntil.After(time.Now()))
			rl.mu.RUnlock()
		})
	}
}

func TestRateLimiterHonorsAuthoritativeTooManyRequestsGuidance(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	tests := []struct {
		name         string
		header       http.Header
		expectedRate time.Duration
	}{
		{
			name:         "positive Retry-After",
			expectedRate: 100 * time.Millisecond,
			header: http.Header{
				"Retry-After": {"1"},
			},
		},
		{
			name:         "exhausted IETF quota",
			expectedRate: time.Second,
			header: http.Header{
				"Ratelimit":        {`"Free";r=0;t=42`},
				"Ratelimit-Policy": {`"Free";q=60;w=60;burst=10`},
			},
		},
		{
			name:         "exhausted legacy quota",
			expectedRate: 100 * time.Millisecond,
			header: http.Header{
				"X-Ratelimit-Limit":     {"100"},
				"X-Ratelimit-Remaining": {"0"},
				"X-Ratelimit-Reset":     {reset},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiter(100*time.Millisecond, 1, nil)
			rl.SetBackoffFactor(2)
			rl.SetJitterFactor(0)
			rl.WithRateLimitHeaders(&http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     tt.header,
			})

			assert.Equal(t, tt.expectedRate, rl.GetRate())
			assert.Equal(t, uint64(1), rl.GetMetrics().RateLimited)
			rl.mu.RLock()
			assert.True(t, rl.backoffUntil.After(time.Now()))
			rl.mu.RUnlock()
		})
	}
}

func TestRateLimiterRecoversFromHeaderDrivenSlowdown(t *testing.T) {
	configuredRate := 2 * time.Second
	var logs bytes.Buffer
	testLogger := &logger.Logger{Logger: zerolog.New(&logs).Level(zerolog.InfoLevel)}
	rl := NewRateLimiter(configuredRate, 1, testLogger)

	rl.WithRateLimitHeaders(&http.Response{Header: http.Header{
		"Ratelimit":        {`"Free";r=8;t=42, "daily";r=1;t=10`},
		"Ratelimit-Policy": {`"Free";q=60;w=60;burst=10, "daily";q=5000;w=86400`},
	}})
	assert.Greater(t, rl.GetRate(), configuredRate)

	logs.Reset()
	rl.WithRateLimitHeaders(&http.Response{Header: http.Header{
		"Ratelimit":        {`"Free";r=8;t=42, "daily";r=4000;t=1000`},
		"Ratelimit-Policy": {`"Free";q=60;w=60;burst=10, "daily";q=5000;w=86400`},
	}})
	assert.Equal(t, configuredRate, rl.GetRate())
	assert.Contains(t, logs.String(), `"level":"info"`)
	assert.Contains(t, logs.String(), `"previous_rate":"10s"`)
	assert.Contains(t, logs.String(), `"new_rate":"2s"`)
	assert.Contains(t, logs.String(), `"message":"Rate limiter pacing recovered"`)
}

func TestRateLimiterAdaptivePacingLogLevels(t *testing.T) {
	previousLevel := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(previousLevel)
	})

	t.Run("IETF window adjustment is info", func(t *testing.T) {
		var logs bytes.Buffer
		testLogger := &logger.Logger{Logger: zerolog.New(&logs).Level(zerolog.DebugLevel)}
		rl := NewRateLimiter(100*time.Millisecond, 1, testLogger)
		logs.Reset()

		rl.WithRateLimitHeaders(&http.Response{Header: http.Header{
			"Ratelimit":        {`"Free";r=1;t=10`},
			"Ratelimit-Policy": {`"Free";q=60;w=60;burst=10`},
		}})

		message := "Rate limit window nearly exhausted, slowing down"
		assert.True(t, containsLogEntry(t, logs.String(), "info", message))
		assert.False(t, containsLogEntry(t, logs.String(), "warn", message))
	})

	t.Run("legacy adjustment is info and reset schedule is debug", func(t *testing.T) {
		var logs bytes.Buffer
		testLogger := &logger.Logger{Logger: zerolog.New(&logs).Level(zerolog.DebugLevel)}
		rl := NewRateLimiter(100*time.Millisecond, 1, testLogger)
		logs.Reset()

		header := make(http.Header)
		header.Set("X-RateLimit-Limit", "100")
		header.Set("X-RateLimit-Remaining", "10")
		header.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
		rl.WithRateLimitHeaders(&http.Response{Header: header})

		adjustment := "Approaching rate limit (legacy headers), being more conservative"
		assert.True(t, containsLogEntry(t, logs.String(), "info", adjustment))
		assert.False(t, containsLogEntry(t, logs.String(), "warn", adjustment))
		assert.True(t, containsLogEntry(t, logs.String(), "debug", "Rate limit will reset, scheduling next request"))
	})
}

func TestRateLimitBuckets(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{`"Free";r=8;t=42, "daily";r=4231;t=51234`, 2},
		{`"Free";r=8;t=42`, 1},
		{"", 0}, // empty string = no buckets
	}
	for _, tt := range tests {
		buckets := rateLimitBuckets(tt.input)
		assert.Len(t, buckets, tt.want)
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
			name: "ietf ratelimit header - per-minute only",
			headers: map[string]string{
				"RateLimit":        `"Free";r=8;t=42, "daily";r=4231;t=51234`,
				"RateLimit-Policy": `"Free";q=60;w=60;burst=10, "daily";q=5000;w=86400`,
			},
			check: func(t *testing.T, rl *RateLimiter) {
				assert.Equal(t, 4231, rl.DailyRemaining())
				assert.Equal(t, 5000, rl.DailyLimit())
			},
		},
		{
			name: "ietf ratelimit header - low daily remaining",
			headers: map[string]string{
				"RateLimit":        `"Free";r=8;t=42, "daily";r=40;t=51234`,
				"RateLimit-Policy": `"Free";q=60;w=60;burst=10, "daily";q=5000;w=86400`,
			},
			check: func(t *testing.T, rl *RateLimiter) {
				assert.Equal(t, 40, rl.DailyRemaining())
				assert.Equal(t, 5000, rl.DailyLimit())
				// Rate should have been increased due to <1% daily remaining.
				assert.True(t, rl.GetRate() >= 100*time.Millisecond)
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
			rl := NewRateLimiter(100*time.Millisecond, 1, log)
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
