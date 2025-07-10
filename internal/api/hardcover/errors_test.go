package hardcover

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBookError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		bookID      string
		expectedMsg string
	}{
		{
			name:        "error with book ID",
			err:         errors.New("book not found"),
			bookID:      "123",
			expectedMsg: "book not found (book ID: 123)",
		},
		{
			name:        "error without book ID",
			err:         errors.New("internal server error"),
			bookID:      "",
			expectedMsg: "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bookErr := &BookError{
				Err:    tt.err,
				BookID: tt.bookID,
			}
			
			// Test Error() method
			assert.Equal(t, tt.expectedMsg, bookErr.Error())
			
			// Test Unwrap() method
			assert.Equal(t, tt.err, bookErr.Unwrap())
		})
	}
}

func TestWithBookID(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		bookID         string
		expectBookID   bool
		expectedBookID string
	}{
		{
			name:           "wrap error with book ID",
			err:            errors.New("book not found"),
			bookID:         "123",
			expectBookID:   true,
			expectedBookID: "123",
		},
		{
			name:         "nil error returns nil",
			err:          nil,
			bookID:       "123",
			expectBookID: false,
		},
		{
			name:           "wrap error with empty book ID",
			err:            errors.New("internal error"),
			bookID:         "",
			expectBookID:   false,
			expectedBookID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrappedErr := WithBookID(tt.err, tt.bookID)
			
			if tt.err == nil {
				assert.Nil(t, wrappedErr)
				return
			}
			
			assert.NotNil(t, wrappedErr)
			
			// Check if the wrapped error is a BookError
			bookID, hasBookID := GetBookID(wrappedErr)
			assert.Equal(t, tt.expectBookID, hasBookID)
			
			if tt.expectBookID {
				assert.Equal(t, tt.expectedBookID, bookID)
			} else {
				assert.Empty(t, bookID)
			}
		})
	}
}

func TestGetBookID(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectBookID   bool
		expectedBookID string
	}{
		{
			name:           "get book ID from BookError",
			err:            &BookError{Err: errors.New("not found"), BookID: "456"},
			expectBookID:   true,
			expectedBookID: "456",
		},
		{
			name:         "no book ID in regular error",
			err:          errors.New("generic error"),
			expectBookID: false,
		},
		{
			name:         "nil error",
			err:          nil,
			expectBookID: false,
		},
		{
			name:           "empty book ID",
			err:            &BookError{Err: errors.New("not found"), BookID: ""},
			expectBookID:   false,
			expectedBookID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bookID, hasBookID := GetBookID(tt.err)
			
			assert.Equal(t, tt.expectBookID, hasBookID)
			assert.Equal(t, tt.expectedBookID, bookID)
		})
	}
}
