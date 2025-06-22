package logger

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Define the context key type
type contextKey string

// Define the context key for IP
const contextKeyIP contextKey = "ip"

func TestSetup(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		expected zerolog.Level
	}{
		{"debug level", "debug", zerolog.DebugLevel},
		{"info level", "info", zerolog.InfoLevel},
		{"warn level", "warn", zerolog.WarnLevel},
		{"error level", "error", zerolog.ErrorLevel},
		{"fatal level", "fatal", zerolog.FatalLevel},
		{"panic level", "panic", zerolog.PanicLevel},
		{"default level", "", zerolog.InfoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset global logger
			globalLogger = nil
			zerolog.SetGlobalLevel(zerolog.NoLevel)

			// Setup logger with test config
			Setup(Config{
				Level:      tt.level,
				Output:     os.Stdout,
				TimeFormat: time.RFC3339,
			})

			// Verify the global level was set correctly
			assert.Equal(t, tt.expected, zerolog.GlobalLevel())
			assert.NotNil(t, Get())
		})
	}
}

func TestHTTPMiddleware(t *testing.T) {
	// Create a buffer to capture log output
	var buf bytes.Buffer
	
	// Reset global logger with our test buffer
	globalLogger = &Logger{
		Logger: zerolog.New(&buf).With().Timestamp().Logger(),
	}

	// Create a test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	})

	// Create a test request
	req, err := http.NewRequest("GET", "/test?param=value", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("X-Forwarded-For", "127.0.0.1:12345")

	rr := httptest.NewRecorder()

	// Create middleware with our test logger
	middleware := HTTPMiddleware(handler)

	// Serve the request
	middleware.ServeHTTP(rr, req)

	// Check the response
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "test response", rr.Body.String())

	// Check the log output
	logOutput := buf.String()
	// The exact format might vary based on zerolog's output format
	assert.Contains(t, logOutput, `"method":"GET"`)
	assert.Contains(t, logOutput, `"path":"/test"`)
	assert.Contains(t, logOutput, `"query":"param=value"`)
	assert.Contains(t, logOutput, `"ip":"127.0.0.1:12345"`)
	assert.Contains(t, logOutput, `"user_agent":"test-agent"`)
	assert.Contains(t, logOutput, `"status":200`)
	assert.Contains(t, logOutput, `"message":"HTTP request"`)
}

func TestWithContext(t *testing.T) {
	// Reset global logger
	globalLogger = nil
	Setup(Config{Level: "debug"})

	// Create a logger with context
	fields := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}

	logger := WithContext(fields)
	require.NotNil(t, logger)

	// Verify the logger has the context fields
	var buf bytes.Buffer
	log := logger.Output(&buf)
	log.Info().Msg("test message")

	logOutput := buf.String()
	assert.Contains(t, logOutput, "\"key1\":\"value1\"")
	assert.Contains(t, logOutput, "\"key2\":42")
}

func TestGet(t *testing.T) {
	// Reset global logger
	globalLogger = nil

	// Before setup, should return a default logger
	logger := Get()
	require.NotNil(t, logger)

	// After setup, should return the configured logger
	Setup(Config{Level: "debug"})
	logger = Get()
	require.NotNil(t, logger)
}

func TestResponseWriterWrapper(t *testing.T) {
	// Create a test response writer
	rr := httptest.NewRecorder()
	wrapper := &responseWriterWrapper{ResponseWriter: rr}

	// Test WriteHeader
	wrapper.WriteHeader(http.StatusNotFound)
	assert.Equal(t, http.StatusNotFound, wrapper.status)
	assert.Equal(t, http.StatusNotFound, rr.Code)

	// Test Write
	n, err := wrapper.Write([]byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "test", rr.Body.String())
}
