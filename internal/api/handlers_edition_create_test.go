package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/require"
)

type editionCreateHardcoverStub struct {
	importFn      func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error)
	bookFn        func(context.Context, string) (*models.HardcoverBook, error)
	editionFn     func(context.Context, string) (*models.Edition, error)
	authorsFn     func(context.Context, string, int) ([]models.Author, error)
	publishersFn  func(context.Context, string, int) ([]models.Publisher, error)
	createEbookFn func(context.Context, *edition.EditionInput) (*edition.EditionResult, error)
}

type editionCreateAudnexStub struct {
	getFn      func(context.Context, string, string) (*audnex.Book, error)
	discoverFn func(context.Context, string, string) (*audnex.Book, string, error)
}

func (s editionCreateAudnexStub) GetBookByASIN(ctx context.Context, asin, region string) (*audnex.Book, error) {
	return s.getFn(ctx, asin, region)
}

func (s editionCreateAudnexStub) DiscoverBookByASIN(ctx context.Context, asin, region string) (*audnex.Book, string, error) {
	return s.discoverFn(ctx, asin, region)
}

func (s editionCreateHardcoverStub) ImportRegionalAudiobook(ctx context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
	return s.importFn(ctx, input)
}

func (s editionCreateHardcoverStub) GetBookByID(ctx context.Context, id string) (*models.HardcoverBook, error) {
	if s.bookFn != nil {
		return s.bookFn(ctx, id)
	}
	return nil, nil
}

func (s editionCreateHardcoverStub) GetEditionUncached(ctx context.Context, id string) (*models.Edition, error) {
	if s.editionFn != nil {
		return s.editionFn(ctx, id)
	}
	return nil, nil
}

func (s editionCreateHardcoverStub) SearchAuthors(ctx context.Context, name string, limit int) ([]models.Author, error) {
	if s.authorsFn != nil {
		return s.authorsFn(ctx, name, limit)
	}
	return nil, nil
}

func (s editionCreateHardcoverStub) SearchPublishers(ctx context.Context, name string, limit int) ([]models.Publisher, error) {
	if s.publishersFn != nil {
		return s.publishersFn(ctx, name, limit)
	}
	return nil, nil
}

func (s editionCreateHardcoverStub) CreateEbook(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
	if s.createEbookFn != nil {
		return s.createEbookFn(ctx, input)
	}
	return nil, nil
}

func configureEditionCreateRoute(t *testing.T, fixture *editionDraftTestFixture) {
	t.Helper()
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("POST /api/profiles/{id}/edition-drafts/create", fixture.handler.CreateEditionFromDraft)
	authConfig := auth.DefaultAuthConfig()
	authConfig.Enabled = true
	fixture.routes = auth.NewAuthMiddleware(fixture.authService.GetSessionManager(), authConfig).RequireAuth(apiMux)
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{
			getFn: func(_ context.Context, asin, _ string) (*audnex.Book, error) {
				return &audnex.Book{ASIN: asin, ReleaseDate: "2024-05-06"}, nil
			},
			discoverFn: func(_ context.Context, asin, preferred string) (*audnex.Book, string, error) {
				return &audnex.Book{ASIN: asin, ReleaseDate: "2024-05-06"}, preferred, nil
			},
		}
	}
	profile, err := fixture.multiUserService.GetProfile("draft-profile")
	require.NoError(t, err)
	profile.SyncConfig.DryRun = false
	require.NoError(t, fixture.multiUserService.UpdateProfileConfig(
		profile.Profile.ID, profile.AudiobookshelfURL, profile.AudiobookshelfToken, profile.HardcoverToken, profile.SyncConfig,
	))
}

