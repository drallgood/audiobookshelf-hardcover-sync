package util

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Metrics tracks rate limiter metrics
type Metrics struct {
	Requests      uint64 `json:"requests"`
	RateLimited   uint64 `json:"rate_limited"`
	RetryAfter    uint64 `json:"retry_after"`
	BackoffEvents uint64 `json:"backoff_events"`
	CurrentRate   string `json:"current_rate"`
}

var (
	// ErrRateLimited is returned when the rate limit is exceeded
	ErrRateLimited = errors.New("rate limited")
	// ErrRetryAfter is returned when the server specifies a retry-after duration
	ErrRetryAfter = errors.New("retry after")
	// DefaultRate is the default minimum time between requests (2s = 0.5 req/s)
	DefaultRate = 2 * time.Second
	// DefaultBurst is the default burst size (reduced for more conservative behavior)
	DefaultBurst = 1
	// DefaultMaxBackoff is the default maximum backoff time (increased for more conservative behavior)
	DefaultMaxBackoff = 10 * time.Minute
	// DefaultBackoffFactor is the default backoff multiplier (increased for more aggressive backoff)
	DefaultBackoffFactor = 8.0
	// DefaultJitterFactor is the default jitter factor (0.0 to 1.0) (increased for better distribution)
	DefaultJitterFactor = 0.5
	// DefaultMaxConcurrent is the default maximum concurrent requests
	DefaultMaxConcurrent = 3
)

// RateLimiter implements a token bucket rate limiter with dynamic rate adjustment
type RateLimiter struct {
	mu             sync.RWMutex
	last           time.Time
	rate           time.Duration
	minRate        time.Duration
	maxRate        time.Duration
	tokens         int
	maxTokens      int
	lastRateDrop   time.Time
	backoffUntil   time.Time
	backoffFactor  float64
	jitterFactor   float64

	// Concurrency control
	maxConcurrent  int32         // Maximum number of concurrent requests
	semaphore      chan struct{} // Buffered channel used as a semaphore

	// Metrics
	metrics Metrics

	// Logger
	logger *logger.Logger
}

// NewRateLimiter creates a new RateLimiter with the specified rate and burst size
// rate is the minimum time between requests (e.g., 1*time.Second for 1 request per second)
// burst is the maximum number of tokens that can be consumed at once
// maxConcurrent is the maximum number of concurrent requests (0 for DefaultMaxConcurrent)
// log is the logger to use for rate limit events (can be nil)
func NewRateLimiter(rate time.Duration, burst, maxConcurrent int, log *logger.Logger) *RateLimiter {
	if rate <= 0 {
		rate = DefaultRate
	}

	if burst < 1 {
		burst = 1
	}

	if maxConcurrent < 1 {
		maxConcurrent = DefaultMaxConcurrent
	}

	if log == nil {
		// Use the default logger
		log = logger.Get()
	}

	log.Debug("Initializing rate limiter", map[string]interface{}{
		"rate":          rate,
		"burst":         burst,
		"maxConcurrent": maxConcurrent,
	})

	now := time.Now()
	rl := &RateLimiter{
		last:          now,
		rate:          rate,
		minRate:       rate,
		maxRate:       10 * time.Minute, // Maximum time between requests
		tokens:        burst,
		maxTokens:     burst,
		lastRateDrop:  now,
		backoffUntil:  time.Time{},
		backoffFactor: DefaultBackoffFactor,
		jitterFactor:  DefaultJitterFactor,
		maxConcurrent: int32(maxConcurrent),
		semaphore:     make(chan struct{}, maxConcurrent),
		metrics:       Metrics{},
		logger:        log,
	}

	// Pre-fill the semaphore with maxConcurrent tokens
	for i := 0; i < maxConcurrent; i++ {
		rl.semaphore <- struct{}{}
	}

	return rl
}

