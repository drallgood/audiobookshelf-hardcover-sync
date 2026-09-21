// Package editiontest provides HTTP fakes of Hardcover and Audiobookshelf for
// tests of the edition draft and create flow. It is shared by the service, API
// and server tests, which all drive the real clients against these servers.
// It imports no project package and is only meant to be imported by tests.
package editiontest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ExistingEdition is an edition already on Hardcover, found by its ASIN or ISBN.
type ExistingEdition struct {
	EditionID int
	BookID    int
	// ReadingFormat is the edition's Hardcover reading format ID: 0 means an
	// audiobook (2), and -1 an edition with no reading format set. The fake only
	// finds an edition for the format the request filters on, as Hardcover does.
	ReadingFormat int
}

// inFormat reports whether the edition matches the reading format filter the
// Hardcover client sends with an ASIN or ISBN search.
func (e ExistingEdition) inFormat(variables map[string]interface{}) bool {
	format := e.ReadingFormat
	if format == 0 {
		format = 2
	}
	requested, _ := variables["format_id"].(float64)
	return format == int(requested)
}

// HardcoverFake stands in for the Hardcover GraphQL endpoint. It answers person
// searches from a name table and ASIN and ISBN searches from edition tables,
// records insert_edition variables, and can hold requests open so tests can
// overlap them. Set the exported tables and FailWith before the first request.
type HardcoverFake struct {
	*httptest.Server

	Authors map[string]int
	ASINs   map[string]ExistingEdition // editions that already carry an ASIN
	ISBNs   map[string]ExistingEdition // editions that already carry an ISBN
	// FailWith is the GraphQL error message returned for insert_edition.
	FailWith string

	// Entered receives once per insert_edition that has started.
	Entered chan struct{}
	// ReadEntered receives once per read that started while HoldReads was set.
	ReadEntered chan struct{}
	// SearchEntered receives once per title or author search held by HoldSearches.
	SearchEntered chan struct{}

	mu          sync.Mutex
	mutations   []map[string]interface{}
	writes      []string // every GraphQL mutation document received, of any kind
	requests    int      // every request received, of any kind
	holdInsert  chan struct{}
	holdReads   chan struct{}
	holdSearch  chan struct{}
	delayAll    time.Duration
	delayInsert time.Duration
}

// NewHardcoverFake starts a Hardcover fake that is closed with the test.
func NewHardcoverFake(t *testing.T) *HardcoverFake {
	t.Helper()
	fake := &HardcoverFake{
		Authors:       map[string]int{},
		ASINs:         map[string]ExistingEdition{},
		ISBNs:         map[string]ExistingEdition{},
		Entered:       make(chan struct{}, 8),
		ReadEntered:   make(chan struct{}, 64),
		SearchEntered: make(chan struct{}, 8),
	}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

// RecordedMutations returns the variables of every insert_edition received.
func (f *HardcoverFake) RecordedMutations() []map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]interface{}(nil), f.mutations...)
}

// RecordedWrites returns every mutation document received, of any kind.
func (f *HardcoverFake) RecordedWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

// RequestCount returns the number of requests received, of any kind.
func (f *HardcoverFake) RequestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// HoldInsert makes insert_edition wait until ch is closed; nil stops holding.
func (f *HardcoverFake) HoldInsert(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdInsert = ch
}

// HoldReads makes every read (any non-mutation request) wait until ch is
// closed; nil stops holding.
func (f *HardcoverFake) HoldReads(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdReads = ch
}

// HoldSearches makes title and author searches, the reads that no other lookup
// answers, wait until ch is closed; nil stops holding.
func (f *HardcoverFake) HoldSearches(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdSearch = ch
}

// ReleaseHolds closes and clears every hold still set, so a held request can
// unwind. Call it from a cleanup that must run before the service shuts down.
func (f *HardcoverFake) ReleaseHolds() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, hold := range []*chan struct{}{&f.holdInsert, &f.holdReads, &f.holdSearch} {
		if *hold != nil {
			close(*hold)
			*hold = nil
		}
	}
}

// SetDelays makes every response wait for all, and insert_edition for a further
// insert.
func (f *HardcoverFake) SetDelays(all, insert time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delayAll, f.delayInsert = all, insert
}