func addCompletedNeedsReviewRun(t *testing.T, fixture *editionDraftTestFixture, runID string, record sync.BookOutcomeRecord) {
	t.Helper()
	snapshot := sync.SyncSnapshot{
		ProfileID: "draft-profile", RunID: runID, State: string(sync.RunPhaseCompleted),
		BookOutcomes: []sync.BookOutcomeRecord{record},
	}
	snapshotJSON, err := json.Marshal(snapshot)
	require.NoError(t, err)
	queuedAt := time.Now().UTC()
	accepted, err := fixture.repository.AcceptSyncRun(&database.SyncRunReport{
		ProfileID: "draft-profile", RunID: runID, Phase: database.SyncRunPhaseQueued,
		QueuedAt: &queuedAt, SnapshotJSON: database.SyncSnapshotJSON(`{"profile_id":"draft-profile","run_id":"` + runID + `","state":"queued"}`),
	})
	require.NoError(t, err)
	finishedAt := queuedAt.Add(time.Second)
	accepted.Phase = database.SyncRunPhaseCompleted
	accepted.FinishedAt = &finishedAt
	accepted.SnapshotJSON = database.SyncSnapshotJSON(snapshotJSON)
	require.NoError(t, fixture.repository.UpsertSyncRunReportContext(context.Background(), accepted))
}

func editionCreateRecord() sync.BookOutcomeRecord {
	return sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, ASIN: "b0source12",
		ISBN: "9780306406157", Format: "Audiobook", HardcoverBookID: "42",
	}
}

