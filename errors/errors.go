package errors

import (
	"fmt"
)

// ErrorType represents different categories of errors
type ErrorType int

// String returns the string representation of the ErrorType
func (et ErrorType) String() string {
	switch et {
	case APIError:
		return "APIError"
	case APIUnauthorized:
		return "APIUnauthorized"
	case APIForbidden:
		return "APIForbidden"
	case APIRateLimit:
		return "APIRateLimit"
	case APIConnection:
		return "APIConnection"
	case APIRequestTimeout:
		return "APIRequestTimeout"
	case APIResponseParse:
		return "APIResponseParse"
	case APINotFound:
		return "APINotFound"
	case APIConflict:
		return "APIConflict"
	case APITooManyRequests:
		return "APITooManyRequests"
	case APIInternalServer:
		return "APIInternalServer"
	case ConfigError:
		return "ConfigError"
	case ValidationError:
		return "ValidationError"
	case NotFoundError:
		return "NotFoundError"
	case PermissionError:
		return "PermissionError"
	case DuplicateError:
		return "DuplicateError"
	case TimeoutError:
		return "TimeoutError"
	case NetworkError:
		return "NetworkError"
	case StorageError:
		return "StorageError"
	case DatabaseError:
		return "DatabaseError"
	case AuthenticationError:
		return "AuthenticationError"
	case AuthorizationError:
		return "AuthorizationError"
	case RateLimitError:
		return "RateLimitError"
	case ServiceUnavailable:
		return "ServiceUnavailable"
	case CircuitBreakerError:
		return "CircuitBreakerError"
	case UnknownError:
		return "UnknownError"
	default:
		return fmt.Sprintf("ErrorType(%d)", et)
	}
}

const (
	// API Errors
	APIError ErrorType = iota
	APIUnauthorized
	APIForbidden
	APIRateLimit
	APIConnection
	APIRequestTimeout
	APIResponseParse
	APINotFound
	APIConflict
	APITooManyRequests
	APIInternalServer

	// Application Errors
	ConfigError
	ValidationError
	NotFoundError
	PermissionError
	DuplicateError
	TimeoutError
	NetworkError
	StorageError
	DatabaseError
	AuthenticationError
	AuthorizationError
	RateLimitError
	ServiceUnavailable
	CircuitBreakerError
	UnknownError
)

// Error represents a structured error with type, message, and optional details
//go:generate stringer -type=Error
type Error struct {
	Type    ErrorType
	Message string
	Details string
	Code    int
	Cause   error
}

// Error implements the error interface
func (e *Error) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s (details: %s)", e.Type.String(), e.Message, e.Details)
	}
	return fmt.Sprintf("%s: %s", e.Type.String(), e.Message)
}

// New creates a new structured error
func New(t ErrorType, format string, args ...interface{}) *Error {
	return &Error{
		Type:    t,
		Message: fmt.Sprintf(format, args...),
	}
}

// NewWithCode creates a new structured error with HTTP status code
func NewWithCode(t ErrorType, code int, format string, args ...interface{}) *Error {
	return &Error{
		Type:    t,
		Message: fmt.Sprintf(format, args...),
		Code:    code,
	}
}

// NewWithDetails creates a new structured error with additional details
func NewWithDetails(t ErrorType, format, details string, args ...interface{}) *Error {
	return &Error{
		Type:    t,
		Message: fmt.Sprintf(format, args...),
		Details: details,
	}
}

// NewWithCause creates a new structured error with a cause
func NewWithCause(t ErrorType, cause error, format string, args ...interface{}) *Error {
	return &Error{
		Type:    t,
		Message: fmt.Sprintf(format, args...),
		Cause:   cause,
	}
}

// IsAPIError returns true if the error is an API-related error
func IsAPIError(err error) bool {
	if e, ok := err.(*Error); ok {
		return e.Type >= APIError && e.Type <= APITooManyRequests
	}
	return false
}

// IsValidationError returns true if the error is a validation error
func IsValidationError(err error) bool {
	if e, ok := err.(*Error); ok {
		return e.Type == ValidationError
	}
	return false
}

// IsNotFoundError returns true if the error is a not found error
func IsNotFoundError(err error) bool {
	if e, ok := err.(*Error); ok {
		return e.Type == NotFoundError
	}
	return false
}

// IsRateLimitError returns true if the error is a rate limit error
func IsRateLimitError(err error) bool {
	if e, ok := err.(*Error); ok {
		return e.Type == RateLimitError
	}
	return false
}

// IsCircuitBreakerError returns true if the error is a circuit breaker error
func IsCircuitBreakerError(err error) bool {
	if e, ok := err.(*Error); ok {
		return e.Type == CircuitBreakerError
	}
	return false
}
