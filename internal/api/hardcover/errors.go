package hardcover

import (
	"errors"
	"fmt"
)

// BookError is a custom error type that includes a book ID
// This is used to pass book IDs through error chains without string parsing
type BookError struct {
	// The underlying error that occurred
	Err error
	// The book ID related to the error, if available
	BookID string
}

// Error implements the error interface
func (e *BookError) Error() string {
	if e.BookID != "" {
		return fmt.Sprintf("%s (book ID: %s)", e.Err.Error(), e.BookID)
	}
	return e.Err.Error()
}

// Unwrap returns the underlying error
func (e *BookError) Unwrap() error {
	return e.Err
}

// WithBookID wraps an error with a book ID
func WithBookID(err error, bookID string) error {
	if err == nil {
		return nil
	}
	return &BookError{
		Err:    err,
		BookID: bookID,
	}
}

// GetBookID returns the book ID from an error if it's a BookError
func GetBookID(err error) (string, bool) {
	var bookErr *BookError
	if errors.As(err, &bookErr) {
		return bookErr.BookID, bookErr.BookID != ""
	}
	return "", false
}
