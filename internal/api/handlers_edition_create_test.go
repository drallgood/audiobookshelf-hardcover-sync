package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
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
	apiMux.HandleFunc("GET /api/profiles/{id}/edition-drafts/source/{itemID}", fixture.handler.GetEditionSourceDraft)
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

func TestCreateEditionFromDraftBoundsExplicitAudnexEnrichmentBeforeWrite(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"9780306406157","publishedDate":"2020-02-03"},
			"duration":100,"numTracks":1
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	record := editionCreateRecord()
	record.Title = "Reviewed title"
	record.Author = "Author"
	addCompletedNeedsReviewRun(t, fixture, "run-explicit-enrichment-budget", record)
	var lookupDeadline time.Time
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{getFn: func(ctx context.Context, _, _ string) (*audnex.Book, error) {
			var ok bool
			lookupDeadline, ok = ctx.Deadline()
			require.True(t, ok)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	}
	var counts editionCreateCallCounts
	fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)
	requestCtx, cancel := context.WithTimeout(context.Background(), editionCreateMutationReserve+1500*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-explicit-enrichment-budget","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`,
	)).WithContext(requestCtx)
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)

	requestDeadline, ok := requestCtx.Deadline()
	require.True(t, ok)
	require.True(t, lookupDeadline.Before(requestDeadline), "optional enrichment must end before the request")
	require.NoError(t, requestCtx.Err())
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, counts.imports.Load())
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "B0SOURCE12:uk", association.RegionalExternalID)
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

func TestCreateEditionFromDraftCreatesEbookWhenOptionalPublisherSearchFails(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"ebook","media":{
			"metadata":{"title":"Reviewed ebook","authorName":"Author","publisher":"Publisher","isbn":"9780306406157","publishedDate":"2020-02-03"},
			"ebookFile":{},"ebookFormat":"epub"
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-ebook", sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Reviewed ebook", Author: "Author", ISBN: "9780306406157",
		Format: "Ebook", HardcoverBookID: "42",
	})
	var createCalls atomic.Int32
	var publisherSearchCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			bookFn: func(_ context.Context, id string) (*models.HardcoverBook, error) {
				require.Equal(t, "42", id)
				return &models.HardcoverBook{ID: "42", Authors: []models.Author{{ID: "7", Name: "Author"}}}, nil
			},
			publishersFn: func(_ context.Context, name string, limit int) ([]models.Publisher, error) {
				publisherSearchCalls.Add(1)
				require.Equal(t, "Publisher", name)
				require.Equal(t, 10, limit)
				return nil, errors.New("Hardcover publisher search unavailable")
			},
			createEbookFn: func(_ context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
				createCalls.Add(1)
				require.Equal(t, 42, input.BookID)
				require.Equal(t, "Reviewed ebook", input.Title)
				require.Equal(t, []int{7}, input.AuthorIDs)
				require.Equal(t, "ebook", input.ReadingFormat)
				require.Equal(t, "9780306406157", input.ISBN13)
				require.Zero(t, input.PublisherID)
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
	require.EqualValues(t, 1, publisherSearchCalls.Load())
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

func TestWriteEditionCreateErrorForInsufficientMutationBudgetIsRetryable(t *testing.T) {
	handler := &Handler{}
	response := httptest.NewRecorder()
	err := errors.Join(edition.ErrCreateEditionPreMutation, edition.ErrCreateEditionInsufficientMutationBudget)

	handler.writeEditionCreateError(response, "profile", err)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, "1", response.Header().Get("Retry-After"))
	require.Contains(t, response.Body.String(), "no edition mutation was sent")
	require.NotContains(t, response.Body.String(), "may have processed")
}

func TestEditionCreateMapsPreSendMutationBudgetGuardToRetryableNoSend(t *testing.T) {
	tests := []struct {
		name     string
		itemJSON string
		runID    string
		record   sync.BookOutcomeRecord
		body     string
	}{
		{
			name:     "regional audiobook",
			itemJSON: `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`,
			runID:    "run-budget-audio",
			record:   sync.BookOutcomeRecord{BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Reviewed title", Author: "Author", ASIN: "B0SOURCE12", ISBN: "9780306406157", Format: "Audiobook", HardcoverBookID: "42"},
			body:     `{"run_id":"run-budget-audio","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`,
		},
		{
			name:     "ebook",
			itemJSON: `{"id":"abs-item-1","mediaType":"ebook","media":{"metadata":{"title":"Reviewed ebook","authorName":"Author","isbn":"9780306406157"},"ebookFile":{},"ebookFormat":"epub"}}`,
			runID:    "run-budget-ebook",
			record:   sync.BookOutcomeRecord{BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Reviewed ebook", Author: "Author", ISBN: "9780306406157", Format: "Ebook", HardcoverBookID: "42"},
			body:     `{"run_id":"run-budget-ebook","abs_item_id":"abs-item-1"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, tt.itemJSON, "us")
			configureEditionCreateRoute(t, fixture)
			addCompletedNeedsReviewRun(t, fixture, tt.runID, tt.record)
			var mutationCalls atomic.Int32
			fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
				return editionCreateHardcoverStub{
					importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
						mutationCalls.Add(1)
						return nil, hardcover.ErrMutationInsufficientBudget
					},
					bookFn: func(context.Context, string) (*models.HardcoverBook, error) {
						return &models.HardcoverBook{ID: "42", Authors: []models.Author{{ID: "7", Name: "Author"}}}, nil
					},
					createEbookFn: func(context.Context, *edition.EditionInput) (*edition.EditionResult, error) {
						mutationCalls.Add(1)
						return nil, hardcover.ErrMutationInsufficientBudget
					},
				}
			}

			response := postEditionCreate(t, fixture, fixture.owner, tt.body)
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.Equal(t, "1", response.Header().Get("Retry-After"))
			require.EqualValues(t, 1, mutationCalls.Load())
			require.Contains(t, response.Body.String(), "mutation was sent")
			require.NotContains(t, response.Body.String(), "may have processed")
			stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			_, exists := stored.GetAssociation("abs-item-1")
			require.False(t, exists)
		})
	}
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
		return audiobookshelf.NewClientWithNetworkTrust(baseURL, token, trust)
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

type editionCreateCallCounts struct {
	imports, ebookCreates atomic.Int32
}

// countingEditionCreateClient records mutation attempts and succeeds for any
// verified ebook or audiobook request on book 42.
func countingEditionCreateClient(counts *editionCreateCallCounts) func(string) editionCreateHardcoverClient {
	return func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
				counts.imports.Add(1)
				return &hardcover.RegionalAudiobookResult{
					Status: hardcover.RegionalAudiobookLoaded, BookID: input.BookID, EditionID: 84,
					ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
					RegionalExternalID: input.ASIN + ":" + input.Region,
				}, nil
			},
			bookFn: func(context.Context, string) (*models.HardcoverBook, error) {
				return &models.HardcoverBook{ID: "42", Authors: []models.Author{{ID: "7", Name: "Author"}}}, nil
			},
			createEbookFn: func(context.Context, *edition.EditionInput) (*edition.EditionResult, error) {
				counts.ebookCreates.Add(1)
				return &edition.EditionResult{Success: true, EditionID: 84}, nil
			},
			editionFn: func(context.Context, string) (*models.Edition, error) {
				return &models.Edition{ID: "84", BookID: "42", ReadingFormatID: "4"}, nil
			},
		}
	}
}

func postEditionCreate(t *testing.T, fixture *editionDraftTestFixture, user *auth.AuthUser, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(body))
	request.AddCookie(fixture.sessionCookie(t, user))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	return response
}

func requireNoEditionCreateEffects(t *testing.T, fixture *editionDraftTestFixture, counts *editionCreateCallCounts) {
	t.Helper()
	require.Zero(t, counts.imports.Load(), "no regional import may be attempted")
	require.Zero(t, counts.ebookCreates.Load(), "no insert_edition may be attempted")
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.False(t, exists)
}

func TestCreateEditionFromDraftRejectsChangedSourceIdentityBeforeMutation(t *testing.T) {
	const audiobookItem = `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`
	tests := []struct {
		name     string
		itemJSON string
		mutate   func(*sync.BookOutcomeRecord)
		wantABS  int32
	}{
		{name: "ISBN added", itemJSON: audiobookItem, mutate: func(r *sync.BookOutcomeRecord) { r.ISBN = "" }, wantABS: 1},
		{name: "ISBN removed", itemJSON: `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12"},"duration":100}}`, mutate: func(*sync.BookOutcomeRecord) {}, wantABS: 1},
		{name: "ASIN removed", itemJSON: `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"isbn":"9780306406157"},"duration":100}}`, mutate: func(*sync.BookOutcomeRecord) {}, wantABS: 1},
		{name: "reading format changed", itemJSON: audiobookItem, mutate: func(r *sync.BookOutcomeRecord) { r.Format = "Ebook" }, wantABS: 1},
		{name: "snapshot lacks reading format", itemJSON: audiobookItem, mutate: func(r *sync.BookOutcomeRecord) { r.Format = "" }},
		{name: "snapshot lacks Hardcover book", itemJSON: audiobookItem, mutate: func(r *sync.BookOutcomeRecord) { r.HardcoverBookID = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, test.itemJSON, "us")
			configureEditionCreateRoute(t, fixture)
			record := editionCreateRecord()
			test.mutate(&record)
			addCompletedNeedsReviewRun(t, fixture, "run-create-identity", record)
			var counts editionCreateCallCounts
			fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)

			response := postEditionCreate(t, fixture, fixture.owner,
				`{"run_id":"run-create-identity","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			require.Equal(t, test.wantABS, fixture.absRequests.Load())
			require.Zero(t, fixture.hardcoverRequests.Load())
			requireNoEditionCreateEffects(t, fixture, &counts)
		})
	}
}

func TestCreateEditionFromDraftRejectsInvalidInputBeforeMutation(t *testing.T) {
	const audiobookItem = `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`
	const isbnOnlyEbook = `{"id":"abs-item-1","mediaType":"ebook","media":{"metadata":{"title":"Ebook","authorName":"Author","isbn":"9780306406157"},"ebookFile":{},"ebookFormat":"epub"}}`
	const malformedASINEbook = `{"id":"abs-item-1","mediaType":"ebook","media":{"metadata":{"title":"Ebook","authorName":"Author","asin":"B0BAD?ASIN","isbn":"9780306406157"},"ebookFile":{},"ebookFormat":"epub"}}`
	ebookRecord := sync.BookOutcomeRecord{BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Ebook", Author: "Author",
		ISBN: "9780306406157", Format: "Ebook", HardcoverBookID: "42"}
	malformedASINEbookRecord := ebookRecord
	malformedASINEbookRecord.ASIN = "B0BAD?ASIN"
	noIdentifierEbookRecord := ebookRecord
	noIdentifierEbookRecord.ISBN = ""
	noASINAudiobookRecord := editionCreateRecord()
	noASINAudiobookRecord.ASIN = ""
	tests := []struct {
		name     string
		itemJSON string
		record   sync.BookOutcomeRecord
		body     string
	}{
		{name: "audiobook metadata edit", itemJSON: audiobookItem, record: editionCreateRecord(),
			body: `"audible_identifier":"B0SOURCE12:uk","title":"Edited"`},
		{name: "audiobook without an ASIN", record: noASINAudiobookRecord,
			itemJSON: `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"isbn":"9780306406157"},"duration":100}}`},
		{name: "ebook without an identifier", record: noIdentifierEbookRecord,
			itemJSON: `{"id":"abs-item-1","mediaType":"ebook","media":{"metadata":{"title":"Ebook","authorName":"Author"},"ebookFile":{},"ebookFormat":"epub"}}`},
		{name: "correction removes the last identifier", itemJSON: isbnOnlyEbook, record: ebookRecord, body: `"isbn_13":""`},
		{name: "whitespace-only ASIN correction", itemJSON: isbnOnlyEbook, record: ebookRecord, body: `"isbn_13":"","asin":"   "`},
		{name: "malformed explicit ASIN correction", itemJSON: malformedASINEbook, record: malformedASINEbookRecord, body: `"asin":"not-an-asin"`},
		{name: "Audible identifier on an ebook", itemJSON: isbnOnlyEbook, record: ebookRecord, body: `"audible_identifier":"B0SOURCE12:uk"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, test.itemJSON, "us")
			configureEditionCreateRoute(t, fixture)
			addCompletedNeedsReviewRun(t, fixture, "run-create-invalid", test.record)
			var counts editionCreateCallCounts
			fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)

			body := `{"run_id":"run-create-invalid","abs_item_id":"abs-item-1"`
			if test.body != "" {
				body += "," + test.body
			}
			response := postEditionCreate(t, fixture, fixture.owner, body+"}")
			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			requireNoEditionCreateEffects(t, fixture, &counts)
		})
	}
}

