package api

import (
	"bytes"
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
	db        *database.Database
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
	multiUser := multiuser.NewMultiUserService(repo, cfg, logger.Get())
	t.Cleanup(func() {
		multiUser.WaitForSyncs()
		require.NoError(t, db.Close())
	})

	return &statusServiceFixture{
		dataDir:   dataDir,
		db:        db,
		repo:      repo,
		multiUser: multiUser,
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

func (f *statusServiceFixture) waitForSyncs(t *testing.T) {
	t.Helper()
	f.multiUser.WaitForSyncs()
}

func TestStartSyncRegistersWorkBeforeResponding(t *testing.T) {
	absServer := newStatusAudiobookshelfServer()
	t.Cleanup(absServer.Close)
	hardcoverServer := newEmptyHardcoverServer(t)
	t.Cleanup(hardcoverServer.Close)

	fixture := newStatusServiceFixture(t, hardcoverServer.URL)
	const profileID = "start-sync-profile"
	fixture.createProfile(t, profileID, "Start sync profile", absServer.URL, profileID)
	absServer.books[profileID] = statusBook(profileID, "Queued sync", "Test Author")

	handler := NewHandler(fixture.multiUser, logger.Get())
	routes := http.NewServeMux()
	routes.HandleFunc("POST /api/profiles/{id}/sync", handler.StartSync)

	recorder := requestJSONRoute(routes, http.MethodPost, "/api/profiles/"+profileID+"/sync")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, fixture.multiUser.GetProfileStatus(profileID))

	fixture.waitForSyncs(t)
	status := fixture.multiUser.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.NotEqual(t, "syncing", status.Status)
}

func TestStartSyncReturnsErrorWhenProfileConfigurationCannotBeLoaded(t *testing.T) {
	fixture := newStatusServiceFixture(t, "http://hardcover.invalid")
	const profileID = "invalid-start-profile"
	fixture.createProfile(t, profileID, "Invalid start profile", "http://audiobookshelf.invalid", profileID)
	result := fixture.db.GetDB().Model(&database.SyncProfileConfig{}).
		Where("profile_id = ?", profileID).
		Update("audiobookshelf_token_encrypted", "invalid-encrypted-token")
	require.NoError(t, result.Error)

	handler := NewHandler(fixture.multiUser, logger.Get())
	routes := http.NewServeMux()
	routes.HandleFunc("POST /api/profiles/{id}/sync", handler.StartSync)

	recorder := requestJSONRoute(routes, http.MethodPost, "/api/profiles/"+profileID+"/sync")
	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	var response struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, "Failed to start sync", response.Error)
	require.False(t, fixture.multiUser.IsProfileSyncing(profileID))
}

func TestStartSyncReturnsControlledErrorForUnknownPublicProfile(t *testing.T) {
	fixture := newStatusServiceFixture(t, "http://hardcover.invalid")
	handler := NewHandler(fixture.multiUser, logger.Get())
	handler.SetAuthEnabled(false)
	routes := http.NewServeMux()
	routes.HandleFunc("POST /api/profiles/{id}/sync", handler.StartSync)

	recorder := requestJSONRoute(routes, http.MethodPost, "/api/profiles/missing-profile/sync")
	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	var response struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, "Failed to start sync", response.Error)
	require.False(t, fixture.multiUser.IsProfileSyncing("missing-profile"))
}

func TestStartSyncReturnsErrorWhenProfileIsAlreadySyncing(t *testing.T) {
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{
		statusBook("blocked-book", "Blocked Until Released", "Author"),
	})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newLiveStatusHardcoverServer()
	fixture := newStatusServiceFixture(t, hardcoverServer.URL)
	t.Cleanup(func() {
		hardcoverServer.releaseBlocked()
		hardcoverServer.Server.Close()
	})
	const profileID = "already-syncing-profile"
	fixture.createProfile(t, profileID, "Already syncing profile", absServer.URL, profileID)
	require.NoError(t, fixture.multiUser.StartSync(profileID))
	select {
	case <-hardcoverServer.blockedStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for initial sync to become active")
	}

	handler := NewHandler(fixture.multiUser, logger.Get())
	routes := http.NewServeMux()
	routes.HandleFunc("POST /api/profiles/{id}/sync", handler.StartSync)
	recorder := requestJSONRoute(routes, http.MethodPost, "/api/profiles/"+profileID+"/sync")
	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	var response struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, "Failed to start sync", response.Error)

	hardcoverServer.releaseBlocked()
	fixture.waitForSyncs(t)
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

