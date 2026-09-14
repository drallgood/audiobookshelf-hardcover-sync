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

func TestPublicStatusAndSummaryRoutesShareCurrentRunSnapshot(t *testing.T) {
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

	absServer := newStatusAudiobookshelfServer()
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newEmptyHardcoverServer(t)
	t.Cleanup(hardcoverServer.Close)

	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	cfg.RateLimit.Rate = time.Nanosecond
	cfg.RateLimit.MaxConcurrent = 1
	cfg.Hardcover.BaseURL = hardcoverServer.URL
	multiUser := multiuser.NewMultiUserService(repo, cfg, logger.Get())

	for _, profile := range []struct {
		id     string
		name   string
		title  string
		author string
	}{
		{id: "profile-a", name: "A", title: "Missing A", author: "Author A"},
		{id: "profile-b", name: "B", title: "Missing B", author: "Author B"},
	} {
		require.NoError(t, repo.CreateProfile(
			profile.id,
			profile.name,
			absServer.Server.URL,
			profile.id,
			"hardcover-token",
			database.SyncConfigData{
				StateFile:          filepath.Join(dataDir, "sync-state.json"),
				ProcessUnreadBooks: true,
				DryRun:             true,
			},
		))
		absServer.books[profile.id] = statusBook(profile.id, profile.title, profile.author)
	}

	for _, profileID := range []string{"profile-a", "profile-b"} {
		require.NoError(t, multiUser.StartSync(profileID))
		status := waitForStatusRun(t, multiUser, profileID)
		require.Equal(t, "completed", status.Status)
		require.NotNil(t, status.Snapshot)
		require.Equal(t, int32(1), status.Snapshot.ProcessedSoFar)
		require.Equal(t, int32(1), status.Snapshot.OutcomeCounts.NotFound)
		require.Len(t, status.Snapshot.AttentionRecords, 1)
	}

	handler := NewHandler(multiUser, nil, logger.Get())
	var statusResponse struct {
		Success bool                        `json:"success"`
		Data    multiuser.SyncProfileStatus `json:"data"`
	}
	callJSONHandler(t, handler.GetProfileStatus, "/api/profiles/profile-a/status", &statusResponse)
	require.True(t, statusResponse.Success)
	require.NotNil(t, statusResponse.Data.Snapshot)

	var summaryResponse struct {
		Success bool                 `json:"success"`
		Data    statusSummaryPayload `json:"data"`
	}
	callJSONHandler(t, handler.GetSyncSummary, "/api/profiles/profile-a/summary", &summaryResponse)
	require.True(t, summaryResponse.Success)
	require.NotNil(t, summaryResponse.Data.Snapshot)

	statusSnapshot := statusResponse.Data.Snapshot
	summarySnapshot := summaryResponse.Data.Snapshot
	require.Equal(t, statusSnapshot.RunID, summaryResponse.Data.RunID)
	require.Equal(t, statusSnapshot.RunID, summarySnapshot.RunID)
	require.Equal(t, statusSnapshot.RunStartedAt, summarySnapshot.RunStartedAt)
	require.Equal(t, statusSnapshot.State, summarySnapshot.State)
	require.Equal(t, statusSnapshot.BooksTotal, summaryResponse.Data.BooksTotal)
	require.Equal(t, statusSnapshot.ProcessedSoFar, summaryResponse.Data.ProcessedSoFar)
	require.Equal(t, statusSnapshot.OutcomeCounts, summaryResponse.Data.OutcomeCounts)
	require.Equal(t, statusSnapshot.AttentionRecords, summaryResponse.Data.AttentionRecords)
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

	var allStatusesResponse struct {
		Success bool                          `json:"success"`
		Data    []multiuser.SyncProfileStatus `json:"data"`
	}
	callJSONHandler(t, handler.GetAllProfileStatuses, "/api/status", &allStatusesResponse)
	require.True(t, allStatusesResponse.Success)
	require.Len(t, allStatusesResponse.Data, 2)
	byID := make(map[string]multiuser.SyncProfileStatus, len(allStatusesResponse.Data))
	for _, status := range allStatusesResponse.Data {
		byID[status.ProfileID] = status
	}
	require.Contains(t, byID, "profile-a")
	require.Contains(t, byID, "profile-b")
	require.NotNil(t, byID["profile-a"].Snapshot)
	require.NotNil(t, byID["profile-b"].Snapshot)
	require.NotEqual(t, byID["profile-a"].Snapshot.RunID, byID["profile-b"].Snapshot.RunID)
	require.Equal(t, "profile-a", byID["profile-a"].Snapshot.AttentionRecords[0].BookID)
	require.Equal(t, "profile-b", byID["profile-b"].Snapshot.AttentionRecords[0].BookID)
	require.Equal(t, "Missing A", byID["profile-a"].Snapshot.AttentionRecords[0].Title)
	require.Equal(t, "Missing B", byID["profile-b"].Snapshot.AttentionRecords[0].Title)
}

