package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/types"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	"github.com/stretchr/testify/require"
)

type statusServiceFixture struct {
	dataDir   string
	repo      *database.Repository
	multiUser *multiuser.MultiUserService
}

func newStatusServiceFixture(t *testing.T, hardcoverURL string) *statusServiceFixture {
	t.Helper()
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})

	dataDir := t.TempDir()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "status-test.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())
	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	cfg.RateLimit.Rate = time.Nanosecond
	cfg.RateLimit.MaxConcurrent = 1
	cfg.Hardcover.BaseURL = hardcoverURL

	return &statusServiceFixture{
		dataDir:   dataDir,
		repo:      repo,
		multiUser: multiuser.NewMultiUserService(repo, cfg, logger.Get()),
	}
}

func (f *statusServiceFixture) createProfile(t *testing.T, id, name, absURL, token string) {
	t.Helper()
	require.NoError(t, f.repo.CreateProfile(
		id,
		name,
		absURL,
		id,
		token,
		database.SyncConfigData{
			StateFile:          filepath.Join(f.dataDir, "sync-state.json"),
			ProcessUnreadBooks: true,
			DryRun:             true,
		},
	))
}

type statusHTTPResponse struct {
	Success bool                        `json:"success"`
	Data    multiuser.SyncProfileStatus `json:"data"`
}

type allStatusHTTPResponse struct {
	Success bool                          `json:"success"`
	Data    []multiuser.SyncProfileStatus `json:"data"`
}

type summaryHTTPResponse struct {
	Success bool                 `json:"success"`
	Data    statusSummaryPayload `json:"data"`
}

func requireSnapshotMatchesSummary(t *testing.T, snapshot *syncsvc.SyncSnapshot, summary statusSummaryPayload) {
	t.Helper()
	require.NotNil(t, snapshot)
	require.NotNil(t, summary.Snapshot)
	require.Equal(t, snapshot.RunID, summary.RunID)
	require.Equal(t, snapshot.RunStartedAt, summary.RunStartedAt)
	require.Equal(t, snapshot.State, summary.State)
	require.Equal(t, snapshot.BooksTotal, summary.BooksTotal)
	require.Equal(t, snapshot.ProcessedSoFar, summary.ProcessedSoFar)
	require.Equal(t, snapshot.OutcomeCounts, summary.OutcomeCounts)
	require.Equal(t, snapshot.AttentionRecords, summary.AttentionRecords)
}