type runDetailsHTTPResponse struct {
	Success bool                 `json:"success"`
	Data    syncsvc.SyncSnapshot `json:"data"`
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

	handler := NewHandler(fixture.multiUser, logger.Get())
	routes := newMountedStatusRoutes(handler)
	var statusResponse statusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/profile-a/status", &statusResponse)
	require.True(t, statusResponse.Success)
	require.NotNil(t, statusResponse.Data.Snapshot)

	var summaryResponse summaryHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/profile-a/summary", &summaryResponse)
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
	rawAggregate := requestJSONRoute(routes, http.MethodGet, "/api/status")
	require.Equal(t, http.StatusOK, rawAggregate.Code, rawAggregate.Body.String())
	var aggregateEnvelope struct {
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rawAggregate.Body.Bytes(), &aggregateEnvelope))
	require.Len(t, aggregateEnvelope.Data, 2)
	for _, item := range aggregateEnvelope.Data {
		snapshot, ok := item["snapshot"].(map[string]interface{})
		require.True(t, ok)
		require.NotContains(t, snapshot, "book_outcomes")
		require.NotContains(t, snapshot, "attention_records")
		counts, ok := snapshot["outcome_counts"].(map[string]interface{})
		require.True(t, ok)
		expectedCounts := map[string]float64{
			"synced": 0, "already_current": 0, "skipped": 0,
			"needs_review": 0, "not_found": 1, "failed": 0, "would_sync": 0,
		}
		for key, expected := range expectedCounts {
			value, exists := counts[key]
			require.True(t, exists, "missing outcome count %q", key)
			require.Equal(t, expected, value, "unexpected outcome count %q", key)
		}
	}
	require.Contains(t, byID, "profile-a")
	require.Contains(t, byID, "profile-b")
	require.NotNil(t, byID["profile-a"].Snapshot)
	require.NotNil(t, byID["profile-b"].Snapshot)
	require.Equal(t, statusSnapshot.RunID, byID["profile-a"].Snapshot.RunID)
	require.Equal(t, statusSnapshot.State, byID["profile-a"].Snapshot.State)
	require.Equal(t, statusSnapshot.RunStartedAt, byID["profile-a"].Snapshot.RunStartedAt)
	require.Equal(t, statusSnapshot.BooksTotal, byID["profile-a"].Snapshot.BooksTotal)
	require.Equal(t, statusSnapshot.ProcessedSoFar, byID["profile-a"].Snapshot.ProcessedSoFar)
	require.Equal(t, statusSnapshot.ProcessedCount, byID["profile-a"].Snapshot.ProcessedCount)
	require.Equal(t, statusSnapshot.OutcomeCounts, byID["profile-a"].Snapshot.OutcomeCounts)
	require.Empty(t, byID["profile-a"].Snapshot.BookOutcomes)
	require.Empty(t, byID["profile-a"].Snapshot.AttentionRecords)
	require.Empty(t, byID["profile-b"].Snapshot.BookOutcomes)
	require.Empty(t, byID["profile-b"].Snapshot.AttentionRecords)
	profileBRunID := fixture.multiUser.GetProfileStatus("profile-b").Snapshot.RunID
	recorder := requestJSONRoute(routes, http.MethodGet, "/api/profiles/profile-a/runs/"+profileBRunID+"/details")
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	recorder = requestJSONRoute(routes, http.MethodGet, "/api/profiles/unknown/runs/"+statusSnapshot.RunID+"/details")
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	require.Nil(t, byID["profile-a"].LastSyncSummary)
	require.Nil(t, byID["profile-b"].LastSyncSummary)
	require.Empty(t, byID["profile-a"].BooksNotFound)
	require.Empty(t, byID["profile-b"].BooksNotFound)
	require.Empty(t, byID["profile-a"].Mismatches)
	require.Empty(t, byID["profile-b"].Mismatches)
	require.Equal(t, 1, byID["profile-a"].BooksTotal)
	require.Equal(t, 1, byID["profile-b"].BooksTotal)
	fixture.waitForSyncs(t)
}