func TestPublicStatusAndSummaryRoutesExposeLiveAttentionOutcomes(t *testing.T) {
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})

	dataDir := t.TempDir()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "live-status-test.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())

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

	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	cfg.RateLimit.Rate = time.Nanosecond
	cfg.RateLimit.MaxConcurrent = 1
	cfg.Hardcover.BaseURL = hardcoverServer.Server.URL
	multiUser := multiuser.NewMultiUserService(repo, cfg, logger.Get())

	profileID := "live-profile"
	require.NoError(t, repo.CreateProfile(
		profileID,
		"Live profile",
		absServer.Server.URL,
		profileID,
		"hardcover-token",
		database.SyncConfigData{
			StateFile:          filepath.Join(dataDir, "sync-state.json"),
			ProcessUnreadBooks: true,
			DryRun:             true,
		},
	))
	require.NoError(t, multiUser.StartSync(profileID))

	// The third lookup cannot complete until released. Since processing is
	// serial for one library, this signal proves the first two outcomes were
	// recorded while the run is still active.
	select {
	case <-hardcoverServer.blockedStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the third Hardcover lookup")
	}

	handler := NewHandler(multiUser, nil, logger.Get())
	var statusResponse struct {
		Success bool                        `json:"success"`
		Data    multiuser.SyncProfileStatus `json:"data"`
	}
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

	var summaryResponse struct {
		Success bool                 `json:"success"`
		Data    statusSummaryPayload `json:"data"`
	}
	callJSONHandler(t, handler.GetSyncSummary, "/api/profiles/"+profileID+"/summary", &summaryResponse)
	require.True(t, summaryResponse.Success)
	require.NotNil(t, summaryResponse.Data.Snapshot)
	require.Equal(t, liveSnapshot.RunID, summaryResponse.Data.RunID)
	require.Equal(t, liveSnapshot.State, summaryResponse.Data.State)
	require.Equal(t, liveSnapshot.ProcessedSoFar, summaryResponse.Data.ProcessedSoFar)
	require.Equal(t, liveSnapshot.OutcomeCounts, summaryResponse.Data.OutcomeCounts)
	require.Len(t, summaryResponse.Data.AttentionRecords, 2)
	require.Len(t, summaryResponse.Data.BooksNotFound, 1)
	require.Len(t, summaryResponse.Data.Mismatches, 1)
	require.Equal(t, liveSnapshot.ProcessedSoFar, summaryResponse.Data.OutcomeCounts.Total())

	hardcoverServer.releaseBlocked()
	completed := waitForStatusRun(t, multiUser, profileID)
	require.Equal(t, "completed", completed.Status)
	require.NotNil(t, completed.Snapshot)
	require.Equal(t, "completed", completed.Snapshot.State)
	require.Equal(t, int32(3), completed.Snapshot.ProcessedSoFar)
	require.Equal(t, int32(3), completed.Snapshot.OutcomeCounts.Total())
	require.Equal(t, int32(1), completed.Snapshot.OutcomeCounts.NeedsReview)
	require.Equal(t, int32(2), completed.Snapshot.OutcomeCounts.NotFound)
}