func TestPublicStatusAndSummaryRoutesShareCurrentRunSnapshot(t *testing.T) {
	absServer := newStatusAudiobookshelfServer()
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newEmptyHardcoverServer(t)
	t.Cleanup(hardcoverServer.Close)
	fixture := newStatusServiceFixture(t, hardcoverServer.URL)

	for _, profile := range []struct {
		id     string
		name   string
		title  string
		author string
	}{
		{id: "profile-a", name: "A", title: "Missing A", author: "Author A"},
		{id: "profile-b", name: "B", title: "Missing B", author: "Author B"},
	} {
		fixture.createProfile(t, profile.id, profile.name, absServer.Server.URL, "hardcover-token")
		absServer.books[profile.id] = statusBook(profile.id, profile.title, profile.author)
	}

	for _, profileID := range []string{"profile-a", "profile-b"} {
		require.NoError(t, fixture.multiUser.StartSync(profileID))
		status := waitForStatusRun(t, fixture.multiUser, profileID)
		require.Equal(t, "completed", status.Status)
		require.NotNil(t, status.Snapshot)
		require.Equal(t, int32(1), status.Snapshot.ProcessedSoFar)
		require.Equal(t, int32(1), status.Snapshot.OutcomeCounts.NotFound)
		require.Len(t, status.Snapshot.AttentionRecords, 1)
	}

	handler := NewHandler(fixture.multiUser, nil, logger.Get())
	var statusResponse statusHTTPResponse
	callJSONHandler(t, handler.GetProfileStatus, "/api/profiles/profile-a/status", &statusResponse)
	require.True(t, statusResponse.Success)
	require.NotNil(t, statusResponse.Data.Snapshot)

	var summaryResponse summaryHTTPResponse
	callJSONHandler(t, handler.GetSyncSummary, "/api/profiles/profile-a/summary", &summaryResponse)
	require.True(t, summaryResponse.Success)
	require.Equal(t, "default", summaryResponse.Data.UserID)

	statusSnapshot := statusResponse.Data.Snapshot
	summarySnapshot := summaryResponse.Data.Snapshot
	requireSnapshotMatchesSummary(t, statusSnapshot, summaryResponse.Data)
	require.Equal(t, statusSnapshot.RunID, summarySnapshot.RunID)
	require.Equal(t, "profile-a", summarySnapshot.UserID)
	require.Equal(t, statusSnapshot.AttentionRecords[0].BookID, "profile-a")

	// Existing consumers can continue using the flattened fields while moving
	// to the run-scoped snapshot contract.
	require.Equal(t, int32(1), summaryResponse.Data.TotalBooksProcessed)
	require.Equal(t, int32(0), summaryResponse.Data.BooksSynced)
	require.Len(t, summaryResponse.Data.BooksNotFound, 1)
	require.Equal(t, "Missing A", summaryResponse.Data.BooksNotFound[0].Title)
	require.Empty(t, summaryResponse.Data.Mismatches)
	require.Equal(t, int32(1), statusResponse.Data.Snapshot.TotalBooksProcessed)
	require.Len(t, statusResponse.Data.BooksNotFound, 1)
	require.Equal(t, "Missing A", statusResponse.Data.BooksNotFound[0].Title)
	require.Empty(t, statusResponse.Data.Mismatches)

	var unknownSummaryResponse summaryHTTPResponse
	routes := newMountedStatusRoutes(handler)
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/unknown/summary", &unknownSummaryResponse)
	require.True(t, unknownSummaryResponse.Success)
	require.Equal(t, "default", unknownSummaryResponse.Data.UserID)
	require.Nil(t, unknownSummaryResponse.Data.Snapshot)
	require.Zero(t, unknownSummaryResponse.Data.TotalBooksProcessed)
	require.Empty(t, unknownSummaryResponse.Data.BooksNotFound)
	require.Empty(t, unknownSummaryResponse.Data.Mismatches)

	var allStatusesResponse allStatusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/status", &allStatusesResponse)
	require.True(t, allStatusesResponse.Success)
	require.Len(t, allStatusesResponse.Data, 2)
	byID := make(map[string]multiuser.SyncProfileStatus, len(allStatusesResponse.Data))
	for _, status := range allStatusesResponse.Data {
		byID[status.ProfileID] = status
	}
	require.Contains(t, byID, "profile-a")
	require.Contains(t, byID, "profile-b")
	require.Nil(t, byID["profile-a"].Snapshot)
	require.Nil(t, byID["profile-b"].Snapshot)
	require.Nil(t, byID["profile-a"].LastSyncSummary)
	require.Nil(t, byID["profile-b"].LastSyncSummary)
	require.Empty(t, byID["profile-a"].BooksNotFound)
	require.Empty(t, byID["profile-b"].BooksNotFound)
	require.Empty(t, byID["profile-a"].Mismatches)
	require.Empty(t, byID["profile-b"].Mismatches)
	require.Equal(t, 1, byID["profile-a"].BooksTotal)
	require.Equal(t, 1, byID["profile-b"].BooksTotal)
}

