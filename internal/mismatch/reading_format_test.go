package mismatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
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

// TestEbookMismatchExportsAnEbookEdition checks the edition export of an ebook
// item: an ebook reading format and label, no audiobook platform hint, no
// audio length, and no "Unabridged" default. An audiobook's export is unchanged.
func TestEbookMismatchExportsAnEbookEdition(t *testing.T) {
	tests := map[string]struct {
		readingFormat  string
		wantFormat     string
		wantReading    string
		wantInfo       string
		wantAudioSecs  int
		wantEditionFmt string
	}{
		"ebook":     {"ebook", "Ebook", "ebook", "", 0, "Ebook"},
		"audiobook": {"", "Audible Audio", "", "Unabridged", 3600, "Audiobook"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"books":[]}}`))
			}))
			defer server.Close()
			hc := hardcover.CreateTestClient(server)
			metadata := MediaMetadata{Title: "Book", AuthorName: "Author", Duration: 3600, ReadingFormat: tt.readingFormat}
			record := NewCollector().AddWithMetadata(metadata, "1", "", "reason", 3600, "abs1", hc, "")
			// An ASIN would otherwise make the record query the public Audnex API.
			record.ASIN = "B0EBOOK001"
			require.Equal(t, tt.wantReading, record.ReadingFormat)
			require.Equal(t, tt.wantEditionFmt, record.EditionFormat)

			export := record.ToEditionExport(logger.WithLogger(context.Background(), logger.Get()), hc)

			require.Equal(t, tt.wantFormat, export.EditionFormat)
			require.Equal(t, tt.wantReading, export.ReadingFormat)
			require.Equal(t, tt.wantInfo, export.EditionInfo)
			require.Equal(t, tt.wantAudioSecs, export.AudioSeconds)
		})
	}
}

// TestSavedExportKeepsTheCanonicalReadingFormatAndNoPlatformInfo checks the
// written edition files: an ebook is exported as "ebook" however the record
// spelled it, and its edition information stays empty (the importer sends it
// as the edition's information) while an audiobook keeps "Unabridged".
func TestSavedExportKeepsTheCanonicalReadingFormatAndNoPlatformInfo(t *testing.T) {
	dir := t.TempDir()
	records := []BookMismatch{
		{BookID: "1", Title: "Ebook", ReadingFormat: "Ebook"},
		{BookID: "2", Title: "Audiobook", DurationSeconds: 60},
	}

	ctx := logger.WithLogger(context.Background(), logger.Get())
	require.NoError(t, saveToFile(ctx, nil, dir, nil, records))

	files, err := filepath.Glob(filepath.Join(dir, "edition_*.json"))
	require.NoError(t, err)
	require.Len(t, files, 2)

	want := []struct{ reading, info string }{{"ebook", ""}, {"", "Unabridged"}}
	for i, file := range files {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		var got map[string]interface{}
		require.NoError(t, json.Unmarshal(data, &got))
		reading, _ := got["reading_format"].(string)
		require.Equal(t, want[i].reading, reading, file)
		require.Equal(t, want[i].info, got["edition_information"], file)
	}
}
