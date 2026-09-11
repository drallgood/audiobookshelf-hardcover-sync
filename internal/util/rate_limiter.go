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
	mu            sync.RWMutex
	last          time.Time
	rate          time.Duration
	minRate       time.Duration
	maxRate       time.Duration
	tokens        int
	maxTokens     int
	lastRateDrop  time.Time
	backoffUntil  time.Time
	backoffFactor float64
	jitterFactor  float64

	// Daily limit tracking from IETF RateLimit headers
	dailyRemaining int
	dailyLimit     int
	dailyResetSec  int

	// Concurrency control
	maxConcurrent int32         // Maximum number of concurrent admission waiters
	semaphore     chan struct{} // Buffered channel used as a semaphore

	// Metrics
	metrics Metrics

	// Logger
	logger *logger.Logger
}

// DailyRemaining returns the number of daily requests remaining, or -1 if unknown.
// Callers can use this to decide whether to skip non-critical operations.
func (r *RateLimiter) DailyRemaining() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.dailyLimit == 0 && r.dailyRemaining == 0 && r.dailyResetSec == 0 {
		return -1
	}
	return r.dailyRemaining
}

// DailyLimit returns the daily request cap, or 0 if unknown.
func (r *RateLimiter) DailyLimit() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dailyLimit
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
		maxRate:       DefaultMaxBackoff, // Maximum time between requests
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

	// Initialize the semaphore with maxConcurrent tokens
	// Each token is represented by sending a value to the channel.
	// To acquire a token, receive from the channel.
	// To release a token, send to the channel.
	rl.semaphore = make(chan struct{}, rl.maxConcurrent)
	for i := 0; i < int(rl.maxConcurrent); i++ {
		rl.semaphore <- struct{}{}
	}

	return rl
}

// Wait blocks until a token is available or the context is cancelled
func (r *RateLimiter) Wait(ctx context.Context) error {
	// Limit the number of goroutines concurrently waiting for admission.
	if r.maxConcurrent > 0 {
		select {
		case <-r.semaphore:
			defer func() {
				r.semaphore <- struct{}{}
			}()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	r.mu.Lock()
	atomic.AddUint64(&r.metrics.Requests, 1)
	now := time.Now()
	readyAt := now
	if r.backoffUntil.After(readyAt) {
		readyAt = r.backoffUntil
		r.logger.Debug("Rate limiter in backoff period", map[string]interface{}{
			"backoff_remaining": readyAt.Sub(now).String(),
		})
	}
	if nextRequestAt := r.last.Add(r.rate); nextRequestAt.After(readyAt) {
		readyAt = nextRequestAt
	}

	// Reserve the request's admission time while holding the lock. Every
	// concurrent waiter therefore receives a distinct slot at least rate apart.
	r.last = readyAt
	r.mu.Unlock()

	waitTime := time.Until(readyAt)
	if waitTime <= 0 {
		return nil
	}
	timer := time.NewTimer(waitTime)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		// Only roll back the reservation when no later waiter depends on it.
		r.mu.Lock()
		if r.last.Equal(readyAt) {
			r.last = time.Now()
		}
		r.mu.Unlock()
		return ctx.Err()
	case <-timer.C:
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

	// Retry-After is an authoritative server delay. Honor it directly rather
	// than multiplying it again as an exponential backoff input.
	if retryAfter > 0 {
		backoff := r.applyRetryAfter(retryAfter)
		r.logger.Warn("Rate limit backoff", map[string]interface{}{
			"retryAfter":   retryAfter.String(),
			"backoffUntil": r.backoffUntil.Format(time.RFC3339),
		})
		return backoff
	}

	// Without server guidance, use exponential backoff. A subsequent healthy
	// rate-limit response restores the configured rate.
	backoff := r.exponentialBackoff(r.rate)
	r.setRate(backoff)
	r.backoffUntil = now.Add(backoff)
	r.drainBucket()

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

// ResetRate restores the configured rate and default backoff settings.
func (r *RateLimiter) ResetRate() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset to the rate configured for this limiter, not the package default.
	r.rate = r.minRate
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
	if !now.Before(r.backoffUntil) {
		return 0
	}

	return r.backoffUntil.Sub(now)
}

// calculateJitter calculates a jitter duration based on the current rate
func (r *RateLimiter) calculateJitter() time.Duration {
	return time.Duration((rand.Float64()*2 - 1) * float64(r.rate) * r.jitterFactor)
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

// WithRateLimitHeaders is a helper to handle rate limit headers from HTTP responses.
// Parses the IETF RFC-draft RateLimit / RateLimit-Policy headers (primary) and
// falls back to legacy X-RateLimit-* headers. Updates the rate limiter accordingly.
func (r *RateLimiter) WithRateLimitHeaders(resp *http.Response) {
	if resp == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Collect rate-limit headers for debug logging.
	headers := make(map[string]string)
	for k, v := range resp.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "ratelimit") ||
			strings.HasPrefix(lower, "x-ratelimit-") ||
			lower == "retry-after" {
			headers[k] = strings.Join(v, ", ")
		}
	}
	if len(headers) > 0 {
		r.logger.Debug("Processing rate limit headers", map[string]interface{}{
			"component":          "rate_limiter",
			"rate_limit_headers": headers,
		})
	}

	// Retry-After takes highest priority (server explicitly telling us to wait).
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if duration, err := ParseRetryAfter(retryAfter); err == nil && duration > 0 {
			logFields := map[string]interface{}{"retryAfter": retryAfter, "status": resp.Status}
			if resp.Request != nil && resp.Request.URL != nil {
				logFields["url"] = resp.Request.URL.String()
			}
			r.logger.Warn("Rate limit error with retry-after header", logFields)
			r.applyRetryAfter(duration)
			r.drainBucket()
			return
		}
	}

	// Try IETF RateLimit headers first (e.g. "Free";r=8;t=42, "daily";r=4231;t=51234).
	if iefRemaining, iefReset := r.parseIETFRateLimit(resp.Header); len(iefRemaining) > 0 {
		r.applyIETFHeaders(iefRemaining, iefReset, resp.Header)
		return
	}

	// A bare 429 has no server guidance to apply, so preserve the current rate
	// and build exponential backoff from it. Processing an empty legacy header
	// set first would reset the rate and prevent repeated 429s from escalating.
	if resp.StatusCode == http.StatusTooManyRequests &&
		resp.Header.Get("X-RateLimit-Remaining") == "" &&
		resp.Header.Get("X-RateLimit-Reset") == "" {
		backoff := r.exponentialBackoff(r.rate)
		r.setRate(backoff)
		r.backoffUntil = time.Now().Add(backoff)
		r.drainBucket()
		r.metrics.RateLimited++
		r.logger.Warn("Rate limit response without reset guidance", map[string]interface{}{
			"component":     "rate_limiter",
			"backoff":       backoff.String(),
			"backoff_until": r.backoffUntil.Format(time.RFC3339),
		})
		return
	}

	// Fall back to legacy X-RateLimit-* headers.
	r.applyLegacyHeaders(resp.Header)
}

