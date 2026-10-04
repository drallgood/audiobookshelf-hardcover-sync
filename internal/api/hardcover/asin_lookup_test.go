package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/cache"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/require"
)

type asinRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

func TestSearchBookByASINResultUsesRegionalMappingsAndPrioritizesThem(t *testing.T) {
	const asin = "B0EXAMPLE01"
	var requestCount int
	var request asinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[
			{"id":100,"title":"ASIN fallback","book_status_id":1,"editions":[{"id":1000,"asin":"B0EXAMPLE01","reading_format_id":2,"book_mappings":[]}]},
			{"id":200,"title":"Mapped book","book_status_id":1,"editions":[{"id":2000,"asin":null,"reading_format_id":2,"book_mappings":[{"external_id":"B0EXAMPLE01:uk","platform":{"name":"Audible"}},{"external_id":"B0EXAMPLE01:ca","platform":{"name":"Audible"}}]}]}
		]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByASINResult(context.Background(), asin)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "200", result.Book.ID)
	require.Equal(t, "2000", result.Book.EditionID)
	require.Equal(t, ASINMatchAudibleMapping, result.MatchKind)
	require.Equal(t, asin+":ca", result.RegionalExternalID)
	require.Equal(t, 1, requestCount, "the exact lookup should use one GraphQL read")

	for _, region := range audnexregion.Regions() {
		variable := "asin_" + region
		require.Equal(t, asin+":"+region, request.Variables[variable])
		require.Contains(t, request.Query, "external_id: {_eq: $"+variable+"}")
	}
	require.NotContains(t, request.Query, "external_id: {_eq: $asin}", "bare Audible mappings must not be queried")
	require.Equal(t, float64(2), request.Variables["format_id"])
}

func TestSearchBookByASINResultReportsConflictingRegionalMappings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[
			{"id":1,"title":"First","editions":[{"id":11,"asin":null,"reading_format_id":2,"book_mappings":[{"external_id":"B0EXAMPLE02:us","platform":{"name":"Audible"}}]}]},
			{"id":2,"title":"Second","editions":[{"id":22,"asin":null,"reading_format_id":2,"book_mappings":[{"external_id":"B0EXAMPLE02:ca","platform":{"name":"Audible"}}]}]}
		]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByASINResult(context.Background(), "B0EXAMPLE02")
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrASINLookupConflict)
}

func TestSearchBookByASINResultAcceptsAudiblePlatformCase(t *testing.T) {
	const asin = "B0EXAMPLE06"
	var request asinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[
			{"id":1,"title":"Other platform","editions":[{"id":11,"asin":null,"reading_format_id":2,"book_mappings":[{"external_id":"B0EXAMPLE06:us","platform":{"name":"Amazon"}}]}]},
			{"id":2,"title":"Audible platform","editions":[{"id":22,"asin":null,"reading_format_id":2,"book_mappings":[{"external_id":"B0EXAMPLE06:us","platform":{"name":"audible"}}]}]}
		]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByASINResult(context.Background(), asin)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "2", result.Book.ID)
	require.Equal(t, ASINMatchAudibleMapping, result.MatchKind)
	require.Equal(t, asin+":us", result.RegionalExternalID)
	require.NotContains(t, request.Query, `name: {_eq: "Audible"}`)
}

func TestSearchBookByASINResultDoesNotMatchAudiobookByEditionASIN(t *testing.T) {
	var request asinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":5,"title":"ASIN-only audiobook","editions":[{"id":55,"asin":"B0EXAMPLE05","reading_format_id":2,"book_mappings":[]}]}]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByASINResult(context.Background(), "B0EXAMPLE05")
	require.NoError(t, err)
	require.Nil(t, result, "an editions.asin-only audiobook must not match")
	require.NotContains(t, request.Query, "asin: {_eq: $asin}")
}

