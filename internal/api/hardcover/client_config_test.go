package hardcover

import (
	"context"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultClientConfig(t *testing.T) {
	cfg := DefaultClientConfig()
	
	require.NotNil(t, cfg)
	assert.Equal(t, DefaultBaseURL, cfg.BaseURL)
	assert.Equal(t, DefaultTimeout, cfg.Timeout)
	assert.Equal(t, DefaultMaxRetries, cfg.MaxRetries)
	assert.Equal(t, DefaultRetryDelay, cfg.RetryDelay)
	assert.Equal(t, 1500*time.Millisecond, cfg.RateLimit) // Match actual implementation
	assert.Equal(t, 2, cfg.Burst)                         // Match actual implementation
	assert.Equal(t, 3, cfg.MaxConcurrent)
}

func TestNewClient(t *testing.T) {
	// Initialize logger for test
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	log := logger.Get()

	token := "test-token"
	client := NewClient(token, log)

	require.NotNil(t, client)
	assert.Equal(t, DefaultBaseURL, client.baseURL)
	assert.Equal(t, token, client.authToken)
	assert.NotNil(t, client.httpClient)
	assert.NotNil(t, client.logger)
	assert.NotNil(t, client.rateLimiter)
	assert.Equal(t, DefaultMaxRetries, client.maxRetries)
	assert.Equal(t, DefaultRetryDelay, client.retryDelay)
}

func TestNewClientWithConfig(t *testing.T) {
	// Initialize logger for test
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	log := logger.Get()

	tests := []struct {
		name   string
		config *ClientConfig
		token  string
	}{
		{
			name: "custom config",
			config: &ClientConfig{
				BaseURL:       "https://custom.api.com/graphql",
				Timeout:       15 * time.Second,
				MaxRetries:    5,
				RetryDelay:    1 * time.Second,
				RateLimit:     200 * time.Millisecond,
				Burst:         5,
				MaxConcurrent: 2,
			},
			token: "custom-token",
		},
		{
			name:   "nil config uses defaults",
			config: nil,
			token:  "default-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClientWithConfig(tt.config, tt.token, log)

			require.NotNil(t, client)
			assert.Equal(t, tt.token, client.authToken)
			assert.NotNil(t, client.httpClient)
			assert.NotNil(t, client.logger)
			assert.NotNil(t, client.rateLimiter)

			if tt.config != nil {
				assert.Equal(t, tt.config.BaseURL, client.baseURL)
				assert.Equal(t, tt.config.MaxRetries, client.maxRetries)
				assert.Equal(t, tt.config.RetryDelay, client.retryDelay)
			} else {
				assert.Equal(t, DefaultBaseURL, client.baseURL)
				assert.Equal(t, DefaultMaxRetries, client.maxRetries)
				assert.Equal(t, DefaultRetryDelay, client.retryDelay)
			}
		})
	}
}

func TestClient_GetAuthHeader(t *testing.T) {
	// Initialize logger for test
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	log := logger.Get()

	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{
			name:     "valid token",
			token:    "test-token-123",
			expected: "Bearer test-token-123",
		},
		{
			name:     "empty token",
			token:    "",
			expected: "Bearer ",
		},
		{
			name:     "token with special characters",
			token:    "test-token_with-special.chars",
			expected: "Bearer test-token_with-special.chars",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.token, log)
			header := client.GetAuthHeader()
			assert.Equal(t, tt.expected, header)
		})
	}
}

func TestClient_enforceRateLimit(t *testing.T) {
	// Initialize logger for test
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	log := logger.Get()

	// Create a client with a very short rate limit for testing
	cfg := &ClientConfig{
		BaseURL:       DefaultBaseURL,
		Timeout:       DefaultTimeout,
		MaxRetries:    DefaultMaxRetries,
		RetryDelay:    DefaultRetryDelay,
		RateLimit:     10 * time.Millisecond, // Very short for testing
		Burst:         1,
		MaxConcurrent: 1,
	}

	client := NewClientWithConfig(cfg, "test-token", log)

	// Test that rate limiting doesn't error
	err1 := client.enforceRateLimit(context.Background())
	assert.NoError(t, err1)

	// Test multiple rapid calls
	start := time.Now()
	err2 := client.enforceRateLimit(context.Background())
	assert.NoError(t, err2)
	err3 := client.enforceRateLimit(context.Background())
	assert.NoError(t, err3)
	duration := time.Since(start)

	// The second and third calls should have been rate limited,
	// so the total duration should be at least the rate limit duration
	assert.GreaterOrEqual(t, duration, cfg.RateLimit)
}