func TestCreateEditionFromDraftCreatesASINOnlyEbook(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"ebook","media":{
		"metadata":{"title":"Ebook","authorName":"Author","asin":"b0ebook123"},"ebookFile":{},"ebookFormat":"epub"}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-asin-ebook", sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Ebook", Author: "Author", ASIN: "B0EBOOK123",
		Format: "Ebook", HardcoverBookID: "42",
	})
	var counts editionCreateCallCounts
	stub := countingEditionCreateClient(&counts)
	fixture.handler.editionCreateHardcoverFactory = func(token string) editionCreateHardcoverClient {
		client := stub(token).(editionCreateHardcoverStub)
		create := client.createEbookFn
		client.createEbookFn = func(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
			require.Equal(t, "B0EBOOK123", input.ASIN)
			require.Empty(t, input.ISBN10)
			require.Empty(t, input.ISBN13)
			return create(ctx, input)
		}
		return client
	}

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-create-asin-ebook","abs_item_id":"abs-item-1"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, counts.ebookCreates.Load())
	require.Zero(t, counts.imports.Load())
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "84", association.HardcoverEditionID)
}

func TestCreateEditionFromDraftCreatesISBNOnlyEbookWhenSourceASINIsMalformed(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"ebook","media":{
		"metadata":{"title":"Ebook","authorName":"Author","asin":"B0BAD?ASIN","isbn":"9780306406157"},
		"ebookFile":{},"ebookFormat":"epub"}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-isbn-ebook", sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Ebook", Author: "Author",
		ASIN: "B0BAD?ASIN", ISBN: "9780306406157", Format: "Ebook", HardcoverBookID: "42",
	})

	draftResponse := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, draftResponse.Code, draftResponse.Body.String())
	var draftEnvelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(draftResponse.Body.Bytes(), &draftEnvelope))
	require.True(t, draftEnvelope.Data.Eligible, "the valid ISBN makes the ebook draft eligible")
	require.Equal(t, "B0BAD?ASIN", draftEnvelope.Data.SourceIdentifiers.ASIN)

	var counts editionCreateCallCounts
	stub := countingEditionCreateClient(&counts)
	fixture.handler.editionCreateHardcoverFactory = func(token string) editionCreateHardcoverClient {
		client := stub(token).(editionCreateHardcoverStub)
		create := client.createEbookFn
		client.createEbookFn = func(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
			require.Empty(t, input.ASIN, "malformed source ASIN is omitted from the default insert")
			require.Equal(t, "0306406152", input.ISBN10)
			require.Equal(t, "9780306406157", input.ISBN13)
			return create(ctx, input)
		}
		return client
	}

	response := postEditionCreate(t, fixture, fixture.owner,
		`{"run_id":"run-create-isbn-ebook","abs_item_id":"abs-item-1"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, counts.ebookCreates.Load())
	require.Zero(t, counts.imports.Load())
}

func TestCreateEditionFromDraftAppliesEbookCorrections(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"ebook","media":{
		"metadata":{"title":"Ebook","authorName":"Author","isbn":"9780306406157"},"ebookFile":{},"ebookFormat":"epub"}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-correct-ebook", sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Ebook", Author: "Author",
		ISBN: "9780306406157", Format: "Ebook", HardcoverBookID: "42",
	})
	var counts editionCreateCallCounts
	stub := countingEditionCreateClient(&counts)
	fixture.handler.editionCreateHardcoverFactory = func(token string) editionCreateHardcoverClient {
		client := stub(token).(editionCreateHardcoverStub)
		create := client.createEbookFn
		client.createEbookFn = func(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
			require.Equal(t, "Corrected title", input.Title)
			require.Equal(t, "0306406152", input.ISBN10)
			require.Equal(t, "9780306406157", input.ISBN13)
			require.Equal(t, "2024-05-06", input.ReleaseDate)
			require.Equal(t, "Digital", input.EditionFormat)
			return create(ctx, input)
		}
		return client
	}
	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-correct-ebook","abs_item_id":"abs-item-1",`+
		`"title":"Corrected title","isbn_10":"0306406152","release_date":"2024-05-06","edition_format":"Digital"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, counts.ebookCreates.Load())
}

func TestCreateEditionFromDraftRejectsConflictingEbookISBNCorrections(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"ebook","media":{
		"metadata":{"title":"Ebook","authorName":"Author","isbn":"9780306406157"},"ebookFile":{},"ebookFormat":"epub"}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-conflicting-isbn", sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Ebook", Author: "Author",
		ISBN: "9780306406157", Format: "Ebook", HardcoverBookID: "42",
	})
	var counts editionCreateCallCounts
	fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)
	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-conflicting-isbn","abs_item_id":"abs-item-1",`+
		`"isbn_10":"0306406152","isbn_13":"9781861972712"}`)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	requireNoEditionCreateEffects(t, fixture, &counts)
}

