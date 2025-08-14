package logger

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

func init() {
	// Disable zerolog's global logger to prevent early initialization
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	zerolog.DefaultContextLogger = &zerolog.Logger{}
}

var (
	// globalLogger is the global logger instance
	globalLogger *Logger

	// once ensures the global logger is only initialized once
	once sync.Once

	// defaultConfig is the default logger configuration
	// Use console format as default to match user expectations
	defaultConfig = Config{
		Level:      "info",
		Format:     FormatConsole,
		TimeFormat: time.RFC3339,
	}
)

// Logger wraps zerolog.Logger to provide our own interface
type Logger struct {
	zerolog.Logger
	level int // Track the log level explicitly as an int to match zerolog's internal representation
}

// GetLevel returns the current log level of the logger
func (l *Logger) GetLevel() zerolog.Level {
	if l == nil {
		return zerolog.NoLevel
	}
	// Return the level from our stored level field
	// This ensures we're returning the level that was explicitly set
	level := zerolog.Level(l.level)
	// Handle the case where level is NoLevel (6) - default to InfoLevel
	if level == zerolog.NoLevel {
		return zerolog.InfoLevel
	}
	return level
}

// LogFormat defines the available log formats
type LogFormat string

const (
	// FormatJSON is the JSON format
	FormatJSON LogFormat = "json"
	// FormatConsole is the console format
	FormatConsole LogFormat = "console"
)

// String returns the string representation of the log format
func (f LogFormat) String() string {
	return string(f)
}

// ParseLogFormat parses a string into a LogFormat
func ParseLogFormat(format string) LogFormat {
	switch strings.ToLower(format) {
	case "console":
		return FormatConsole
	case "json":
		return FormatJSON
	default:
		return FormatJSON // Default to JSON
	}
}

// Config holds the configuration for the logger
type Config struct {
	// Level is the log level (debug, info, warn, error, fatal, panic)
	Level string
	// Format is the log format (json, console)
	Format LogFormat
	// Output is the output writer (default: os.Stdout)
	Output io.Writer
	// TimeFormat is the time format (default: time.RFC3339)
	TimeFormat string
}

// HTTPMiddleware is a middleware that logs HTTP requests
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		l := Get()

		// Create a response writer that captures the status code
		rww := &responseWriterWrapper{ResponseWriter: w, status: http.StatusOK}

		// Process the request
		next.ServeHTTP(rww, r)

		// Calculate request duration
		duration := time.Since(start)

		// Get the client IP, checking X-Forwarded-For header first
		ip := r.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = r.RemoteAddr
		}

		// Get the request ID from context if available
		fields := map[string]interface{}{
			"method":     r.Method,
			"path":       r.URL.Path,
			"query":      r.URL.RawQuery,
			"ip":         ip,
			"user_agent": r.UserAgent(),
			"status":     rww.status,
			"duration":   duration.String(),
		}

		// Add request ID to log fields if available
		if requestID, ok := r.Context().Value(ContextKeyRequestID).(string); ok && requestID != "" {
			fields["request_id"] = requestID
		}

		// Log the request with all details
		l.Info("HTTP request", fields)
	})
}

// responseWriterWrapper wraps http.ResponseWriter to capture the status code
type responseWriterWrapper struct {
	http.ResponseWriter
	status int
}

func (r *responseWriterWrapper) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Get returns the global logger instance
func Get() *Logger {
	once.Do(func() {
		if globalLogger == nil {
			// Initialize with our default configuration
			setupLogger(defaultConfig)
		}
	})
	return globalLogger
}

// ResetForTesting resets the global logger and sync.Once variable for testing purposes
// This should only be used in tests
func ResetForTesting() {
	globalLogger = nil
	once = sync.Once{}
}

// Setup initializes the global logger with the given configuration
// Can only be called once - subsequent calls will be ignored
func Setup(cfg Config) {
	once.Do(func() {
		setupLogger(cfg)
	})
}

// ForceSetup forces re-initialization of the global logger with the given configuration
// This bypasses the once.Do() protection and should be used carefully
func ForceSetup(cfg Config) {
	setupLogger(cfg)
	// Log a re-initialization message with the new format
	if globalLogger != nil {
		globalLogger.Info("Logger re-initialized with new configuration", map[string]interface{}{
			"format":      string(cfg.Format),
			"time_format": cfg.TimeFormat,
		})
	}
}

// setupLogger is the internal function that actually sets up the logger
func setupLogger(cfg Config) {
	// Parse log level with default to InfoLevel if not specified
	level := zerolog.InfoLevel
	if cfg.Level != "" {
		var err error
		level, err = zerolog.ParseLevel(cfg.Level)
		if err != nil {
			level = zerolog.InfoLevel // Default to info level if invalid
		}
	}

	// Set default values if not provided
	if cfg.Format == "" {
		cfg.Format = FormatJSON
	}
	if cfg.TimeFormat == "" {
		cfg.TimeFormat = time.RFC3339
	}

	// Set up the output writer
	output := cfg.Output
	if output == nil {
		output = os.Stdout
	}

	// Create the base logger with the specified level
	var logger zerolog.Logger

	// Configure the logger based on the format
	switch cfg.Format {
	case FormatConsole:
		logger = zerolog.New(zerolog.ConsoleWriter{
			Out:        output,
			TimeFormat: cfg.TimeFormat,
		})
	default: // Default to JSON
		logger = zerolog.New(output)
	}

	// Configure the logger with the specified level and timestamp
	logger = logger.Level(level).With().Timestamp().Logger()

	// Set the global log level to ensure consistency
	zerolog.SetGlobalLevel(level)

	// Create our wrapper logger with the configured logger
	globalLogger = &Logger{
		Logger: logger,
		level:  int(level), // Store the level as an int
	}

	// Log the logger setup with the configured level
	// Note: This message will use the newly configured format
	globalLogger.Info("Logger initialized", map[string]interface{}{
		"format":      string(cfg.Format),
		"time_format": cfg.TimeFormat,
	})
}

