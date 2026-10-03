package hardcover

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
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
	assert.Equal(t, 2*time.Second, cfg.RateLimit) // 30 requests/minute
	assert.Equal(t, 1, cfg.MaxConcurrent)
}

func TestNewClientWithConfigUsesConfiguredEndpointAuthenticationAndRetryLimit(t *testing.T) {
	logger.Setup(logger.Config{Level: "error", Format: "json"})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Equal(t, "/graphql", r.URL.Path)
		assert.Equal(t, "Bearer configured-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClientWithConfig(&ClientConfig{
		BaseURL:       server.URL + "/graphql",
		Timeout:       time.Second,
		MaxRetries:    1,
		RetryDelay:    time.Nanosecond,
		RateLimit:     time.Nanosecond,
		MaxConcurrent: 1,
	}, "configured-token", logger.Get())
	var result struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}
	err := client.GraphQLQuery(context.Background(), "query ConfiguredClient { books { id } }", nil, &result)
	var httpErr *HTTPError
	require.Error(t, err)
	require.True(t, errors.As(err, &httpErr), "expected the last HTTP failure to remain inspectable: %v", err)
	assert.Equal(t, http.StatusBadGateway, httpErr.StatusCode)
	assert.Equal(t, int32(2), requests.Load(), "one configured retry should make two total requests")
}

func TestNewClientWithConfigNilUsesDefaultEndpoint(t *testing.T) {
	client := NewClientWithConfig(nil, "default-token", logger.Get())
	client.httpClient.Transport = graphqlRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, DefaultBaseURL, req.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":{"books":[{"id":42}]}}`)),
			Request:    req,
		}, nil
	})
	var result struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}
	require.NoError(t, client.GraphQLQuery(context.Background(), "query DefaultClient { books { id } }", nil, &result))
	require.Len(t, result.Books, 1)
	assert.Equal(t, 42, result.Books[0].ID)
}

func TestNewClientWithConfigReusesProvidedRateLimiter(t *testing.T) {
	logger.Setup(logger.Config{Level: "error", Format: "json"})
	limiter := util.NewRateLimiter(time.Nanosecond, 2, logger.Get())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"books":[]}}`)
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.BaseURL = server.URL + "/graphql"
	config.Timeout = time.Second
	config.RateLimit = time.Nanosecond
	config.MaxConcurrent = 2
	config.RateLimiter = limiter

	client := NewClientWithConfig(config, "profile-token", logger.Get())
	var result struct {
		Books []struct{} `json:"books"`
	}
	require.NoError(t, client.GraphQLQuery(context.Background(), "query SharedLimiter { books { id } }", nil, &result))
	assert.Equal(t, uint64(1), limiter.GetMetrics().Requests, "the supplied limiter must admit the client's request")
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