func TestPublicStatusPublishesSecondLookupOutcomeBeforeEnrichment(t *testing.T) {
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})

	dataDir := t.TempDir()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "delayed-status-test.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())

	book := statusBook("delayed-book", "Delayed Enrichment", "Author")
	book["progress"] = map[string]interface{}{"currentTime": 40.0}
	media := book["media"].(map[string]interface{})
	metadata := media["metadata"].(map[string]interface{})
	metadata["isbn"] = "9780306406157"
	metadata["publisher"] = "Delayed Publisher"
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{book})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newDelayedSecondLookupHardcoverServer()
	t.Cleanup(func() {
		hardcoverServer.releaseEnrichment()
		hardcoverServer.Server.Close()
	})

	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	cfg.RateLimit.Rate = time.Nanosecond
	cfg.RateLimit.MaxConcurrent = 1
	cfg.Hardcover.BaseURL = hardcoverServer.Server.URL
	multiUser := multiuser.NewMultiUserService(repo, cfg, logger.Get())
	profileID := "delayed-profile"
	require.NoError(t, repo.CreateProfile(
		profileID,
		"Delayed profile",
		absServer.Server.URL,
		profileID,
		"hardcover-token",
		database.SyncConfigData{
			StateFile:          filepath.Join(dataDir, "sync-state.json"),
			ProcessUnreadBooks: true,
			DryRun:             true,
		},
	))
	require.NoError(t, multiUser.StartSync(profileID))

	select {
	case <-hardcoverServer.enrichmentStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delayed enrichment")
	}

	status := multiUser.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, "syncing", status.Snapshot.State)
	require.Equal(t, int32(1), status.Snapshot.ProcessedSoFar)
	require.Equal(t, int32(1), status.Snapshot.OutcomeCounts.NotFound)
	require.Len(t, status.Snapshot.BooksNotFound, 1)
	require.Equal(t, "delayed-book", status.Snapshot.BooksNotFound[0].BookID)

	hardcoverServer.releaseEnrichment()
	completed := waitForStatusRun(t, multiUser, profileID)
	require.Equal(t, "completed", completed.Status)
}

func TestPublicStatusRunReplacementKeepsNewRunCurrent(t *testing.T) {
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})

	dataDir := t.TempDir()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "replacement-status-test.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	book := statusBook("replacement-book", "Replacement", "Author")
	book["progress"] = map[string]interface{}{"currentTime": 40.0}
	media := book["media"].(map[string]interface{})
	metadata := media["metadata"].(map[string]interface{})
	metadata["isbn"] = "9780306406157"
	metadata["publisher"] = "Delayed Publisher"
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{book})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newDelayedSecondLookupHardcoverServer()
	t.Cleanup(func() {
		hardcoverServer.releaseEnrichment()
		hardcoverServer.Server.Close()
	})

	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())
	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	cfg.RateLimit.Rate = time.Nanosecond
	cfg.RateLimit.MaxConcurrent = 1
	cfg.Hardcover.BaseURL = hardcoverServer.Server.URL
	multiUser := multiuser.NewMultiUserService(repo, cfg, logger.Get())
	profileID := "replacement-profile"
	require.NoError(t, repo.CreateProfile(
		profileID,
		"Replacement profile",
		absServer.Server.URL,
		profileID,
		"hardcover-token",
		database.SyncConfigData{
			StateFile:          filepath.Join(dataDir, "sync-state.json"),
			ProcessUnreadBooks: true,
			DryRun:             true,
		},
	))
	require.NoError(t, multiUser.StartSync(profileID))
	select {
	case <-hardcoverServer.enrichmentStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for first run to block in enrichment")
	}

	oldStatus := multiUser.GetProfileStatus(profileID)
	require.NotNil(t, oldStatus)
	require.NotNil(t, oldStatus.Snapshot)
	oldRunID := oldStatus.Snapshot.RunID
	require.NoError(t, multiUser.CancelSync(profileID))
	require.NoError(t, multiUser.StartSync(profileID))
	newStatus := multiUser.GetProfileStatus(profileID)
	require.NotNil(t, newStatus)
	require.NotNil(t, newStatus.Snapshot)
	newRunID := newStatus.Snapshot.RunID
	require.NotEqual(t, oldRunID, newRunID)

	// Let the canceled run return first; its terminal publication must be
	// rejected by generation, leaving the replacement run current.
	hardcoverServer.releaseEnrichment()
	completed := waitForStatusRun(t, multiUser, profileID)
	require.Equal(t, "completed", completed.Status)
	require.NotNil(t, completed.Snapshot)
	require.Equal(t, newRunID, completed.Snapshot.RunID)
}

