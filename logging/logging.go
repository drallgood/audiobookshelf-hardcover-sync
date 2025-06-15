package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Logger is a simple wrapper around slog.Logger
type Logger struct {
	*slog.Logger
}

// New creates a new logger with the given log level
func New(level slog.Level) *Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize attribute handling if needed
			return a
		},
	})

	// Initialize metrics if not already done
	initMetrics()

	return &Logger{
		Logger: slog.New(handler),
	}
}

// WithContext returns a new logger with the given context
func (l *Logger) WithContext(ctx context.Context) *Logger {
	// slog.Logger doesn't have a WithContext method, so we'll just return a new logger
	// with the same handler but include the context in the log entries
	return &Logger{
		Logger: l.Logger,
	}
}

// With adds attributes to the logger
func (l *Logger) With(args ...interface{}) *Logger {
	return &Logger{
		Logger: l.Logger.With(args...),
	}
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, args ...interface{}) {
	logCount.WithLabelValues("debug").Inc()
	l.Logger.Debug(msg, args...)
}

// Info logs an info message
func (l *Logger) Info(msg string, args ...interface{}) {
	logCount.WithLabelValues("info").Inc()
	l.Logger.Info(msg, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, args ...interface{}) {
	logCount.WithLabelValues("warn").Inc()
	l.Logger.Warn(msg, args...)
}

// Error logs an error message
func (l *Logger) Error(msg string, err error, args ...interface{}) {
	logCount.WithLabelValues("error").Inc()
	if err != nil {
		args = append([]interface{}{slog.String("error", err.Error())}, args...)
	}
	l.Logger.Error(msg, args...)
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(msg string, err error, args ...interface{}) {
	logCount.WithLabelValues("fatal").Inc()
	l.Error(msg, err, args...)
	os.Exit(1)
}

// Default logger instance
var (
	defaultLogger = New(slog.LevelInfo)

	// Prometheus metrics
	logCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "log_messages_total",
			Help: "Total number of log messages by level",
		},
		[]string{"level"},
	)
)

var metricsInitialized = false

func initMetrics() {
	if metricsInitialized {
		return
	}
	// Initialize metrics with zero values for all log levels
	for _, level := range []string{"debug", "info", "warn", "error", "fatal"} {
		logCount.WithLabelValues(level).Add(0)
	}
	metricsInitialized = true
}

// SetLevel sets the log level for the default logger
func SetLevel(level slog.Level) {
	defaultLogger = New(level)
}

// Package-level logging functions that use the default logger

// Debug logs a debug message using the default logger
func Debug(msg string, args ...interface{}) {
	defaultLogger.Debug(msg, args...)
}

// Info logs an info message using the default logger
func Info(msg string, args ...interface{}) {
	defaultLogger.Info(msg, args...)
}

// Warn logs a warning message using the default logger
func Warn(msg string, args ...interface{}) {
	defaultLogger.Warn(msg, args...)
}

// Error logs an error message using the default logger
func Error(msg string, err error, args ...interface{}) {
	defaultLogger.Error(msg, err, args...)
}

// Fatal logs a fatal message and exits using the default logger
func Fatal(msg string, err error, args ...interface{}) {
	defaultLogger.Fatal(msg, err, args...)
}

// With returns a new logger with the given attributes
func With(args ...interface{}) *Logger {
	return defaultLogger.With(args...)
}

// WithContext returns a new logger with the given context
func WithContext(ctx context.Context) *Logger {
	return defaultLogger.WithContext(ctx)
}

// WithTrace adds trace and span IDs to the logger
func WithTrace(traceID, spanID string) *Logger {
	return defaultLogger.With(
		"trace_id", traceID,
		"span_id", spanID,
	)
}

// WithError adds an error to the logger
func WithError(err error) *Logger {
	return defaultLogger.With("error", err)
}

// InitForTesting initializes the logger for testing
func InitForTesting() {
	defaultLogger = New(slog.LevelDebug)
}

// getSource returns the source file and line number
func getSource() string {
	_, file, line, ok := runtime.Caller(2) // Adjust the call depth as needed
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s:%d", file, line)
}