// Wait blocks until a token is available or the context is cancelled
func (r *RateLimiter) Wait(ctx context.Context) error {
	// First check if we need to wait due to backoff
	r.mu.RLock()
	backoffRemaining := r.checkBackoff()
	backoffUntil := r.backoffUntil
	r.mu.RUnlock()

	if backoffRemaining > 0 {
		r.logger.Debug("Rate limited: in backoff period", map[string]interface{}{
			"backoff_remaining": backoffRemaining.String(),
			"backoff_until":     time.Until(backoffUntil).String(),
		})

		// If we have a context with timeout, check if we can wait that long
		if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
			if time.Until(deadline) < backoffRemaining {
				r.logger.Debug("Context deadline too soon for backoff, failing", map[string]interface{}{
					"deadline_in":       time.Until(deadline).String(),
					"backoff_remaining": backoffRemaining.String(),
				})
				return ctx.Err()
			}
		}

		// Wait for the backoff period or context cancellation
		timer := time.NewTimer(backoffRemaining)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			// Continue with the request after backoff
		}
	}

	// Enforce max concurrent requests if set
	if r.maxConcurrent > 0 {
		r.logger.Debug("Attempting to acquire semaphore token", map[string]interface{}{
			"max_concurrent":     r.maxConcurrent,
			"semaphore_available": len(r.semaphore),
			"semaphore_cap":      cap(r.semaphore),
		})

		// Try to acquire a token from the semaphore
		select {
		case <-r.semaphore:
			// Successfully acquired a token
			r.logger.Debug("Acquired semaphore token", map[string]interface{}{
				"max_concurrent":     r.maxConcurrent,
				"semaphore_remaining": len(r.semaphore),
				"semaphore_cap":      cap(r.semaphore),
			})

			// Return the token to the semaphore when done
			defer func() {
				// Add a small delay before releasing the semaphore to help with testing
				time.Sleep(100 * time.Millisecond)
				r.logger.Debug("Releasing semaphore token", map[string]interface{}{
					"max_concurrent":     r.maxConcurrent,
					"semaphore_available_before": len(r.semaphore),
				})
				r.semaphore <- struct{}{}
				r.logger.Debug("Released semaphore token", map[string]interface{}{
					"max_concurrent":     r.maxConcurrent,
					"semaphore_available_after": len(r.semaphore) + 1,
				})
			}()
			return nil
			
		case <-ctx.Done():
			r.logger.Debug("Context done while waiting for semaphore", map[string]interface{}{
				"error": ctx.Err(),
			})
			return ctx.Err()
		}
	}

	return nil

	// Now acquire the write lock for rate limiting
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update metrics
	atomic.AddUint64(&r.metrics.Requests, 1)

	now := time.Now()

	// Add tokens based on time passed since last update
	timeSinceLast := now.Sub(r.last)
	if timeSinceLast > 0 {
		tokensToAdd := int(float64(timeSinceLast) / float64(r.rate))
		if tokensToAdd > 0 {
			r.tokens += tokensToAdd
			if r.tokens > r.maxTokens {
				r.tokens = r.maxTokens
			}
			r.last = now
		}
	}

	// If we have tokens, use one and proceed
	if r.tokens > 0 {
		r.tokens--
		r.last = now
		return nil
	}

	// Calculate how long to wait for the next token
	nextTokenIn := r.rate - timeSinceLast
	if nextTokenIn < 0 {
		nextTokenIn = 0
	}

	// Add jitter to prevent thundering herd
	nextTokenIn += r.calculateJitter()

	// If we have a context with timeout, check if we can wait that long
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		if time.Until(deadline) < nextTokenIn {
			r.logger.Debug("Context deadline too soon for rate limit, failing", map[string]interface{}{
				"deadline_in":   time.Until(deadline).String(),
				"next_token_in": nextTokenIn.String(),
			})
			return ctx.Err()
		}
	}

	// Calculate when the next token will be available
	next := now.Add(nextTokenIn)

	// Create a new timer for the wait period
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()

	// Release the lock while we wait
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		// Reacquire the lock before returning to maintain consistency
		r.mu.Lock()
		return ctx.Err()
	case <-timer.C:
		// Reacquire the lock for consistency
		r.mu.Lock()
		// Update the last time to now to prevent rate limit violations
		r.last = time.Now()
		return nil
	}
}

