package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogFormat tests the LogFormat type and its methods
func TestLogFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected LogFormat
	}{
		{"json format", "json", FormatJSON},
		{"JSON uppercase", "JSON", FormatJSON},
		{"console format", "console", FormatConsole},
		{"CONSOLE uppercase", "CONSOLE", FormatConsole},
		{"invalid format defaults to json", "invalid", FormatJSON}, // default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseLogFormat(tt.input)
			assert.Equal(t, tt.expected, result, "ParseLogFormat returned unexpected format")
			
			// For invalid input, we expect the default format (JSON)
			if tt.input == "invalid" {
				assert.Equal(t, "json", result.String(), "Expected default format 'json' for invalid input")
			} else {
				assert.Equal(t, strings.ToLower(tt.input), result.String(), "String() returned unexpected format")
			}
		})
	}
}

func TestLogMethods(t *testing.T) {
	// Create a buffer to capture output
	var buf bytes.Buffer
	
	// Reset the global logger for testing
	ResetForTesting()
	
	// Setup the logger with debug level to capture all logs
	config := Config{
		Level:      "debug", // Set to debug to capture all log levels
		Format:     FormatJSON, // Explicitly set format to JSON
		Output:     &buf,
		TimeFormat: time.RFC3339,
	}
	Setup(config)
	
	// Get the logger instance
	log := Get()
	
	// Ensure the logger is using our buffer
	if log == nil {
		t.Fatal("Failed to get logger instance")
	}

	tests := []struct {
		name     string
		logFunc  func()
		contains string
		level    string
	}{
		{
			"Info",
			func() { log.Info("test info") },
			"test info",
			"info",
		},
		{
			"Infof",
			func() { log.Infof("test %s", "infof") },
			"test infof",
			"info",
		},
		{
			"Warn",
			func() { log.Warn("test warn") },
			"test warn",
			"warn",
		},
		{
			"Warnf",
			func() { log.Warnf("test %s", "warnf") },
			"test warnf",
			"warn",
		},
		{
			"Debug",
			func() { log.Debug("test debug") },
			"test debug",
			"debug",
		},
		{
			"Debugf",
			func() { log.Debugf("test %s", "debugf") },
			"test debugf",
			"debug",
		},
		{
			"Error",
			func() { log.Error("test error") },
			"test error",
			"error",
		},
		{
			"Errorf",
			func() { log.Errorf("test %s", "errorf") },
			"test errorf",
			"error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			tt.logFunc()
			
			// Get the raw output
			output := buf.String()
			
			// Check if the output contains the expected level and message
			// This is more flexible than strict JSON parsing
			assert.Contains(t, output, `"level":"`+tt.level+`"`, "Log output should contain the correct level")
			assert.Contains(t, output, `"message":"`+strings.TrimSuffix(tt.contains, "\n")+`"`, "Log output should contain the correct message")
		})
	}
}

func TestWithFields(t *testing.T) {
	// Create a buffer to capture output
	var buf bytes.Buffer
	
	// Create a logger with the buffer as output
	logger := &Logger{
		Logger: zerolog.New(&buf).With().Timestamp().Logger(),
	}
	
	// Test With
	logger = logger.With(map[string]interface{}{"service": "test"})
	logger.Info("test with fields")

	// Parse the JSON output
	var output map[string]interface{}
	outputStr := strings.TrimSpace(buf.String())
	err := json.Unmarshal([]byte(outputStr), &output)
	require.NoError(t, err, "Failed to parse log output as JSON")
	assert.Equal(t, "test", output["service"])

	// Test WithFields
	buf.Reset()
	logger = logger.WithFields(map[string]interface{}{"request_id": "12345"})
	logger.Info("test with more fields")

	outputStr = strings.TrimSpace(buf.String())
	err = json.Unmarshal([]byte(outputStr), &output)
	require.NoError(t, err, "Failed to parse log output as JSON")
	assert.Equal(t, "12345", output["request_id"])
	assert.Equal(t, "test", output["service"], "Original fields should be preserved")
}