func TestCreateEditionFromDraftUsesExactRunAndPersistsVerifiedAssociation(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Reviewed title","authorName":"Author","asin":" B0SOURCE12 ","isbn":"978-0-306-40615-7","publishedDate":"2020-02-03"},
			"duration":100,"numTracks":1
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	record := editionCreateRecord()
	record.Title = "  reviewed TITLE "
	record.Author = " author "
	addCompletedNeedsReviewRun(t, fixture, "run-create-1", record)
	var mutationCalls, insertEditionCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(token string) editionCreateHardcoverClient {
		require.Equal(t, "hardcover-token", token)
		return editionCreateHardcoverStub{
			createEbookFn: func(context.Context, *edition.EditionInput) (*edition.EditionResult, error) {
				insertEditionCalls.Add(1)
				return nil, errors.New("audiobook create must not call insert_edition")
			},
			importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
				mutationCalls.Add(1)
				require.Equal(t, 42, input.BookID)
				require.Equal(t, "B0SOURCE12", input.ASIN)
				require.Equal(t, "uk", input.Region)
				return &hardcover.RegionalAudiobookResult{
					Status: hardcover.RegionalAudiobookCreated, BookID: 42, EditionID: 84,
					ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
					RegionalExternalID: "B0SOURCE12:uk",
				}, nil
			}}
	}

	body := `{"run_id":"run-create-1","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:UK"}`
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, mutationCalls.Load())
	require.Zero(t, insertEditionCalls.Load(), "an audiobook create must not send insert_edition")
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			ABSItemID          string `json:"abs_item_id"`
			ReadingFormat      string `json:"reading_format"`
			Status             string `json:"status"`
			HardcoverBookID    string `json:"hardcover_book_id"`
			HardcoverEditionID string `json:"hardcover_edition_id"`
			RegionalExternalID string `json:"regional_external_id"`
			MetadataPreview    struct {
				ReleaseDate string `json:"release_date"`
			} `json:"metadata_preview"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.Equal(t, "abs-item-1", envelope.Data.ABSItemID)
	require.Equal(t, models.ReadingFormatAudiobook, envelope.Data.ReadingFormat)
	require.Equal(t, "created", envelope.Data.Status)
	require.Equal(t, "42", envelope.Data.HardcoverBookID)
	require.Equal(t, "84", envelope.Data.HardcoverEditionID)
	require.Equal(t, "B0SOURCE12:uk", envelope.Data.RegionalExternalID)
	require.Equal(t, "2024-05-06", envelope.Data.MetadataPreview.ReleaseDate)

	statePath := editionCreateProfileStatePath(fixture)
	stored, err := statepkg.LoadState(statePath)
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "B0SOURCE12", association.SourceASIN)
	require.Equal(t, "978-0-306-40615-7", association.SourceISBN13)
	require.Equal(t, "B0SOURCE12:uk", association.RegionalExternalID)
	require.Equal(t, "42", association.HardcoverBookID)
	require.Equal(t, "84", association.HardcoverEditionID)
	require.Equal(t, "audiobook", association.ReadingFormat)
}

func TestCreateEditionFromDraftRejectsSupersededNeedsReviewCandidate(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-old", editionCreateRecord())
	newerRecord := editionCreateRecord()
	newerRecord.HardcoverBookID = "43"
	addCompletedNeedsReviewRun(t, fixture, "run-create-new", newerRecord)
	var mutationCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutationCalls.Add(1)
			return nil, nil
		}}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-old","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Zero(t, fixture.absRequests.Load())
	require.Zero(t, mutationCalls.Load())
}

func TestCreateEditionFromDraftAllowsCandidateAfterUnrelatedLaterRun(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-candidate", editionCreateRecord())
	unrelatedRecord := editionCreateRecord()
	unrelatedRecord.BookID = "unrelated-item"
	unrelatedRecord.HardcoverBookID = "43"
	addCompletedNeedsReviewRun(t, fixture, "run-create-unrelated", unrelatedRecord)
	var mutationCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutationCalls.Add(1)
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookLoaded, BookID: input.BookID, EditionID: 84,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: input.ASIN + ":" + strings.ToLower(input.Region),
			}, nil
		}}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-candidate","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, mutationCalls.Load())
}

func TestCreateEditionFromDraftDoesNotReplaceSavedAssociation(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-associated", editionCreateRecord())
	stored := statepkg.NewState()
	require.NoError(t, stored.SetAssociation(statepkg.Association{
		ABSItemID: "abs-item-1", HardcoverBookID: "43", HardcoverEditionID: "84",
		ReadingFormat: models.ReadingFormatAudiobook, Provenance: "sync",
	}))
	require.NoError(t, stored.Save(editionCreateProfileStatePath(fixture)))
	var mutationCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutationCalls.Add(1)
			return nil, nil
		}}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-associated","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Zero(t, fixture.absRequests.Load())
	require.Zero(t, mutationCalls.Load())
	verified, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := verified.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "43", association.HardcoverBookID)
}

func TestCreateEditionFromDraftFallsBackToABSDateWhenAudnexHasNoBook(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"9780306406157","publishedDate":"2020-02-03"},
			"duration":100,"numTracks":1
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{
			getFn: func(context.Context, string, string) (*audnex.Book, error) {
				return nil, audnex.ErrNotFound
			},
			discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
				return nil, "", audnex.ErrNotFound
			},
		}
	}
	record := editionCreateRecord()
	record.Title = "Reviewed title"
	record.Author = "Author"
	addCompletedNeedsReviewRun(t, fixture, "run-create-date-fallback", record)
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookLoaded, BookID: input.BookID, EditionID: 84,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: input.ASIN + ":" + strings.ToLower(input.Region),
			}, nil
		}}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-date-fallback","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data struct {
			MetadataPreview struct {
				ReleaseDate string `json:"release_date"`
			} `json:"metadata_preview"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "2020-02-03", envelope.Data.MetadataPreview.ReleaseDate)
}

