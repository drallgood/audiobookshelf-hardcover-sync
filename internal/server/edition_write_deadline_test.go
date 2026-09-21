package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition/editiontest"
	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

const deadlineTestWriteTimeout = 300 * time.Millisecond

// slowEditionServers holds the Audiobookshelf and Hardcover fakes for the
// write-deadline tests. Tests slow Hardcover down with hardcover.SetDelays.
type slowEditionServers struct {
	abs       *editiontest.AudiobookshelfFake
	hardcover *editiontest.HardcoverFake
}

func newSlowEditionServers(t *testing.T) *slowEditionServers {
	t.Helper()
	return &slowEditionServers{
		abs: editiontest.NewAudiobookshelfFake(t, map[string]map[string]interface{}{
			"item-1": {
				"id":        "item-1",
				"libraryId": "library",
				"mediaType": "book",
				"media": map[string]interface{}{
					"metadata": map[string]interface{}{"title": "A Title", "authorName": "An Author", "isbn": "9780306406157"},
					"duration": 3600.0,
				},
			},
		}),
		hardcover: editiontest.NewHardcoverFake(t),
	}
}

// startEditionHTTPServer serves the real handler chain, with authentication,
// from an http.Server whose write timeout is far below the request duration.
// It returns the base URL and the session cookie of a profile owner whose
// profile has a needs-review book ready for an edition.
func startEditionHTTPServer(t *testing.T, servers *slowEditionServers) (baseURL string, cookie *http.Cookie) {
	t.Helper()
	fixture := newRouteTestFixtureWithHardcoverURL(t, servers.hardcover.URL)
	owner := newRouteSession(t, fixture, "edition-owner", auth.RoleUser)

	created := fixture.requestWithCookies(http.MethodPost, "/api/profiles", []byte(`{
		"id": "edition-profile",
		"name": "edition-profile",
		"audiobookshelf_url": "`+servers.abs.URL+`",
		"audiobookshelf_token": "abs-token",
		"hardcover_token": "hc-token",
		"sync_config": {"process_unread_books": true, "dry_run": false}
	}`), []*http.Cookie{owner.cookie})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	now := time.Now().UTC()
	raw, err := json.Marshal(syncsvc.SyncSnapshot{
		ProfileID: "edition-profile", RunID: "run-1", State: string(syncsvc.RunPhaseCompleted),
		BookOutcomes: []syncsvc.BookOutcomeRecord{{BookID: "item-1", Outcome: syncsvc.OutcomeNeedsReview, HardcoverBookID: "4242"}},
	})
	require.NoError(t, err)
	report, err := fixture.repo.AcceptSyncRun(&database.SyncRunReport{
		ProfileID: "edition-profile", RunID: "run-1", Phase: database.SyncRunPhaseQueued, QueuedAt: &now, SnapshotJSON: "{}",
	})
	require.NoError(t, err)
	report.Phase = database.SyncRunPhaseCompleted
	report.FinishedAt = &now
	report.SnapshotJSON = database.SyncSnapshotJSON(raw)
	require.NoError(t, fixture.repo.UpsertSyncRunReportContext(context.Background(), report))

	httpServer := httptest.NewUnstartedServer(fixture.server.server.Handler)
	httpServer.Config.WriteTimeout = deadlineTestWriteTimeout
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return httpServer.URL, owner.cookie
}

func doEditionRequest(t *testing.T, method, url, body string, cookie *http.Cookie) (int, []byte, time.Duration) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	started := time.Now()
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err, "the response must reach the client even though the request outlived the server's write timeout")
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, payload, time.Since(started)
}

func TestEditionDraftResponseSurvivesTheServerWriteTimeout(t *testing.T) {
	servers := newSlowEditionServers(t)
	servers.hardcover.SetDelays(deadlineTestWriteTimeout/2, 0)
	baseURL, cookie := startEditionHTTPServer(t, servers)

	status, payload, elapsed := doEditionRequest(t, http.MethodGet,
		baseURL+"/api/profiles/edition-profile/runs/run-1/books/item-1/edition-draft", "", cookie)

	require.Greater(t, elapsed, deadlineTestWriteTimeout, "the request must outlive the server write timeout for this test to mean anything")
	require.Equal(t, http.StatusOK, status, string(payload))
}
