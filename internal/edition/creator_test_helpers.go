package edition

import (
	"context"
	"net/http"
	"strings"
)

// TestCaseHeaderKey is the context key for test case headers
type testCaseHeaderKeyType string
var TestCaseHeaderKey = testCaseHeaderKeyType("test-case-header")

// TestHelpers provides access to unexported methods for testing
type TestHelpers struct {
	creator *Creator
	testServerURL string // URL of the test server for replacing hardcoded URLs
}

// NewTestHelpers creates a new TestHelpers instance
func NewTestHelpers(creator *Creator) *TestHelpers {
	return &TestHelpers{creator: creator}
}

// WithTestServer configures the helper to replace hardcoded URLs with the test server URL
func (h *TestHelpers) WithTestServer(testServerURL string) *TestHelpers {
	h.testServerURL = testServerURL
	return h
}

// UploadImageToGCS exposes the private uploadImageToGCS method for testing
func (h *TestHelpers) UploadImageToGCS(ctx context.Context, editionID int, imageURL string) (string, error) {
	// Check if we need to substitute test server URLs
	if h.testServerURL != "" {
		// Replace any instance of hardcover.app with the test server URL
		imageURL = strings.Replace(imageURL, "https://hardcover.app", h.testServerURL, 1)
	}

	// Get the test case value from context if present
	var testCaseValue string
	if ctxVal := ctx.Value(TestCaseHeaderKey); ctxVal != nil {
		if val, ok := ctxVal.(string); ok {
			testCaseValue = val
		}
	}
	
	// Create a custom HTTP client for this request that redirects all hardcover.app URLs
	// to our test server and adds the test case header
	testClient := &http.Client{
		Transport: &testRoundTripper{
			base: http.DefaultTransport,
			testServerURL: h.testServerURL,
			testCase: testCaseValue,
		},
	}
	
	// Save the original client
	originalClient := h.creator.httpClient
	
	// Replace with our test client
	h.creator.httpClient = testClient
	
	// Ensure we restore the original client when we're done
	defer func() {
		h.creator.httpClient = originalClient
	}()
	
	// Call the original method with our modified HTTP client
	return h.creator.uploadImageToGCS(ctx, editionID, imageURL)
}

// UpdateEditionImage exposes the private updateEditionImage method for testing
func (h *TestHelpers) UpdateEditionImage(ctx context.Context, editionID int, imageID int) error {
	// Call the original method directly
	return h.creator.updateEditionImage(ctx, editionID, imageID)
}

// CreateEdition exposes the private createEdition method for testing
func (h *TestHelpers) CreateEdition(ctx context.Context, input *EditionInput, imageID int) (int, error) {
	// Call the original method directly
	return h.creator.createEdition(ctx, input, imageID)
}

// testRoundTripper is a custom http.RoundTripper that redirects hardcover.app URLs
// to the test server and can also set special test case headers
type testRoundTripper struct {
	base http.RoundTripper
	testServerURL string
	testCase string      // Optional test case identifier
}

// RoundTrip implements the http.RoundTripper interface
func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request so we can modify it
	reqCopy := req.Clone(req.Context())
	
	// If we have a test case identifier, set it as a header
	if t.testCase != "" {
		reqCopy.Header.Set("X-Test-Case", t.testCase)
	}
	
	// Replace any URL with the test server URL for test isolation
	// This includes both hardcover.app URLs and the image URLs in tests
	if t.testServerURL != "" && req.URL.String() != "" {
		// Check if this is an image URL or API URL we need to redirect
		if strings.Contains(reqCopy.URL.String(), "{{TEST_SERVER_URL}}") ||
		   strings.Contains(reqCopy.URL.Host, "hardcover.app") ||
		   strings.Contains(reqCopy.URL.String(), "hardcover.app") {
			// Extract the path and query from the original URL
			path := reqCopy.URL.Path
			query := reqCopy.URL.RawQuery
			
			// Parse the test server URL
			testURL := t.testServerURL
			if !strings.HasSuffix(testURL, "/") {
				testURL += "/"
			}
			
			// Create a new URL using the test server as base
			newURL := testURL
			if !strings.HasPrefix(path, "/") {
				newURL += path
			} else {
				newURL += strings.TrimPrefix(path, "/")
			}
			
			// Add the query string if present
			if query != "" {
				newURL += "?" + query
			}
			
			// Parse the new URL
			parsedURL, err := req.URL.Parse(newURL)
			if err == nil {
				reqCopy.URL = parsedURL
				// Update Host to match the new URL's host
				reqCopy.Host = parsedURL.Host
			}
		}
	}
	
	// Use the base transport to perform the actual request
	// Ensure base is never nil to avoid panics
	if t.base == nil {
		t.base = http.DefaultTransport
	}
	return t.base.RoundTrip(reqCopy)
}