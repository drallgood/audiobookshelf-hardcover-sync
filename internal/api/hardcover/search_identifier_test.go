package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturedSearch struct {
	Query     string
	Variables map[string]interface{}
}

// captureSearchRequests returns a client whose server records every GraphQL
// request and answers with an empty books list (a clean miss).
func captureSearchRequests(t *testing.T) (*Client, *[]capturedSearch) {
	t.Helper()
	var got []capturedSearch
	client, server := CreateTestClientWithHandler(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		got = append(got, capturedSearch{Query: req.Query, Variables: req.Variables})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[]}}`))
	})
	t.Cleanup(server.Close)
	return client, &got
}

func TestClient_SearchBookByISBN_NormalizesIdentifier(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  string
		field string
	}{
		{name: "hyphenated ISBN-13", raw: "978-1-4028-9462-6", want: "9781402894626", field: "isbn_13"},
		{name: "spaces and dots in ISBN-13", raw: " 978.1402 894626 ", want: "9781402894626", field: "isbn_13"},
		{name: "lowercase x check digit", raw: "0-8044-2957-x", want: "080442957X", field: "isbn_10"},
		{name: "underscore and en dash separators", raw: "0_8044–2957-X", want: "080442957X", field: "isbn_10"},
		{name: "already normalized", raw: "080442957X", want: "080442957X", field: "isbn_10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, got := captureSearchRequests(t)

			var err error
			if tt.field == "isbn_13" {
				_, err = client.SearchBookByISBN13(context.Background(), tt.raw)
			} else {
				_, err = client.SearchBookByISBN10(context.Background(), tt.raw)
			}
			require.NoError(t, err)

			require.Len(t, *got, 1)
			req := (*got)[0]
			assert.Equal(t, tt.want, req.Variables["isbn"])
			assert.Contains(t, req.Query, tt.field+": {_eq: $isbn}")
		})
	}
}

func TestClient_SearchBookByISBN_RejectsEmptyAfterNormalization(t *testing.T) {
	client, got := captureSearchRequests(t)

	_, err := client.SearchBookByISBN10(context.Background(), " - ")
	require.Error(t, err)
	assert.Empty(t, *got, "no request is sent for an ISBN that normalizes to nothing")
}

func TestClient_SearchBookByISBN_KeepsReadingFormatFilter(t *testing.T) {
	tests := []struct {
		name         string
		ctx          context.Context
		wantFormatID float64
	}{
		{name: "defaults to audiobook", ctx: context.Background(), wantFormatID: 2},
		{name: "audiobook", ctx: WithReadingFormat(context.Background(), "audiobook"), wantFormatID: 2},
		{name: "ebook", ctx: WithReadingFormat(context.Background(), "ebook"), wantFormatID: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, got := captureSearchRequests(t)

			_, err := client.SearchBookByISBN13(tt.ctx, "9781402894626")
			require.NoError(t, err)

			require.Len(t, *got, 1)
			req := (*got)[0]
			assert.Equal(t, tt.wantFormatID, req.Variables["format_id"])
			assert.Equal(t, 2, strings.Count(req.Query, "reading_format: {id: {_eq: $format_id}}"),
				"both the books filter and the editions filter keep the reading format")
		})
	}
}
