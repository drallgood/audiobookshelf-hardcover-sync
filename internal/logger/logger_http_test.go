package logger

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
		expectedBody   string
		expectedLogs   []string
	}{
		{
			name: "successful request",
			path: "/test",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte("test response")); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "test response",
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
				Level:      "debug",
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
			if tt.expectedBody != "" {
				assert.Equal(t, tt.expectedBody, rr.Body.String(), "Unexpected response body")
			}

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

func TestHTTPMiddlewarePropagatesResponseWriteError(t *testing.T) {
	ResetForTesting()
	Setup(Config{Level: "debug", Format: FormatJSON, Output: &bytes.Buffer{}})

	responseErr := errors.New("response write failed")
	writer := &failingResponseWriter{err: responseErr}
	var observedErr error
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, observedErr = w.Write([]byte("response"))
	}))
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/write-error", nil))

	require.ErrorIs(t, observedErr, responseErr)
}

type failingResponseWriter struct {
	headers http.Header
	err     error
}

func (w *failingResponseWriter) Header() http.Header {
	if w.headers == nil {
		w.headers = make(http.Header)
	}
	return w.headers
}

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func (w *failingResponseWriter) WriteHeader(int) {}

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