func TestCreateEditionFromDraftRefusesUnconfirmedAudibleRegion(t *testing.T) {
	tests := []struct {
		name     string
		discover func(context.Context, string, string) (*audnex.Book, string, error)
		wantCode int
	}{
		{name: "completed sweep without a match", wantCode: http.StatusUnprocessableEntity,
			discover: func(context.Context, string, string) (*audnex.Book, string, error) { return nil, "", nil }},
		{name: "rate limited", wantCode: http.StatusServiceUnavailable,
			discover: func(context.Context, string, string) (*audnex.Book, string, error) {
				return nil, "", &audnex.APIError{Kind: audnex.ErrRateLimited, Err: errors.New("429")}
			}},
		{name: "transient failure", wantCode: http.StatusServiceUnavailable,
			discover: func(context.Context, string, string) (*audnex.Book, string, error) {
				return nil, "", &audnex.APIError{Kind: audnex.ErrTransient, Err: errors.New("503")}
			}},
		{name: "different ASIN returned", wantCode: http.StatusUnprocessableEntity,
			discover: func(context.Context, string, string) (*audnex.Book, string, error) {
				return &audnex.Book{ASIN: "B0OTHER123"}, "uk", nil
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
			configureEditionCreateRoute(t, fixture)
			addCompletedNeedsReviewRun(t, fixture, "run-create-region", editionCreateRecord())
			fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
				return editionCreateAudnexStub{discoverFn: test.discover}
			}
			var counts editionCreateCallCounts
			fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)

			response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-create-region","abs_item_id":"abs-item-1"}`)
			require.Equal(t, test.wantCode, response.Code, response.Body.String())
			requireNoEditionCreateEffects(t, fixture, &counts)
		})
	}
}

