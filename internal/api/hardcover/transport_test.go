package hardcover

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRoundTripper is a mock http.RoundTripper for testing
type mockRoundTripper struct {
	response *http.Response
	err      error
}

func (m *mockRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return m.response, m.err
}

func TestHeaderAddingTransport_RoundTrip(t *testing.T) {
	// Initialize logger
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	// Create a mock response
	mockResp := &http.Response{
		StatusCode: 200,
		Body:       http.NoBody,
	}

	// Create mock roundtripper
	mockRT := &mockRoundTripper{
		response: mockResp,
		err:      nil,
	}

	// Create the transport under test
	transport := &headerAddingTransport{
		token:   "test-token",
		baseURL: "http://example.com",
		rt:      mockRT,
	}

	// Create a test request
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)

	// Call the RoundTrip method
	resp, err := transport.RoundTrip(req)

	// Assertions
	require.NoError(t, err)
	assert.Equal(t, mockResp, resp)
	assert.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
}

func TestLoggingRoundTripper_RoundTrip(t *testing.T) {
	// Initialize logger
	logger := logger.Get()

	// Create a mock response
	mockResp := &http.Response{
		StatusCode: 200,
		Body:       http.NoBody,
	}

	// Create mock roundtripper
	mockRT := &mockRoundTripper{
		response: mockResp,
		err:      nil,
	}

	// Create the transport under test
	transport := loggingRoundTripper{
		logger: logger,
		rt:     mockRT,
	}

	// Create a test request
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)

	// Call the RoundTrip method
	resp, err := transport.RoundTrip(req)

	// Assertions
	require.NoError(t, err)
	assert.Equal(t, mockResp, resp)
}

func TestLoggingRoundTripper_RoundTrip_Error(t *testing.T) {
	// Initialize logger
	logger := logger.Get()

	// Create an error
	expectedErr := errors.New("network error")

	// Create mock roundtripper with error
	mockRT := &mockRoundTripper{
		response: nil,
		err:      expectedErr,
	}

	// Create the transport under test
	transport := loggingRoundTripper{
		logger: logger,
		rt:     mockRT,
	}

	// Create a test request
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)

	// Call the RoundTrip method
	resp, err := transport.RoundTrip(req)

	// Assertions
	assert.Nil(t, resp)
	assert.Equal(t, expectedErr, err)
}