func TestPublicStatusRoutesIsolateConcurrentProfileRuns(t *testing.T) {
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})

	dataDir := t.TempDir()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "concurrent-status-test.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())
	hardcoverServer := newConcurrentStatusHardcoverServer()
	t.Cleanup(func() {
		hardcoverServer.releaseBlocked()
		hardcoverServer.Server.Close()
	})

	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	cfg.RateLimit.Rate = time.Nanosecond
	cfg.RateLimit.MaxConcurrent = 1
	cfg.Hardcover.BaseURL = hardcoverServer.Server.URL
	multiUser := multiuser.NewMultiUserService(repo, cfg, logger.Get())

	profileIDs := []string{"concurrent-a", "concurrent-b"}
	for _, profileID := range profileIDs {
		absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{
			statusBook(profileID+"-title", "Title "+profileID, "Author "+profileID),
			statusBook(profileID+"-missing", "Missing "+profileID, "Author "+profileID),
			statusBook(profileID+"-blocked", "Blocked "+profileID, "Author "+profileID),
		})
		t.Cleanup(absServer.Server.Close)
		require.NoError(t, repo.CreateProfile(
			profileID,
			profileID,
			absServer.Server.URL,
			profileID,
			"hardcover-"+profileID,
			database.SyncConfigData{
				StateFile:          filepath.Join(dataDir, "sync-state.json"),
				ProcessUnreadBooks: true,
				DryRun:             true,
			},
		))
	}

	for _, profileID := range profileIDs {
		require.NoError(t, multiUser.StartSync(profileID))
	}

	seenTokens := make(map[string]bool, len(profileIDs))
	for range profileIDs {
		select {
		case token := <-hardcoverServer.blockedStarted:
			seenTokens[token] = true
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for both profiles to reach their blocked lookup")
		}
	}
	require.Equal(t, map[string]bool{
		"Bearer hardcover-concurrent-a": true,
		"Bearer hardcover-concurrent-b": true,
	}, seenTokens)

	handler := NewHandler(multiUser, nil, logger.Get())
	var allStatusesResponse struct {
		Success bool                          `json:"success"`
		Data    []multiuser.SyncProfileStatus `json:"data"`
	}
	callJSONHandler(t, handler.GetAllProfileStatuses, "/api/status", &allStatusesResponse)
	require.True(t, allStatusesResponse.Success)
	require.Len(t, allStatusesResponse.Data, len(profileIDs))

	statusByID := make(map[string]multiuser.SyncProfileStatus, len(allStatusesResponse.Data))
	for _, status := range allStatusesResponse.Data {
		statusByID[status.ProfileID] = status
	}
	for _, profileID := range profileIDs {
		status := statusByID[profileID]
		require.NotNil(t, status.Snapshot)
		require.Equal(t, "syncing", status.Snapshot.State)
		require.Equal(t, int32(3), status.Snapshot.BooksTotal)
		require.Equal(t, int32(2), status.Snapshot.ProcessedSoFar)
		require.Equal(t, int32(2), status.Snapshot.OutcomeCounts.Total())
		require.Equal(t, int32(1), status.Snapshot.OutcomeCounts.NeedsReview)
		require.Equal(t, int32(1), status.Snapshot.OutcomeCounts.NotFound)
		require.Len(t, status.Snapshot.AttentionRecords, 2)
		for _, record := range status.Snapshot.AttentionRecords {
			require.True(t, strings.HasPrefix(record.BookID, profileID+"-"), "profile %q received %q", profileID, record.BookID)
		}

		var profileStatusResponse struct {
			Success bool                        `json:"success"`
			Data    multiuser.SyncProfileStatus `json:"data"`
		}
		callJSONHandler(t, handler.GetProfileStatus, "/api/profiles/"+profileID+"/status", &profileStatusResponse)
		require.True(t, profileStatusResponse.Success)
		require.NotNil(t, profileStatusResponse.Data.Snapshot)
		require.Equal(t, status.Snapshot.RunID, profileStatusResponse.Data.Snapshot.RunID)

		var summaryResponse struct {
			Success bool                 `json:"success"`
			Data    statusSummaryPayload `json:"data"`
		}
		callJSONHandler(t, handler.GetSyncSummary, "/api/profiles/"+profileID+"/summary", &summaryResponse)
		require.True(t, summaryResponse.Success)
		require.NotNil(t, summaryResponse.Data.Snapshot)
		require.Equal(t, status.Snapshot.RunID, summaryResponse.Data.RunID)
		require.Equal(t, status.Snapshot.OutcomeCounts, summaryResponse.Data.OutcomeCounts)
		require.Equal(t, status.Snapshot.ProcessedSoFar, summaryResponse.Data.OutcomeCounts.Total())
	}

	hardcoverServer.releaseBlocked()
	for _, profileID := range profileIDs {
		completed := waitForStatusRun(t, multiUser, profileID)
		require.Equal(t, "completed", completed.Status)
		require.NotNil(t, completed.Snapshot)
		require.Equal(t, int32(3), completed.Snapshot.ProcessedSoFar)
		require.Equal(t, int32(3), completed.Snapshot.OutcomeCounts.Total())
	}
}