func TestPublicStatusAndSummaryRoutesExposeLiveAttentionOutcomes(t *testing.T) {
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{
		statusBook("title-only-book", "Title Only", "Author One"),
		statusBook("no-result-book", "No Result", "Author Two"),
		statusBook("blocked-book", "Blocked Until Released", "Author Three"),
	})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newLiveStatusHardcoverServer()
	t.Cleanup(func() {
		hardcoverServer.releaseBlocked()
		hardcoverServer.Server.Close()
	})
	fixture := newStatusServiceFixture(t, hardcoverServer.Server.URL)

	profileID := "live-profile"
	fixture.createProfile(t, profileID, "Live profile", absServer.Server.URL, "hardcover-token")
	require.NoError(t, fixture.multiUser.StartSync(profileID))

	// The third lookup cannot complete until released. Since processing is
	// serial for one library, this signal proves the first two outcomes were
	// recorded while the run is still active.
	select {
	case <-hardcoverServer.blockedStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the third Hardcover lookup")
	}

	handler := NewHandler(fixture.multiUser, nil, logger.Get())
	var statusResponse statusHTTPResponse
	callJSONHandler(t, handler.GetProfileStatus, "/api/profiles/"+profileID+"/status", &statusResponse)
	require.True(t, statusResponse.Success)
	require.NotNil(t, statusResponse.Data.Snapshot)
	liveSnapshot := statusResponse.Data.Snapshot
	require.Equal(t, "syncing", liveSnapshot.State)
	require.Equal(t, int32(3), liveSnapshot.BooksTotal)
	require.Equal(t, int32(2), liveSnapshot.ProcessedSoFar)
	require.Equal(t, int32(2), liveSnapshot.OutcomeCounts.Total())
	require.Equal(t, int32(1), liveSnapshot.OutcomeCounts.NeedsReview)
	require.Equal(t, int32(1), liveSnapshot.OutcomeCounts.NotFound)
	require.Len(t, liveSnapshot.BookOutcomes, 2)
	require.Len(t, liveSnapshot.AttentionRecords, 2)
	require.Len(t, liveSnapshot.BooksNotFound, 1)
	require.Len(t, liveSnapshot.Mismatches, 1)

	attentionByID := make(map[string]syncsvc.BookOutcomeRecord, len(liveSnapshot.AttentionRecords))
	for _, record := range liveSnapshot.AttentionRecords {
		attentionByID[record.BookID] = record
	}
	require.Equal(t, syncsvc.OutcomeNeedsReview, attentionByID["title-only-book"].Outcome)
	require.Equal(t, "title_author", attentionByID["title-only-book"].MatchMethod)
	require.Equal(t, syncsvc.OutcomeNotFound, attentionByID["no-result-book"].Outcome)
	require.NotContains(t, attentionByID, "blocked-book")

	var summaryResponse summaryHTTPResponse
	callJSONHandler(t, handler.GetSyncSummary, "/api/profiles/"+profileID+"/summary", &summaryResponse)
	require.True(t, summaryResponse.Success)
	requireSnapshotMatchesSummary(t, liveSnapshot, summaryResponse.Data)
	require.Equal(t, "default", summaryResponse.Data.UserID)
	require.Equal(t, profileID, summaryResponse.Data.Snapshot.UserID)
	require.Len(t, summaryResponse.Data.AttentionRecords, 2)
	require.Len(t, summaryResponse.Data.BooksNotFound, 1)
	require.Len(t, summaryResponse.Data.Mismatches, 1)
	require.Equal(t, liveSnapshot.ProcessedSoFar, summaryResponse.Data.OutcomeCounts.Total())

	hardcoverServer.releaseBlocked()
	completed := waitForStatusRun(t, fixture.multiUser, profileID)
	require.Equal(t, "completed", completed.Status)
	require.NotNil(t, completed.Snapshot)
	require.Equal(t, "completed", completed.Snapshot.State)
	require.Equal(t, int32(3), completed.Snapshot.ProcessedSoFar)
	require.Equal(t, int32(3), completed.Snapshot.OutcomeCounts.Total())
	require.Equal(t, int32(1), completed.Snapshot.OutcomeCounts.NeedsReview)
	require.Equal(t, int32(2), completed.Snapshot.OutcomeCounts.NotFound)
}

