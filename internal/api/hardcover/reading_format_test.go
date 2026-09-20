package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/require"
)

// TestLookupsRequestEditionsOfTheContextReadingFormat verifies at the GraphQL
// boundary that ASIN and ISBN lookups ask Hardcover for editions of the
// reading format carried by the context, defaulting to audiobook.
func TestLookupsRequestEditionsOfTheContextReadingFormat(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	lookups := map[string]func(context.Context, *Client) error{
		"asin": func(ctx context.Context, c *Client) error {
			_, err := c.SearchBookByASIN(ctx, "B000000000")
			return err
		},
		"isbn13": func(ctx context.Context, c *Client) error {
			_, err := c.SearchBookByISBN13(ctx, "9781234567890")
			return err
		},
		"isbn10": func(ctx context.Context, c *Client) error {
			_, err := c.SearchBookByISBN10(ctx, "1234567890")
			return err
		},
	}
	formats := map[string]struct {
		ctx  context.Context
		want float64
	}{
		"ebook":     {WithReadingFormat(context.Background(), "ebook"), 4},
		"audiobook": {WithReadingFormat(context.Background(), "Audiobook"), 2},
		"default":   {context.Background(), 2},
	}

	for lookupName, lookup := range lookups {
		for formatName, format := range formats {
			t.Run(lookupName+"/"+formatName, func(t *testing.T) {
				var sent map[string]interface{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					var req struct {
						Variables map[string]interface{} `json:"variables"`
					}
					require.NoError(t, json.Unmarshal(body, &req))
					sent = req.Variables
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"books":[]}}`))
				}))
				defer server.Close()

				require.NoError(t, lookup(format.ctx, CreateTestClient(server)))
				require.Equal(t, format.want, sent["format_id"])
			})
		}
	}
}

// TestGetBookByIDOnlyAcceptsEditionsOfTheContextReadingFormat verifies that the
// book query is restricted to the context's reading format and that an edition
// of another format is never adopted, even if the API returns one.
func TestGetBookByIDOnlyAcceptsEditionsOfTheContextReadingFormat(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	tests := map[string]struct {
		ctx           context.Context
		wantFormatID  float64
		returnedEdits []interface{}
		wantEditionID string
	}{
		"ebook picks the ebook edition": {
			ctx: WithReadingFormat(context.Background(), "ebook"), wantFormatID: 4,
			returnedEdits: []interface{}{
				map[string]interface{}{"id": 10, "reading_format_id": 2},
				map[string]interface{}{"id": 11, "reading_format_id": 4},
			},
			wantEditionID: "11",
		},
		"ebook ignores audiobook-only editions": {
			ctx: WithReadingFormat(context.Background(), "ebook"), wantFormatID: 4,
			returnedEdits: []interface{}{map[string]interface{}{"id": 10, "reading_format_id": 2}},
		},
		"default picks the audiobook edition": {
			ctx: context.Background(), wantFormatID: 2,
			returnedEdits: []interface{}{
				map[string]interface{}{"id": 11, "reading_format_id": 4},
				map[string]interface{}{"id": 10, "reading_format_id": 2},
			},
			wantEditionID: "10",
		},
		"default ignores ebook-only editions": {
			ctx: context.Background(), wantFormatID: 2,
			returnedEdits: []interface{}{map[string]interface{}{"id": 11, "reading_format_id": 4}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var sent map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var req struct {
					Variables map[string]interface{} `json:"variables"`
				}
				require.NoError(t, json.Unmarshal(body, &req))
				sent = req.Variables
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{"books": []interface{}{map[string]interface{}{
						"id": 1, "title": "Book", "editions": tt.returnedEdits,
					}}},
				}))
			}))
			defer server.Close()

			book, err := CreateTestClient(server).GetBookByID(tt.ctx, "1")
			require.NoError(t, err)
			require.NotNil(t, book)
			require.Equal(t, tt.wantFormatID, sent["format_id"])
			require.Equal(t, tt.wantEditionID, book.EditionID)
		})
	}
}