func TestCreateEditionFromDraftDiscoversRegionBeforeImport(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "ca")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-discover", editionCreateRecord())
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{discoverFn: func(_ context.Context, asin, preferred string) (*audnex.Book, string, error) {
			require.Equal(t, "B0SOURCE12", asin)
			require.Equal(t, "ca", preferred)
			return &audnex.Book{ASIN: asin}, "uk", nil
		}}
	}
	var counts editionCreateCallCounts
	stub := countingEditionCreateClient(&counts)
	fixture.handler.editionCreateHardcoverFactory = func(token string) editionCreateHardcoverClient {
		client := stub(token).(editionCreateHardcoverStub)
		importFn := client.importFn
		client.importFn = func(ctx context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			require.Equal(t, "uk", input.Region)
			return importFn(ctx, input)
		}
		return client
	}
	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-discover","abs_item_id":"abs-item-1"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, counts.imports.Load())
}

func TestCreateEditionFromDraftStopsDiscoveryBeforeWriteReserve(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-discovery-budget", editionCreateRecord())
	var discoveryDeadline time.Time
	fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
		return editionCreateAudnexStub{discoverFn: func(ctx context.Context, _, _ string) (*audnex.Book, string, error) {
			var ok bool
			discoveryDeadline, ok = ctx.Deadline()
			require.True(t, ok)
			<-ctx.Done()
			return nil, "", ctx.Err()
		}}
	}
	var counts editionCreateCallCounts
	fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)
	requestCtx, cancel := context.WithTimeout(context.Background(), editionCreateMutationReserve+500*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-discovery-budget","abs_item_id":"abs-item-1"}`,
	)).WithContext(requestCtx)
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)

	requestDeadline, ok := requestCtx.Deadline()
	require.True(t, ok)
	require.Equal(t, requestDeadline.Add(-editionCreateMutationReserve), discoveryDeadline)
	require.NoError(t, requestCtx.Err(), "discovery should stop while the write reserve remains")
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "audible_identifier")
	require.Empty(t, response.Header().Get("Retry-After"))
	requireNoEditionCreateEffects(t, fixture, &counts)
}