func (f *HardcoverFake) serve(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	respond := func(data map[string]interface{}) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}

	f.mu.Lock()
	f.requests++
	delayAll, delayInsert := f.delayAll, f.delayInsert
	holdInsert, holdReads, holdSearch := f.holdInsert, f.holdReads, f.holdSearch
	isMutation := strings.HasPrefix(strings.TrimSpace(request.Query), "mutation")
	if isMutation {
		f.writes = append(f.writes, request.Query)
	}
	f.mu.Unlock()

	if !isMutation && holdReads != nil {
		f.ReadEntered <- struct{}{}
		select {
		case <-holdReads:
		case <-r.Context().Done():
			return
		}
	}
	time.Sleep(delayAll)

	switch {
	case strings.Contains(request.Query, "insert_edition"):
		f.mu.Lock()
		f.mutations = append(f.mutations, request.Variables)
		failWith := f.FailWith
		f.mu.Unlock()
		f.Entered <- struct{}{}
		if holdInsert != nil {
			<-holdInsert
		}
		time.Sleep(delayInsert)
		if failWith != "" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]string{{"message": failWith}}})
			return
		}
		respond(map[string]interface{}{"insert_edition": map[string]interface{}{"id": 777, "errors": []string{}}})
	case strings.Contains(request.Query, "insert_image"):
		respond(map[string]interface{}{"insert_image": map[string]interface{}{"id": 55}})
	case strings.Contains(request.Query, "update_edition"):
		respond(map[string]interface{}{"update_edition": map[string]interface{}{"id": 777, "errors": []string{}}})
	case strings.Contains(request.Query, "BookByISBN"):
		isbn, _ := request.Variables["isbn"].(string)
		respond(map[string]interface{}{"books": f.existingBooks(f.ISBNs[isbn], request.Variables, "isbn_13", isbn)})
	case strings.Contains(request.Query, "BookByASIN"):
		asin, _ := request.Variables["asin"].(string)
		respond(map[string]interface{}{"books": f.existingBooks(f.ASINs[asin], request.Variables, "asin", asin)})
	case strings.Contains(request.Query, "query GetEdition("):
		editionID, _ := request.Variables["editionId"].(float64)
		editions := []interface{}{}
		for asin, found := range f.ASINs {
			if float64(found.EditionID) == editionID {
				editions = append(editions, map[string]interface{}{"id": found.EditionID, "book_id": found.BookID, "asin": asin})
			}
		}
		respond(map[string]interface{}{"editions": editions})
	case strings.Contains(request.Query, "SearchPeopleDirect") || strings.Contains(request.Query, "SearchNarrators"):
		name, _ := request.Variables["name"].(string)
		people := []map[string]interface{}{}
		if id, ok := f.Authors[name]; ok {
			people = append(people, map[string]interface{}{"id": id, "name": name, "books_count": 3})
		}
		respond(map[string]interface{}{"authors": people})
	default:
		// Any other read (candidate search, publisher lookup) finds nothing.
		if holdSearch != nil {
			f.SearchEntered <- struct{}{}
			select {
			case <-holdSearch:
			case <-r.Context().Done():
				return
			}
		}
		respond(map[string]interface{}{
			"search":     map[string]interface{}{"error": "", "results": map[string]interface{}{"hits": []interface{}{}}},
			"publishers": []interface{}{},
			"editions":   []interface{}{},
			"books":      []interface{}{},
		})
	}
}

// existingBooks returns the book of found when its reading format matches the
// request's filter, keyed by the identifier field it was found by, or no books.
func (f *HardcoverFake) existingBooks(found ExistingEdition, variables map[string]interface{}, field, value string) []interface{} {
	if found.EditionID == 0 || !found.inFormat(variables) {
		return []interface{}{}
	}
	return []interface{}{map[string]interface{}{
		"id": found.BookID, "title": "Existing",
		"editions": []interface{}{map[string]interface{}{"id": found.EditionID, field: value}},
	}}
}

// AudiobookshelfFake serves /api/items/{id} for a fixed set of items, and the
// endpoints a full sync of a one-book library needs (that book is the item
// named "sync-book", if any).
type AudiobookshelfFake struct {
	*httptest.Server
	// Status, when non-zero, makes every item request fail with that status.
	Status int
}

// NewAudiobookshelfFake starts an Audiobookshelf fake that is closed with the test.
func NewAudiobookshelfFake(t *testing.T, items map[string]map[string]interface{}) *AudiobookshelfFake {
	t.Helper()
	fake := &AudiobookshelfFake{}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/me":
			_, _ = w.Write([]byte(`{"mediaProgress":[],"listeningSessions":[]}`))
		case r.URL.Path == "/api/libraries":
			_, _ = w.Write([]byte(`{"libraries":[{"id":"library","name":"Library"}]}`))
		case r.URL.Path == "/api/libraries/library/items":
			results := []map[string]interface{}{}
			if book, ok := items["sync-book"]; ok {
				results = append(results, book)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
		case strings.HasPrefix(r.URL.Path, "/api/items/"):
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