func TestCreateProfileValidatesIDs(t *testing.T) {
	fixture := newStatusServiceFixture(t, "http://hardcover.invalid")
	handler := NewHandler(fixture.multiUser, logger.Get())

	for _, test := range []struct {
		name       string
		profileID  string
		statusCode int
	}{
		{name: "empty", statusCode: http.StatusBadRequest},
		{name: "current directory", profileID: ".", statusCode: http.StatusBadRequest},
		{name: "parent directory", profileID: "..", statusCode: http.StatusBadRequest},
		{name: "slash", profileID: "unsafe/profile", statusCode: http.StatusBadRequest},
		{name: "query", profileID: "unsafe?query", statusCode: http.StatusBadRequest},
		{name: "fragment", profileID: "unsafe#fragment", statusCode: http.StatusBadRequest},
		{name: "percent", profileID: "unsafe%id", statusCode: http.StatusBadRequest},
		{name: "backslash", profileID: "unsafe\\id", statusCode: http.StatusBadRequest},
		{name: "maximum length", profileID: strings.Repeat("a", maxNewProfileIDBytes), statusCode: http.StatusOK},
		{name: "one byte over maximum", profileID: strings.Repeat("a", maxNewProfileIDBytes+1), statusCode: http.StatusBadRequest},
		{name: "allowed punctuation", profileID: "valid-profile_1.~", statusCode: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(CreateProfileRequest{
				ID:                  test.profileID,
				Name:                "Profile ID validation",
				AudiobookshelfURL:   "http://audiobookshelf.invalid",
				AudiobookshelfToken: "abs-token",
				HardcoverToken:      "hardcover-token",
			})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			handler.CreateProfile(recorder, httptest.NewRequest(http.MethodPost, "/api/profiles", bytes.NewReader(payload)))
			require.Equal(t, test.statusCode, recorder.Code, recorder.Body.String())
			profile, err := fixture.multiUser.GetProfile(test.profileID)
			require.NoError(t, err)
			if test.statusCode == http.StatusOK {
				require.NotNil(t, profile)
			} else {
				require.Nil(t, profile)
			}
		})
	}
}