func TestCreateEditionFromDraftIgnoresTransientAudnexEnrichmentFailure(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"9780306406157","publishedDate":"2020-02-03"},
			"duration":100,"numTracks":1
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	var audnexLookupCalls atomic.Int32
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{
			getFn: func(context.Context, string, string) (*audnex.Book, error) {
				audnexLookupCalls.Add(1)
				return nil, audnex.ErrTransient
			},
			discoverFn: func(context.Context, string, string) (*audnex.Book, string, error) {
				return nil, "", audnex.ErrTransient
			},
		}
	}
	record := editionCreateRecord()
	record.Title = "Reviewed title"
	record.Author = "Author"
	addCompletedNeedsReviewRun(t, fixture, "run-create-transient-enrichment", record)
	var importCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			importCalls.Add(1)
			require.Equal(t, 42, input.BookID)
			require.Equal(t, "B0SOURCE12", input.ASIN)
			require.Equal(t, "uk", input.Region)
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookLoaded, BookID: input.BookID, EditionID: 84,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: input.ASIN + ":" + strings.ToLower(input.Region),
			}, nil
		}}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-transient-enrichment","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, audnexLookupCalls.Load())
	require.EqualValues(t, 1, importCalls.Load())
	var envelope struct {
		Data struct {
			MetadataPreview struct {
				ReleaseDate string `json:"release_date"`
			} `json:"metadata_preview"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "2020-02-03", envelope.Data.MetadataPreview.ReleaseDate)
}

func TestCreateEditionFromDraftCreatesEbookAtHTTPBoundary(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"ebook","media":{
			"metadata":{"title":"Reviewed ebook","authorName":"Author","isbn":"9780306406157","publishedDate":"2020-02-03"},
			"ebookFile":{},"ebookFormat":"epub"
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-ebook", sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Reviewed ebook", Author: "Author", ISBN: "9780306406157",
		Format: "Ebook", HardcoverBookID: "42",
	})
	var createCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			bookFn: func(_ context.Context, id string) (*models.HardcoverBook, error) {
				require.Equal(t, "42", id)
				return &models.HardcoverBook{ID: "42", Authors: []models.Author{{ID: "7", Name: "Author"}}}, nil
			},
			createEbookFn: func(_ context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
				createCalls.Add(1)
				require.Equal(t, 42, input.BookID)
				require.Equal(t, "Reviewed ebook", input.Title)
				require.Equal(t, []int{7}, input.AuthorIDs)
				require.Equal(t, "ebook", input.ReadingFormat)
				require.Equal(t, "9780306406157", input.ISBN13)
				return &edition.EditionResult{Success: true, EditionID: 84}, nil
			},
			editionFn: func(_ context.Context, id string) (*models.Edition, error) {
				require.Equal(t, "84", id)
				return &models.Edition{ID: "84", BookID: "42", ReadingFormatID: "4"}, nil
			},
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-ebook","abs_item_id":"abs-item-1"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, createCalls.Load())
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			ReadingFormat      string `json:"reading_format"`
			Status             string `json:"status"`
			HardcoverBookID    string `json:"hardcover_book_id"`
			HardcoverEditionID string `json:"hardcover_edition_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.Equal(t, models.ReadingFormatEbook, envelope.Data.ReadingFormat)
	require.Equal(t, "created", envelope.Data.Status)
	require.Equal(t, "42", envelope.Data.HardcoverBookID)
	require.Equal(t, "84", envelope.Data.HardcoverEditionID)
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "42", association.HardcoverBookID)
	require.Equal(t, "84", association.HardcoverEditionID)
	require.Equal(t, models.ReadingFormatEbook, association.ReadingFormat)
}

func TestCreateEditionFromDraftRejectsMismatchedReadBackEbookEditionID(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"ebook","media":{
			"metadata":{"title":"Reviewed ebook","authorName":"Author","isbn":"9780306406157","publishedDate":"2020-02-03"},
			"ebookFile":{},"ebookFormat":"epub"
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-ebook-mismatched-readback", sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Reviewed ebook", Author: "Author", ISBN: "9780306406157",
		Format: "Ebook", HardcoverBookID: "42",
	})
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			bookFn: func(_ context.Context, id string) (*models.HardcoverBook, error) {
				require.Equal(t, "42", id)
				return &models.HardcoverBook{ID: "42", Authors: []models.Author{{ID: "7", Name: "Author"}}}, nil
			},
			createEbookFn: func(_ context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
				require.Equal(t, 42, input.BookID)
				return &edition.EditionResult{Success: true, EditionID: 84}, nil
			},
			editionFn: func(_ context.Context, id string) (*models.Edition, error) {
				require.Equal(t, "84", id)
				return &models.Edition{ID: "85", BookID: "42", ReadingFormatID: "4"}, nil
			},
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-ebook-mismatched-readback","abs_item_id":"abs-item-1"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Verify the Hardcover result before retrying")
	require.Contains(t, response.Body.String(), "retrying may create another edition")
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.False(t, exists)
}

