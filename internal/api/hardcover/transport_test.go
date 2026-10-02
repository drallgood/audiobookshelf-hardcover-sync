package hardcover

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRoundTripper is a mock http.RoundTripper for testing
type mockRoundTripper struct {
	response *http.Response
}

func (m *mockRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return m.response, nil
}

func TestHeaderAddingTransport_RoundTrip(t *testing.T) {
	// Create a mock response
	mockResp := &http.Response{
		StatusCode: 200,
		Body:       http.NoBody,
	}

	// Create mock roundtripper
	mockRT := &mockRoundTripper{
		response: mockResp,
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