func TestProfileStateFilenameValidationAtHTTPBoundary(t *testing.T) {
	fixture := newStatusServiceFixture(t, "http://hardcover.invalid")
	handler := NewHandler(fixture.multiUser, logger.Get())
	routes := http.NewServeMux()
	routes.HandleFunc("POST /api/profiles", handler.CreateProfile)
	routes.HandleFunc("PUT /api/profiles/{id}/config", handler.UpdateProfileConfig)

	unsafeStateFiles := []struct {
		name string
		path string
	}{
		{name: "encoded filename too long", path: strings.Repeat("s", 245) + ".json"},
		{name: "parent component too long", path: filepath.Join(strings.Repeat("p", 256), "state.json")},
		{name: "NUL byte", path: "state\x00.json"},
	}
	for _, test := range unsafeStateFiles {
		t.Run("create "+test.name, func(t *testing.T) {
			profileID := "new-profile-" + strings.ReplaceAll(test.name, " ", "-")
			createPayload, err := json.Marshal(CreateProfileRequest{
				ID:                  profileID,
				Name:                "New profile",
				AudiobookshelfURL:   "http://audiobookshelf.invalid",
				AudiobookshelfToken: "abs-token",
				HardcoverToken:      "hardcover-token",
				SyncConfig:          database.SyncConfigData{StateFile: test.path},
			})
			require.NoError(t, err)
			createResponse := httptest.NewRecorder()
			routes.ServeHTTP(createResponse, httptest.NewRequest(http.MethodPost, "/api/profiles", bytes.NewReader(createPayload)))
			require.Equal(t, http.StatusBadRequest, createResponse.Code, createResponse.Body.String())
			created, err := fixture.multiUser.GetProfile(profileID)
			require.NoError(t, err)
			require.Nil(t, created)
		})
	}

	profileID := "configured-profile"
	originalURL := "http://original.invalid"
	originalStateFile := "original.json"
	require.NoError(t, fixture.repo.CreateProfile(
		profileID,
		"Configured profile",
		originalURL,
		"abs-token",
		"hardcover-token",
		database.SyncConfigData{StateFile: originalStateFile},
	))
	updatePayload, err := json.Marshal(UpdateProfileConfigRequest{
		AudiobookshelfURL: "http://should-not-persist.invalid",
		SyncConfig:        database.SyncConfigData{StateFile: "../escape.json"},
	})
	require.NoError(t, err)
	updateResponse := httptest.NewRecorder()
	routes.ServeHTTP(updateResponse, httptest.NewRequest(
		http.MethodPut,
		"/api/profiles/"+profileID+"/config",
		bytes.NewReader(updatePayload),
	))
	require.Equal(t, http.StatusBadRequest, updateResponse.Code, updateResponse.Body.String())
	unchanged, err := fixture.multiUser.GetProfile(profileID)
	require.NoError(t, err)
	require.NotNil(t, unchanged)
	require.Equal(t, originalURL, unchanged.AudiobookshelfURL)
	require.Equal(t, originalStateFile, unchanged.SyncConfig.StateFile)

	legacyID := strings.Repeat("l", 245)
	require.NoError(t, fixture.repo.CreateProfile(
		legacyID,
		"Legacy profile",
		"http://legacy.invalid",
		"abs-token",
		"hardcover-token",
		database.SyncConfigData{},
	))
	legacyPayload, err := json.Marshal(UpdateProfileConfigRequest{AudiobookshelfToken: "updated-token"})
	require.NoError(t, err)
	legacyResponse := httptest.NewRecorder()
	routes.ServeHTTP(legacyResponse, httptest.NewRequest(
		http.MethodPut,
		"/api/profiles/"+legacyID+"/config",
		bytes.NewReader(legacyPayload),
	))
	require.Equal(t, http.StatusOK, legacyResponse.Code, legacyResponse.Body.String())
	legacy, err := fixture.multiUser.GetProfile(legacyID)
	require.NoError(t, err)
	require.NotNil(t, legacy)
	require.Equal(t, "updated-token", legacy.AudiobookshelfToken)
}

