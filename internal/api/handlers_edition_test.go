package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition/editiontest"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

const (
	editionProfileID = "edition-profile"
	editionRunID     = "seeded-run"
	editionBasePath  = "/api/profiles/" + editionProfileID + "/runs/" + editionRunID + "/books/"
)

func editionAPIItem(id, title, author, coverPath string) map[string]interface{} {
	return map[string]interface{}{
		"id":        id,
		"libraryId": "library",
		"mediaType": "book",
		"media": map[string]interface{}{
			"coverPath": coverPath,
			"metadata":  map[string]interface{}{"title": title, "authorName": author, "isbn": "978-0-306-40615-7"},
			"duration":  3600.0,
		},
	}
}

func expandedEditionAPIItem(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("audiobookshelf", "testdata", name))
	require.NoError(t, err)
	var item map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &item))
	return item
}

type editionAPIFixture struct {
	*statusServiceFixture
	routes    http.Handler
	hardcover *editiontest.HardcoverFake
	abs       *editiontest.AudiobookshelfFake
}

func newEditionAPIFixture(t *testing.T, dryRun bool, records []syncsvc.BookOutcomeRecord, items map[string]map[string]interface{}) *editionAPIFixture {
	t.Helper()
	hardcover := editiontest.NewHardcoverFake(t)
	abs := editiontest.NewAudiobookshelfFake(t, items)
	fixture := newStatusServiceFixture(t, hardcover.URL)
	// Runs before the service shuts down, so a held sync can unwind.
	t.Cleanup(hardcover.ReleaseHolds)

	require.NoError(t, fixture.repo.CreateProfile(
		editionProfileID, "Edition profile", abs.URL, "abs-secret-token", "hc-secret-token",
		database.SyncConfigData{
			StateFile:          filepath.Join(fixture.dataDir, "sync-state.json"),
			ProcessUnreadBooks: true,
			DryRun:             dryRun,
		},
	))
	seedEditionRun(t, fixture.repo, records)

	handler := NewHandler(fixture.multiUser, logger.Get())
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/profiles/{id}/runs/{runID}/books/{bookID}/edition-draft", handler.GetEditionDraft)
	root := http.NewServeMux()
	root.Handle("/api/", apiMux)

	return &editionAPIFixture{statusServiceFixture: fixture, routes: root, hardcover: hardcover, abs: abs}
}

// seedEditionRun stores a completed run report holding the given records.
func seedEditionRun(t *testing.T, repo *database.Repository, records []syncsvc.BookOutcomeRecord) {
	t.Helper()
	now := time.Now().UTC()
	raw, err := json.Marshal(syncsvc.SyncSnapshot{
		ProfileID: editionProfileID, RunID: editionRunID, State: string(syncsvc.RunPhaseCompleted), BookOutcomes: records,
	})
	require.NoError(t, err)
	report, err := repo.AcceptSyncRun(&database.SyncRunReport{
		ProfileID: editionProfileID, RunID: editionRunID, Phase: database.SyncRunPhaseQueued, QueuedAt: &now, SnapshotJSON: "{}",
	})
	require.NoError(t, err)
	report.Phase = database.SyncRunPhaseCompleted
	report.FinishedAt = &now
	report.SnapshotJSON = database.SyncSnapshotJSON(raw)
	require.NoError(t, repo.UpsertSyncRunReportContext(context.Background(), report))
}

func (f *editionAPIFixture) do(method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	f.routes.ServeHTTP(recorder, request)
	return recorder
}

type editionEnvelope struct {
	Success bool                   `json:"success"`
	Data    map[string]interface{} `json:"data"`
	Error   string                 `json:"error"`
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) editionEnvelope {
	t.Helper()
	var envelope editionEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope), recorder.Body.String())
	return envelope
}

func reviewRecord(bookID, hardcoverBookID string) syncsvc.BookOutcomeRecord {
	return syncsvc.BookOutcomeRecord{BookID: bookID, Outcome: syncsvc.OutcomeNeedsReview, HardcoverBookID: hardcoverBookID}
}

func singleItemFixture(t *testing.T, dryRun bool, item map[string]interface{}) *editionAPIFixture {
	t.Helper()
	return newEditionAPIFixture(t, dryRun,
		[]syncsvc.BookOutcomeRecord{reviewRecord("item-1", "4242")},
		map[string]map[string]interface{}{"item-1": item},
	)
}

