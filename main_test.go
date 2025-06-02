package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFetchAudiobookShelfStats_NoEnv(t *testing.T) {
	// Clear env vars to simulate missing configuration
	os.Unsetenv("AUDIOBOOKSHELF_URL")
	os.Unsetenv("AUDIOBOOKSHELF_TOKEN")
	os.Unsetenv("HARDCOVER_TOKEN")
	_, err := fetchAudiobookShelfStats()
	if err == nil {
		t.Error("expected error when env vars are missing, got nil")
	}
}

func TestFetchAudiobookShelfStats_404(t *testing.T) {
	// Start a test server that always returns 404 for any path
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()

	os.Setenv("AUDIOBOOKSHELF_URL", ts.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "dummy")

	_, err := fetchAudiobookShelfStats()
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error from /api/libraries, got: %v", err)
	}
}

func TestFetchAudiobookShelfStats_LibraryItems404(t *testing.T) {
	// /api/libraries returns one library, but /api/libraries/{id}/items returns 404
	libResp := `{"libraries":[{"id":"lib1","name":"Test Library"}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/libraries" {
			w.WriteHeader(200)
			w.Write([]byte(libResp))
		} else if strings.HasPrefix(r.URL.Path, "/api/libraries/") && strings.HasSuffix(r.URL.Path, "/items") {
			w.WriteHeader(404)
		} else {
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	os.Setenv("AUDIOBOOKSHELF_URL", ts.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "dummy")

	books, err := fetchAudiobookShelfStats()
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if len(books) != 0 {
		t.Errorf("expected 0 audiobooks, got: %d", len(books))
	}
}

func TestFetchAudiobookShelfStats_MultipleLibrariesAndItems(t *testing.T) {
	// /api/libraries returns two libraries, each with items (some audiobooks, some not)
	libResp := `{"libraries":[{"id":"lib1","name":"Lib1"},{"id":"lib2","name":"Lib2"}]}`
	itemsResp1 := `{"results":[{"id":"a1","mediaType":"book","media":{"id":"m1","metadata":{"title":"Book1","authorName":"Auth1"}},"progress":1.0},{"id":"e1","mediaType":"epub","media":{"id":"m2","metadata":{"title":"Epub1","authorName":"Auth2"}},"progress":0.5}]}`
	itemsResp2 := `{"results":[{"id":"a2","mediaType":"book","media":{"id":"m3","metadata":{"title":"Book2","authorName":"Auth3"}},"progress":0.7}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/libraries" {
			w.WriteHeader(200)
			w.Write([]byte(libResp))
		} else if strings.HasPrefix(r.URL.Path, "/api/libraries/") && strings.HasSuffix(r.URL.Path, "/items") {
			if strings.Contains(r.URL.Path, "lib1") {
				w.WriteHeader(200)
				w.Write([]byte(itemsResp1))
			} else if strings.Contains(r.URL.Path, "lib2") {
				w.WriteHeader(200)
				w.Write([]byte(itemsResp2))
			} else {
				w.WriteHeader(404)
			}
		} else {
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	os.Setenv("AUDIOBOOKSHELF_URL", ts.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "dummy")

	books, err := fetchAudiobookShelfStats()
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if len(books) != 2 {
		t.Errorf("expected 2 audiobooks, got: %d", len(books))
		return
	}
	if books[0].ID != "a1" || books[1].ID != "a2" {
		t.Errorf("unexpected audiobook IDs: %+v", books)
	}
	if books[0].Title != "Book1" || books[1].Title != "Book2" {
		t.Errorf("unexpected audiobook titles: %+v", books)
	}
	if books[0].Author != "Auth1" || books[1].Author != "Auth3" {
		t.Errorf("unexpected audiobook authors: %+v", books)
	}
}

func TestFetchLibraryItems_EmptyResults(t *testing.T) {
	itemsResp := `{"results":[]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(itemsResp))
	}))
	defer ts.Close()
	os.Setenv("AUDIOBOOKSHELF_URL", ts.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "dummy")
	items, err := fetchLibraryItems("lib1")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got: %d", len(items))
	}
}

func TestFetchLibraryItems_MalformedJSON(t *testing.T) {
	badJSON := `{"results": [ { "id": "a1", "mediaType": "book" "media": { "id": "m1", "metadata": { "title": "Book1" } } } ]}` // missing comma

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(badJSON))
	}))
	defer ts.Close()
	os.Setenv("AUDIOBOOKSHELF_URL", ts.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "dummy")
	_, err := fetchLibraryItems("lib1")
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestFetchLibraries_MalformedJSON(t *testing.T) {
	badJSON := `{"libraries": [ { "id": "lib1" "name": "Lib1" } ]}` // missing comma

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(badJSON))
	}))
	defer ts.Close()
	os.Setenv("AUDIOBOOKSHELF_URL", ts.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "dummy")
	_, err := fetchLibraries()
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestSyncToHardcover_NotFinished(t *testing.T) {
	book := Audiobook{Title: "Test", Author: "Author", Progress: 0.5}
	// Save and clear HARDCOVER_TOKEN
	oldToken := os.Getenv("HARDCOVER_TOKEN")
	os.Setenv("HARDCOVER_TOKEN", "dummy")
	defer os.Setenv("HARDCOVER_TOKEN", oldToken)
	// Expect an error because the dummy token will fail the API call
	err := syncToHardcover(book)
	if err == nil {
		t.Error("expected error for unfinished book with dummy token, got nil")
	}
}

func TestSyncToHardcover_Finished_NoToken(t *testing.T) {
	book := Audiobook{Title: "Test", Author: "Author", Progress: 1.0}
	// Save and clear HARDCOVER_TOKEN
	oldToken := os.Getenv("HARDCOVER_TOKEN")
	os.Setenv("HARDCOVER_TOKEN", "")
	defer os.Setenv("HARDCOVER_TOKEN", oldToken)
	err := syncToHardcover(book)
	if err == nil {
		t.Error("expected error when HARDCOVER_TOKEN is missing, got nil")
	}
}

func TestRunSync_NoPanic(t *testing.T) {
	// Should not panic or crash even if env vars are missing
	runSync()
}

// Test the minimum progress threshold function
func TestGetMinimumProgressThreshold(t *testing.T) {
	// Test default value when env var is not set
	os.Unsetenv("MINIMUM_PROGRESS_THRESHOLD")
	if threshold := getMinimumProgressThreshold(); threshold != 0.01 {
		t.Errorf("expected default threshold 0.01, got %f", threshold)
	}

	// Test valid threshold value
	os.Setenv("MINIMUM_PROGRESS_THRESHOLD", "0.05")
	if threshold := getMinimumProgressThreshold(); threshold != 0.05 {
		t.Errorf("expected threshold 0.05, got %f", threshold)
	}

	// Test invalid threshold value (non-numeric)
	os.Setenv("MINIMUM_PROGRESS_THRESHOLD", "invalid")
	if threshold := getMinimumProgressThreshold(); threshold != 0.01 {
		t.Errorf("expected default threshold 0.01 for invalid input, got %f", threshold)
	}

	// Test threshold value too high
	os.Setenv("MINIMUM_PROGRESS_THRESHOLD", "1.5")
	if threshold := getMinimumProgressThreshold(); threshold != 0.01 {
		t.Errorf("expected default threshold 0.01 for value > 1, got %f", threshold)
	}

	// Test negative threshold value
	os.Setenv("MINIMUM_PROGRESS_THRESHOLD", "-0.1")
	if threshold := getMinimumProgressThreshold(); threshold != 0.01 {
		t.Errorf("expected default threshold 0.01 for negative value, got %f", threshold)
	}

	// Test edge case: exactly 1.0
	os.Setenv("MINIMUM_PROGRESS_THRESHOLD", "1.0")
	if threshold := getMinimumProgressThreshold(); threshold != 1.0 {
		t.Errorf("expected threshold 1.0, got %f", threshold)
	}

	// Test edge case: exactly 0.0
	os.Setenv("MINIMUM_PROGRESS_THRESHOLD", "0.0")
	if threshold := getMinimumProgressThreshold(); threshold != 0.0 {
		t.Errorf("expected threshold 0.0, got %f", threshold)
	}

	// Clean up
	os.Unsetenv("MINIMUM_PROGRESS_THRESHOLD")
}

func TestFetchUserProgress_ListeningSessions(t *testing.T) {
	// Mock server for testing listening sessions parsing
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me/listening-sessions" {
			// Mock response with listening sessions data that mimics your AudiobookShelf server
			response := `{
				"sessions": [
					{
						"id": "session1",
						"libraryItemId": "li_item123",
						"currentTime": 5031.93,
						"duration": 21039.77,
						"progress": 0.239,
						"createdAt": 1672531200,
						"updatedAt": 1672531200
					},
					{
						"id": "session2", 
						"libraryItemId": "li_item456",
						"currentTime": 1800.0,
						"duration": 7200.0,
						"progress": 0.25,
						"createdAt": 1672531300,
						"updatedAt": 1672531300
					}
				]
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Set environment variables for the test
	originalURL := os.Getenv("AUDIOBOOKSHELF_URL")
	originalToken := os.Getenv("AUDIOBOOKSHELF_TOKEN")
	originalDebug := debugMode
	
	os.Setenv("AUDIOBOOKSHELF_URL", server.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "test-token")
	debugMode = true
	
	defer func() {
		os.Setenv("AUDIOBOOKSHELF_URL", originalURL)
		os.Setenv("AUDIOBOOKSHELF_TOKEN", originalToken)
		debugMode = originalDebug
	}()

	// Test the function
	progressData, err := fetchUserProgress()
	
	// Verify results
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	
	if len(progressData) != 2 {
		t.Errorf("Expected 2 progress items, got: %d", len(progressData))
	}
	
	// Check first item progress (should be calculated from currentTime/duration)
	expectedProgress1 := 5031.93 / 21039.77 // ~0.239
	if progress, exists := progressData["li_item123"]; !exists {
		t.Errorf("Expected progress for li_item123, but not found")
	} else if math.Abs(progress-expectedProgress1) > 0.001 {
		t.Errorf("Expected progress %.6f for li_item123, got %.6f", expectedProgress1, progress)
	}
	
	// Check second item progress
	expectedProgress2 := 1800.0 / 7200.0 // 0.25
	if progress, exists := progressData["li_item456"]; !exists {
		t.Errorf("Expected progress for li_item456, but not found")
	} else if math.Abs(progress-expectedProgress2) > 0.001 {
		t.Errorf("Expected progress %.6f for li_item456, got %.6f", expectedProgress2, progress)
	}
}

func TestFetchUserProgress_MediaProgress(t *testing.T) {
	// Mock server for testing /api/me endpoint with mediaProgress parsing
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" {
			// Mock response with mediaProgress data that includes manually finished books
			response := `{
				"id": "usr_123456789",
				"username": "testuser",
				"email": "test@example.com",
				"type": "user",
				"token": "test-token",
				"mediaProgress": [
					{
						"id": "progress_id_1",
						"libraryItemId": "li_manual_finished",
						"progress": 0.98,
						"isFinished": true,
						"currentTime": 19800.0,
						"duration": 20000.0
					},
					{
						"id": "progress_id_2",
						"libraryItemId": "li_in_progress",
						"progress": 0.45,
						"isFinished": false,
						"currentTime": 9000.0,
						"duration": 20000.0
					},
					{
						"id": "progress_id_3",
						"libraryItemId": "li_manual_finished_2",
						"progress": 0.75,
						"isFinished": true,
						"currentTime": 15000.0,
						"duration": 20000.0
					}
				],
				"librariesAccessible": []
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Set environment variables for the test
	originalURL := os.Getenv("AUDIOBOOKSHELF_URL")
	originalToken := os.Getenv("AUDIOBOOKSHELF_TOKEN")
	originalDebug := debugMode
	
	os.Setenv("AUDIOBOOKSHELF_URL", server.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "test-token")
	debugMode = true
	
	defer func() {
		os.Setenv("AUDIOBOOKSHELF_URL", originalURL)
		os.Setenv("AUDIOBOOKSHELF_TOKEN", originalToken)
		debugMode = originalDebug
	}()

	// Test the function
	progressData, err := fetchUserProgress()
	
	// Verify results
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	
	if len(progressData) != 3 {
		t.Errorf("Expected 3 progress items, got %d", len(progressData))
	}
	
	// Check manually finished book 1 (should be 1.0 despite progress being 0.98)
	if progress, exists := progressData["li_manual_finished"]; !exists {
		t.Errorf("Expected progress for li_manual_finished, but not found")
	} else if progress != 1.0 {
		t.Errorf("Expected progress 1.0 for manually finished book li_manual_finished, got %.6f", progress)
	}
	
	// Check in-progress book (should keep original progress)
	if progress, exists := progressData["li_in_progress"]; !exists {
		t.Errorf("Expected progress for li_in_progress, but not found")
	} else if progress != 0.45 {
		t.Errorf("Expected progress 0.45 for li_in_progress, got %.6f", progress)
	}
	
	// Check manually finished book 2 (should be 1.0 despite progress being 0.75)
	if progress, exists := progressData["li_manual_finished_2"]; !exists {
		t.Errorf("Expected progress for li_manual_finished_2, but not found")
	} else if progress != 1.0 {
		t.Errorf("Expected progress 1.0 for manually finished book li_manual_finished_2, got %.6f", progress)
	}
}

func TestIntegration_ManuallyFinishedBooks(t *testing.T) {
	// This test validates that manually finished books detected from /api/me 
	// are properly integrated into the overall progress detection hierarchy
	
	// Mock server that provides both /api/me and library endpoints
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			// Mock /api/me response with manually finished book
			response := `{
				"id": "usr_123456789",
				"mediaProgress": [
					{
						"id": "progress_id_manually_finished",
						"libraryItemId": "li_manually_finished_book",
						"progress": 0.85,
						"isFinished": true,
						"currentTime": 17000.0,
						"duration": 20000.0
					}
				]
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		case "/api/libraries":
			// Mock libraries response
			response := `{
				"libraries": [
					{
						"id": "lib_test123",
						"name": "Test Library",
						"mediaType": "book"
					}
				]
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		case "/api/libraries/lib_test123/items":
			// Mock library items with a book that should be detected as manually finished
			response := `{
				"results": [
					{
						"id": "li_manually_finished_book",
						"mediaType": "book",
						"progress": 0.85,
						"isFinished": false,
						"media": {
							"metadata": {
								"title": "Test Manually Finished Book",
								"authorName": "Test Author",
								"duration": 20000.0
							}
						}
					}
				]
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Set environment variables for the test
	originalURL := os.Getenv("AUDIOBOOKSHELF_URL")
	originalToken := os.Getenv("AUDIOBOOKSHELF_TOKEN")
	originalDebug := debugMode
	
	os.Setenv("AUDIOBOOKSHELF_URL", server.URL)
	os.Setenv("AUDIOBOOKSHELF_TOKEN", "test-token")
	debugMode = true
	
	defer func() {
		os.Setenv("AUDIOBOOKSHELF_URL", originalURL)
		os.Setenv("AUDIOBOOKSHELF_TOKEN", originalToken)
		debugMode = originalDebug
	}()

	// Test the integration
	audiobooks, err := fetchAudiobookShelfStats()
	
	// Verify results
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	
	if len(audiobooks) != 1 {
		t.Errorf("Expected 1 audiobook, got %d", len(audiobooks))
		return
	}
	
	book := audiobooks[0]
	
	// Verify that the manually finished book is detected as fully complete (progress 1.0)
	// even though the library item shows progress 0.85
	if book.Progress != 1.0 {
		t.Errorf("Expected manually finished book to have progress 1.0, got %.6f", book.Progress)
	}
	
	if book.Title != "Test Manually Finished Book" {
		t.Errorf("Expected title 'Test Manually Finished Book', got '%s'", book.Title)
	}
	
	if book.Author != "Test Author" {
		t.Errorf("Expected author 'Test Author', got '%s'", book.Author)
	}
}

func TestCheckExistingUserBook_NoBook(t *testing.T) {
	// Mock Hardcover API for checking user books - no books found
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := `{
			"data": {
				"user_books": []
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	// Create a temporary test that uses the mock server
	oldToken := getHardcoverToken()
	os.Setenv("HARDCOVER_TOKEN", "test-token")
	defer func() {
		if oldToken == "" {
			os.Unsetenv("HARDCOVER_TOKEN")
		} else {
			os.Setenv("HARDCOVER_TOKEN", oldToken)
		}
	}()

	// We need to patch the function to use our test server
	// For now, let's test with a simple mock that expects certain behavior
	
	// This test is more for documentation than actual testing since we can't easily 
	// mock the HTTP client in the current implementation
	t.Skip("Skipping integration test - requires mocking HTTP client")
}

func TestSyncToHardcover_ConditionalSync(t *testing.T) {
	// Test that the sync logic properly checks for existing books
	// This is more of an integration test that would require mocking
	// the Hardcover API responses
	
	t.Skip("Skipping integration test - requires full API mocking")
}