// WithContext adds context to the logger
func WithContext(fields map[string]interface{}) *Logger {
	log := Get()
	if log == nil {
		// If no logger is set up, create a new one with default config
		Setup(defaultConfig)
		log = Get()
		if log == nil {
			// If still nil, create a minimal logger to avoid nil pointer dereference
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
			logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
			return &Logger{Logger: logger}
		}
	}

	if fields == nil {
		return log
	}

	logger := log.Logger
	for k, v := range fields {
		logger = logger.With().Interface(k, v).Logger()
	}
	return &Logger{Logger: logger}
}

// ContextKey is a type for context keys
type ContextKey string

// ContextKeyRequestID is the key used to store the request ID in the context
const ContextKeyRequestID ContextKey = "request_id"

// loggerKey is the key used to store the logger in the context
// This is an unexported type to avoid collisions with other context keys
type loggerKey struct{}

// WithLogger adds a logger to the context
// If logger is nil, the original context is returned unchanged
func WithLogger(ctx context.Context, logger *Logger) context.Context {
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerKey{}, logger)
}

// NewContext creates a new context with the logger
// If logger is nil, the original context is returned unchanged
func NewContext(ctx context.Context, logger *Logger) context.Context {
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerKey{}, logger)
}

// FromContext returns the logger from the context
// If the context is nil or doesn't contain a logger, it returns nil
func FromContext(ctx context.Context) *Logger {
	if ctx == nil {
		return nil
	}
	if l, ok := ctx.Value(loggerKey{}).(*Logger); ok {
		return l
	}
	return nil
}

// WithFields adds the given fields to the logger and returns a new logger instance
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	if l == nil {
		return Get()
	}

	if len(fields) == 0 {
		return l
	}

	logger := l.Logger
	for k, v := range fields {
		logger = logger.With().Interface(k, v).Logger()
	}

	return &Logger{
		Logger: logger,
		level:  l.level,
	}
}

// Info logs a message at Info level with the given message and optional fields
func (l *Logger) Info(msg string, fields ...map[string]interface{}) {
	if l == nil {
		return
	}

	if msg == "" {
		msg = "" // Use empty string as default message if not provided
	}

	if len(fields) > 0 && fields[0] != nil && len(fields[0]) > 0 {
		l.WithFields(fields[0]).Logger.Info().Msg(msg)
	} else {
		l.Logger.Info().Msg(msg)
	}
}

// Infof logs a message at Info level with the given message
func (l *Logger) Infof(format string, args ...interface{}) {
	if l == nil {
		return
	}
	l.Logger.Info().Msgf(format, args...)
}

// Warn logs a message at Warn level with the given message and optional fields
func (l *Logger) Warn(msg string, fields ...map[string]interface{}) {
	if l == nil {
		return
	}

	if msg == "" {
		msg = "" // Use empty string as default message if not provided
	}

	if len(fields) > 0 && fields[0] != nil && len(fields[0]) > 0 {
		l.WithFields(fields[0]).Logger.Warn().Msg(msg)
	} else {
		l.Logger.Warn().Msg(msg)
	}
}

// Warnf logs a message at Warn level with the given format and args
func (l *Logger) Warnf(format string, args ...interface{}) {
	if l == nil {
		return
	}
	l.Logger.Warn().Msgf(format, args...)
}

// Debug logs a message at Debug level with the given message and optional fields
func (l *Logger) Debug(msg string, fields ...map[string]interface{}) {
	if l == nil {
		return
	}

	if msg == "" {
		msg = "" // Use empty string as default message if not provided
	}

	if len(fields) > 0 && fields[0] != nil && len(fields[0]) > 0 {
		l.WithFields(fields[0]).Logger.Debug().Msg(msg)
	} else {
		l.Logger.Debug().Msg(msg)
	}
}

// Debugf logs a message at Debug level with the given format and args
func (l *Logger) Debugf(format string, args ...interface{}) {
	if l == nil {
		return
	}
	l.Logger.Debug().Msgf(format, args...)
}

// Error logs a message at Error level with the given message and optional fields
func (l *Logger) Error(msg string, fields ...map[string]interface{}) {
	if l == nil {
		return
	}

	if msg == "" {
		msg = "" // Use empty string as default message if not provided
	}

	if len(fields) > 0 && fields[0] != nil && len(fields[0]) > 0 {
		l.WithFields(fields[0]).Logger.Error().Msg(msg)
	} else {
		l.Logger.Error().Msg(msg)
	}
}

// Errorf logs a message at Error level with the given format and args
func (l *Logger) Errorf(format string, args ...interface{}) {
	if l == nil {
		return
	}
	l.Logger.Error().Msgf(format, args...)
}

// With creates a child logger with the given fields
func (l *Logger) With(fields map[string]interface{}) *Logger {
	if l == nil {
		return Get()
	}
	return l.WithFields(fields)
}