func TestCreateEditionFromDraftLookupFailureBeforeInsertAllowsOrdinaryRetry(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"ebook","media":{
			"metadata":{"title":"Reviewed ebook","authorName":"Author","isbn":"9780306406157"},
			"ebookFile":{},"ebookFormat":"epub"
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-ebook-lookup-failure", sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Reviewed ebook", Author: "Author", ISBN: "9780306406157",
		Format: "Ebook", HardcoverBookID: "42",
	})
	var createCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			bookFn: func(_ context.Context, id string) (*models.HardcoverBook, error) {
				require.Equal(t, "42", id)
				return &models.HardcoverBook{ID: "42", Authors: []models.Author{{ID: "7", Name: "Author"}}}, nil
			},
			createEbookFn: func(_ context.Context, _ *edition.EditionInput) (*edition.EditionResult, error) {
				createCalls.Add(1)
				return nil, errors.Join(edition.ErrCreateEditionPreMutation, context.DeadlineExceeded)
			},
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-ebook-lookup-failure","abs_item_id":"abs-item-1"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)

	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "retry the edition create")
	require.NotContains(t, response.Body.String(), "may have processed")
	require.EqualValues(t, 1, createCalls.Load())
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.False(t, exists)
}

func TestCreateEditionFromDraftRejectsChangedTitleOrAuthorBeforeHardcoverMutation(t *testing.T) {
	tests := []struct {
		name            string
		currentTitle    string
		currentAuthor   string
		currentASIN     string
		laterTitle      string
		laterAuthor     string
		wantError       string
		wantABSRequests int32
	}{
		{name: "current title changed", currentTitle: "Changed title", currentAuthor: "Author", currentASIN: "B0SOURCE12", wantError: "source data or reading format changed", wantABSRequests: 1},
		{name: "current author changed", currentTitle: "Reviewed title", currentAuthor: "Changed author", currentASIN: "B0SOURCE12", wantError: "source data or reading format changed", wantABSRequests: 1},
		{name: "current ASIN changed", currentTitle: "Reviewed title", currentAuthor: "Author", currentASIN: "B0CHANGED12", wantError: "source data or reading format changed", wantABSRequests: 1},
		{name: "later run title changed", currentTitle: "Reviewed title", currentAuthor: "Author", currentASIN: "B0SOURCE12", laterTitle: "Changed title", laterAuthor: "Author", wantError: "sync run no longer contains a usable needs-review source record"},
		{name: "later run author changed", currentTitle: "Reviewed title", currentAuthor: "Author", currentASIN: "B0SOURCE12", laterTitle: "Reviewed title", laterAuthor: "Changed author", wantError: "sync run no longer contains a usable needs-review source record"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, `{
				"id":"abs-item-1","mediaType":"book","media":{
					"metadata":{"title":"`+test.currentTitle+`","authorName":"`+test.currentAuthor+`","asin":"`+test.currentASIN+`","isbn":"9780306406157"},
					"duration":100,"numTracks":1
				}}`, "us")
			configureEditionCreateRoute(t, fixture)
			record := editionCreateRecord()
			record.Title = "Reviewed title"
			record.Author = "Author"
			addCompletedNeedsReviewRun(t, fixture, "run-create-stale", record)
			if test.laterTitle != "" || test.laterAuthor != "" {
				laterRecord := record
				laterRecord.Title = test.laterTitle
				laterRecord.Author = test.laterAuthor
				addCompletedNeedsReviewRun(t, fixture, "run-create-later", laterRecord)
			}
			var factoryCalls, mutationCalls atomic.Int32
			fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
				factoryCalls.Add(1)
				return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
					mutationCalls.Add(1)
					return nil, nil
				}}
			}
			body := `{"run_id":"run-create-stale","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`
			request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
			request.AddCookie(fixture.sessionCookie(t, fixture.owner))
			response := httptest.NewRecorder()
			fixture.routes.ServeHTTP(response, request)
			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), test.wantError)
			require.Zero(t, factoryCalls.Load())
			require.Zero(t, mutationCalls.Load())
			require.Equal(t, test.wantABSRequests, fixture.absRequests.Load())
			require.Zero(t, fixture.hardcoverRequests.Load())
			stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			_, exists := stored.GetAssociation("abs-item-1")
			require.False(t, exists)
		})
	}
}