func TestCreateEditionFromDraftDoesNotStartWriteWithShortDeadline(t *testing.T) {
	tests := []struct {
		name     string
		itemJSON string
		record   sync.BookOutcomeRecord
		body     string
	}{
		{name: "audiobook", itemJSON: `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`,
			record: editionCreateRecord(), body: `{"run_id":"run-short","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`},
		{name: "ebook", itemJSON: `{"id":"abs-item-1","mediaType":"ebook","media":{"metadata":{"title":"Ebook","authorName":"Author","isbn":"9780306406157"},"ebookFile":{},"ebookFormat":"epub"}}`,
			record: sync.BookOutcomeRecord{BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Ebook", Author: "Author", ISBN: "9780306406157", Format: "Ebook", HardcoverBookID: "42"},
			body:   `{"run_id":"run-short","abs_item_id":"abs-item-1"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, test.itemJSON, "us")
			configureEditionCreateRoute(t, fixture)
			addCompletedNeedsReviewRun(t, fixture, "run-short", test.record)
			var counts editionCreateCallCounts
			fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(test.body)).WithContext(ctx)
			request.AddCookie(fixture.sessionCookie(t, fixture.owner))
			response := httptest.NewRecorder()
			fixture.routes.ServeHTTP(response, request)
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.Equal(t, "1", response.Header().Get("Retry-After"))
			requireNoEditionCreateEffects(t, fixture, &counts)
		})
	}
}

func TestCreateEditionFromDraftReportsDefiniteImportFailure(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-failed-import", editionCreateRecord())
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			return nil, fmt.Errorf("%w: not_found", hardcover.ErrRegionalAudiobookImportFailed)
		}}
	}
	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-failed-import","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
	require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "may have processed")
	require.NotContains(t, response.Body.String(), "retrying may create")
}

func TestCreateEditionFromDraftReportsUnverifiedIdentityOnce(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-identity-conflict", editionCreateRecord())
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			return nil, hardcover.ErrRegionalAudiobookIdentityConflict
		}}
	}
	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-identity-conflict","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Equal(t, 1, strings.Count(response.Body.String(), "Verify the Hardcover result before retrying"))
}

func TestCreateEditionFromDraftRefusesDryRunAndForeignProfiles(t *testing.T) {
	t.Run("dry run", func(t *testing.T) {
		fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
		configureEditionCreateRoute(t, fixture)
		profile, err := fixture.multiUserService.GetProfile("draft-profile")
		require.NoError(t, err)
		profile.SyncConfig.DryRun = true
		require.NoError(t, fixture.multiUserService.UpdateProfileConfig(
			profile.Profile.ID, profile.AudiobookshelfURL, profile.AudiobookshelfToken, profile.HardcoverToken, profile.SyncConfig,
		))
		addCompletedNeedsReviewRun(t, fixture, "run-create-dry", editionCreateRecord())
		var counts editionCreateCallCounts
		fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)

		response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-create-dry","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
		require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "dry run")
		require.Zero(t, fixture.absRequests.Load())
		require.Zero(t, fixture.hardcoverRequests.Load())
		requireNoEditionCreateEffects(t, fixture, &counts)
	})

	t.Run("foreign user", func(t *testing.T) {
		fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
		configureEditionCreateRoute(t, fixture)
		addCompletedNeedsReviewRun(t, fixture, "run-create-foreign", editionCreateRecord())
		var counts editionCreateCallCounts
		fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)
		foreignUser, err := fixture.authService.CreateUser(context.Background(), "foreign-create-user", "foreign-create@example.invalid", "password", auth.RoleUser, "local")
		require.NoError(t, err)

		response := postEditionCreate(t, fixture, foreignUser, `{"run_id":"run-create-foreign","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		require.Zero(t, fixture.absRequests.Load())
		requireNoEditionCreateEffects(t, fixture, &counts)
	})
}

// editionCreateCatalogClient backs a real edition.Creator: duplicate lookups
// return existing (or not found), and every GraphQL request reaches Hardcover
// through the real client.
type editionCreateCatalogClient struct {
	*hardcover.Client
	existing *models.Edition
}

func (c editionCreateCatalogClient) lookup() (*models.Edition, error) {
	if c.existing == nil {
		return nil, models.ErrEditionNotFound
	}
	return c.existing, nil
}

func (c editionCreateCatalogClient) GetEditionByASIN(context.Context, string) (*models.Edition, error) {
	return c.lookup()
}

func (c editionCreateCatalogClient) GetEditionByISBN13(context.Context, string) (*models.Edition, error) {
	return c.lookup()
}

func (c editionCreateCatalogClient) GetEditionByISBN10(context.Context, string) (*models.Edition, error) {
	return c.lookup()
}

// newEditionCreateHardcoverServer returns a real Hardcover client whose server
// answers every request with body and counts the requests it receives.
func newEditionCreateHardcoverServer(t *testing.T, body string) (*hardcover.Client, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	client, server := hardcover.CreateTestClientWithHandler(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	t.Cleanup(server.Close)
	return client, requests
}

// newEditionCreateEbookFixture prepares a needs-review ebook run for book 42
// whose ebook insertion goes through the production edition.Creator.
func newEditionCreateEbookFixture(t *testing.T, runID string, catalog editionCreateCatalogClient) *editionDraftTestFixture {
	t.Helper()
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"ebook","media":{
			"metadata":{"title":"Reviewed ebook","authorName":"Author","isbn":"9780306406157","publishedDate":"2020-02-03"},
			"ebookFile":{},"ebookFormat":"epub"
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, runID, sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Reviewed ebook", Author: "Author", ISBN: "9780306406157",
		Format: "Ebook", HardcoverBookID: "42",
	})
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			bookFn: func(context.Context, string) (*models.HardcoverBook, error) {
				return &models.HardcoverBook{ID: "42", Authors: []models.Author{{ID: "7", Name: "Author"}}}, nil
			},
			createEbookFn: func(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
				creator := edition.NewCreatorWithHTTPClient(catalog, logger.Get(), false, "", &http.Client{})
				return creator.CreateEditionWithMutationReserve(ctx, input, editionCreateMutationReserve)
			},
			editionFn: func(_ context.Context, id string) (*models.Edition, error) {
				return &models.Edition{ID: id, BookID: "42", ReadingFormatID: "4"}, nil
			},
		}
	}
	return fixture
}