func TestPublicStatusAndSummaryRoutesExposeLiveAttentionOutcomes(t *testing.T) {
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{
		statusBook("title-only-book", "Title Only", "Author One"),
		statusBook("no-result-book", "No Result", "Author Two"),
		statusBook("blocked-book", "Blocked Until Released", "Author Three"),
	})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newLiveStatusHardcoverServer()
	fixture := newStatusServiceFixture(t, hardcoverServer.Server.URL)
	t.Cleanup(func() {
		hardcoverServer.releaseBlocked()
		hardcoverServer.Server.Close()
	})

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

	handler := NewHandler(fixture.multiUser, logger.Get())
	routes := newMountedStatusRoutes(handler)
	var statusResponse statusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/status", &statusResponse)
	require.True(t, statusResponse.Success)
	require.NotNil(t, statusResponse.Data.Snapshot)
	liveSnapshot := statusResponse.Data.Snapshot
	require.Equal(t, absServer.Server.URL, liveSnapshot.AudiobookshelfURL)
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

	// Aggregate polling must observe the active service snapshot as outcomes
	// arrive, while omitting the potentially large per-book detail arrays.
	var aggregateResponse allStatusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/status", &aggregateResponse)
	require.True(t, aggregateResponse.Success)
	var aggregateLiveStatus *multiuser.SyncProfileStatus
	for i := range aggregateResponse.Data {
		if aggregateResponse.Data[i].ProfileID == profileID {
			aggregateLiveStatus = &aggregateResponse.Data[i]
			break
		}
	}
	require.NotNil(t, aggregateLiveStatus)
	require.NotNil(t, aggregateLiveStatus.Snapshot)
	require.Equal(t, liveSnapshot.RunID, aggregateLiveStatus.Snapshot.RunID)
	require.Equal(t, int32(2), aggregateLiveStatus.Snapshot.ProcessedSoFar)
	require.Equal(t, liveSnapshot.OutcomeCounts, aggregateLiveStatus.Snapshot.OutcomeCounts)
	require.Empty(t, aggregateLiveStatus.Snapshot.AudiobookshelfURL)
	require.Empty(t, aggregateLiveStatus.Snapshot.BookOutcomes)
	require.Empty(t, aggregateLiveStatus.Snapshot.AttentionRecords)

	attentionByID := make(map[string]syncsvc.BookOutcomeRecord, len(liveSnapshot.AttentionRecords))
	for _, record := range liveSnapshot.AttentionRecords {
		attentionByID[record.BookID] = record
	}
	require.Equal(t, syncsvc.OutcomeNeedsReview, attentionByID["title-only-book"].Outcome)
	require.Equal(t, "title_author", attentionByID["title-only-book"].MatchMethod)
	require.Equal(t, syncsvc.OutcomeNotFound, attentionByID["no-result-book"].Outcome)
	require.NotContains(t, attentionByID, "blocked-book")

	var liveDetails runDetailsHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/runs/"+liveSnapshot.RunID+"/details", &liveDetails)
	require.True(t, liveDetails.Success)
	require.Equal(t, liveSnapshot.RunID, liveDetails.Data.RunID)
	require.Equal(t, absServer.Server.URL, liveDetails.Data.AudiobookshelfURL)
	require.Equal(t, liveSnapshot.ProcessedSoFar, liveDetails.Data.ProcessedSoFar)
	require.Len(t, liveDetails.Data.BookOutcomes, 2)
	require.Len(t, liveDetails.Data.AttentionRecords, 2)
	for _, record := range liveDetails.Data.BookOutcomes {
		require.Equal(t, absServer.Server.URL+"/api/items/"+record.BookID+"/cover", record.CoverURL)
		require.Equal(t, "Audiobook", record.Format)
		require.Equal(t, "Status Series", record.Series)
		require.Equal(t, "2", record.SeriesNumber)
	}
	require.Len(t, liveDetails.Data.Mismatches, 1)
	require.Equal(t, "Hardcover Series", liveDetails.Data.Mismatches[0].HardcoverSeries)
	require.Equal(t, "3", liveDetails.Data.Mismatches[0].HardcoverSeriesNumber)

	var summaryResponse summaryHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/summary", &summaryResponse)
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
	var terminalDetails runDetailsHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/runs/"+completed.Snapshot.RunID+"/details", &terminalDetails)
	require.True(t, terminalDetails.Success)
	require.Equal(t, completed.Snapshot.RunID, terminalDetails.Data.RunID)
	require.Len(t, terminalDetails.Data.BookOutcomes, 3)
	fixture.waitForSyncs(t)
}

func TestPublicStatusPublishesSecondLookupOutcomeBeforeEnrichment(t *testing.T) {
	book := statusBookWithEnrichment("delayed-book", "Delayed Enrichment", "Author")
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{book})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newDelayedSecondLookupHardcoverServer()
	fixture := newStatusServiceFixture(t, hardcoverServer.Server.URL)
	t.Cleanup(func() {
		hardcoverServer.releaseEnrichment()
		hardcoverServer.Server.Close()
	})
	profileID := "delayed-profile"
	fixture.createProfile(t, profileID, "Delayed profile", absServer.Server.URL, "hardcover-token")
	require.NoError(t, fixture.multiUser.StartSync(profileID))

	select {
	case <-hardcoverServer.enrichmentStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delayed enrichment")
	}

	handler := NewHandler(fixture.multiUser, logger.Get())
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
	fixture.waitForSyncs(t)
}