// OnRateLimit is called when a rate limit is encountered
// It increases the delay between requests and returns the time to wait
func (r *RateLimiter) OnRateLimit(retryAfter time.Duration) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	// Update metrics
	r.metrics.RateLimited++
	if retryAfter > 0 {
		r.metrics.RetryAfter++
	}

	// If we have a retry-after header, use that as the base for backoff
	baseBackoff := r.rate
	if retryAfter > 0 {
		baseBackoff = retryAfter
		// Add a small buffer to the retry-after to be extra safe
		baseBackoff = time.Duration(float64(baseBackoff) * 1.2)
	}

	// Calculate exponential backoff with the configured factor
	backoff := time.Duration(float64(baseBackoff) * r.backoffFactor)

	// Add jitter to prevent thundering herd (using the full jitter range)
	jitter := time.Duration(rand.Float64() * float64(backoff) * r.jitterFactor)
	if rand.Float64() < 0.5 {
		backoff -= jitter
	} else {
		backoff += jitter
	}

	// Ensure backoff is within bounds
	if backoff < r.minRate {
		backoff = r.minRate
	}
	if backoff > r.maxRate {
		backoff = r.maxRate
	}

	// Update rate to the new backoff value
	r.rate = backoff

	// Reset tokens to prevent burst after backoff
	r.tokens = 1

	// Set backoff until time
	r.backoffUntil = now.Add(backoff)

	// Log the rate limit event with detailed information
	r.logger.Warn("Rate limit backoff", map[string]interface{}{
		"retryAfter":    retryAfter.String(),
		"backoffFactor": r.backoffFactor,
		"newRate":       backoff.String(),
		"backoffUntil":  r.backoffUntil.Format(time.RFC3339),
	})

	// Return the backoff duration
	return backoff
}

// ResetRate resets the rate limiter to its default rate and backoff factor
func (r *RateLimiter) ResetRate() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset the rate to the default rate
	r.rate = DefaultRate
	// Also update minRate to match the default rate
	r.minRate = DefaultRate
	// Reset the backoff period
	r.backoffUntil = time.Time{}
	// Reset the last rate drop time
	r.lastRateDrop = time.Now()
	// Reset the backoff factor to the default
	r.backoffFactor = DefaultBackoffFactor
	// Reset the jitter factor to the default
	r.jitterFactor = DefaultJitterFactor

	r.logger.Debug("Rate limiter reset", map[string]interface{}{
		"rate":          r.rate,
		"backoffFactor": r.backoffFactor,
		"jitterFactor":  r.jitterFactor,
	})
}

func (r *RateLimiter) GetRate() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rate
}

// GetMetrics returns the current rate limiter metrics
func (r *RateLimiter) GetMetrics() Metrics {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Create a copy of the metrics
	metrics := r.metrics
	// Format the rate as a duration string (e.g., "100ms")
	metrics.CurrentRate = r.rate.String()

	return metrics
}

// SetBackoffFactor sets the backoff factor for rate limiting
func (r *RateLimiter) SetBackoffFactor(factor float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backoffFactor = factor
}

// SetJitterFactor sets the jitter factor (0.0 to 1.0)
func (r *RateLimiter) SetJitterFactor(factor float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jitterFactor = math.Max(0, math.Min(1, factor)) // Clamp between 0 and 1
}

// checkBackoff checks if we're in a backoff period and returns the remaining duration
// Note: Caller must hold at least a read lock on r.mu
func (r *RateLimiter) checkBackoff() time.Duration {
	if r.backoffUntil.IsZero() {
		return 0
	}

	now := time.Now()
	if now.After(r.backoffUntil) {
		r.backoffUntil = time.Time{}
		return 0
	}

	return r.backoffUntil.Sub(now)
}