func TestGetEditionDraftReturnsTheDraftContract(t *testing.T) {
	item := editionAPIItem("item-1", "Contract Title", "Contract Author", "/covers/item-1.jpg")
	f := singleItemFixture(t, false, item)
	f.hardcover.Authors["Contract Author"] = 55

	recorder := f.do(http.MethodGet, editionBasePath+"item-1/edition-draft", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	envelope := decodeEnvelope(t, recorder)
	require.True(t, envelope.Success)

	keys := make([]string, 0, len(envelope.Data))
	for key := range envelope.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	require.Equal(t, []string{
		"asin", "audio_seconds", "author_ids", "author_names", "country_id", "dry_run",
		"edition_format", "edition_information", "hardcover_book_id", "isbn_10", "isbn_10_valid", "isbn_13",
		"isbn_13_valid", "language_id", "narrator_ids", "narrator_names", "publisher_id", "publisher_name",
		"reading_format", "release_date", "subtitle", "title", "warnings",
	}, keys)
	require.EqualValues(t, 4242, envelope.Data["hardcover_book_id"])
	require.Equal(t, "Contract Title", envelope.Data["title"])
	require.Equal(t, []interface{}{float64(55)}, envelope.Data["author_ids"])
	require.Equal(t, []interface{}{}, envelope.Data["narrator_ids"])
	require.Equal(t, "Contract Author", envelope.Data["author_names"])
	require.EqualValues(t, 3600, envelope.Data["audio_seconds"])
	require.Equal(t, false, envelope.Data["dry_run"])
	require.IsType(t, []interface{}{}, envelope.Data["warnings"])
	require.NotContains(t, recorder.Body.String(), "abs-secret-token")
	require.Empty(t, f.hardcover.RecordedMutations(), "previewing must not create anything")
}

func TestGetEditionDraftRejectsIneligibleTargets(t *testing.T) {
	records := []syncsvc.BookOutcomeRecord{
		reviewRecord("ok-item", "4242"),
		{BookID: "synced-item", Outcome: syncsvc.OutcomeSynced, HardcoverBookID: "4242"},
		reviewRecord("no-candidate", ""),
		reviewRecord("text-id", "abc"),
	}
	items := map[string]map[string]interface{}{}
	for _, record := range records {
		items[record.BookID] = editionAPIItem(record.BookID, "Title", "Author", "")
	}

	tests := []struct {
		name string
		path string // path after /api/profiles/
		want int
	}{
		{"not needs-review", editionProfileID + "/runs/" + editionRunID + "/books/synced-item", http.StatusConflict},
		{"no Hardcover book", editionProfileID + "/runs/" + editionRunID + "/books/no-candidate", http.StatusConflict},
		{"non-numeric Hardcover book", editionProfileID + "/runs/" + editionRunID + "/books/text-id", http.StatusConflict},
		{"book not in run", editionProfileID + "/runs/" + editionRunID + "/books/absent", http.StatusNotFound},
		{"run not found", editionProfileID + "/runs/other-run/books/ok-item", http.StatusNotFound},
		{"profile not found", "missing-profile/runs/" + editionRunID + "/books/ok-item", http.StatusNotFound},
	}
	f := newEditionAPIFixture(t, false, records, items)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := f.do(http.MethodGet, "/api/profiles/"+tt.path+"/edition-draft", "")
			require.Equal(t, tt.want, draft.Code, draft.Body.String())
			require.False(t, decodeEnvelope(t, draft).Success)
		})
	}
	require.Empty(t, f.hardcover.RecordedMutations())
}

