package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// isbn10Request is the GraphQL request GetEditionByISBN10 sent to the server.
type isbn10Request struct {
	Query     string
	Variables map[string]interface{}
}

// newISBN10TestClient serves the given books array for every GraphQL request
// and records the last request so tests can assert on the query boundary.
func newISBN10TestClient(t *testing.T, books []map[string]interface{}) (*Client, *isbn10Request) {
	t.Helper()

	got := &isbn10Request{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		got.Query = req.Query
		got.Variables = req.Variables

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"books": books},
		}))
	}))
	t.Cleanup(server.Close)

	return CreateTestClient(server), got
}

func TestClient_GetEditionByISBN10(t *testing.T) {
	hitBooks := []map[string]interface{}{{
		"id":             "123",
		"title":          "Test Book",
		"book_status_id": 1,
		"canonical_id":   nil,
		"editions": []map[string]interface{}{{
			"id":                "456",
			"asin":              nil,
			"isbn_13":           "9781234567890",
			"isbn_10":           "1234567890",
			"reading_format_id": 2,
		}},
	}}

	t.Run("hit maps edition and book ids and queries isbn_10", func(t *testing.T) {
		client, got := newISBN10TestClient(t, hitBooks)

		edition, err := client.GetEditionByISBN10(context.Background(), "1234567890")
		require.NoError(t, err)
		require.NotNil(t, edition)
		assert.Equal(t, "456", edition.ID)
		assert.Equal(t, "123", edition.BookID)
		assert.Equal(t, "Test Book", edition.Title)
		assert.Equal(t, "1234567890", edition.ISBN10)
		assert.Equal(t, "9781234567890", edition.ISBN13)

		assert.Contains(t, got.Query, "isbn_10: {_eq: $isbn}")
		assert.NotContains(t, got.Query, "isbn_13: {_eq")
		assert.Equal(t, "1234567890", got.Variables["isbn"])
		assert.EqualValues(t, models.ReadingFormatID(""), got.Variables["format_id"])
	})

	t.Run("hyphens are stripped from the requested isbn", func(t *testing.T) {
		client, got := newISBN10TestClient(t, hitBooks)

		_, err := client.GetEditionByISBN10(context.Background(), "12-34 56-7890")
		require.NoError(t, err)
		assert.Equal(t, "1234567890", got.Variables["isbn"])
	})

	t.Run("format filter follows the reading format on the context", func(t *testing.T) {
		client, got := newISBN10TestClient(t, hitBooks)

		ctx := models.WithReadingFormat(context.Background(), models.ReadingFormatEbook)
		_, err := client.GetEditionByISBN10(ctx, "1234567890")
		require.NoError(t, err)

		assert.Contains(t, got.Query, "reading_format: {id: {_eq: $format_id}}")
		assert.EqualValues(t, models.ReadingFormatID(models.ReadingFormatEbook), got.Variables["format_id"])
		assert.NotEqual(t, models.ReadingFormatID(""), models.ReadingFormatID(models.ReadingFormatEbook))
	})

	t.Run("miss returns an error", func(t *testing.T) {
		client, _ := newISBN10TestClient(t, []map[string]interface{}{})

		edition, err := client.GetEditionByISBN10(context.Background(), "0000000000")
		require.Error(t, err)
		assert.Nil(t, edition)
		assert.True(t, strings.Contains(err.Error(), "no book found with ISBN-10"), err.Error())
	})

	t.Run("empty isbn returns an error", func(t *testing.T) {
		client, _ := newISBN10TestClient(t, hitBooks)

		edition, err := client.GetEditionByISBN10(context.Background(), "")
		require.Error(t, err)
		assert.Nil(t, edition)
	})
}
