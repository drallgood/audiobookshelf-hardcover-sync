package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFetchAudiobookShelfStats_NoEnv(t *testing.T) {
	// This test expects no env vars set, so the function should fail
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
	itemsResp1 := `{"results":[{"id":"a1","title":"Book1","author":"Auth1","type":"audiobook","progress":1.0},{"id":"e1","title":"Epub1","author":"Auth2","type":"epub","progress":0.5}]}`
	itemsResp2 := `{"results":[{"id":"a2","title":"Book2","author":"Auth3","type":"audiobook","progress":0.7}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/libraries" {
			w.WriteHeader(200)
			w.Write([]byte(libResp))
		} else if r.URL.Path == "/api/libraries/lib1/items" {
			w.WriteHeader(200)
			w.Write([]byte(itemsResp1))
		} else if r.URL.Path == "/api/libraries/lib2/items" {
			w.WriteHeader(200)
			w.Write([]byte(itemsResp2))
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
	}
	if books[0].ID != "a1" || books[1].ID != "a2" {
		t.Errorf("unexpected audiobook IDs: %+v", books)
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
	badJSON := `{"results": [ { "id": "a1", "title": "Book1" "author": "Auth1" } ]}` // missing comma
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
	err := syncToHardcover(book)
	if err != nil {
		t.Errorf("expected nil error for unfinished book, got %v", err)
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