func TestPublicStatusPublishesSecondLookupOutcomeBeforeEnrichment(t *testing.T) {
	book := statusBookWithEnrichment("delayed-book", "Delayed Enrichment", "Author")
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{book})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newDelayedSecondLookupHardcoverServer()
	t.Cleanup(func() {
		hardcoverServer.releaseEnrichment()
		hardcoverServer.Server.Close()
	})
	fixture := newStatusServiceFixture(t, hardcoverServer.Server.URL)
	profileID := "delayed-profile"
	fixture.createProfile(t, profileID, "Delayed profile", absServer.Server.URL, "hardcover-token")
	require.NoError(t, fixture.multiUser.StartSync(profileID))

	select {
	case <-hardcoverServer.enrichmentStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delayed enrichment")
	}

	handler := NewHandler(fixture.multiUser, nil, logger.Get())
	routes := newMountedStatusRoutes(handler)
	var statusResponse statusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/status", &statusResponse)
	require.True(t, statusResponse.Success)
	status := &statusResponse.Data
	require.NotNil(t, status.Snapshot)
	require.Equal(t, "syncing", status.Snapshot.State)
	require.Equal(t, int32(1), status.Snapshot.ProcessedSoFar)
	require.Equal(t, int32(1), status.Snapshot.OutcomeCounts.NotFound)
	require.Len(t, status.Snapshot.BooksNotFound, 1)
	require.Equal(t, "delayed-book", status.Snapshot.BooksNotFound[0].BookID)

	hardcoverServer.releaseEnrichment()
	completed := waitForMountedStatusRun(t, routes, profileID, status.Snapshot.RunID)
	require.Equal(t, "completed", completed.Status)
}

func TestPublicStatusRunReplacementKeepsNewRunCurrent(t *testing.T) {
	book := statusBookWithEnrichment("replacement-book", "Replacement", "Author")
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{book})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newDelayedSecondLookupHardcoverServer()
	t.Cleanup(func() {
		hardcoverServer.releaseEnrichment()
		hardcoverServer.Server.Close()
	})
	fixture := newStatusServiceFixture(t, hardcoverServer.Server.URL)
	profileID := "replacement-profile"
	fixture.createProfile(t, profileID, "Replacement profile", absServer.Server.URL, "hardcover-token")
	require.NoError(t, fixture.multiUser.StartSync(profileID))
	select {
	case <-hardcoverServer.enrichmentStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for first run to block in enrichment")
	}

	handler := NewHandler(fixture.multiUser, nil, logger.Get())
	routes := newMountedStatusRoutes(handler)
	var oldStatusResponse statusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/status", &oldStatusResponse)
	require.True(t, oldStatusResponse.Success)
	require.NotNil(t, oldStatusResponse.Data.Snapshot)
	oldRunID := oldStatusResponse.Data.Snapshot.RunID
	require.NoError(t, fixture.multiUser.CancelSync(profileID))
	require.NoError(t, fixture.multiUser.StartSync(profileID))
	var newStatusResponse statusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/status", &newStatusResponse)
	require.True(t, newStatusResponse.Success)
	require.NotNil(t, newStatusResponse.Data.Snapshot)
	newRunID := newStatusResponse.Data.Snapshot.RunID
	require.NotEqual(t, oldRunID, newRunID)

	// Release the canceled run's blocked upstream request and wait until its
	// handler has returned before asserting the replacement remains current.
	hardcoverServer.releaseEnrichment()
	select {
	case <-hardcoverServer.firstEnrichmentDone:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for canceled run to finish its blocked lookup")
	}
	waitForMountedStatusRun(t, routes, profileID, newRunID)

	var finalStatusResponse statusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/status", &finalStatusResponse)
	require.True(t, finalStatusResponse.Success)
	require.NotNil(t, finalStatusResponse.Data.Snapshot)
	require.Equal(t, newRunID, finalStatusResponse.Data.Snapshot.RunID)
}