func TestCreateEditionFromDraftReusesExistingEbookEditionOfSameBook(t *testing.T) {
	client, hardcoverRequests := newEditionCreateHardcoverServer(t, `{"data":{"insert_edition":{"id":999,"errors":[]}}}`)
	fixture := newEditionCreateEbookFixture(t, "run-existing-ebook", editionCreateCatalogClient{
		Client: client, existing: &models.Edition{ID: "91", BookID: "42"},
	})

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-existing-ebook","abs_item_id":"abs-item-1"}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data editionCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "existing", envelope.Data.Status)
	require.Equal(t, "42", envelope.Data.HardcoverBookID)
	require.Equal(t, "91", envelope.Data.HardcoverEditionID)
	require.Zero(t, hardcoverRequests.Load(), "an existing edition must not be inserted again")
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "91", association.HardcoverEditionID)
	require.Equal(t, models.ReadingFormatEbook, association.ReadingFormat)
}

func TestCreateEditionFromDraftRefusesISBNOwnedByAnotherBook(t *testing.T) {
	client, hardcoverRequests := newEditionCreateHardcoverServer(t, `{"data":{"insert_edition":{"id":999,"errors":[]}}}`)
	fixture := newEditionCreateEbookFixture(t, "run-isbn-conflict", editionCreateCatalogClient{
		Client: client, existing: &models.Edition{ID: "91", BookID: "77"},
	})

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-isbn-conflict","abs_item_id":"abs-item-1"}`)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "different book")
	require.NotContains(t, response.Body.String(), "may have processed")
	require.Zero(t, hardcoverRequests.Load(), "a cross-book ISBN must stop before insert_edition")
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.False(t, exists)
}

