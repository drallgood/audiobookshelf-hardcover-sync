package mismatch

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/stretchr/testify/require"
)

// TestAddWithMetadataEnrichesAgainstTheSourceReadingFormat verifies at the
// GraphQL boundary that the Hardcover lookups made while enriching a mismatch
// ask for editions of the source item's reading format.
func TestAddWithMetadataEnrichesAgainstTheSourceReadingFormat(t *testing.T) {
	tests := map[string]struct {
		readingFormat string
		wantFormatID  float64
	}{
		"ebook":     {"ebook", 4},
		"audiobook": {"audiobook", 2},
		"default":   {"", 2},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var formatIDs []interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var req struct {
					Variables map[string]interface{} `json:"variables"`
				}
				require.NoError(t, json.Unmarshal(body, &req))
				if id, ok := req.Variables["format_id"]; ok {
					mu.Lock()
					formatIDs = append(formatIDs, id)
					mu.Unlock()
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"books":[]}}`))
			}))
			defer server.Close()

			// ISBN-only metadata reaches the Hardcover ISBN lookup without Audnex.
			metadata := MediaMetadata{Title: "Book", AuthorName: "Author", ISBN: "9781234567890", ReadingFormat: tt.readingFormat}
			NewCollector().AddWithMetadata(metadata, "1", "", "reason", 0, "abs1", hardcover.CreateTestClient(server), "")

			mu.Lock()
			defer mu.Unlock()
			require.NotEmpty(t, formatIDs, "enrichment should have queried Hardcover by format")
			for _, id := range formatIDs {
				require.Equal(t, tt.wantFormatID, id)
			}
		})
	}
}