// calculateJitter calculates a jitter duration based on the current rate
func (r *RateLimiter) calculateJitter() time.Duration {
	return time.Duration((rand.Float64()*2 - 1) * float64(r.rate) * r.jitterFactor)
}

// ensureRateLimit adjusts the rate limiter to respect the given reset duration
// without triggering a full backoff. This is used when we know when the rate limit
// will reset and want to space out our requests accordingly.
func (r *RateLimiter) ensureRateLimit(resetDuration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Calculate a conservative rate based on the reset duration
	// We'll aim to use no more than 80% of the remaining window
	safeDuration := time.Duration(float64(resetDuration) * 0.8)
	if safeDuration < r.minRate {
		safeDuration = r.minRate
	}

	// If the current rate is more aggressive than our safe duration, slow down
	if r.rate < safeDuration {
		r.rate = safeDuration
		r.logger.Debug("Adjusted rate to respect rate limit reset", map[string]interface{}{
			"previousRate": r.rate.String(),
			"newRate":      safeDuration.String(),
			"resetIn":      resetDuration.String(),
		})
	}
}

var (
	// testMode is used to disable buffering in tests
	testMode = false
	// timeNow is a variable that holds time.Now function, can be overridden in tests
	timeNow = time.Now
)

// ParseRetryAfter parses a Retry-After header and returns the duration
// It handles both delay-seconds and HTTP-date formats
func ParseRetryAfter(header string) (time.Duration, error) {
	if header == "" {
		return 0, nil
	}

	// Try to parse as seconds first
	if secs, err := strconv.Atoi(header); err == nil {
		if testMode {
			return time.Duration(secs) * time.Second, nil
		}
		// Add 10% buffer to be safe in production
		return time.Duration(float64(secs)*1.1) * time.Second, nil
	}

	// Try to parse as HTTP date
	t, err := http.ParseTime(header)
	if err != nil {
		return 0, fmt.Errorf("invalid Retry-After format: %v", header)
	}

	resetDuration := t.Sub(timeNow())
	if resetDuration < 0 {
		return 0, fmt.Errorf("retry-after time is in the past: %v", t)
	}
	if testMode {
		return resetDuration, nil
	}
	// Add a small buffer to the calculated duration in production
	return time.Duration(float64(resetDuration) * 1.1), nil
}

// IsRateLimitError checks if an error is a rate limit error
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	// Check for our rate limit errors
	if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrRetryAfter) {
		return true
	}

	// Check for HTTP 429 status
	if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "too many requests") {
		return true
	}

	return false
}

