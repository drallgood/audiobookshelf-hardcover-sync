package logger

import (
	"bytes"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			// Reset global state
			zerolog.SetGlobalLevel(zerolog.NoLevel)
			ResetForTesting()

			// Create a buffer to capture output
			var buf bytes.Buffer

			// Debug: Print the test case info
			t.Logf("Running test case: %s, level: %s, expected: %v (%d)",
				tt.name, tt.level, tt.expected, tt.expected)

			// Setup logger with test config
			config := Config{
				Level:      tt.level,
				Output:     &buf, // Use buffer instead of os.Stdout
				TimeFormat: time.RFC3339,
			}

			Setup(config)

			// Get the logger and verify its level
			logger := Get()
			assert.NotNil(t, logger, "Get() returned nil logger")

			// Debug: Print the logger's level right after getting it
			t.Logf("Logger type: %T, level field: %v", logger, logger.level)

			// Debug logging to help diagnose the issue
			actualLevel := logger.GetLevel()
			t.Logf("Test case: %s, Expected level: %v (%d), Actual level: %v (%d)",
				tt.name, tt.expected, tt.expected, actualLevel, actualLevel)

			// Verify the level
			if !assert.Equal(t, tt.expected, actualLevel, "logger level does not match expected") {
				t.Logf("Logger output: %s", buf.String())
				t.Logf("Logger struct: %+v", logger)
			}
		})
	}
}

func TestWithContext(t *testing.T) {
	// Reset global logger
	ResetForTesting()

	// Setup test logger with JSON output
	var buf bytes.Buffer
	Setup(Config{
		Level:  "debug",
		Format: FormatJSON,
		Output: &buf,
	})

	// Create a logger with context fields
	fields := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}

	// Create a child logger with the fields
	logger := Get().With(fields)
	require.NotNil(t, logger)

	// Log a message
	logger.Info("test message")

	// Get the log output
	logOutput := buf.String()
	
	// Verify the output contains our fields
	assert.Contains(t, logOutput, "\"key1\":\"value1\"")
	assert.Contains(t, logOutput, "\"key2\":42")
}

func TestGet(t *testing.T) {
	// Reset global logger
	ResetForTesting()

	// Setup a test logger
	Setup(Config{
		Level:  "debug",
		Format: FormatJSON,
	})

	// Get the logger
	logger := Get()
	require.NotNil(t, logger)
	
	// Verify it's the same logger we just set up
	assert.Equal(t, globalLogger, logger)
}
