package hardcover

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
)

// MockHTTPClient is a mock HTTP client for testing that implements http.RoundTripper
type MockHTTPClient struct {
	mock.Mock
	requests []*http.Request
}

// RoundTrip mocks the http.RoundTripper interface
func (m *MockHTTPClient) RoundTrip(req *http.Request) (*http.Response, error) {
	m.requests = append(m.requests, req)
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

// AssertExpectations asserts that everything specified with On and Return was in fact called as expected.
// Calls may have occurred in any order.
func (m *MockHTTPClient) AssertExpectations(t mock.TestingT) bool {
	return m.Mock.AssertExpectations(t)
}

// Reset clears the mock's state
func (m *MockHTTPClient) Reset() {
	m.requests = nil
	m.Mock = mock.Mock{}
}

// newTestClient creates a new test client with mock dependencies
func newTestClient() (*Client, *MockHTTPClient) {
	mockHTTP := &MockHTTPClient{}
	
	// Create a no-op rate limiter for testing
	rateLimiter := util.NewRateLimiter(1*time.Second, 10, 10, logger.Get())
	
	// Create a custom HTTP client that uses our mock transport
	httpClient := &http.Client{
		Transport: mockHTTP,
		// Set a timeout to prevent hanging tests
		Timeout: 5 * time.Second,
	}
	
	client := &Client{
		baseURL:     "https://api.hardcover.test",
		authToken:   "test-token",
		httpClient:  httpClient,
		logger:      logger.Get(),
		rateLimiter: rateLimiter,
	}
	return client, mockHTTP
}

// readTestFile reads a test file from the testdata directory
func readTestFile(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", filename))
	if err != nil {
		t.Fatalf("failed to read test file %s: %v", filename, err)
	}
	return data
}

func TestClient_GetEditionByISBN13(t *testing.T) {
	// Set up test cases
	tests := []struct {
		name          string
		isbn13        string
		setupMock    func(*Client, *MockHTTPClient)
		expected     *models.Edition
		expectedError string
	}{
		{
			name:   "successful lookup",
			isbn13: "9781234567890",
			setupMock: func(c *Client, mockHTTP *MockHTTPClient) {
				// Mock the search response
				searchResp := `{"data":{"books":[{"id":"123","title":"Test Book","editions":[{"id":"456"}]}]}}`
				mockHTTP.On("RoundTrip", mock.Anything).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(searchResp)),
				}, nil).Once()
			},
			expected: &models.Edition{
				ID: "456",
			},
		},
		{
			name:          "empty isbn",
			isbn13:        "",
			setupMock:    nil,
			expected:     nil,
			expectedError: "ISBN-13 cannot be empty",
		},
		{
			name:   "book not found",
			isbn13: "9781234567890",
			setupMock: func(c *Client, mockHTTP *MockHTTPClient) {
				// Mock empty search response
				searchResp := `{"data": {"books": []}}`
				mockHTTP.On("RoundTrip", mock.Anything).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(searchResp)),
				}, nil).Once()
			},
			expected:     nil,
			expectedError: "book not found",
		},
		{
			name:   "search error",
			isbn13: "9781234567890",
			setupMock: func(c *Client, mockHTTP *MockHTTPClient) {
				// Mock HTTP error
				mockHTTP.On("RoundTrip", mock.Anything).Return(nil, assert.AnError).Once()
			},
			expected:     nil,
			expectedError: "failed to search book",
		},
		{
			name:   "get edition error",
			isbn13: "9781234567890",
			setupMock: func(c *Client, mockHTTP *MockHTTPClient) {
				// Mock successful search but failed edition fetch
				searchResp := `{"data": {"books": [{"id": "123", "title": "Test Book", "editions": [{"id": "456"}]}]}}`
				editionResp := `{"errors": [{"message": "edition not found"}]}`
				mockHTTP.On("RoundTrip", mock.Anything).
					Return(&http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(searchResp)),
					}, nil).
					Once().
					Return(&http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(editionResp)),
					}, nil).
					Once()
			},
			expected:     nil,
			expectedError: "failed to get edition",
		},
	}

	// Run test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new test client with mock HTTP client
			client, mockHTTP := newTestClient()

			// Set up the mock
			if tt.setupMock != nil {
				tt.setupMock(client, mockHTTP)
			}

			// Call the method being tested
			edition, err := client.GetEditionByISBN13(context.Background(), tt.isbn13)

			// Check the results
			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, edition)
			}

			// Verify all expectations were met
			mockHTTP.AssertExpectations(t)
		})
	}
}

func TestClient_SearchBookByISBN13(t *testing.T) {
	// Set up test cases
	tests := []struct {
		name          string
		isbn13        string
		setupMock    func(*Client, *MockHTTPClient)
		expected     *models.HardcoverBook
		expectedError string
	}{
		{
			name:   "successful search",
			isbn13: "9781234567890",
			setupMock: func(c *Client, mockHTTP *MockHTTPClient) {
				// Mock the search response
				resp := `{"data":{"books":[{"id":"123","title":"Test Book","editions":[{"id":"456"}]}]}}`
				mockHTTP.On("RoundTrip", mock.Anything).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(resp)),
				}, nil).Once()
			},
			expected: &models.HardcoverBook{
				ID:        "123",
				Title:     "Test Book",
				EditionID: "456",
			},
		},
		{
			name:          "empty isbn",
			isbn13:        "",
			setupMock:    nil,
			expected:     nil,
			expectedError: "ISBN-13 cannot be empty",
		},
		{
			name:   "search error",
			isbn13: "9781234567890",
			setupMock: func(c *Client, mockHTTP *MockHTTPClient) {
				mockHTTP.On("RoundTrip", mock.Anything).Return(nil, assert.AnError).Once()
			},
			expected:     nil,
			expectedError: "failed to search book",
		},
	}

	// Run test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new test client with mock HTTP client
			client, mockHTTP := newTestClient()

			// Set up the mock
			if tt.setupMock != nil {
				tt.setupMock(client, mockHTTP)
			}

			// Call the method being tested
			book, err := client.SearchBookByISBN13(context.Background(), tt.isbn13)

			// Check the results
			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, book)
			}

			// Verify all expectations were met
			mockHTTP.AssertExpectations(t)
		})
	}
}