func TestGetEditionDraftRequiresAllPathIdentifiers(t *testing.T) {
	f := singleItemFixture(t, false, editionAPIItem("item-1", "Title", "Author", ""))
	handler := NewHandler(f.multiUser, logger.Get())

	recorder := httptest.NewRecorder()
	handler.GetEditionDraft(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestGetEditionDraftRejectsABookWithoutAnASINOrISBN(t *testing.T) {
	item := editionAPIItem("item-1", "Title", "Author", "/cover.jpg")
	delete(item["media"].(map[string]interface{})["metadata"].(map[string]interface{}), "isbn")
	f := singleItemFixture(t, false, item)
	const want = "This book has no ASIN or ISBN in Audiobookshelf, so an edition created for it could not be matched by a sync. Add an ASIN or ISBN in Audiobookshelf first."

	draft := f.do(http.MethodGet, editionBasePath+"item-1/edition-draft", "")
	require.Equal(t, http.StatusConflict, draft.Code, draft.Body.String())
	require.Equal(t, want, decodeEnvelope(t, draft).Error)
	require.Empty(t, f.hardcover.RecordedMutations())
}

func ebookAPIItem() map[string]interface{} {
	item := editionAPIItem("item-1", "Ebook Title", "Ebook Author", "/cover.jpg")
	media := item["media"].(map[string]interface{})
	delete(media, "duration")
	media["ebookFormat"] = "epub"
	return item
}

func TestGetEditionDraftForAnEbook(t *testing.T) {
	f := singleItemFixture(t, false, ebookAPIItem())
	f.hardcover.Authors["Ebook Author"] = 55

	draft := f.do(http.MethodGet, editionBasePath+"item-1/edition-draft", "")
	require.Equal(t, http.StatusOK, draft.Code, draft.Body.String())
	data := decodeEnvelope(t, draft).Data
	require.Equal(t, "ebook", data["reading_format"])
	require.Equal(t, "Ebook", data["edition_format"])
	require.EqualValues(t, 0, data["audio_seconds"])
	require.Equal(t, []interface{}{}, data["narrator_ids"])

	require.Empty(t, f.hardcover.RecordedMutations())
}

func TestGetEditionDraftMapsExpandedAudiobookAtHTTPBoundary(t *testing.T) {
	item := expandedEditionAPIItem(t, "expanded-audiobook.json")
	f := newEditionAPIFixture(t, false,
		[]syncsvc.BookOutcomeRecord{reviewRecord("item-audiobook", "4242")},
		map[string]map[string]interface{}{"item-audiobook": item},
	)
	f.hardcover.Authors["Fixture Author"] = 55

	response := f.do(http.MethodGet, editionBasePath+"item-audiobook/edition-draft", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	data := decodeEnvelope(t, response).Data
	require.Equal(t, "Expanded Audiobook", data["title"])
	require.Equal(t, "2020-06-15", data["release_date"])
	require.Equal(t, "Abridged", data["edition_information"])
	require.Equal(t, "audiobook", data["reading_format"])
	require.EqualValues(t, 33855, data["audio_seconds"])
	require.Equal(t, "9780306406158", data["isbn_13"])
	require.Equal(t, "", data["isbn_10"])
	require.Equal(t, false, data["isbn_13_valid"])
	require.Equal(t, []interface{}{float64(55)}, data["author_ids"])

	warnings, ok := data["warnings"].([]interface{})
	require.True(t, ok)
	warningText := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		warningText = append(warningText, warning.(string))
	}
	require.Contains(t, strings.Join(warningText, "\n"), "Fixture Publisher")
	require.Contains(t, strings.Join(warningText, "\n"), "Fixture Narrator")
	require.Contains(t, strings.Join(warningText, "\n"), "incorrect check digit")
	require.Contains(t, strings.Join(warningText, "\n"), "tagged \"German\"")
	require.Empty(t, f.hardcover.RecordedMutations())
}

func TestGetEditionDraftMapsExpandedEbookAtHTTPBoundary(t *testing.T) {
	item := expandedEditionAPIItem(t, "expanded-ebook.json")
	f := newEditionAPIFixture(t, false,
		[]syncsvc.BookOutcomeRecord{reviewRecord("item-ebook", "4242")},
		map[string]map[string]interface{}{"item-ebook": item},
	)
	f.hardcover.Authors["Ebook Fixture Author"] = 66

	response := f.do(http.MethodGet, editionBasePath+"item-ebook/edition-draft", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	data := decodeEnvelope(t, response).Data
	require.Equal(t, "Expanded Ebook", data["title"])
	require.Equal(t, "2019-01-01", data["release_date"])
	require.Equal(t, "ebook", data["reading_format"])
	require.Equal(t, "Ebook", data["edition_format"])
	require.EqualValues(t, 0, data["audio_seconds"])
	require.Equal(t, "9780525505143", data["isbn_13"])
	require.Equal(t, "0525505148", data["isbn_10"])
	require.Equal(t, true, data["isbn_13_valid"])
	require.Equal(t, true, data["isbn_10_valid"])
	require.Equal(t, []interface{}{float64(66)}, data["author_ids"])
	require.Equal(t, []interface{}{}, data["narrator_ids"])
	require.Equal(t, []interface{}{}, data["warnings"])
	require.Empty(t, f.hardcover.RecordedMutations())
}

func TestGetEditionDraftReportsDryRunProfiles(t *testing.T) {
	f := singleItemFixture(t, true, editionAPIItem("item-1", "Title", "Author", ""))

	draft := f.do(http.MethodGet, editionBasePath+"item-1/edition-draft", "")
	require.Equal(t, http.StatusOK, draft.Code, draft.Body.String())
	require.Equal(t, true, decodeEnvelope(t, draft).Data["dry_run"])

	require.Empty(t, f.hardcover.RecordedMutations())
}

func TestGetEditionDraftReportsMissingAndFailingAudiobookshelfItems(t *testing.T) {
	f := newEditionAPIFixture(t, false,
		[]syncsvc.BookOutcomeRecord{reviewRecord("gone-item", "4242")},
		map[string]map[string]interface{}{},
	)
	missing := f.do(http.MethodGet, editionBasePath+"gone-item/edition-draft", "")
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())

	f.abs.Status = http.StatusInternalServerError
	recorder := f.do(http.MethodGet, editionBasePath+"gone-item/edition-draft", "")
	require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "abs-secret-token")
	require.NotContains(t, recorder.Body.String(), "forced failure")
	require.Empty(t, f.hardcover.RecordedMutations())
}
