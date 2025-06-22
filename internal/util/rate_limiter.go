package util

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

var (
	// ErrRateLimited is returned when the rate limit is exceeded
	ErrRateLimited = errors.New("rate limited")
	// DefaultRate is the default minimum time between requests
	DefaultRate = 200 * time.Millisecond
	// DefaultBurst is the default burst size
	DefaultBurst = 5
)

// RateLimiter implements a token bucket rate limiter with dynamic rate adjustment
type RateLimiter struct {
	mu           sync.Mutex
	last         time.Time
	rate         time.Duration
	minRate      time.Duration
	maxRate      time.Duration
	tokens       int
	maxTokens    int
	lastRateDrop time.Time
}

// NewRateLimiter creates a new RateLimiter with the specified rate and burst size
// rate is the minimum time between requests (e.g., 1*time.Second for 1 request per second)
// burst is the maximum number of tokens that can be consumed at once
func NewRateLimiter(rate time.Duration, burst int) *RateLimiter {
	if rate <= 0 {
		rate = DefaultRate
	}
	if burst <= 0 {
		burst = DefaultBurst
	}

	return &RateLimiter{
		last:         time.Now(),
		rate:         rate,
		minRate:      rate,
		maxRate:      5 * time.Second, // Maximum time between requests
		tokens:       burst,
		maxTokens:    burst,
		lastRateDrop: time.Now(),
	}
}

// Wait blocks until a token is available or the context is cancelled
func (r *RateLimiter) Wait(ctx context.Context) error {
	r.mu.Lock()

	now := time.Now()

	// Add tokens based on time passed
	delta := now.Sub(r.last)
	newTokens := int(float64(delta) / float64(r.rate))
	if newTokens > 0 {
		r.tokens += newTokens
		if r.tokens > r.maxTokens {
			r.tokens = r.maxTokens
		}
		r.last = now
	}

	// If we have tokens, use one and return immediately
	if r.tokens > 0 {
		r.tokens--
		r.mu.Unlock()
		return nil
	}

	// Calculate wait time with jitter (up to 20% of rate)
	waitTime := r.rate + time.Duration(rand.Float64()*0.2*float64(r.rate))
	next := r.last.Add(waitTime)

	r.mu.Unlock()

	// Wait for the next token or context cancellation
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		r.mu.Lock()
		r.last = next
		r.tokens = 0
		r.mu.Unlock()
		return nil
	}
}

// OnRateLimit is called when a rate limit is encountered
// It increases the delay between requests and returns the time to wait
func (r *RateLimiter) OnRateLimit(retryAfter time.Duration) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	// If we've had a rate limit recently, be more aggressive with backoff
	if now.Sub(r.lastRateDrop) < 5*time.Minute {
		r.rate = time.Duration(1.5 * float64(r.rate))
	} else {
		r.rate = time.Duration(1.2 * float64(r.rate))
	}

	// Enforce maximum rate
	if r.rate > r.maxRate {
		r.rate = r.maxRate
	}

	r.lastRateDrop = now

	log.Warn().
		Dur("new_rate", r.rate).
		Dur("retry_after", retryAfter).
		Msg("Rate limited, increasing delay between requests")

	// Return the longer of our calculated backoff or the server's retry-after
	if retryAfter > 0 && retryAfter > r.rate {
		return retryAfter
	}
	return r.rate
}

// ResetRate resets the rate limiter to its minimum rate
func (r *RateLimiter) ResetRate() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.rate = r.minRate
	r.lastRateDrop = time.Now()
}

// GetRate returns the current rate
func (r *RateLimiter) GetRate() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rate
}