func TestPublicStatusAndSummaryRoutesExposeTechnicalTimeoutAsFailedOutcome(t *testing.T) {
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{
		statusBook("timeout-book", "Timeout Candidate", "Timeout Author"),
	})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newTimeoutStatusHardcoverServer()
	t.Cleanup(hardcoverServer.Close)
	fixture := newStatusServiceFixture(t, hardcoverServer.URL)
	profileID := "timeout-profile"
	fixture.createProfile(t, profileID, "Timeout profile", absServer.Server.URL, "hardcover-token")
	require.NoError(t, fixture.multiUser.StartSync(profileID))

	completed := waitForStatusRun(t, fixture.multiUser, profileID)
	require.Equal(t, "completed", completed.Status)
	require.NotNil(t, completed.Snapshot)
	require.Equal(t, "completed", completed.Snapshot.State)
	require.Equal(t, int32(1), completed.Snapshot.ProcessedSoFar)
	require.Equal(t, int32(1), completed.Snapshot.OutcomeCounts.Total())
	require.Equal(t, int32(1), completed.Snapshot.OutcomeCounts.Failed)
	require.Len(t, completed.Snapshot.AttentionRecords, 1)
	require.Equal(t, syncsvc.OutcomeFailed, completed.Snapshot.AttentionRecords[0].Outcome)
	require.Equal(t, "timeout-book", completed.Snapshot.AttentionRecords[0].BookID)

	handler := NewHandler(fixture.multiUser, nil, logger.Get())
	var statusResponse statusHTTPResponse
	callJSONHandler(t, handler.GetProfileStatus, "/api/profiles/"+profileID+"/status", &statusResponse)
	require.True(t, statusResponse.Success)
	require.NotNil(t, statusResponse.Data.Snapshot)
	require.Equal(t, completed.Snapshot.RunID, statusResponse.Data.Snapshot.RunID)
	require.Equal(t, syncsvc.OutcomeFailed, statusResponse.Data.Snapshot.AttentionRecords[0].Outcome)

	var summaryResponse summaryHTTPResponse
	callJSONHandler(t, handler.GetSyncSummary, "/api/profiles/"+profileID+"/summary", &summaryResponse)
	require.True(t, summaryResponse.Success)
	require.NotNil(t, summaryResponse.Data.Snapshot)
	require.Equal(t, "default", summaryResponse.Data.UserID)
	require.Equal(t, profileID, summaryResponse.Data.Snapshot.UserID)
	require.Equal(t, completed.Snapshot.RunID, summaryResponse.Data.RunID)
	require.Equal(t, completed.Snapshot.OutcomeCounts, summaryResponse.Data.OutcomeCounts)
	require.Equal(t, int32(1), summaryResponse.Data.OutcomeCounts.Failed)
	require.Len(t, summaryResponse.Data.AttentionRecords, 1)
	require.Equal(t, syncsvc.OutcomeFailed, summaryResponse.Data.AttentionRecords[0].Outcome)
}

type statusSummaryPayload struct {
	Snapshot            *syncsvc.SyncSnapshot       `json:"snapshot"`
	UserID              string                      `json:"user_id"`
	RunID               string                      `json:"run_id"`
	RunStartedAt        time.Time                   `json:"run_started_at"`
	State               string                      `json:"state"`
	BooksTotal          int32                       `json:"books_total"`
	ProcessedSoFar      int32                       `json:"processed_so_far"`
	OutcomeCounts       syncsvc.OutcomeCounts       `json:"outcome_counts"`
	AttentionRecords    []syncsvc.BookOutcomeRecord `json:"attention_records"`
	TotalBooksProcessed int32                       `json:"total_books_processed"`
	BooksSynced         int32                       `json:"books_synced"`
	BooksNotFound       []types.BookNotFoundInfo    `json:"books_not_found"`
	Mismatches          []map[string]interface{}    `json:"mismatches"`
}

type statusAudiobookshelfServer struct {
	*httptest.Server
	books map[string]map[string]interface{}
}

func newStatusAudiobookshelfServer() *statusAudiobookshelfServer {
	fixture := &statusAudiobookshelfServer{books: make(map[string]map[string]interface{})}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		profileID := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch {
		case r.URL.Path == "/api/me":
			_, _ = io.WriteString(w, `{"mediaProgress":[],"listeningSessions":[]}`)
		case r.URL.Path == "/api/libraries":
			_, _ = io.WriteString(w, `{"libraries":[{"id":"library","name":"Library"}]}`)
		case r.URL.Path == "/api/libraries/library/items":
			book, ok := fixture.books[profileID]
			if !ok {
				http.Error(w, "unknown profile", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []map[string]interface{}{book}})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	return fixture
}

func newEmptyHardcoverServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"search": map[string]interface{}{
					"error":   "",
					"results": map[string]interface{}{"hits": []interface{}{}},
				},
			},
		})
	}))
}