// TestContext tests the context-related functionality of the logger
func TestContext(t *testing.T) {
	// Reset the global logger before testing
	ResetForTesting()
	
	// Create a buffer for test output
	var buf1, buf2 bytes.Buffer
	
	// Create a new logger with output to buf1
	logger1 := &Logger{
		Logger: zerolog.New(&buf1).With().Timestamp().Logger(),
		level:  int(zerolog.DebugLevel),
	}
	
	// Add a field to the logger using the With method
	logger1 = logger1.With(map[string]interface{}{"test": "logger1"})

	// Test NewContext and FromContext
	ctx := context.Background()
	ctx = NewContext(ctx, logger1)
	
	// Retrieve logger from context
	logFromCtx := FromContext(ctx)
	assert.NotNil(t, logFromCtx, "Logger should be retrievable from context")
	
	// Log a test message with the original logger
	logger1.Info("test message 1")
	
	// Log the same message with the logger from context
	logFromCtx.Info("test message 1 from ctx")
	
	// Check the output from the first logger
	output1 := buf1.String()
	t.Logf("Logger 1 output: %s", output1)
	
	// The output should contain our test field and message
	assert.Contains(t, output1, `"test":"logger1"`, "Logger should have test field")
	assert.Contains(t, output1, `"message":"test message 1"`, "Logger should log the correct message")
	assert.Contains(t, output1, `"message":"test message 1 from ctx"`, "Logger from context should log the correct message")

	// Create a new logger with output to buf2
	logger2 := &Logger{
		Logger: zerolog.New(&buf2).With().Timestamp().Logger(),
		level:  int(zerolog.DebugLevel),
	}
	// Add a field to the new logger
	logger2 = logger2.With(map[string]interface{}{"test": "logger2"})
	
	// Set the new logger in the context
	ctx = WithLogger(ctx, logger2)
	logFromCtx = FromContext(ctx)
	assert.NotNil(t, logFromCtx, "Should get a logger from context after WithLogger")
	
	// Log a message with the new logger
	logFromCtx.Info("test message 2")
	
	// Check the output from the second logger
	output2 := buf2.String()
	t.Logf("Logger 2 output: %s", output2)
	
	// The output should contain the new logger's test field and message
	assert.Contains(t, output2, `"test":"logger2"`, "New logger should have the correct test field")
	assert.Contains(t, output2, `"message":"test message 2"`, "New logger should log the correct message")
	
	// Test that the original logger is not affected by the new logger
	logger1.Info("test message 3")
	output3 := buf1.String()
	t.Logf("Logger 1 final output: %s", output3)
	
	// The original logger's output should contain its original test field and the new message
	assert.Contains(t, output3, `"test":"logger1"`, "Original logger should still have original test field")
	assert.Contains(t, output3, `"message":"test message 3"`, "Original logger should still work")
}