func TestCreateEditionFromDraftRejectsInvalidExplicitRegionalIdentifierBeforeMutation(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-invalid-region", editionCreateRecord())
	var mutationCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutationCalls.Add(1)
			return nil, nil
		}}
	}
	body := `{"run_id":"run-create-invalid-region","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:moon"}`
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	require.Zero(t, mutationCalls.Load())
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestCreateEditionFromDraftRequiresExactCompletedRun(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	var mutationCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutationCalls.Add(1)
			return nil, nil
		}}
	}
	body := `{"run_id":"missing-run","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Zero(t, fixture.absRequests.Load())
	require.Zero(t, mutationCalls.Load())
}

func TestCreateEditionFromDraftBusyReturnsRetryAfter(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	fixture.handler.editionDraftSlots <- struct{}{}
	fixture.handler.editionDraftSlots <- struct{}{}
	body := `{"run_id":"run","abs_item_id":"abs-item-1"}`
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusTooManyRequests, response.Code, response.Body.String())
	require.Equal(t, "1", response.Header().Get("Retry-After"))
	require.Zero(t, fixture.absRequests.Load())
}

func TestCreateEditionFromDraftStateLockReturnsRetryAfterBeforeABSFetch(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-locked", editionCreateRecord())
	lock, err := statepkg.AcquireFileLock(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	defer func() { require.NoError(t, lock.Close()) }()

	body := `{"run_id":"run-create-locked","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusTooManyRequests, response.Code, response.Body.String())
	require.Equal(t, "1", response.Header().Get("Retry-After"))
	require.Zero(t, fixture.absRequests.Load())
}

func TestCreateEditionFromDraftSaveFailureExplainsRecovery(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"9780306406157"},
			"duration":100,"numTracks":1
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	record := editionCreateRecord()
	record.Title = "Reviewed title"
	record.Author = "Author"
	addCompletedNeedsReviewRun(t, fixture, "run-create-save-fail", record)
	var importCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			statePath := editionCreateProfileStatePath(fixture)
			importCalls.Add(1)
			if importCalls.Load() == 1 {
				// The association file is a directory by the time the transaction saves.
				require.NoError(t, os.Mkdir(statePath, 0700))
			}
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookCreated, BookID: input.BookID, EditionID: 84,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: input.ASIN + ":" + strings.ToLower(input.Region),
			}, nil
		}}
	}
	body := `{"run_id":"run-create-save-fail","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Verify the Hardcover result before retrying; retrying may create another edition")
	require.EqualValues(t, 1, importCalls.Load())
	require.NoError(t, os.Remove(editionCreateProfileStatePath(fixture)))

	retry := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	retry.AddCookie(fixture.sessionCookie(t, fixture.owner))
	retryResponse := httptest.NewRecorder()
	fixture.routes.ServeHTTP(retryResponse, retry)
	require.Equal(t, http.StatusOK, retryResponse.Code, retryResponse.Body.String())
	saved, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := saved.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "42", association.HardcoverBookID)
	require.Equal(t, "84", association.HardcoverEditionID)
}

func TestCreateEditionFromDraftConstructorUsesGlobalNetworkTrust(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	var actualTrust string
	fixture.handler.editionCreateABSClientFactory = func(baseURL, token, trust string) (editionCreateABSClient, error) {
		actualTrust = trust
		return audiobookshelf.NewClient(baseURL, token), nil
	}
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			return nil, errors.New("must fail before mutation test completes")
		}}
	}
	addCompletedNeedsReviewRun(t, fixture, "run-create-trust", editionCreateRecord())
	body := `{"run_id":"run-create-trust","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
	require.Equal(t, audiobookshelf.NetworkTrustAllowPrivate, actualTrust)
}

func editionCreateProfileStatePath(fixture *editionDraftTestFixture) string {
	return filepath.Join(fixture.dataDir, "sync_state.draft-profile")
}