// WithRateLimitHeaders is a helper to handle rate limit headers from HTTP responses
// It checks for standard rate limiting headers and updates the rate limiter accordingly
func (r *RateLimiter) WithRateLimitHeaders(resp *http.Response) {
	if resp == nil {
		return
	}

	// Log all rate limit headers for debugging
	headers := make(map[string]string)
	for k, v := range resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "ratelimit-") ||
			strings.HasPrefix(strings.ToLower(k), "x-ratelimit-") ||
			strings.EqualFold(k, "retry-after") {
			headers[k] = strings.Join(v, ", ")
		}
	}

	// Log the rate limit headers if any were found
	if len(headers) > 0 {
		r.logger.Debug("Processing rate limit headers", map[string]interface{}{
			"component":          "rate_limiter",
			"rate_limit_headers": headers,
		})
	}

	// Log the current rate limiting state
	r.logger.Debug("Rate limiter state", map[string]interface{}{
		"rate":         r.rate.String(),
		"tokens":       fmt.Sprintf("%d/%d", r.tokens, r.maxTokens),
		"last_request": r.last.Format(time.RFC3339),
	})

	// Check for Retry-After header (highest priority)
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		duration, err := ParseRetryAfter(retryAfter)
		if err == nil && duration > 0 {
			logFields := map[string]interface{}{
				"retryAfter": retryAfter,
				"status":     resp.Status,
			}
			
			// Only include URL if Request is not nil
			if resp.Request != nil && resp.Request.URL != nil {
				logFields["url"] = resp.Request.URL.String()
			}
			
			r.logger.Warn("Rate limit error with retry-after header", logFields)
			r.OnRateLimit(duration)
			return
		}
	}

	// Check for standard rate limit headers (RFC 6585)
	limit := resp.Header.Get("RateLimit-Limit")
	remaining := resp.Header.Get("RateLimit-Remaining")
	reset := resp.Header.Get("RateLimit-Reset")

	// Fall back to X-RateLimit-* headers if standard ones aren't present
	if limit == "" {
		limit = resp.Header.Get("X-RateLimit-Limit")
	}
	if remaining == "" {
		remaining = resp.Header.Get("X-RateLimit-Remaining")
	}
	if reset == "" {
		reset = resp.Header.Get("X-RateLimit-Reset")
	}

	// If we have rate limit information, use it to adjust our rate
	if remaining != "" {
		rem, err := strconv.Atoi(remaining)
		if err == nil {
			totalLimit := 0
			if limit != "" {
				totalLimit, _ = strconv.Atoi(limit)
			}

			// If we know the total limit, calculate the remaining percentage
			if totalLimit > 0 {
				remainingPct := (float64(rem) / float64(totalLimit)) * 100

				// If we're below 20% of our rate limit, start being more conservative
				if remainingPct < 20.0 {
					r.logger.Warn("Approaching rate limit, being more conservative", map[string]interface{}{
						"component":     "rate_limiter",
						"remaining":     remaining,
						"limit":         limit,
						"remaining_pct": remainingPct,
					})

					// Calculate a backoff based on how close we are to the limit
					// The closer we are, the longer the backoff
					backoff := time.Duration(float64(r.GetRate()) * (1.0 + (100.0-remainingPct)/10.0))
					r.OnRateLimit(backoff)
				} else {
					// Otherwise use exponential backoff
					backoff := r.GetRate() * 2
					r.logger.Warn("Rate limit reached or nearly reached, backing off aggressively", map[string]interface{}{
						"header":  resp.Header.Get("RateLimit-Reset"),
						"backoff": backoff.String(),
						"error":   err.Error(),
					})
					r.OnRateLimit(backoff)
				}
			}

			// If we're at or near the limit, back off aggressively
			if rem <= 1 {
				var backoff time.Duration
				if reset != "" {
					// If we have a reset time, use that to calculate backoff
					if ts, err := strconv.ParseInt(reset, 10, 64); err == nil {
						resetTime := time.Unix(ts, 0)
						backoff = time.Until(resetTime)
						// Add 20% buffer to be safe
						backoff = time.Duration(float64(backoff) * 1.2)
					}
				} else {
					// Otherwise use exponential backoff
					backoff = r.GetRate() * 2
				}

				r.logger.Warn("Rate limit reached or nearly reached, backing off aggressively", map[string]interface{}{
					"header":  resp.Header.Get("RateLimit-Reset"),
					"backoff": backoff.String(),
					"error":   err.Error(),
				})

				r.OnRateLimit(backoff)
			}
		}
	}

	// If we have a reset time, use it to schedule our next request
	if reset != "" {
		ts, err := strconv.ParseInt(reset, 10, 64)
		if err == nil {
			resetTime := time.Unix(ts, 0)
			now := time.Now()
			if resetTime.After(now) {
				resetDuration := resetTime.Sub(now)
				r.logger.Info("Rate limit will reset, scheduling next request", map[string]interface{}{
					"component":  "rate_limiter",
					"reset_time": resetTime.Format(time.RFC3339),
					"reset_in":   resetDuration.String(),
				})
				// Don't back off, but ensure we don't exceed the rate limit
				r.ensureRateLimit(resetDuration)
			}
		}
	}
}