func TestLogLevels(t *testing.T) {
	tests := []struct {
		name          string
		configuredLevel string
		shouldLogDebug bool
		shouldLogInfo  bool
		shouldLogWarn  bool
		shouldLogError bool
	}{
		{
			name:           "debug level - all messages should be logged",
			configuredLevel: "debug",
			shouldLogDebug:  true,
			shouldLogInfo:   true,
			shouldLogWarn:   true,
			shouldLogError:  true,
		},
		{
			name:           "info level - debug messages should be filtered out",
			configuredLevel: "info",
			shouldLogDebug:  false,
			shouldLogInfo:   true,
			shouldLogWarn:   true,
			shouldLogError:  true,
		},
		{
			name:           "warn level - only warn and error messages should be logged",
			configuredLevel: "warn",
			shouldLogDebug:  false,
			shouldLogInfo:   false,
			shouldLogWarn:   true,
			shouldLogError:  true,
		},
		{
			name:           "error level - only error messages should be logged",
			configuredLevel: "error",
			shouldLogDebug:  false,
			shouldLogInfo:   false,
			shouldLogWarn:   false,
			shouldLogError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a buffer to capture output
			var buf bytes.Buffer

			// Reset the global logger for testing
			ResetForTesting()

			// Configure the logger with the test level
			Setup(Config{
				Level:      tt.configuredLevel,
				Format:     FormatJSON, // Use JSON for easier parsing
				Output:     &buf,
				TimeFormat: time.RFC3339,
			})

			// Log messages at different levels
			log := Get()
			log.Debug("debug message")
			log.Info("info message")
			log.Warn("warn message")
			log.Error("error message")

			// Get the output
			output := buf.String()

			// Helper function to check if a message was logged
			messageWasLogged := func(level, message string) bool {
				lines := strings.Split(strings.TrimSpace(output), "\n")
				for _, line := range lines {
					if line == "" {
						continue
					}
					var logEntry map[string]interface{}
					err := json.Unmarshal([]byte(line), &logEntry)
					if err != nil {
						t.Fatalf("Failed to parse log entry: %v", err)
					}
					if logEntry["level"] == level && logEntry["message"] == message {
						return true
					}
				}
				return false
			}

			// Check if each message was logged based on the configured level
			if tt.shouldLogDebug {
				assert.True(t, messageWasLogged("debug", "debug message"), 
					"Debug message should be logged when level is %s", tt.configuredLevel)
			} else {
				assert.False(t, messageWasLogged("debug", "debug message"), 
					"Debug message should not be logged when level is %s", tt.configuredLevel)
			}

			if tt.shouldLogInfo {
				assert.True(t, messageWasLogged("info", "info message"), 
					"Info message should be logged when level is %s", tt.configuredLevel)
			} else {
				assert.False(t, messageWasLogged("info", "info message"), 
					"Info message should not be logged when level is %s", tt.configuredLevel)
			}

			if tt.shouldLogWarn {
				assert.True(t, messageWasLogged("warn", "warn message"), 
					"Warn message should be logged when level is %s", tt.configuredLevel)
			} else {
				assert.False(t, messageWasLogged("warn", "warn message"), 
					"Warn message should not be logged when level is %s", tt.configuredLevel)
			}

			if tt.shouldLogError {
				assert.True(t, messageWasLogged("error", "error message"), 
					"Error message should be logged when level is %s", tt.configuredLevel)
			} else {
				assert.False(t, messageWasLogged("error", "error message"), 
					"Error message should not be logged when level is %s", tt.configuredLevel)
			}
		})
	}
}

// TestLogFormatConfiguration tests the logger's behavior with different log formats
func TestLogFormatConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		format         LogFormat
		shouldBeJSON   bool
		shouldBePretty bool
	}{
		{
			name:           "JSON format",
			format:         FormatJSON,
			shouldBeJSON:   true,
			shouldBePretty: false,
		},
		{
			name:           "Console format",
			format:         FormatConsole,
			shouldBeJSON:   false,
			shouldBePretty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a buffer to capture output
			var buf bytes.Buffer

			// Reset the global logger for testing
			ResetForTesting()

			// Configure the logger with the test format
			Setup(Config{
				Level:      "debug", // Set to debug to ensure all levels are logged
				Format:     tt.format,
				Output:     &buf,
				TimeFormat: time.RFC3339,
			})

			// Clear the buffer after logger initialization
			buf.Reset()

			// Log a test message with a unique identifier
			log := Get()
			testMessage := "test_message_" + t.Name()
			log.Debug(testMessage, map[string]interface{}{"key": "value"})

			// Get the output
			output := buf.String()

			// Basic validation
			assert.NotEmpty(t, output, "Log output should not be empty")

			// For JSON format, verify it's valid JSON and contains our test message
			if tt.shouldBeJSON {
				// Split the output into lines (each log entry is on a separate line)
				lines := strings.Split(strings.TrimSpace(output), "\n")
				var found bool
				
				// Look for our test message in any of the log entries
				for _, line := range lines {
					if line == "" {
						continue
					}
					
					var jsonData map[string]interface{}
					if err := json.Unmarshal([]byte(line), &jsonData); err != nil {
						continue // Skip invalid JSON lines
					}
					
					if msg, ok := jsonData["message"].(string); ok && msg == testMessage {
						found = true
						// Verify the key-value pair is present
						assert.Equal(t, "value", jsonData["key"], "JSON should contain the key-value pair")
						break
					}
				}
				
				assert.True(t, found, "Test message not found in JSON output")
			}

			// For console format, check for the message and key-value pair
			if tt.shouldBePretty {
				// Remove ANSI color codes for easier matching
				ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
				cleanOutput := ansiRegex.ReplaceAllString(output, "")
				
				// Check for our test message and key-value pair
				assert.Contains(t, cleanOutput, testMessage, "Console output should contain the test message")
				assert.Contains(t, cleanOutput, "key=value", "Console output should contain the key-value pair")
			}
		})
	}
}
