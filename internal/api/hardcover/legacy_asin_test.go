package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchBookByASINPreservesConfiguredRegionLookup(t *testing.T) {
	const asin = "B0LEGACY01"
	var request asinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":12,"title":"Legacy book","book_status_id":1,"editions":[{"id":34,"asin":null,"reading_format_id":2,"book_mappings":[{"external_id":"B0LEGACY01:uk","platform":{"name":"Audible"}}]}]}]}}`))
	}))
	defer server.Close()

	ctx := WithAudnexRegion(context.Background(), "uk")
	book, err := CreateTestClient(server).SearchBookByASIN(ctx, asin)
	require.NoError(t, err)
	require.NotNil(t, book)
	require.Equal(t, "12", book.ID)
	require.Equal(t, "34", book.EditionID)
	require.Equal(t, asin, book.EditionASIN)
	require.Equal(t, asin+":uk", request.Variables["asin_us"])
	require.Equal(t, float64(2), request.Variables["format_id"])
	require.Contains(t, request.Query, "external_id: {_eq: $asin}")
	require.Contains(t, request.Query, "external_id: {_eq: $asin_us}")
	require.Contains(t, request.Query, "limit: 1")
	require.NotContains(t, request.Query, "$asin_ca")
}

func TestSearchBookByASINPreservesLegacyBareMappingMatch(t *testing.T) {
	const asin = "B0LEGACY03"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":13,"title":"Bare mapping book","editions":[{"id":35,"asin":null,"reading_format_id":2,"book_mappings":[{"external_id":"B0LEGACY03","platform":{"name":"Audible"}}]}]}]}}`))
	}))
	defer server.Close()

	book, err := CreateTestClient(server).SearchBookByASIN(context.Background(), asin)
	require.NoError(t, err)
	require.NotNil(t, book)
	require.Equal(t, "13", book.ID)
	require.Equal(t, "35", book.EditionID)
	require.Equal(t, asin, book.EditionASIN)
}

func TestGetEditionByASINPreservesLegacyDuplicateLookup(t *testing.T) {
	const asin = "B0LEGACY02"
	var requests []asinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request asinRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{"data":{"books":[{"id":22,"title":"Existing book","book_status_id":1,"editions":[{"id":220,"asin":null,"isbn_10":"1234567890","reading_format_id":2,"book_mappings":[{"external_id":"B0LEGACY02:uk","platform":{"name":"Audible"}}]}]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"editions":[{"id":220,"book_id":22,"title":"Existing edition","isbn_10":"1234567890","asin":"B0LEGACY02","reading_format_id":2}]}}`))
	}))
	defer server.Close()

	ctx := WithAudnexRegion(context.Background(), "uk")
	edition, err := CreateTestClient(server).GetEditionByASIN(ctx, asin)
	require.NoError(t, err)
	require.NotNil(t, edition)
	require.Equal(t, "220", edition.ID)
	require.Equal(t, "22", edition.BookID)
	require.Equal(t, "Existing edition", edition.Title)
	require.Len(t, requests, 2)
	require.Contains(t, requests[0].Query, "external_id: {_eq: $asin_us}")
}
