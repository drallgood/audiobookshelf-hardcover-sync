// Package editiontest provides an Audiobookshelf HTTP fake and a minimal
// Hardcover request counter for tests of the read-only edition draft flow.
// It imports no project package and is only meant to be imported by tests.
package editiontest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// HardcoverRequestCounter records any HTTP request sent to the configured
// Hardcover endpoint. It intentionally provides no GraphQL behavior: draft
// tests use it only to verify that previewing sends no request.
type HardcoverRequestCounter struct {
	*httptest.Server
	mu       sync.Mutex
	requests int
}

// NewHardcoverRequestCounter starts a request counter closed with the test.
func NewHardcoverRequestCounter(t *testing.T) *HardcoverRequestCounter {
	t.Helper()
	counter := &HardcoverRequestCounter{}
	counter.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		counter.mu.Lock()
		counter.requests++
		counter.mu.Unlock()
		http.Error(w, "unexpected Hardcover request during edition draft", http.StatusInternalServerError)
	}))
	t.Cleanup(counter.Close)
	return counter
}

// RequestCount returns the number of requests received, of any kind.
func (c *HardcoverRequestCounter) RequestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

// AudiobookshelfFake serves /api/items/{id} for a fixed set of items and the
// endpoints a full sync of a one-book library needs (that book is sync-book).
type AudiobookshelfFake struct {
	*httptest.Server
	// Status, when non-zero, makes every item request fail with that status.
	Status int
	// ReadEntered receives once for each item request held by HoldReads.
	ReadEntered chan struct{}

	mu        sync.Mutex
	holdReads chan struct{}
}

// NewAudiobookshelfFake starts an Audiobookshelf fake closed with the test.
func NewAudiobookshelfFake(t *testing.T, items map[string]map[string]interface{}) *AudiobookshelfFake {
	t.Helper()
	fake := &AudiobookshelfFake{ReadEntered: make(chan struct{}, 64)}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/me":
			_, _ = w.Write([]byte("{\"mediaProgress\":[],\"listeningSessions\":[]}"))
		case r.URL.Path == "/api/libraries":
			_, _ = w.Write([]byte("{\"libraries\":[{\"id\":\"library\",\"name\":\"Library\"}]}"))
		case r.URL.Path == "/api/libraries/library/items":
			results := []map[string]interface{}{}
			if book, ok := items["sync-book"]; ok {
				results = append(results, book)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
		case strings.HasPrefix(r.URL.Path, "/api/items/"):
			if !fake.waitForReadRelease(r) {
				return
			}
			if fake.Status != 0 {
				http.Error(w, "forced failure with abs-token detail", fake.Status)
				return
			}
			item, ok := items[strings.TrimPrefix(r.URL.Path, "/api/items/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(item)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.Close)
	return fake
}

// HoldReads makes item requests wait until ch is closed; nil stops holding.
func (f *AudiobookshelfFake) HoldReads(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdReads = ch
}

// ReleaseReads closes and clears the current hold, if any.
func (f *AudiobookshelfFake) ReleaseReads() {
	f.mu.Lock()
	ch := f.holdReads
	f.holdReads = nil
	f.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (f *AudiobookshelfFake) waitForReadRelease(r *http.Request) bool {
	f.mu.Lock()
	ch := f.holdReads
	f.mu.Unlock()
	if ch == nil {
		return true
	}
	select {
	case f.ReadEntered <- struct{}{}:
	default:
	}
	select {
	case <-ch:
		return true
	case <-r.Context().Done():
		return false
	}
}
