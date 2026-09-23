package audnex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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
		_, err := w.Write([]byte(`{
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
		if err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
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
	book, err := client.GetBookByASIN(context.Background(), "INVALID000", "")

	// Check the results
	if err == nil {
		t.Error("Expected error, got nil")
	}
	if book != nil {
		t.Errorf("Expected book to be nil, got %v", book)
	}
}

func TestGetBookByASIN_WithRegion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/books/B0BXJF2LW5" {
			t.Errorf("Expected request to '/books/B0BXJF2LW5', got '%s'", r.URL.Path)
		}
		if r.URL.Query().Get("region") != "ca" {
			t.Errorf("Expected region query param to be 'ca', got '%s'", r.URL.Query().Get("region"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{
			"asin": "B0BXJF2LW5",
			"title": "Canadian Test Book",
			"authors": ["Author CA"],
			"narrators": ["Narrator CA"],
			"releaseDate": "2023-05-15",
			"language": "English"
		}`))
		if err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		logger:     logger.Get(),
	}

	book, err := client.GetBookByASIN(context.Background(), "B0BXJF2LW5", "ca")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if book == nil {
		t.Fatal("Expected book to be non-nil")
	}
	if book.Title != "Canadian Test Book" {
		t.Errorf("Expected Title to be 'Canadian Test Book', got '%s'", book.Title)
	}
}

func TestGetBookByASIN_NoRegion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("Expected no query string, got '%s'", r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"asin": "NOREGION01", "title": "No Region Book"}`))
		if err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		logger:     logger.Get(),
	}

	book, err := client.GetBookByASIN(context.Background(), "NOREGION01", "")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if book == nil {
		t.Fatal("Expected book to be non-nil")
	}
}

func TestGetBookByASIN_RejectsMalformedPathInputsWithoutRequest(t *testing.T) {
	transport := &countingRoundTripper{}

	client := &Client{
		httpClient: &http.Client{Transport: transport},
		baseURL:    "https://audnex.example",
		logger:     logger.Get(),
	}
	tests := []struct {
		name string
		asin string
	}{
		{name: "path traversal", asin: "B0BXJF2LW5/../../admin"},
		{name: "query injection", asin: "B0BXJF2LW5?region=ca"},
		{name: "fragment injection", asin: "B0BXJF2LW5#fragment"},
		{name: "encoded separator", asin: "B0BXJF2LW5%2Fadmin"},
		{name: "dot segment characters", asin: ".........."},
		{name: "wrong length", asin: "INVALID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := client.GetBookByASIN(context.Background(), tt.asin, ""); err == nil {
				t.Fatal("GetBookByASIN() error = nil, want invalid ASIN error")
			}
			if got := transport.requests.Load(); got != 0 {
				t.Fatalf("outbound requests = %d, want 0 for malformed ASIN", got)
			}
		})
	}
}

type countingRoundTripper struct {
	requests atomic.Int32
}

func (t *countingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    request,
	}, nil
}
