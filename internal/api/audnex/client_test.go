package audnex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

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
	book, err := client.GetBookByASIN(context.Background(), "INVALID", "")

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
		_, err := w.Write([]byte(`{"asin": "NO_REGION", "title": "No Region Book"}`))
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

	book, err := client.GetBookByASIN(context.Background(), "NO_REGION", "")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if book == nil {
		t.Fatal("Expected book to be non-nil")
	}
}

func TestGetBookByASIN_TypedNotFoundAndRateLimit(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       error
	}{
		{name: "not found", statusCode: http.StatusNotFound, want: ErrNotFound},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, want: ErrRateLimited},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			client := &Client{httpClient: server.Client(), baseURL: server.URL, logger: logger.Get()}
			book, err := client.GetBookByASIN(context.Background(), "B0BXJF2LW5", "us")
			if book != nil {
				t.Fatalf("expected nil book, got %#v", book)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected error matching %v, got %v", tt.want, err)
			}
			if calls != 1 {
				t.Fatalf("expected one request, got %d", calls)
			}
		})
	}
}

func TestGetBookByASIN_RetriesServerErrorsAsTransient(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), baseURL: server.URL, logger: logger.Get()}
	book, err := client.GetBookByASIN(context.Background(), "B0BXJF2LW5", "us")
	if book != nil {
		t.Fatalf("expected nil book, got %#v", book)
	}
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("expected transient error, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected typed HTTP 502 error, got %#v", err)
	}
	if calls != 3 {
		t.Fatalf("expected three attempts, got %d", calls)
	}
}

func TestGetBookByASIN_RetriesRequestTimeoutAsTransient(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusRequestTimeout)
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), baseURL: server.URL, logger: logger.Get()}
	book, err := client.GetBookByASIN(context.Background(), "B0BXJF2LW5", "us")
	if book != nil {
		t.Fatalf("expected nil book, got %#v", book)
	}
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("expected transient error after HTTP 408 retries, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("expected typed HTTP 408 error, got %#v", err)
	}
	if calls != 3 {
		t.Fatalf("expected three attempts, got %d", calls)
	}
}

func TestDiscoverBookByASIN_TriesPreferredThenStopsOnExactMatch(t *testing.T) {
	var regions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		region := r.URL.Query().Get("region")
		regions = append(regions, region)
		if region != "uk" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"asin":"B0BXJF2LW5","title":"UK title","releaseDate":"2024-06-01"}`)
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), baseURL: server.URL, logger: logger.Get()}
	book, region, err := client.DiscoverBookByASIN(context.Background(), "B0BXJF2LW5", "ca")
	if err != nil {
		t.Fatalf("expected discovery success, got %v", err)
	}
	if book == nil || book.ReleaseDate != "2024-06-01" || region != "uk" {
		t.Fatalf("expected UK book and release date, got book=%#v region=%q", book, region)
	}
	if want := []string{"ca", "us", "uk"}; !reflect.DeepEqual(regions, want) {
		t.Fatalf("expected preferred and ordered fallback regions %v, got %v", want, regions)
	}
}

func TestDiscoverBookByASIN_WrongASINAndFullMissIsUnknown(t *testing.T) {
	var regions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		regions = append(regions, r.URL.Query().Get("region"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"asin":"OTHER-ASIN","releaseDate":"2024-06-01"}`)
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), baseURL: server.URL, logger: logger.Get()}
	book, region, err := client.DiscoverBookByASIN(context.Background(), "B0BXJF2LW5", "in")
	if err != nil || book != nil || region != "" {
		t.Fatalf("expected unknown result, got book=%#v region=%q err=%v", book, region, err)
	}
	want := []string{"in", "us", "ca", "uk", "au", "de", "fr", "es", "it", "jp"}
	if !reflect.DeepEqual(regions, want) {
		t.Fatalf("expected ten unique regions %v, got %v", want, regions)
	}
}

func TestDiscoverBookByASIN_RateLimitStopsSweep(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), baseURL: server.URL, logger: logger.Get()}
	book, region, err := client.DiscoverBookByASIN(context.Background(), "B0BXJF2LW5", "ca")
	if !errors.Is(err, ErrRateLimited) || book != nil || region != "" {
		t.Fatalf("expected temporarily unavailable rate-limit result, got book=%#v region=%q err=%v", book, region, err)
	}
	if calls != 1 {
		t.Fatalf("expected sweep to stop after rate limit, got %d requests", calls)
	}
}

func TestDiscoverBookByASIN_OverallDeadlineReturnsTransient(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), baseURL: server.URL, logger: logger.Get()}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	book, region, err := client.DiscoverBookByASIN(ctx, "B0BXJF2LW5", "us")
	if !errors.Is(err, ErrTransient) || book != nil || region != "" {
		t.Fatalf("expected transient deadline result, got book=%#v region=%q err=%v", book, region, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline cause to remain inspectable, got %v", err)
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatalf("discovery exceeded caller deadline: elapsed %s", time.Since(started))
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected deadline to bound the whole sweep after one request, got %d requests", calls)
	}
}