func TestPublicStatusAndSummaryRoutesExposeTechnicalTimeoutAsFailedOutcome(t *testing.T) {
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})

	dataDir := t.TempDir()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "timeout-status-test.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())
	absServer := newLiveStatusAudiobookshelfServer([]map[string]interface{}{
		statusBook("timeout-book", "Timeout Candidate", "Timeout Author"),
	})
	t.Cleanup(absServer.Server.Close)
	hardcoverServer := newTimeoutStatusHardcoverServer()
	t.Cleanup(hardcoverServer.Close)

	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	cfg.Hardcover.BaseURL = hardcoverServer.URL
	multiUser := multiuser.NewMultiUserService(repo, cfg, logger.Get())
	profileID := "timeout-profile"
	require.NoError(t, repo.CreateProfile(
		profileID,
		"Timeout profile",
		absServer.Server.URL,
		profileID,
		"hardcover-token",
		database.SyncConfigData{
			StateFile:          filepath.Join(dataDir, "sync-state.json"),
			ProcessUnreadBooks: true,
			DryRun:             true,
		},
	))
	require.NoError(t, multiUser.StartSync(profileID))

	completed := waitForStatusRun(t, multiUser, profileID)
	require.Equal(t, "completed", completed.Status)
	require.NotNil(t, completed.Snapshot)
	require.Equal(t, "completed", completed.Snapshot.State)
	require.Equal(t, int32(1), completed.Snapshot.ProcessedSoFar)
	require.Equal(t, int32(1), completed.Snapshot.OutcomeCounts.Total())
	require.Equal(t, int32(1), completed.Snapshot.OutcomeCounts.Failed)
	require.Len(t, completed.Snapshot.AttentionRecords, 1)
	require.Equal(t, syncsvc.OutcomeFailed, completed.Snapshot.AttentionRecords[0].Outcome)
	require.Equal(t, "timeout-book", completed.Snapshot.AttentionRecords[0].BookID)

	handler := NewHandler(multiUser, nil, logger.Get())
	var statusResponse struct {
		Success bool                        `json:"success"`
		Data    multiuser.SyncProfileStatus `json:"data"`
	}
	callJSONHandler(t, handler.GetProfileStatus, "/api/profiles/"+profileID+"/status", &statusResponse)
	require.True(t, statusResponse.Success)
	require.NotNil(t, statusResponse.Data.Snapshot)
	require.Equal(t, completed.Snapshot.RunID, statusResponse.Data.Snapshot.RunID)
	require.Equal(t, syncsvc.OutcomeFailed, statusResponse.Data.Snapshot.AttentionRecords[0].Outcome)

	var summaryResponse struct {
		Success bool                 `json:"success"`
		Data    statusSummaryPayload `json:"data"`
	}
	callJSONHandler(t, handler.GetSyncSummary, "/api/profiles/"+profileID+"/summary", &summaryResponse)
	require.True(t, summaryResponse.Success)
	require.NotNil(t, summaryResponse.Data.Snapshot)
	require.Equal(t, completed.Snapshot.RunID, summaryResponse.Data.RunID)
	require.Equal(t, completed.Snapshot.OutcomeCounts, summaryResponse.Data.OutcomeCounts)
	require.Equal(t, int32(1), summaryResponse.Data.OutcomeCounts.Failed)
	require.Len(t, summaryResponse.Data.AttentionRecords, 1)
	require.Equal(t, syncsvc.OutcomeFailed, summaryResponse.Data.AttentionRecords[0].Outcome)
}