func TestSearchBookByASINResultKeepsEbookASINMatchingAndIgnoresMappings(t *testing.T) {
	var request asinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":3,"title":"Kindle edition","editions":[{"id":33,"asin":"B0EXAMPLE03","reading_format_id":4,"book_mappings":[{"external_id":"B0EXAMPLE03:uk","platform":{"name":"Audible"}}]}]}]}}`))
	}))
	defer server.Close()

	ctx := WithReadingFormat(context.Background(), "ebook")
	result, err := CreateTestClient(server).SearchBookByASINResult(ctx, "B0EXAMPLE03")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "33", result.Book.EditionID)
	require.Equal(t, ASINMatchEditionASIN, result.MatchKind)
	require.Empty(t, result.RegionalExternalID)
	require.Equal(t, float64(4), request.Variables["format_id"])
	require.NotContains(t, request.Query, "book_mappings")
	require.Contains(t, request.Query, "asin: {_eq: $asin}")
}

func TestSearchBookByASINResultRejectsUnexpectedReadingFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":4,"title":"Wrong format","editions":[{"id":44,"asin":"B0EXAMPLE04","reading_format_id":4,"book_mappings":[{"external_id":"B0EXAMPLE04:uk","platform":{"name":"Audible"}}]}]}]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByASINResult(context.Background(), "B0EXAMPLE04")
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestSearchBookByEditionASINResultFindsAudiobookEditionByExactASIN(t *testing.T) {
	const asin = "B0EDITION01"
	var request asinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":51,"title":"Exact edition match","book_status_id":1,"canonical_id":9,"editions":[{"id":510,"asin":"B0EDITION01","isbn_13":"9780000000001","isbn_10":"0000000001","reading_format_id":2}]}]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByEditionASINResult(context.Background(), asin)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "51", result.Book.ID)
	require.Equal(t, "510", result.Book.EditionID)
	require.Equal(t, asin, result.Book.EditionASIN)
	require.Equal(t, ASINMatchEditionASIN, result.MatchKind)
	require.Empty(t, result.RegionalExternalID)
	require.Equal(t, float64(2), request.Variables["format_id"])
	require.Equal(t, asin, request.Variables["asin"])
	require.Contains(t, request.Query, "asin: {_eq: $asin}")
	require.Contains(t, request.Query, "reading_format_id: {_eq: $format_id}")
	require.NotContains(t, request.Query, "book_mappings")
}

func TestSearchBookByEditionASINResultFiltersUnexpectedReadingFormat(t *testing.T) {
	var request asinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":52,"title":"Ebook with same ASIN","editions":[{"id":520,"asin":"B0EDITION02","reading_format_id":4}]}]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByEditionASINResult(context.Background(), "B0EDITION02")
	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, float64(2), request.Variables["format_id"])
}

func TestSearchBookByEditionASINResultReportsConflictingEditions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[
			{"id":1,"title":"First","editions":[{"id":11,"asin":"B0EDITION03","reading_format_id":2}]},
			{"id":2,"title":"Second","editions":[{"id":22,"asin":"B0EDITION03","reading_format_id":2}]}
		]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByEditionASINResult(context.Background(), "B0EDITION03")
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrASINLookupConflict)
}

func TestSearchBookByEditionASINResultReturnsNilOnMiss(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[]}}`))
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByEditionASINResult(context.Background(), "B0EDITION04")
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestSearchBookByEditionASINResultReturnsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	result, err := CreateTestClient(server).SearchBookByEditionASINResult(context.Background(), "B0EDITION05")
	require.Nil(t, result)
	require.Error(t, err)
}

func TestGetEditionUncachedBypassesEditionCache(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"editions":[]}}`))
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.editionCache = cache.WithTTL[int, *models.Edition](
		cache.NewMemoryCache[int, *models.Edition](logger.Get()), time.Hour,
	)
	cached := &models.Edition{ID: "77", BookID: "8", Title: "Cached"}
	client.editionCache.Set(77, cached, 0)

	got, err := client.GetEdition(context.Background(), "77")
	require.NoError(t, err)
	require.Same(t, cached, got)
	require.Zero(t, requestCount)

	got, err = client.GetEditionUncached(context.Background(), "77")
	require.Nil(t, got)
	require.ErrorIs(t, err, models.ErrEditionNotFound)
	require.Equal(t, 1, requestCount)
}
