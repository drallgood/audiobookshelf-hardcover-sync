package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestWithContext_NilLogger(t *testing.T) {
	// Test that With handles nil logger gracefully
	var l *Logger
	fields := map[string]interface{}{"test": "value"}
	
	// With should return a new logger even when called on a nil logger
	// This matches the behavior of zerolog's With() method
	result := l.With(fields)
	assert.NotNil(t, result, "Expected non-nil logger")
	assert.NotEqual(t, l, result, "Expected new logger instance")
}

func TestWith_EmptyFields(t *testing.T) {
	// Test With with empty fields
	var buf bytes.Buffer
	logger := &Logger{
		Logger: zerolog.New(&buf).With().Logger(),
	}
	
	result := logger.With(nil)
	assert.NotNil(t, result, "Expected non-nil logger")
	assert.Equal(t, logger, result, "Expected same logger when fields is nil")
	
	result = logger.With(map[string]interface{}{})
	assert.NotNil(t, result, "Expected non-nil logger")
	assert.Equal(t, logger, result, "Expected same logger when fields is empty")
}

func TestWith_WithFields(t *testing.T) {
	// Test With with fields
	var buf bytes.Buffer
	logger := &Logger{
		Logger: zerolog.New(&buf).With().Logger(),
	}
	
	fields := map[string]interface{}{
		"field1": "value1",
		"field2": 42,
	}
	
	result := logger.With(fields)
	assert.NotNil(t, result, "Expected non-nil logger")
	assert.NotEqual(t, logger, result, "Expected new logger instance")
}

func TestFromContext_NilContext(t *testing.T) {
	// Test FromContext with nil context
	logger := FromContext(nil)
	assert.Nil(t, logger, "Expected nil logger from nil context")
}

func TestFromContext_NoLogger(t *testing.T) {
	// Test FromContext with context that doesn't contain a logger
	ctx := context.Background()
	logger := FromContext(ctx)
	assert.Nil(t, logger, "Expected nil logger from context with no logger")
}

func TestWithLogger_NilContext(t *testing.T) {
	// Test WithLogger with nil context
	var buf bytes.Buffer
	logger := &Logger{
		Logger: zerolog.New(&buf).With().Logger(),
	}
	
	// Expect a panic when context is nil
	assert.Panics(t, func() {
		_ = WithLogger(nil, logger)
	}, "Expected panic when context is nil")
}

func TestWithLogger_NilLogger(t *testing.T) {
	// Test WithLogger with nil logger
	// With a nil logger, WithLogger should return the original context
	// and FromContext should return nil
	ctx := context.Background()
	result := WithLogger(ctx, nil)
	
	// The result should be the same context since logger is nil
	assert.Equal(t, ctx, result, "Expected same context when logger is nil")
	
	// The logger from the context should be nil
	loggerFromCtx := FromContext(result)
	assert.Nil(t, loggerFromCtx, "Expected nil logger from context")
}

func TestNewContext_NilLogger(t *testing.T) {
	// Test NewContext with nil logger
	// With a nil logger, NewContext should return the original context
	// and FromContext should return nil
	ctx := context.Background()
	result := NewContext(ctx, nil)
	
	// The result should be the same context since logger is nil
	assert.Equal(t, ctx, result, "Expected same context when logger is nil")
	
	// The logger from the context should be nil
	loggerFromCtx := FromContext(result)
	assert.Nil(t, loggerFromCtx, "Expected nil logger from context")
}

func TestContextChain(t *testing.T) {
	// Test chaining of context operations
	var buf1, buf2 bytes.Buffer
	
	// Create first logger
	logger1 := &Logger{
		Logger: zerolog.New(&buf1).With().Str("logger", "first").Logger(),
	}
	
	// Create second logger
	logger2 := &Logger{
		Logger: zerolog.New(&buf2).With().Str("logger", "second").Logger(),
	}
	
	// Create context with first logger
	ctx1 := NewContext(context.Background(), logger1)
	fromCtx1 := FromContext(ctx1)
	assert.Equal(t, logger1, fromCtx1, "Expected to get first logger from context")
	
	// Update context with second logger
	ctx2 := WithLogger(ctx1, logger2)
	fromCtx2 := FromContext(ctx2)
	assert.Equal(t, logger2, fromCtx2, "Expected to get second logger from updated context")
	
	// Original context should still have first logger
	fromCtx1Again := FromContext(ctx1)
	assert.Equal(t, logger1, fromCtx1Again, "Expected first logger to remain in original context")
}

func TestWith_Logging(t *testing.T) {
	// Test that With properly adds fields to log entries
	var buf bytes.Buffer
	logger := &Logger{
		Logger: zerolog.New(&buf).With().Logger(),
	}
	
	// Add context fields
	fields := map[string]interface{}{
		"request_id": "12345",
		"user_id":    "user1",
	}
	
	loggerWithFields := logger.With(fields)
	loggerWithFields.Info("test message")
	
	// Parse the log output
	var logEntry map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	assert.NoError(t, err, "Failed to unmarshal log entry")
	
	// Verify the fields were added to the log entry
	assert.Equal(t, "12345", logEntry["request_id"], "Expected request_id in log entry")
	assert.Equal(t, "user1", logEntry["user_id"], "Expected user_id in log entry")
	assert.Equal(t, "test message", logEntry["message"], "Expected message in log entry")
}