func TestCreateEditionFromDraftSavesNothingWhenTokenLacksCatalogueScope(t *testing.T) {
	// Hasura hides mutations a role cannot run, so a token without catalogue
	// write scope sees the mutation field as missing.
	const denied = `{"errors":[{"message":"field '%s' not found in type: 'mutation_root'","extensions":{"code":"validation-failed"}}]}`

	t.Run("ebook", func(t *testing.T) {
		client, hardcoverRequests := newEditionCreateHardcoverServer(t, fmt.Sprintf(denied, "insert_edition"))
		fixture := newEditionCreateEbookFixture(t, "run-ebook-no-scope", editionCreateCatalogClient{Client: client})

		response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-ebook-no-scope","abs_item_id":"abs-item-1"}`)

		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "catalogue write permission")
		require.Contains(t, response.Body.String(), "no edition was created")
		require.NotContains(t, response.Body.String(), "may have processed")
		require.EqualValues(t, 1, hardcoverRequests.Load(), "a denied insert_edition must not be retried")
		stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
		require.NoError(t, err)
		_, exists := stored.GetAssociation("abs-item-1")
		require.False(t, exists)
	})

	t.Run("audiobook", func(t *testing.T) {
		client, hardcoverRequests := newEditionCreateHardcoverServer(t, fmt.Sprintf(denied, "upsert_book"))
		fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
		configureEditionCreateRoute(t, fixture)
		addCompletedNeedsReviewRun(t, fixture, "run-audiobook-no-scope", editionCreateRecord())
		var ebookCreates atomic.Int32
		fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
			return editionCreateHardcoverStub{
				importFn: client.ImportRegionalAudiobook,
				createEbookFn: func(context.Context, *edition.EditionInput) (*edition.EditionResult, error) {
					ebookCreates.Add(1)
					return nil, errors.New("unexpected insert_edition")
				},
			}
		}

		response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-audiobook-no-scope","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)

		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "catalogue write permission")
		require.Contains(t, response.Body.String(), "no edition was created")
		require.NotContains(t, response.Body.String(), "may have processed")
		require.EqualValues(t, 1, hardcoverRequests.Load(), "a denied upsert_book must not be retried")
		require.Zero(t, ebookCreates.Load(), "a denied audiobook import must not fall back to insert_edition")
		stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
		require.NoError(t, err)
		_, exists := stored.GetAssociation("abs-item-1")
		require.False(t, exists)
	})
}