// parseIETFRateLimit parses the IETF RateLimit header value.
// Returns maps from bucket name -> remaining/seconds-until-reset.
func (r *RateLimiter) parseIETFRateLimit(h http.Header) (remaining map[string]int, reset map[string]int) {
	val := h.Get("RateLimit")
	if val == "" {
		return nil, nil
	}

	remaining = make(map[string]int)
	reset = make(map[string]int)

	for _, bucket := range rateLimitBuckets(val) {
		name, params := parseRateLimitBucket(bucket)
		if name == "" {
			continue
		}
		if v, ok := params["r"]; ok {
			remaining[name] = v
		}
		if v, ok := params["t"]; ok {
			reset[name] = v
		}
	}
	return remaining, reset
}

// parseIETFRateLimitPolicy parses the IETF RateLimit-Policy header value.
// Returns maps from bucket name to quota, burst allowance, and window seconds.
func (r *RateLimiter) parseIETFRateLimitPolicy(h http.Header) (quota, burst, window map[string]int) {
	val := h.Get("RateLimit-Policy")
	if val == "" {
		return nil, nil, nil
	}

	quota = make(map[string]int)
	burst = make(map[string]int)
	window = make(map[string]int)

	for _, bucket := range rateLimitBuckets(val) {
		name, params := parseRateLimitBucket(bucket)
		if name == "" {
			continue
		}
		if v, ok := params["q"]; ok {
			quota[name] = v
		}
		if v, ok := params["burst"]; ok {
			burst[name] = v
		}
		if v, ok := params["w"]; ok {
			window[name] = v
		}
	}
	return quota, burst, window
}

