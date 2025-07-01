package audnex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

func TestGetBookByASIN(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that the request is made to the correct endpoint
		if r.URL.Path != "/books/B0BXJF2LW5" {
			t.Errorf("Expected request to '/books/B0BXJF2LW5', got '%s'", r.URL.Path)
		}

		// Return a mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"asin": "B0BXJF2LW5",
			"title": "Test Book",
			"authors": ["Test Author"],
			"narrators": ["Test Narrator"],
			"publisherName": "Test Publisher",
			"releaseDate": "2023-05-15",
			"image": "https://example.com/cover.jpg",
			"isbn": "1234567890",
			"language": "English",
			"runtimeLengthMin": 480
		}`))
	}))
	defer server.Close()

	// Create a client that uses the mock server
	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		logger:     logger.Get(),
	}

	// Call the method
	book, err := client.GetBookByASIN(context.Background(), "B0BXJF2LW5", "")

	// Check the results
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if book == nil {
		t.Fatal("Expected book to be non-nil")
	}
	if book.ASIN != "B0BXJF2LW5" {
		t.Errorf("Expected ASIN to be 'B0BXJF2LW5', got '%s'", book.ASIN)
	}
	if book.Title != "Test Book" {
		t.Errorf("Expected Title to be 'Test Book', got '%s'", book.Title)
	}
	if book.ReleaseDate != "2023-05-15" {
		t.Errorf("Expected ReleaseDate to be '2023-05-15', got '%s'", book.ReleaseDate)
	}
}

func TestGetBookByASIN_NotFound(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a 404 response
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Create a client that uses the mock server
	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		logger:     logger.Get(),
	}

	// Call the method
	book, err := client.GetBookByASIN(context.Background(), "INVALID", "")

	// Check the results
	if err == nil {
		t.Error("Expected error, got nil")
	}
	if book != nil {
		t.Errorf("Expected book to be nil, got %v", book)
	}
}