func TestPublicStatusRunReplacementKeepsNewRunCurrent(t *testing.T) {
	book := statusBookWithEnrichment("replacement-book", "Replacement", "Author")
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{book})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newDelayedSecondLookupHardcoverServer()
	fixture := newStatusServiceFixture(t, hardcoverServer.Server.URL)
	t.Cleanup(func() {
		hardcoverServer.releaseEnrichment()
		hardcoverServer.Server.Close()
	})
	profileID := "replacement-profile"
	fixture.createProfile(t, profileID, "Replacement profile", absServer.Server.URL, "hardcover-token")
	require.NoError(t, fixture.multiUser.StartSync(profileID))
	select {
	case <-hardcoverServer.enrichmentStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for first run to block in enrichment")
	}

	handler := NewHandler(fixture.multiUser, logger.Get())
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

	for _, runID := range []string{oldRunID, "unknown-run"} {
		recorder := requestJSONRoute(routes, http.MethodGet, "/api/profiles/"+profileID+"/runs/"+runID+"/details")
		require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	}
	var currentDetails runDetailsHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/runs/"+newRunID+"/details", &currentDetails)
	require.True(t, currentDetails.Success)
	require.Equal(t, newRunID, currentDetails.Data.RunID)

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
	fixture.waitForSyncs(t)
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

	handler := NewHandler(fixture.multiUser, logger.Get())
	routes := newMountedStatusRoutes(handler)
	var statusResponse statusHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/status", &statusResponse)
	require.True(t, statusResponse.Success)
	require.NotNil(t, statusResponse.Data.Snapshot)
	require.Equal(t, completed.Snapshot.RunID, statusResponse.Data.Snapshot.RunID)
	require.Equal(t, syncsvc.OutcomeFailed, statusResponse.Data.Snapshot.AttentionRecords[0].Outcome)

	var summaryResponse summaryHTTPResponse
	callJSONRoute(t, routes, http.MethodGet, "/api/profiles/"+profileID+"/summary", &summaryResponse)
	require.True(t, summaryResponse.Success)
	require.NotNil(t, summaryResponse.Data.Snapshot)
	require.Equal(t, "default", summaryResponse.Data.UserID)
	require.Equal(t, profileID, summaryResponse.Data.Snapshot.UserID)
	require.Equal(t, completed.Snapshot.RunID, summaryResponse.Data.RunID)
	require.Equal(t, completed.Snapshot.OutcomeCounts, summaryResponse.Data.OutcomeCounts)
	require.Equal(t, int32(1), summaryResponse.Data.OutcomeCounts.Failed)
	require.Len(t, summaryResponse.Data.AttentionRecords, 1)
	require.Equal(t, syncsvc.OutcomeFailed, summaryResponse.Data.AttentionRecords[0].Outcome)
	fixture.waitForSyncs(t)
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
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected method "+r.Method, http.StatusMethodNotAllowed)
			return
		}
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
				"data": map[string]interface{}{"books": []interface{}{map[string]interface{}{
					"id":            101,
					"title":         "Title Only",
					"slug":          "title-only",
					"contributions": []interface{}{},
					"editions":      []interface{}{},
					"book_series": []interface{}{map[string]interface{}{
						"position": 3,
						"series":   map[string]interface{}{"name": "Hardcover Series"},
					}},
				}},
				}})
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
			"coverPath": "/covers/" + id + ".jpg",
			"metadata": map[string]interface{}{
				"title":      title,
				"authorName": author,
				"series": []map[string]interface{}{{
					"name":     "Status Series",
					"sequence": "2",
				}},
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
	apiMux.HandleFunc("GET /api/profiles/{id}/status", handler.GetProfileStatus)
	apiMux.HandleFunc("GET /api/profiles/{id}/summary", handler.GetSyncSummary)
	apiMux.HandleFunc("GET /api/profiles/{id}/runs/{runID}/details", handler.GetRunDetails)

	root := http.NewServeMux()
	root.HandleFunc("GET /api/status", handler.GetAllProfileStatuses)
	root.Handle("/api/", apiMux)
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

func callJSONRoute(t *testing.T, routes http.Handler, method, path string, target interface{}) {
	recorder := requestJSONRoute(routes, method, path)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), target))
}

func requestJSONRoute(routes http.Handler, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	routes.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}