type liveStatusAudiobookshelfServer struct {
	*httptest.Server
	books []map[string]interface{}
}

func newLiveStatusAudiobookshelfServer(books []map[string]interface{}) *liveStatusAudiobookshelfServer {
	fixture := &liveStatusAudiobookshelfServer{books: books}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			_, _ = io.WriteString(w, `{"mediaProgress":[],"listeningSessions":[]}`)
		case "/api/libraries":
			_, _ = io.WriteString(w, `{"libraries":[{"id":"library","name":"Library"}]}`)
		case "/api/libraries/library/items":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": fixture.books})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	return fixture
}

type liveStatusHardcoverServer struct {
	*httptest.Server
	blockedStarted chan struct{}
	release        chan struct{}
	startedOnce    sync.Once
	releaseOnce    sync.Once
}

func newLiveStatusHardcoverServer() *liveStatusHardcoverServer {
	fixture := &liveStatusHardcoverServer{
		blockedStarted: make(chan struct{}),
		release:        make(chan struct{}),
	}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string `json:"query"`
			Variables struct {
				Query string `json:"query"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid GraphQL request", http.StatusBadRequest)
			return
		}

		if strings.Contains(request.Query, "GetBookByID") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{"books": []interface{}{}},
			})
			return
		}

		searchTerm := request.Variables.Query
		if strings.Contains(searchTerm, "Blocked Until Released") {
			fixture.startedOnce.Do(func() { close(fixture.blockedStarted) })
			select {
			case <-fixture.release:
			case <-r.Context().Done():
				return
			}
		}

		results := []map[string]interface{}{}
		if strings.Contains(searchTerm, "Title Only") {
			results = append(results, map[string]interface{}{
				"id":    "101",
				"title": "Title Only",
			})
		}
		hits := make([]map[string]interface{}, 0, len(results))
		for _, result := range results {
			hits = append(hits, map[string]interface{}{"document": result})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"search": map[string]interface{}{
					"error":   "",
					"results": map[string]interface{}{"hits": hits},
				},
			},
		})
	}))
	return fixture
}

func (s *liveStatusHardcoverServer) releaseBlocked() {
	s.releaseOnce.Do(func() { close(s.release) })
}

func newTimeoutStatusHardcoverServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A gateway timeout is a real HTTP-level upstream timeout. The
		// Hardcover client retries it, allowing the sync outcome to exercise
		// the technical failure classification rather than no-result handling.
		http.Error(w, "upstream timeout", http.StatusGatewayTimeout)
	}))
}

type delayedSecondLookupHardcoverServer struct {
	*httptest.Server
	enrichmentStarted   chan struct{}
	firstEnrichmentDone chan struct{}
	release             chan struct{}
	startOnce           sync.Once
	firstDoneOnce       sync.Once
	releaseOnce         sync.Once
	mu                  sync.Mutex
	isbnCalls           int
	enrichmentCalls     int
}

func newDelayedSecondLookupHardcoverServer() *delayedSecondLookupHardcoverServer {
	fixture := &delayedSecondLookupHardcoverServer{
		enrichmentStarted:   make(chan struct{}),
		firstEnrichmentDone: make(chan struct{}),
		release:             make(chan struct{}),
	}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid GraphQL request", http.StatusBadRequest)
			return
		}

		switch {
		case strings.Contains(request.Query, "BookByISBN"):
			fixture.mu.Lock()
			fixture.isbnCalls++
			firstCall := fixture.isbnCalls == 1
			fixture.mu.Unlock()
			books := []map[string]interface{}{}
			if firstCall {
				books = []map[string]interface{}{{
					"id": 501, "title": "Delayed Enrichment", "book_status_id": 0,
					"editions": []map[string]interface{}{{
						"id": 601, "isbn_13": "9780306406157", "reading_format_id": 2,
					}},
				}}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"books": books}})
		case strings.Contains(request.Query, "GetEdition"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"editions": []map[string]interface{}{{
				"id": 601, "book_id": 501, "title": "Delayed Enrichment", "isbn_13": "9780306406157",
			}}}})
		case strings.Contains(request.Query, "GetCurrentUserID"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"me": []map[string]interface{}{{"id": 1}}}})
		case strings.Contains(request.Query, "SearchPublishers"):
			fixture.mu.Lock()
			fixture.enrichmentCalls++
			enrichmentCall := fixture.enrichmentCalls
			fixture.mu.Unlock()
			fixture.startOnce.Do(func() { close(fixture.enrichmentStarted) })
			defer func() {
				if enrichmentCall == 1 {
					fixture.firstDoneOnce.Do(func() { close(fixture.firstEnrichmentDone) })
				}
			}()
			select {
			case <-fixture.release:
			case <-r.Context().Done():
				// Keep the fixture's completion barrier deterministic: the test
				// releases the blocked request after replacing the run, then
				// waits for this handler to return before its final HTTP read.
				<-fixture.release
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"publishers": []interface{}{}}})
		case strings.Contains(request.Query, "SearchBooks"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"search": map[string]interface{}{
				"error": "", "results": map[string]interface{}{"hits": []interface{}{}},
			}}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"user_books": []interface{}{}}})
		}
	}))
	return fixture
}

func (s *delayedSecondLookupHardcoverServer) releaseEnrichment() {
	s.releaseOnce.Do(func() { close(s.release) })
}

func statusBook(id, title, author string) map[string]interface{} {
	return map[string]interface{}{
		"id":        id,
		"libraryId": "library",
		"mediaType": "book",
		"media": map[string]interface{}{
			"metadata": map[string]interface{}{
				"title":      title,
				"authorName": author,
			},
			"duration": 100,
		},
	}
}

func statusBookWithEnrichment(id, title, author string) map[string]interface{} {
	book := statusBook(id, title, author)
	book["progress"] = map[string]interface{}{"currentTime": 40.0}
	metadata := book["media"].(map[string]interface{})["metadata"].(map[string]interface{})
	metadata["isbn"] = "9780306406157"
	metadata["publisher"] = "Delayed Publisher"
	return book
}

func waitForStatusRun(t *testing.T, service *multiuser.MultiUserService, profileID string) *multiuser.SyncProfileStatus {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status := service.GetProfileStatus(profileID)
		if status != nil && status.Status != "syncing" && status.Snapshot != nil && status.Snapshot.RunID != "" {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for profile %q sync status", profileID)
	return nil
}

func newMountedStatusRoutes(handler *Handler) http.Handler {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /profiles/{id}/status", handler.GetProfileStatus)
	apiMux.HandleFunc("GET /profiles/{id}/summary", handler.GetSyncSummary)

	root := http.NewServeMux()
	root.HandleFunc("GET /api/status", handler.GetAllProfileStatuses)
	root.Handle("/api/", http.StripPrefix("/api", apiMux))
	return root
}

func waitForMountedStatusRun(t *testing.T, routes http.Handler, profileID, runID string) *multiuser.SyncProfileStatus {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var response struct {
			Success bool                        `json:"success"`
			Data    multiuser.SyncProfileStatus `json:"data"`
		}
		callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/status", &response)
		if response.Success && response.Data.Status == "completed" &&
			response.Data.Snapshot != nil && response.Data.Snapshot.RunID == runID {
			return &response.Data
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for mounted status run %q to complete", runID)
	return nil
}

func callJSONHandler(t *testing.T, handler http.HandlerFunc, path string, target interface{}) {
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), target))
}

func callJSONRoute(t *testing.T, routes http.Handler, method, path string, target interface{}) {
	recorder := httptest.NewRecorder()
	routes.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), target))
}