// rateLimitBuckets splits a raw IETF RateLimit header into individual bucket entries.
// Format: "name";k=v;...[, "name2";k=v;...]
func rateLimitBuckets(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var buckets []string
	inQuotes := false
	start := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				buckets = append(buckets, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(raw) {
		buckets = append(buckets, strings.TrimSpace(raw[start:]))
	}
	return buckets
}

// parseRateLimitBucket parses a single bucket entry like `"Free";r=8;t=42`
// into the bucket name and a map of key=value pairs.
func parseRateLimitBucket(bucket string) (string, map[string]int) {
	params := make(map[string]int)

	// Extract the quoted bucket name.
	if !strings.HasPrefix(bucket, `"`) {
		return "", params
	}
	nameEnd := strings.IndexByte(bucket[1:], '"')
	if nameEnd < 0 {
		return "", params
	}
	name := bucket[1 : nameEnd+1]
	if name == "" {
		return "", params
	}

	// Parse the ;k=v pairs after the name.
	rest := bucket[nameEnd+2:]
	for _, kv := range strings.Split(rest, ";") {
		kv = strings.TrimSpace(kv)
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err == nil {
			params[strings.TrimSpace(parts[0])] = v
		}
	}

	return strings.ToLower(name), params
}

// applyIETFHeaders applies rate limit info parsed from IETF-format headers.
func (r *RateLimiter) applyIETFHeaders(remaining, reset map[string]int, h http.Header) {
	quota, burst, window := r.parseIETFRateLimitPolicy(h)
	desiredRate := r.minRate

	// Update daily tracking.
	dailyName := findBucketName(remaining, "daily")
	if dailyName != "" {
		r.dailyRemaining = remaining[dailyName]
		if q, ok := quota[dailyName]; ok {
			r.dailyLimit = q
		}
		if t, ok := reset[dailyName]; ok && t > 0 {
			r.dailyResetSec = t
		}

		if r.dailyRemaining <= 0 && reset[dailyName] > 0 {
			pause := time.Duration(reset[dailyName]) * time.Second
			r.logger.Warn("Daily rate limit exhausted, pausing", map[string]interface{}{
				"component":       "rate_limiter",
				"daily_remaining": r.dailyRemaining,
				"daily_limit":     r.dailyLimit,
				"pause":           pause.String(),
			})
			r.applyRetryAfter(pause)
			r.drainBucket()
		} else if r.dailyRemaining > 0 && r.dailyLimit > 0 {
			pct := float64(r.dailyRemaining) / float64(r.dailyLimit) * 100
			if pct < 1.0 {
				recoveryWindow := time.Duration(reset[dailyName]) * time.Second
				if recoveryWindow > 0 {
					desiredRate = max(desiredRate, recoveryWindow/time.Duration(r.dailyRemaining))
				} else {
					desiredRate = max(desiredRate, r.minRate*2)
				}
				r.logger.Warn("Daily rate limit exhausted, slowing down", map[string]interface{}{
					"component":       "rate_limiter",
					"daily_remaining": r.dailyRemaining,
					"daily_limit":     r.dailyLimit,
					"remaining_pct":   fmt.Sprintf("%.1f%%", pct),
					"new_rate":        desiredRate.String(),
				})
			} else if pct < 5.0 {
				r.logger.Warn("Daily rate limit approaching, being conservative", map[string]interface{}{
					"component":       "rate_limiter",
					"daily_remaining": r.dailyRemaining,
					"daily_limit":     r.dailyLimit,
					"remaining_pct":   fmt.Sprintf("%.1f%%", pct),
				})
			}
		}
	}

	// Apply per-window rate limiting. The policy quota/window describes the
	// sustainable request interval; burst is not a requests-per-minute quota.
	for name, rem := range remaining {
		if name == dailyName {
			continue
		}
		if q := quota[name]; q > 0 && window[name] > 0 {
			sustainableRate := time.Duration(window[name]) * time.Second / time.Duration(q)
			desiredRate = max(desiredRate, sustainableRate)
		}
		b := 0
		if v, ok := burst[name]; ok {
			b = v
		}
		if rem <= 1 {
			resetSec := reset[name]
			var pause time.Duration
			if resetSec > 0 {
				pause = time.Duration(float64(resetSec)*1.2) * time.Second
			}
			if pause > 0 || desiredRate > r.minRate {
				r.logger.Info("Rate limit window nearly exhausted, slowing down", map[string]interface{}{
					"component":  "rate_limiter",
					"bucket":     name,
					"remaining":  rem,
					"burst":      b,
					"reset_in_s": reset[name],
					"pause":      pause.String(),
					"new_rate":   desiredRate.String(),
				})
			}
			if pause > 0 {
				r.applyRetryAfter(pause)
				r.drainBucket()
			}
		}
	}

	r.setRate(desiredRate)
}

// findBucketName returns the key in the map whose lowercase form matches target.
func findBucketName(m map[string]int, target string) string {
	for k := range m {
		if strings.Contains(strings.ToLower(k), target) {
			return k
		}
	}
	return ""
}

// applyLegacyHeaders parses legacy X-RateLimit-* headers.
func (r *RateLimiter) applyLegacyHeaders(h http.Header) {
	limit := h.Get("X-RateLimit-Limit")
	remaining := h.Get("X-RateLimit-Remaining")
	reset := h.Get("X-RateLimit-Reset")
	desiredRate := r.minRate

	if remaining != "" {
		rem, err := strconv.Atoi(remaining)
		if err == nil {
			totalLimit := 0
			if limit != "" {
				totalLimit, _ = strconv.Atoi(limit)
			}
			if totalLimit > 0 {
				remainingPct := (float64(rem) / float64(totalLimit)) * 100
				if rem > 0 && remainingPct < 20.0 {
					if resetAt, ok := parseUnixReset(reset); ok {
						desiredRate = max(desiredRate, time.Until(resetAt)/time.Duration(rem))
					} else {
						desiredRate = max(desiredRate, r.minRate*2)
					}
					r.logger.Info("Approaching rate limit (legacy headers), being more conservative", map[string]interface{}{
						"component":     "rate_limiter",
						"remaining":     remaining,
						"limit":         limit,
						"remaining_pct": remainingPct,
						"new_rate":      desiredRate.String(),
					})
				}
			}
			if rem <= 0 {
				pause := time.Duration(0)
				if resetAt, ok := parseUnixReset(reset); ok {
					pause = time.Until(resetAt)
				}
				if pause <= 0 {
					desiredRate = max(desiredRate, r.minRate*2)
				}
				r.logger.Warn("Rate limit reached (legacy headers), backing off", map[string]interface{}{
					"component": "rate_limiter",
					"backoff":   pause.String(),
					"new_rate":  desiredRate.String(),
				})
				if pause > 0 {
					r.applyRetryAfter(pause)
					r.drainBucket()
				}
			}
		}
	}
	r.setRate(desiredRate)

	if reset != "" {
		ts, err := strconv.ParseInt(reset, 10, 64)
		if err == nil {
			resetTime := time.Unix(ts, 0)
			if resetTime.After(time.Now()) {
				r.logger.Debug("Rate limit will reset, scheduling next request", map[string]interface{}{
					"component": "rate_limiter",
					"reset_in":  time.Until(resetTime).String(),
				})
			}
		}
	}
}

func parseUnixReset(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	ts, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	resetAt := time.Unix(ts, 0)
	return resetAt, resetAt.After(time.Now())
}

// exponentialBackoff calculates a bounded client-selected retry delay.
func (r *RateLimiter) exponentialBackoff(baseBackoff time.Duration) time.Duration {
	backoff := time.Duration(float64(baseBackoff) * r.backoffFactor)
	jitter := time.Duration(rand.Float64() * float64(backoff) * r.jitterFactor)
	if rand.Float64() < 0.5 {
		backoff -= jitter
	} else {
		backoff += jitter
	}
	if backoff < r.minRate {
		backoff = r.minRate
	}
	if backoff > r.maxRate {
		backoff = r.maxRate
	}
	return backoff
}

// applyRetryAfter installs an exact, temporary server-directed pause.
func (r *RateLimiter) applyRetryAfter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	if delay > r.maxRate {
		delay = r.maxRate
	}
	until := time.Now().Add(delay)
	if until.After(r.backoffUntil) {
		r.backoffUntil = until
		r.metrics.BackoffEvents++
	}
	return delay
}

// setRate updates the steady request interval. Header processing calls this on
// every response, so authoritative healthy quota data restores the configured
// rate after a temporary slowdown.
func (r *RateLimiter) setRate(rate time.Duration) {
	if rate < r.minRate {
		rate = r.minRate
	}
	if rate > r.maxRate {
		rate = r.maxRate
	}
	if rate == r.rate {
		return
	}
	if rate > r.rate {
		r.metrics.BackoffEvents++
	} else {
		r.logger.Info("Rate limiter pacing recovered", map[string]interface{}{
			"component":     "rate_limiter",
			"previous_rate": r.rate.String(),
			"new_rate":      rate.String(),
		})
	}
	r.rate = rate
	r.lastRateDrop = time.Now()
}

// drainBucket empties the token bucket to prevent burst after backoff.
func (r *RateLimiter) drainBucket() {
	r.tokens = 1
}

// Metrics returns a snapshot of the rate limiter's internal state.