type statusSummaryPayload struct {
	Snapshot            *syncsvc.SyncSnapshot       `json:"snapshot"`
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

type concurrentStatusHardcoverServer struct {
	*httptest.Server
	blockedStarted chan string
	release        chan struct{}
	blockedAOnce   sync.Once
	blockedBOnce   sync.Once
	releaseOnce    sync.Once
}

func newConcurrentStatusHardcoverServer() *concurrentStatusHardcoverServer {
	fixture := &concurrentStatusHardcoverServer{
		blockedStarted: make(chan string, 2),
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
		if strings.Contains(searchTerm, "Blocked concurrent-a") {
			firstBlockedRequest := false
			fixture.blockedAOnce.Do(func() {
				firstBlockedRequest = true
				fixture.blockedStarted <- r.Header.Get("Authorization")
			})
			if firstBlockedRequest {
				select {
				case <-fixture.release:
				case <-r.Context().Done():
					return
				}
			}
		}
		if strings.Contains(searchTerm, "Blocked concurrent-b") {
			firstBlockedRequest := false
			fixture.blockedBOnce.Do(func() {
				firstBlockedRequest = true
				fixture.blockedStarted <- r.Header.Get("Authorization")
			})
			if firstBlockedRequest {
				select {
				case <-fixture.release:
				case <-r.Context().Done():
					return
				}
			}
		}

		results := []map[string]interface{}{}
		for _, profileID := range []string{"concurrent-a", "concurrent-b"} {
			if strings.Contains(searchTerm, "Title "+profileID) {
				results = append(results, map[string]interface{}{
					"id":    profileID + "-hardcover",
					"title": "Title " + profileID,
				})
				break
			}
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

func (s *concurrentStatusHardcoverServer) releaseBlocked() {
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
	enrichmentStarted chan struct{}
	release           chan struct{}
	startOnce         sync.Once
	releaseOnce       sync.Once
	mu                sync.Mutex
	isbnCalls         int
}

func newDelayedSecondLookupHardcoverServer() *delayedSecondLookupHardcoverServer {
	fixture := &delayedSecondLookupHardcoverServer{
		enrichmentStarted: make(chan struct{}),
		release:           make(chan struct{}),
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
			fixture.startOnce.Do(func() { close(fixture.enrichmentStarted) })
			select {
			case <-fixture.release:
			case <-r.Context().Done():
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

func callJSONHandler(t *testing.T, handler http.HandlerFunc, path string, target interface{}) {
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), target))
}
