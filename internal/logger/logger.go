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
	// globalLogger is the global logger instance
	globalLogger *Logger

	// once ensures the global logger is only initialized once
	once sync.Once

	// testOnce is used to allow resetting the once variable in tests
	testOnce = &once

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
	level int // Track the log level explicitly as an int to match zerolog's internal representation
}

// GetLevel returns the current log level of the logger
func (l *Logger) GetLevel() zerolog.Level {
	if l == nil {
		return zerolog.NoLevel
	}
	// Return the stored level as a zerolog.Level
	// Note: zerolog.Level is an int8 with these values:
	// DebugLevel = 0, InfoLevel = 1, WarnLevel = 2, ErrorLevel = 3, FatalLevel = 4, PanicLevel = 5, NoLevel = 6, Disabled = -128, TraceLevel = -1
	return zerolog.Level(l.level)
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
		// If still nil, create a minimal logger to avoid nil pointer dereference
		if globalLogger == nil {
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
			logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
			globalLogger = &Logger{Logger: logger}
		}
	}
	return globalLogger
}

// ResetForTesting resets the global logger and sync.Once variable for testing purposes
// This should only be used in tests
func ResetForTesting() {
	globalLogger = nil
	testOnce = &sync.Once{}
}

// Setup initializes the global logger with the given configuration
// Can only be called once - subsequent calls will be ignored
func Setup(cfg Config) {
	testOnce.Do(func() {
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

		// Debug: Print the level value before creating the logger
		println("DEBUG: Creating logger with level:", level, "(int:", int(level), ")")

		// Set up the output writer
		output := cfg.Output
		if output == nil {
			output = os.Stdout
		}

		// Set the global logger level and format
		zerolog.SetGlobalLevel(level)
		zerolog.TimeFieldFormat = cfg.TimeFormat

		// Debug: Print the current global level
		println("DEBUG: Global level set to:", zerolog.GlobalLevel(), "(int:", int(zerolog.GlobalLevel()), ")")

		// Configure the logger based on the format
		var baseLogger zerolog.Logger
		if cfg.Format == FormatConsole {
			// Use console writer for human-readable output
			baseLogger = zerolog.New(zerolog.ConsoleWriter{
				Out:        output,
				TimeFormat: cfg.TimeFormat,
				NoColor:    false,
			})
		} else {
			// Default to JSON
			baseLogger = zerolog.New(output)
		}

		// Debug: Print the base logger level before setting it
		println("DEBUG: Base logger level before setting:", baseLogger.GetLevel(), "(int:", int(baseLogger.GetLevel()), ")")

		// Create the logger with the configured level and timestamp
		logger := baseLogger.Level(level).With().Timestamp().Logger()
		
		// Debug: Print the logger level after setting it
		println("DEBUG: Logger level after setting:", logger.GetLevel(), "(int:", int(logger.GetLevel()), ")")
		
		// Debug: Print the level value before storing it
		println("DEBUG: Storing level in Logger struct as:", level, "(int:", int(level), ")")
		
		// Create our custom logger with the configured zerolog logger
		globalLogger = &Logger{
			Logger: logger,
			level:  int(level), // Store the level as an int
		}

		// Debug: Verify the stored level
		println("DEBUG: Stored level in globalLogger:", globalLogger.GetLevel(), "(int:", globalLogger.level, ")")

		// Log the logger setup with the configured level
		globalLogger.Info().
			Str("level", level.String()).
			Str("format", string(cfg.Format)).
			Str("time_format", cfg.TimeFormat).
			Msg("Logger initialized")
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