func TestCreateEditionFromDraftRefusesDuringShutdownBeforeAnyRequest(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-create-shutdown", editionCreateRecord())
	var counts editionCreateCallCounts
	fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)
	require.NoError(t, fixture.multiUserService.Shutdown(context.Background()))

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-create-shutdown","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)

	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "shutting down")
	require.Zero(t, fixture.absRequests.Load())
	requireNoEditionCreateEffects(t, fixture, &counts)
}

func TestEditionRegionDiscoveryUsesSyncRegionPreference(t *testing.T) {
	const audiobookJSON = `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`
	for _, test := range []struct {
		name          string
		profileRegion string
		globalRegion  string
		wantPreferred string
	}{
		{name: "legacy global region when profile has none", globalRegion: "uk", wantPreferred: "uk"},
		{name: "profile region wins", profileRegion: "ca", globalRegion: "uk", wantPreferred: "ca"},
		{name: "US when neither is set", wantPreferred: "us"},
	} {
		t.Run(test.name+"/draft", func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, audiobookJSON, test.profileRegion)
			fixture.config.Audiobookshelf.AudnexusRegion = test.globalRegion
			var gotPreferred string
			fixture.setDiscovery(func(_ context.Context, asin, preferred string) (*audnex.Book, string, error) {
				gotPreferred = preferred
				return &audnex.Book{ASIN: asin}, preferred, nil
			})

			response := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, test.wantPreferred, gotPreferred)
			require.NotContains(t, response.Body.String(), "unsupported_audnex_region")
		})

		t.Run(test.name+"/create", func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, audiobookJSON, test.profileRegion)
			fixture.config.Audiobookshelf.AudnexusRegion = test.globalRegion
			configureEditionCreateRoute(t, fixture)
			addCompletedNeedsReviewRun(t, fixture, "run-region-preference", editionCreateRecord())
			var gotPreferred string
			fixture.handler.editionCreateAudnexClientFactory = func() editionCreateAudnexDiscoverer {
				return editionCreateAudnexStub{discoverFn: func(_ context.Context, asin, preferred string) (*audnex.Book, string, error) {
					gotPreferred = preferred
					return &audnex.Book{ASIN: asin}, preferred, nil
				}}
			}
			var counts editionCreateCallCounts
			fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(&counts)

			response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-region-preference","abs_item_id":"abs-item-1"}`)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, test.wantPreferred, gotPreferred)
			require.EqualValues(t, 1, counts.imports.Load())
		})
	}
}
