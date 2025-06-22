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

var (
	// globalLogger is the singleton logger instance
	globalLogger *Logger
	// once ensures the logger is only initialized once
	once sync.Once
	// defaultConfig is the default logger configuration
	defaultConfig = Config{
		Level:      "info",
		Format:     FormatJSON,
		TimeFormat: time.RFC3339,
	}
)

// Logger wraps zerolog.Logger to provide our own interface
type Logger struct {
	zerolog.Logger
}

// GetLevel returns the current log level
func (l *Logger) GetLevel() zerolog.Level {
	return zerolog.GlobalLevel()
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

		// Log the request
		duration := time.Since(start)

		// Get the client IP, checking X-Forwarded-For header first
		ip := r.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = r.RemoteAddr
		}

		l.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("query", r.URL.RawQuery).
			Str("ip", ip).
			Str("user_agent", r.UserAgent()).
			Int("status", rww.status).
			Dur("duration", duration).
			Msg("HTTP request")
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
	if globalLogger == nil {
		// Initialize with default config if not already initialized
		Setup(defaultConfig)
	}
	return globalLogger
}

// Setup initializes the global logger with the given configuration
// Can only be called once - subsequent calls will be ignored
func Setup(cfg Config) {
	once.Do(func() {
		// Set default values if not provided
		if cfg.Level == "" {
			cfg.Level = defaultConfig.Level
		}
		// Ensure format is valid
		if cfg.Format == "" {
			cfg.Format = defaultConfig.Format
		} else {
			// Convert string to LogFormat if needed
			switch v := any(cfg.Format).(type) {
			case string:
				cfg.Format = ParseLogFormat(v)
			}
		}
		if cfg.TimeFormat == "" {
			cfg.TimeFormat = defaultConfig.TimeFormat
		}

		// Parse log level
		var level zerolog.Level
		switch strings.ToLower(cfg.Level) {
		case "debug":
			level = zerolog.DebugLevel
		case "info":
			level = zerolog.InfoLevel
		case "warn", "warning":
			level = zerolog.WarnLevel
		case "error":
			level = zerolog.ErrorLevel
		case "fatal":
			level = zerolog.FatalLevel
		case "panic":
			level = zerolog.PanicLevel
		default:
			level = zerolog.InfoLevel
		}

		// Set up the logger
		zerolog.SetGlobalLevel(level)
		zerolog.TimeFieldFormat = cfg.TimeFormat

		// Set up the output writer
		output := cfg.Output
		if output == nil {
			output = os.Stdout
		}

		// Configure the logger based on the format
		var logger zerolog.Logger
		switch cfg.Format {
		case FormatConsole:
			// Use console writer for human-readable output
			logger = zerolog.New(zerolog.ConsoleWriter{
				Out:        output,
				TimeFormat: cfg.TimeFormat,
				NoColor:    false,
			}).With().Timestamp().Logger()
		default: // Default to JSON
			logger = zerolog.New(output).With().Timestamp().Logger()
		}

		// Set the global logger
		globalLogger = &Logger{logger}

		// Log the logger setup
		globalLogger.Info().
			Str("level", cfg.Level).
			Str("format", string(cfg.Format)).
			Str("time_format", cfg.TimeFormat).
			Msg("Logger initialized")
	})
}

// WithContext adds context to the logger
func WithContext(fields map[string]interface{}) *Logger {
	log := Get()
	if fields == nil {
		return log
	}

	logger := log.Logger
	for k, v := range fields {
		logger = logger.With().Interface(k, v).Logger()
	}
	return &Logger{logger}
}

// WithLogger adds a logger to the context
func WithLogger(ctx context.Context, logger *Logger) context.Context {
	return logger.Logger.WithContext(ctx)
}

// NewContext creates a new context with the logger
func NewContext(ctx context.Context, logger *Logger) context.Context {
	return logger.Logger.WithContext(ctx)
}

// FromContext returns the logger from the context
func FromContext(ctx context.Context) *Logger {
	if l, ok := ctx.Value(zerolog.Logger{}).(*Logger); ok {
		return l
	}
	return Get()
}
