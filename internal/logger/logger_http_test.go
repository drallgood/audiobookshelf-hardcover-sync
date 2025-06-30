package logger

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WithRequestID is a middleware that adds a unique request ID to the request context
// and sets it in the response headers
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate a random request ID if not present in headers
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			// Generate a random 16-byte ID and encode it as hex
			var id [8]byte
			_, _ = rand.Read(id[:])
			requestID = hex.EncodeToString(id[:])
		}

		// Set the request ID in the response headers
		w.Header().Set("X-Request-Id", requestID)

		// Add the request ID to the context
		ctx := context.WithValue(r.Context(), ContextKeyRequestID, requestID)

		// Call the next handler with the new context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestHTTPMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		handler        http.HandlerFunc
		expectedStatus int
		expectedLogs   []string
	}{
		{
			name: "successful request",
			path: "/test",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("test response"))
			},
			expectedStatus: http.StatusOK,
			expectedLogs: []string{
				`"method":"GET"`,
				`"status":200`,
				`"path":"/test"`,
			},
		},
		{
			name: "not found",
			path: "/not-found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
			expectedStatus: http.StatusNotFound,
			expectedLogs: []string{
				`"method":"GET"`,
				`"status":404`,
				`"path":"/not-found"`,
			},
		},
		{
			name: "internal server error",
			path: "/error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "something went wrong", http.StatusInternalServerError)
			},
			expectedStatus: http.StatusInternalServerError,
			expectedLogs: []string{
				`"method":"GET"`,
				`"status":500`,
				`"path":"/error"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a buffer to capture logs
			var buf bytes.Buffer

			// Reset the global logger for testing
			ResetForTesting()

			// Configure the logger with JSON format for easier parsing
			Setup(Config{
				Level:      "info",
				Format:     FormatJSON,
				Output:     &buf,
				TimeFormat: "", // No timestamp in tests for easier assertions
			})

			// Create a test request
			req, err := http.NewRequest("GET", tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}

			// Create a response recorder
			rr := httptest.NewRecorder()

			// Create the middleware chain with our test handler
			handler := WithRequestID(HTTPMiddleware(tt.handler))

		// Serve the request
		handler.ServeHTTP(rr, req)

		// Check the response status code
		assert.Equal(t, tt.expectedStatus, rr.Code, "Unexpected status code")

		// Get the log output
		output := buf.String()

		// Check that the log contains the expected fields
		for _, expected := range tt.expectedLogs {
			assert.Contains(t, output, expected, "Log output should contain %q", expected)
		}

		// Check that the response headers include the request ID
		requestID := rr.Header().Get("X-Request-Id")
		assert.NotEmpty(t, requestID, "Response should include X-Request-Id header")

		// Check that the request ID is included in the logs
		assert.Contains(t, output, requestID, "Log output should include the request ID")
	})
}
}

func TestResponseWriterWrapper(t *testing.T) {
	// Create a test response writer
	rr := httptest.NewRecorder()

	// Create a response writer wrapper
	w := &responseWriterWrapper{
		ResponseWriter: rr,
		status:         http.StatusOK,
	}

	// Test WriteHeader
	testStatus := http.StatusCreated
	w.WriteHeader(testStatus)
	assert.Equal(t, testStatus, w.status, "Status should be set")
	assert.Equal(t, testStatus, rr.Code, "Underlying response writer status should be set")

	// Test Write
	testBody := []byte("test response")
	n, err := w.Write(testBody)
	assert.NoError(t, err, "Write should not return an error")
	assert.Equal(t, len(testBody), n, "Write should return the number of bytes written")
	assert.Equal(t, testBody, rr.Body.Bytes(), "Response body should match")

	// Test Write with error
	w = &responseWriterWrapper{
		ResponseWriter: &errorResponseWriter{},
		status:         http.StatusOK,
	}
	n, err = w.Write([]byte("test"))
	assert.Error(t, err, "Write should return an error")
	assert.Equal(t, 0, n, "Write should return 0 bytes written on error")
}

// errorResponseWriter is a mock http.ResponseWriter that always returns an error on Write
type errorResponseWriter struct {
	headers http.Header
}

func (w *errorResponseWriter) Header() http.Header {
	if w.headers == nil {
		w.headers = make(http.Header)
	}
	return w.headers
}

func (w *errorResponseWriter) Write([]byte) (int, error) {
	return 0, assert.AnError
}

func (w *errorResponseWriter) WriteHeader(statusCode int) {}

func TestWithRequestID(t *testing.T) {
	// Create a test request
	req, err := http.NewRequest("GET", "/test", nil)
	require.NoError(t, err, "Failed to create request")

	// Create a test handler that checks for the request ID
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get the request ID from the context
		requestID := r.Context().Value(ContextKeyRequestID).(string)
		assert.NotEmpty(t, requestID, "Request ID should not be empty")

		// Check that the request ID is in the response headers
		assert.Equal(t, requestID, w.Header().Get("X-Request-Id"), "Response should include X-Request-Id header")

		w.WriteHeader(http.StatusOK)
	})

	// Create a response recorder
	rr := httptest.NewRecorder()

	// Create the middleware chain
	middleware := WithRequestID(handler)

	// Serve the request
	middleware.ServeHTTP(rr, req)

	// Check that the response includes the X-Request-Id header
	assert.NotEmpty(t, rr.Header().Get("X-Request-Id"), "Response should include X-Request-Id header")
}
