package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetUserBook and GetUserBookWithReads return the same user book; the second also
// returns its reads, newest first, from the same single request.
func TestClient_GetUserBookWithReadsUsesOneRequest(t *testing.T) {
	var requests atomic.Int32
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var request struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		query = request.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user_books":[{
			"id":300,"book_id":100,"status_id":2,"book":{"id":100,"title":"Progress"},
			"edition_id":200,"edition":{"id":200,"asin":"B0ONEREQ01","isbn_13":null,"isbn_10":null,"book_mappings":[]},
			"user_book_reads":[
				{"id":402,"user_book_id":300,"progress":12.5,"progress_seconds":125,"started_at":"2026-01-02","finished_at":null,"edition_id":200},
				{"id":401,"user_book_id":300,"progress":100,"progress_seconds":1000,"started_at":"2025-01-02","finished_at":"2025-02-03","edition_id":null}
			]}]}}`))
	}))
	defer server.Close()
	client := regionalImportTestClient(server.URL)

	book, reads, err := client.GetUserBookWithReads(context.Background(), "300")

	require.NoError(t, err)
	assert.EqualValues(t, 1, requests.Load())
	assert.Contains(t, query, "user_book_reads(order_by: { id: desc })")
	assert.Equal(t, "100", book.ID)
	assert.Equal(t, "200", book.EditionID)
	assert.Equal(t, 2, book.BookStatusID)
	assert.Equal(t, "B0ONEREQ01", book.EditionASIN)
	require.Len(t, reads, 2)
	assert.EqualValues(t, 402, reads[0].ID)
	require.NotNil(t, reads[0].ProgressSeconds)
	assert.Equal(t, 125, *reads[0].ProgressSeconds)
	assert.Nil(t, reads[0].FinishedAt)
	assert.EqualValues(t, 401, reads[1].ID)
	assert.Nil(t, reads[1].EditionID)
}

func TestClient_GetUserBookDoesNotRequestReads(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		query = request.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user_books":[]}}`))
	}))
	defer server.Close()

	_, err := regionalImportTestClient(server.URL).GetUserBook(context.Background(), "300")

	require.ErrorContains(t, err, "user book not found")
	assert.False(t, strings.Contains(query, "user_book_reads"), "the plain lookup must keep its request shape")
}

// A search hit's contributions supply its authors, using the same role rule as
// GetBookByID; a hit without usable contributions leaves them empty.
func TestClient_SearchBooksReadsAuthorsFromContributions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"search":{"error":"","results":{"hits":[
			{"document":{"id":"901","title":"With Authors","slug":"with-authors","image":{"url":"http://example.com/a.jpg"},
				"contributions":[
					{"author":{"id":11,"name":"Ernest Cline"},"contribution":"Author"},
					{"author":{"id":"12","name":"Wil Wheaton"},"contribution":"Narrator"},
					{"author":{"id":13,"name":"Main Author"},"contribution":null}]}},
			{"document":{"id":"902","title":"No Contributions","slug":"no-contributions","image":{"url":""}}},
			{"document":{"id":"903","title":"Unreadable","slug":"unreadable","image":{"url":""},"contributions":"unexpected"}}
		]}}}}`))
	}))
	defer server.Close()

	books, err := regionalImportTestClient(server.URL).SearchBooks(context.Background(), "Title Author", "")

	require.NoError(t, err)
	require.Len(t, books, 3)
	require.Len(t, books[0].Authors, 1)
	assert.Equal(t, "Ernest Cline", books[0].Authors[0].Name)
	assert.Equal(t, "11", books[0].Authors[0].ID)
	assert.Equal(t, "http://example.com/a.jpg", books[0].CoverImageURL)
	assert.Empty(t, books[1].Authors)
	assert.Empty(t, books[2].Authors)
}
