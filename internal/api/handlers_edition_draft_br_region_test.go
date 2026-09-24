package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/require"
)

func TestEditionDraftKeepsBrazilForLegacyUseButSweepsAudnexFromUS(t *testing.T) {
	item := `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"Brazil region","authorName":"Author","asin":"B0SOURCE12","language":"English"},"duration":100,"numTracks":1}}`
	fixture := newEditionDraftTestFixture(t, item, "br")
	regions := make(chan string, 10)
	audnexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		regions <- r.URL.Query().Get("region")
		http.NotFound(w, r)
	}))
	t.Cleanup(audnexServer.Close)
	fixture.handler.editionDraftAudnexClientFactory = func() editionDraftAudnexDiscoverer {
		return audnex.NewClientForTesting(audnexServer.URL, logger.Get())
	}

	response := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "unknown", envelope.Data.RegionStatus)
	require.True(t, hasEditionDraftWarning(envelope.Data, "unsupported_audnex_region"))

	var gotRegions []string
	for len(regions) > 0 {
		gotRegions = append(gotRegions, <-regions)
	}
	require.Equal(t, []string{"us", "ca", "uk", "au", "de", "fr", "es", "in", "it", "jp"}, gotRegions)
	require.Zero(t, fixture.hardcoverRequests.Load())
}
