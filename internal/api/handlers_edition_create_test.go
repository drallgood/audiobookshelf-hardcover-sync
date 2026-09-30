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
	stdsync "sync"
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
	checkFn       func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error)
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

type editionCreateABSClientFunc func(context.Context, string) (*models.AudiobookshelfBook, error)

func (f editionCreateABSClientFunc) GetLibraryItemByID(ctx context.Context, itemID string) (*models.AudiobookshelfBook, error) {
	return f(ctx, itemID)
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

func (s editionCreateHardcoverStub) CheckRegionalAudiobookImport(ctx context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error) {
	if s.checkFn != nil {
		return s.checkFn(ctx, input)
	}
	return nil, false, nil
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
	apiMux.HandleFunc("POST /api/profiles/{id}/edition-drafts/check-import", fixture.handler.CheckEditionImport)
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
	addNeedsReviewRunWithState(t, fixture, runID, record, sync.RunPhaseCompleted, database.SyncRunPhaseCompleted)
}

func addNeedsReviewRunWithState(t *testing.T, fixture *editionDraftTestFixture, runID string, record sync.BookOutcomeRecord, snapshotState sync.RunPhase, reportPhase string) {
	t.Helper()
	snapshot := sync.SyncSnapshot{
		ProfileID: "draft-profile", RunID: runID, State: string(snapshotState),
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
	accepted.Phase = reportPhase
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

func TestCreateEditionFromDraftReturnsStructuredRecoveryForSubmittedTimeout(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-timeout-recovery", editionCreateRecord())
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			require.Equal(t, 42, input.BookID)
			require.Equal(t, "B0SOURCE12", input.ASIN)
			require.Equal(t, "uk", input.Region)
			return nil, hardcover.ErrRegionalAudiobookImportTimeout
		}}
	}
	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-timeout-recovery","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	var envelope struct {
		Error     string `json:"error"`
		ErrorCode string `json:"error_code"`
		Outcome   string `json:"outcome"`
		Data      struct {
			AudibleIdentifier string `json:"audible_identifier"`
			HardcoverBookID   string `json:"hardcover_book_id"`
			RecoveryToken     string `json:"recovery_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Contains(t, envelope.Error, "may have processed")
	require.Equal(t, "hardcover_import_unconfirmed", envelope.ErrorCode)
	require.Equal(t, editionOutcomeUnconfirmed, envelope.Outcome)
	require.Equal(t, "B0SOURCE12:uk", envelope.Data.AudibleIdentifier)
	require.Equal(t, "42", envelope.Data.HardcoverBookID)
	require.NotEmpty(t, envelope.Data.RecoveryToken)
	claims := editionRecoveryClaims{
		ProfileID: "draft-profile", RunID: "run-timeout-recovery", ABSItemID: "abs-item-1",
		HardcoverBookID: "42", AudibleIdentifier: "B0SOURCE12:uk", Correction: "B0SOURCE12:uk",
	}
	_, validToken := verifyEditionRecoveryToken("hardcover-token", envelope.Data.RecoveryToken, claims)
	require.True(t, validToken)
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.False(t, exists, "an unconfirmed attempt must not save local state")
}

func TestCheckEditionImportPersistsReadOnlyRecoveryAndIsIdempotent(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-check-recovery", editionCreateRecord())
	var imports, checks atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			imports.Add(1)
			return nil, hardcover.ErrRegionalAudiobookImportTimeout
		}}
	}
	created := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-check-recovery","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
	require.Equal(t, http.StatusServiceUnavailable, created.Code, created.Body.String())
	var createEnvelope struct {
		Data struct {
			AudibleIdentifier string `json:"audible_identifier"`
			HardcoverBookID   string `json:"hardcover_book_id"`
			RecoveryToken     string `json:"recovery_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createEnvelope))
	require.NotEmpty(t, createEnvelope.Data.RecoveryToken)

	confirmed := false
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{checkFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error) {
			checks.Add(1)
			require.Equal(t, 42, input.BookID)
			require.Equal(t, "B0SOURCE12", input.ASIN)
			require.Equal(t, "uk", input.Region)
			if !confirmed {
				return nil, false, nil
			}
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookCreated, BookID: 42, EditionID: 84,
				ReadingFormatID: models.ReadingFormatID(models.ReadingFormatAudiobook), RegionalExternalID: "B0SOURCE12:uk",
			}, true, nil
		}}
	}
	body := fmt.Sprintf(`{"run_id":"run-check-recovery","abs_item_id":"abs-item-1","audible_identifier":%q,"recovery_token":%q}`,
		createEnvelope.Data.AudibleIdentifier, createEnvelope.Data.RecoveryToken)
	pending := postEditionImportCheck(t, fixture, fixture.owner, body)
	require.Equal(t, http.StatusServiceUnavailable, pending.Code, pending.Body.String())
	var pendingEnvelope struct {
		ErrorCode string `json:"error_code"`
		Outcome   string `json:"outcome"`
		Data      struct {
			RecoveryToken string `json:"recovery_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(pending.Body.Bytes(), &pendingEnvelope))
	require.Equal(t, "hardcover_import_unconfirmed", pendingEnvelope.ErrorCode)
	require.Equal(t, editionOutcomeUnconfirmed, pendingEnvelope.Outcome)
	require.Equal(t, createEnvelope.Data.RecoveryToken, pendingEnvelope.Data.RecoveryToken)
	require.EqualValues(t, 1, imports.Load(), "status checks never resubmit the remote import")
	require.EqualValues(t, 1, checks.Load())

	confirmed = true
	first := postEditionImportCheck(t, fixture, fixture.owner, body)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var successEnvelope struct {
		Success bool `json:"success"`
		Data    struct {
			ReadingFormat      string `json:"reading_format"`
			Status             string `json:"status"`
			HardcoverBookID    string `json:"hardcover_book_id"`
			HardcoverEditionID string `json:"hardcover_edition_id"`
			RegionalExternalID string `json:"regional_external_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &successEnvelope))
	require.True(t, successEnvelope.Success)
	require.Equal(t, models.ReadingFormatAudiobook, successEnvelope.Data.ReadingFormat)
	require.Equal(t, "created", successEnvelope.Data.Status)
	require.Equal(t, "42", successEnvelope.Data.HardcoverBookID)
	require.Equal(t, "84", successEnvelope.Data.HardcoverEditionID)
	require.Equal(t, "B0SOURCE12:uk", successEnvelope.Data.RegionalExternalID)
	require.NotContains(t, first.Body.String(), "recovery_token", "recovery details are returned only for recoverable errors")

	checksBeforeRepeat := checks.Load()
	second := postEditionImportCheck(t, fixture, fixture.owner, body)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.Greater(t, checks.Load(), checksBeforeRepeat, "idempotent confirmation re-verifies the remote identity")
	require.EqualValues(t, 1, imports.Load())
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "42", association.HardcoverBookID)
	require.Equal(t, "84", association.HardcoverEditionID)
	require.Equal(t, "B0SOURCE12:uk", association.RegionalExternalID)
}

func TestCheckEditionImportRejectsTamperedTokenBeforeExternalLookup(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-check-tampered", editionCreateRecord())
	var externalCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{checkFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error) {
			externalCalls.Add(1)
			return nil, false, nil
		}}
	}
	claims := editionRecoveryClaims{
		ProfileID: "draft-profile", RunID: "run-check-tampered", ABSItemID: "abs-item-1",
		HardcoverBookID: "42", AudibleIdentifier: "B0SOURCE12:uk",
	}
	token := signEditionRecoveryToken("hardcover-token", claims)
	response := postEditionImportCheck(t, fixture, fixture.owner,
		fmt.Sprintf(`{"run_id":"run-check-tampered","abs_item_id":"abs-item-1","audible_identifier":"B0OTHER123:uk","recovery_token":%q}`, token))
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	var envelope struct {
		ErrorCode string `json:"error_code"`
		Outcome   string `json:"outcome"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "edition_recovery_invalid", envelope.ErrorCode)
	require.Equal(t, editionOutcomeNotSubmitted, envelope.Outcome)
	require.Zero(t, externalCalls.Load())
	require.Zero(t, fixture.absRequests.Load(), "token validation precedes the fresh ABS source lookup")
}

func TestCheckEditionImportRejectsExpiredTokenBeforeExternalLookup(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-check-expired", editionCreateRecord())
	var externalCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{checkFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error) {
			externalCalls.Add(1)
			return nil, false, nil
		}}
	}
	claims := editionRecoveryClaims{
		ProfileID: "draft-profile", RunID: "run-check-expired", ABSItemID: "abs-item-1",
		HardcoverBookID: "42", AudibleIdentifier: "B0SOURCE12:uk",
	}
	token := signEditionRecoveryTokenAt("hardcover-token", claims, time.Now().Add(-49*time.Hour))
	response := postEditionImportCheck(t, fixture, fixture.owner,
		fmt.Sprintf(`{"run_id":"run-check-expired","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk","recovery_token":%q}`, token))
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	var envelope struct {
		ErrorCode string `json:"error_code"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "edition_recovery_invalid", envelope.ErrorCode)
	require.Zero(t, externalCalls.Load())
	require.Zero(t, fixture.absRequests.Load(), "expired recovery is rejected before the ABS source lookup")
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.False(t, exists)
}

func TestCheckEditionImportABSReadFailuresPreserveRecoveryAndCanRetry(t *testing.T) {
	tests := []struct {
		name    string
		factory func() (editionCreateABSClient, error)
	}{
		{
			name: "client configuration",
			factory: func() (editionCreateABSClient, error) {
				return nil, errors.New("temporary Audiobookshelf client configuration failure")
			},
		},
		{
			name: "transient item read",
			factory: func() (editionCreateABSClient, error) {
				return editionCreateABSClientFunc(func(context.Context, string) (*models.AudiobookshelfBook, error) {
					return nil, errors.New("temporary Audiobookshelf read failure")
				}), nil
			},
		},
		{
			name: "nil item",
			factory: func() (editionCreateABSClient, error) {
				return editionCreateABSClientFunc(func(context.Context, string) (*models.AudiobookshelfBook, error) {
					return nil, nil
				}), nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
			configureEditionCreateRoute(t, fixture)
			addCompletedNeedsReviewRun(t, fixture, "run-check-abs-read-failure", editionCreateRecord())
			var imports, checks atomic.Int32
			fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
				return editionCreateHardcoverStub{
					importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
						imports.Add(1)
						return nil, hardcover.ErrRegionalAudiobookImportTimeout
					},
					checkFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error) {
						checks.Add(1)
						return &hardcover.RegionalAudiobookResult{
							Status: hardcover.RegionalAudiobookCreated, BookID: input.BookID, EditionID: 84,
							ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
							RegionalExternalID: input.ASIN + ":" + input.Region,
						}, true, nil
					},
				}
			}
			created := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-check-abs-read-failure","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
			require.Equal(t, http.StatusServiceUnavailable, created.Code, created.Body.String())
			var createdEnvelope struct {
				Data struct {
					AudibleIdentifier string `json:"audible_identifier"`
					RecoveryToken     string `json:"recovery_token"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createdEnvelope))
			require.NotEmpty(t, createdEnvelope.Data.RecoveryToken)
			body := fmt.Sprintf(`{"run_id":"run-check-abs-read-failure","abs_item_id":"abs-item-1","audible_identifier":%q,"recovery_token":%q}`,
				createdEnvelope.Data.AudibleIdentifier, createdEnvelope.Data.RecoveryToken)

			fixture.handler.editionCreateABSClientFactory = func(string, string, string) (editionCreateABSClient, error) {
				return test.factory()
			}
			failed := postEditionImportCheck(t, fixture, fixture.owner, body)
			require.Equal(t, http.StatusServiceUnavailable, failed.Code, failed.Body.String())
			var failedEnvelope struct {
				ErrorCode string `json:"error_code"`
				Outcome   string `json:"outcome"`
				Data      struct {
					AudibleIdentifier string `json:"audible_identifier"`
					HardcoverBookID   string `json:"hardcover_book_id"`
					RecoveryToken     string `json:"recovery_token"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(failed.Body.Bytes(), &failedEnvelope))
			require.Equal(t, "hardcover_import_unconfirmed", failedEnvelope.ErrorCode)
			require.Equal(t, editionOutcomeUnconfirmed, failedEnvelope.Outcome)
			require.Equal(t, "B0SOURCE12:uk", failedEnvelope.Data.AudibleIdentifier)
			require.Equal(t, "42", failedEnvelope.Data.HardcoverBookID)
			require.Equal(t, createdEnvelope.Data.RecoveryToken, failedEnvelope.Data.RecoveryToken)
			require.EqualValues(t, 1, imports.Load(), "a source read failure must not resubmit the import")
			require.Zero(t, checks.Load(), "Hardcover status is not checked until the fresh source read succeeds")
			stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			_, exists := stored.GetAssociation("abs-item-1")
			require.False(t, exists, "an unverified source read must not persist an association")

			fixture.handler.editionCreateABSClientFactory = nil
			retried := postEditionImportCheck(t, fixture, fixture.owner, body)
			require.Equal(t, http.StatusOK, retried.Code, retried.Body.String())
			require.EqualValues(t, 1, checks.Load())
			require.EqualValues(t, 1, imports.Load())
			stored, err = statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			association, exists := stored.GetAssociation("abs-item-1")
			require.True(t, exists)
			require.Equal(t, "42", association.HardcoverBookID)
			require.Equal(t, "84", association.HardcoverEditionID)
		})
	}
}

func TestCheckEditionImportTokenRotationInvalidatesRecoveryButLeavesCreateAvailable(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, "run-check-token-rotation", editionCreateRecord())
	var checkCalls, importCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			importCalls.Add(1)
			return nil, hardcover.ErrRegionalAudiobookImportTimeout
		}}
	}
	created := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-check-token-rotation","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
	require.Equal(t, http.StatusServiceUnavailable, created.Code, created.Body.String())
	var createEnvelope struct {
		Data struct {
			AudibleIdentifier string `json:"audible_identifier"`
			RecoveryToken     string `json:"recovery_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createEnvelope))
	require.NotEmpty(t, createEnvelope.Data.RecoveryToken)

	profile, err := fixture.multiUserService.GetProfile("draft-profile")
	require.NoError(t, err)
	require.NoError(t, fixture.multiUserService.UpdateProfileConfig(
		profile.Profile.ID, profile.AudiobookshelfURL, profile.AudiobookshelfToken, "rotated-hardcover-token", profile.SyncConfig,
	))
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			checkFn: func(context.Context, hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, bool, error) {
				checkCalls.Add(1)
				return nil, false, nil
			},
			importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
				importCalls.Add(1)
				return &hardcover.RegionalAudiobookResult{
					Status: hardcover.RegionalAudiobookLoaded, BookID: input.BookID, EditionID: 84,
					ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
					RegionalExternalID: input.ASIN + ":" + input.Region,
				}, nil
			},
		}
	}
	checkBody := fmt.Sprintf(`{"run_id":"run-check-token-rotation","abs_item_id":"abs-item-1","audible_identifier":%q,"recovery_token":%q}`,
		createEnvelope.Data.AudibleIdentifier, createEnvelope.Data.RecoveryToken)
	beforeABS := fixture.absRequests.Load()
	invalidated := postEditionImportCheck(t, fixture, fixture.owner, checkBody)
	require.Equal(t, http.StatusConflict, invalidated.Code, invalidated.Body.String())
	var invalidatedEnvelope struct {
		ErrorCode string `json:"error_code"`
		Outcome   string `json:"outcome"`
	}
	require.NoError(t, json.Unmarshal(invalidated.Body.Bytes(), &invalidatedEnvelope))
	require.Equal(t, "edition_recovery_invalid", invalidatedEnvelope.ErrorCode)
	require.Equal(t, editionOutcomeNotSubmitted, invalidatedEnvelope.Outcome)
	require.Zero(t, checkCalls.Load())
	require.Equal(t, beforeABS, fixture.absRequests.Load(), "rotated tokens are rejected before source or Hardcover lookups")

	// Recovery-token rotation must not disable a later ordinary create action.
	createdAgain := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-check-token-rotation","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
	require.Equal(t, http.StatusOK, createdAgain.Code, createdAgain.Body.String())
	require.EqualValues(t, 2, importCalls.Load())
}

func TestCreateEditionFromDraftUsesRecordFromCanceledRun(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Reviewed title","authorName":"Author","asin":" B0SOURCE12 ","isbn":"978-0-306-40615-7","publishedDate":"2020-02-03"},
			"duration":100,"numTracks":1
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	record := editionCreateRecord()
	record.Title = "  reviewed TITLE "
	record.Author = " author "
	// A run can be canceled partway through (e.g. the service restarted) while
	// still having already recorded a real, final outcome for this book before
	// cancellation; that per-book record must remain usable.
	addNeedsReviewRunWithState(t, fixture, "run-create-canceled", record, sync.RunPhaseCanceled, database.SyncRunPhaseCanceled)
	var mutationCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
			mutationCalls.Add(1)
			return &hardcover.RegionalAudiobookResult{
				Status: hardcover.RegionalAudiobookCreated, BookID: input.BookID, EditionID: 84,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: input.ASIN + ":" + strings.ToLower(input.Region),
			}, nil
		}}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-create-canceled","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, mutationCalls.Load())
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

func TestCreateEditionFromDraftChecksNewerCanceledItemOutcomes(t *testing.T) {
	for _, test := range []struct {
		name            string
		bookID          string
		hardcoverBookID string
		outcome         sync.SyncOutcome
		wantStatus      int
	}{
		{name: "changed candidate", bookID: "abs-item-1", hardcoverBookID: "43", outcome: sync.OutcomeNeedsReview, wantStatus: http.StatusConflict},
		{name: "resolved item", bookID: "abs-item-1", hardcoverBookID: "42", outcome: sync.OutcomeSynced, wantStatus: http.StatusConflict},
		{name: "unchanged candidate", bookID: "abs-item-1", hardcoverBookID: "42", outcome: sync.OutcomeNeedsReview, wantStatus: http.StatusOK},
		{name: "unrelated item", bookID: "another-item", hardcoverBookID: "43", outcome: sync.OutcomeSynced, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100}}`, "us")
			configureEditionCreateRoute(t, fixture)
			addCompletedNeedsReviewRun(t, fixture, "older-run", editionCreateRecord())
			newerRecord := editionCreateRecord()
			newerRecord.BookID = test.bookID
			newerRecord.HardcoverBookID = test.hardcoverBookID
			newerRecord.Outcome = test.outcome
			addNeedsReviewRunWithState(t, fixture, "newer-canceled-run", newerRecord, sync.RunPhaseCanceled, database.SyncRunPhaseCanceled)
			var mutations atomic.Int32
			fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
				return editionCreateHardcoverStub{importFn: func(_ context.Context, input hardcover.RegionalAudiobookInput) (*hardcover.RegionalAudiobookResult, error) {
					mutations.Add(1)
					return &hardcover.RegionalAudiobookResult{
						Status: hardcover.RegionalAudiobookLoaded, BookID: input.BookID, EditionID: 84,
						ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
						RegionalExternalID: input.ASIN + ":" + strings.ToLower(input.Region),
					}, nil
				}}
			}
			response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"older-run","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:us"}`)
			require.Equal(t, test.wantStatus, response.Code, response.Body.String())
			stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
			require.NoError(t, err)
			_, associated := stored.GetAssociation("abs-item-1")
			if test.wantStatus == http.StatusConflict {
				require.Zero(t, mutations.Load())
				require.Zero(t, fixture.absRequests.Load())
				require.False(t, associated)
			} else {
				require.EqualValues(t, 1, mutations.Load())
				require.True(t, associated)
			}
		})
	}
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

	handler.writeEditionCreateError(response, "profile", err, nil)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, "1", response.Header().Get("Retry-After"))
	require.Contains(t, response.Body.String(), "The request took too long to add this edition")
	require.Contains(t, response.Body.String(), "Nothing was added to Hardcover")
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
			require.Contains(t, response.Body.String(), "Nothing was added to Hardcover")
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

func postEditionImportCheck(t *testing.T, fixture *editionDraftTestFixture, user *auth.AuthUser, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/check-import", strings.NewReader(body))
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

func TestCreateEditionFromDraftRetainsCounterpartWhenClearingOneEbookISBN(t *testing.T) {
	tests := []struct {
		name       string
		sourceISBN string
		request    string
		wantISBN10 string
		wantISBN13 string
	}{
		{
			name: "clear ISBN-10 while retaining ISBN-13", sourceISBN: "9780306406157",
			request:    `{"isbn_10":"","isbn_13":"9780306406157"}`,
			wantISBN13: "9780306406157",
		},
		{
			name: "clear ISBN-13 while retaining ISBN-10", sourceISBN: "0306406152",
			request:    `{"isbn_10":"0306406152","isbn_13":""}`,
			wantISBN10: "0306406152",
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			itemJSON := fmt.Sprintf(`{"id":"abs-item-1","mediaType":"ebook","media":{
				"metadata":{"title":"Ebook","authorName":"Author","isbn":%q},"ebookFile":{},"ebookFormat":"epub"}}`, test.sourceISBN)
			fixture := newEditionDraftTestFixture(t, itemJSON, "us")
			configureEditionCreateRoute(t, fixture)
			runID := fmt.Sprintf("run-clear-isbn-%d", index)
			addCompletedNeedsReviewRun(t, fixture, runID, sync.BookOutcomeRecord{
				BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Ebook", Author: "Author",
				ISBN: test.sourceISBN, Format: "Ebook", HardcoverBookID: "42",
			})
			var counts editionCreateCallCounts
			stub := countingEditionCreateClient(&counts)
			fixture.handler.editionCreateHardcoverFactory = func(token string) editionCreateHardcoverClient {
				client := stub(token).(editionCreateHardcoverStub)
				create := client.createEbookFn
				client.createEbookFn = func(ctx context.Context, input *edition.EditionInput) (*edition.EditionResult, error) {
					require.Equal(t, test.wantISBN10, input.ISBN10)
					require.Equal(t, test.wantISBN13, input.ISBN13)
					return create(ctx, input)
				}
				return client
			}

			body := fmt.Sprintf(`{"run_id":%q,"abs_item_id":"abs-item-1",%s}`, runID, strings.TrimSuffix(strings.TrimPrefix(test.request, "{"), "}"))
			response := postEditionCreate(t, fixture, fixture.owner, body)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.EqualValues(t, 1, counts.ebookCreates.Load())
		})
	}
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
func newEditionCreateHardcoverServer(t *testing.T, status int, body string) (*hardcover.Client, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	client, server := hardcover.CreateTestClientWithHandler(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
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
	client, hardcoverRequests := newEditionCreateHardcoverServer(t, http.StatusOK, `{"data":{"insert_edition":{"id":999,"errors":[]}}}`)
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

func TestCreateEditionFromDraftRejectsAmbiguousSameNameAuthorFallback(t *testing.T) {
	fixture := newEditionCreateEbookFixture(t, "run-ambiguous-author", editionCreateCatalogClient{})
	var createCalls atomic.Int32
	fixture.handler.editionCreateHardcoverFactory = func(string) editionCreateHardcoverClient {
		return editionCreateHardcoverStub{
			bookFn: func(context.Context, string) (*models.HardcoverBook, error) {
				return &models.HardcoverBook{ID: "42", Authors: []models.Author{{ID: "invalid", Name: "Author"}}}, nil
			},
			authorsFn: func(_ context.Context, name string, limit int) ([]models.Author, error) {
				require.Equal(t, "Author", name)
				require.Equal(t, 10, limit)
				return []models.Author{
					{ID: "7", Name: "Author"},
					{ID: "7", Name: "Author"},
					{ID: "8", Name: "author"},
				}, nil
			},
			createEbookFn: func(context.Context, *edition.EditionInput) (*edition.EditionResult, error) {
				createCalls.Add(1)
				return nil, errors.New("ebook mutation must not run")
			},
		}
	}

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-ambiguous-author","abs_item_id":"abs-item-1"}`)

	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "multiple Hardcover authors named")
	require.Zero(t, createCalls.Load(), "ambiguous fallback must stop before the edition mutation")
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.False(t, exists)
}

func TestCreateEditionFromDraftMapsLocalStateReadFailureToInternalServerError(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"9780306406157"},
			"duration":100,"numTracks":1
		}}`, "us")
	configureEditionCreateRoute(t, fixture)
	statePath := editionCreateProfileStatePath(fixture)
	require.NoError(t, os.WriteFile(statePath, []byte("not valid state JSON"), 0600))

	request := httptest.NewRequest(http.MethodPost, "/api/profiles/draft-profile/edition-drafts/create", strings.NewReader(
		`{"run_id":"run-state-read-failure","abs_item_id":"abs-item-1"}`,
	))
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	fixture.routes.ServeHTTP(response, request)

	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Local profile or state data could not be prepared")
	require.Zero(t, fixture.absRequests.Load(), "local state failure must stop before Audiobookshelf or Hardcover calls")
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestCreateEditionFromDraftRefusesISBNOwnedByAnotherBook(t *testing.T) {
	client, hardcoverRequests := newEditionCreateHardcoverServer(t, http.StatusOK, `{"data":{"insert_edition":{"id":999,"errors":[]}}}`)
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
	// A limited live token received this pre-execution HTTP 403 for both writes.
	const denied = `{"error":"insufficient_scope","error_description":"Missing scopes: write:catalog:append","scope":"write:catalog:append"}`

	t.Run("ebook", func(t *testing.T) {
		client, hardcoverRequests := newEditionCreateHardcoverServer(t, http.StatusForbidden, denied)
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
		client, hardcoverRequests := newEditionCreateHardcoverServer(t, http.StatusForbidden, denied)
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

func newEditionResyncFixture(t *testing.T, runID string) (*editionDraftTestFixture, *editionCreateCallCounts) {
	t.Helper()
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"ebook","media":{
		"metadata":{"title":"Ebook","authorName":"Author","asin":"b0ebook123"},"ebookFile":{},"ebookFormat":"epub"}}`, "us")
	configureEditionCreateRoute(t, fixture)
	addCompletedNeedsReviewRun(t, fixture, runID, sync.BookOutcomeRecord{
		BookID: "abs-item-1", Outcome: sync.OutcomeNeedsReview, Title: "Ebook", Author: "Author", ASIN: "B0EBOOK123",
		Format: "Ebook", HardcoverBookID: "42",
	})
	counts := &editionCreateCallCounts{}
	fixture.handler.editionCreateHardcoverFactory = countingEditionCreateClient(counts)
	return fixture, counts
}

func TestCreateEditionFromDraftResyncIsOptIn(t *testing.T) {
	fixture, counts := newEditionResyncFixture(t, "run-resync-optin")
	var resyncs atomic.Int32
	fixture.handler.editionResyncRunner = func(context.Context, *database.ProfileWithTokens, models.AudiobookshelfBook, *statepkg.State, string) (sync.BookResyncResult, error) {
		resyncs.Add(1)
		return sync.BookResyncResult{Outcome: sync.OutcomeSynced}, nil
	}

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-resync-optin","abs_item_id":"abs-item-1"}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, counts.ebookCreates.Load())
	require.Zero(t, resyncs.Load())
	require.NotContains(t, response.Body.String(), `"resync"`)
}

func TestCreateEditionFromDraftResyncReportsOutcomeAfterAssociationIsSaved(t *testing.T) {
	fixture, _ := newEditionResyncFixture(t, "run-resync-ok")
	fixture.handler.editionResyncRunner = func(_ context.Context, _ *database.ProfileWithTokens, book models.AudiobookshelfBook, syncState *statepkg.State, statePath string) (sync.BookResyncResult, error) {
		require.Equal(t, "abs-item-1", book.ID)
		_, exists := syncState.GetAssociation("abs-item-1")
		require.True(t, exists, "resync must see the new association")
		wantPath, evalErr := filepath.EvalSymlinks(editionCreateProfileStatePath(fixture))
		require.NoError(t, evalErr)
		gotPath, evalErr := filepath.EvalSymlinks(statePath)
		require.NoError(t, evalErr)
		require.Equal(t, wantPath, gotPath)
		return sync.BookResyncResult{Outcome: sync.OutcomeSynced, Reason: "progress updated"}, nil
	}

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-resync-ok","abs_item_id":"abs-item-1","resync":true}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data editionCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotNil(t, body.Data.Resync)
	require.Equal(t, editionResyncResponse{Attempted: true, Outcome: "synced", Reason: "progress updated"}, *body.Data.Resync)
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
}

func TestCreateEditionFromDraftResyncFailureDoesNotFailCreate(t *testing.T) {
	fixture, counts := newEditionResyncFixture(t, "run-resync-fail")
	fixture.handler.editionResyncRunner = func(context.Context, *database.ProfileWithTokens, models.AudiobookshelfBook, *statepkg.State, string) (sync.BookResyncResult, error) {
		return sync.BookResyncResult{}, errors.New("abs unavailable")
	}

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-resync-fail","abs_item_id":"abs-item-1","resync":true}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 1, counts.ebookCreates.Load())
	var body struct {
		Data editionCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotNil(t, body.Data.Resync)
	require.True(t, body.Data.Resync.Attempted)
	require.Contains(t, body.Data.Resync.Error, "abs unavailable")
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists, "a failed resync must not undo the saved association")
}

func TestCreateEditionFromDraftDryRunNeverResyncs(t *testing.T) {
	fixture, counts := newEditionResyncFixture(t, "run-resync-dry")
	profile, err := fixture.multiUserService.GetProfile("draft-profile")
	require.NoError(t, err)
	profile.SyncConfig.DryRun = true
	require.NoError(t, fixture.multiUserService.UpdateProfileConfig(
		profile.Profile.ID, profile.AudiobookshelfURL, profile.AudiobookshelfToken, profile.HardcoverToken, profile.SyncConfig,
	))
	var resyncs atomic.Int32
	fixture.handler.editionResyncRunner = func(context.Context, *database.ProfileWithTokens, models.AudiobookshelfBook, *statepkg.State, string) (sync.BookResyncResult, error) {
		resyncs.Add(1)
		return sync.BookResyncResult{}, nil
	}

	response := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-resync-dry","abs_item_id":"abs-item-1","resync":true}`)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Zero(t, resyncs.Load())
	requireNoEditionCreateEffects(t, fixture, counts)
}

func TestCreateEditionFromDraftResyncUsesConcreteClients(t *testing.T) {
	const itemJSON = `{"id":"abs-item-1","mediaType":"book","media":{
		"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"9780306406157"},
		"duration":1000,"numTracks":1},"progress":{"currentTime":300}}`
	fixture := newEditionDraftTestFixture(t, itemJSON, "us")
	configureEditionCreateRoute(t, fixture)
	record := editionCreateRecord()
	record.Title = "Reviewed title"
	record.Author = "Author"
	addCompletedNeedsReviewRun(t, fixture, "run-resync-concrete", record)

	var itemRequests, progressRequests atomic.Int32
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer abs-token" {
			t.Errorf("unexpected Audiobookshelf request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/items/abs-item-1" && r.URL.Query().Get("expanded") == "1":
			itemRequests.Add(1)
			_, _ = w.Write([]byte(itemJSON))
		case r.URL.Path == "/api/me":
			progressRequests.Add(1)
			_, _ = w.Write([]byte(`{"id":"abs-user","mediaProgress":[],"listeningSessions":[]}`))
		default:
			t.Errorf("unexpected Audiobookshelf path: %s?%s", r.URL.Path, r.URL.RawQuery)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(absServer.Close)
	profile, err := fixture.multiUserService.GetProfile("draft-profile")
	require.NoError(t, err)
	require.NoError(t, fixture.multiUserService.UpdateProfileConfig(
		profile.Profile.ID, absServer.URL, profile.AudiobookshelfToken, profile.HardcoverToken, profile.SyncConfig,
	))

	type graphqlRequest struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}
	operationCounts := make(map[string]int)
	var editionRequest, userBookLookupRequest, userBookRequest, readsRequest, updateRequest graphqlRequest
	var captureMutex stdsync.Mutex
	hardcoverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer hardcover-token" {
			t.Errorf("unexpected Hardcover request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var request graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode Hardcover request: %v", err)
			http.Error(w, "invalid GraphQL request", http.StatusBadRequest)
			return
		}
		fields := strings.Fields(request.Query)
		operation := ""
		if len(fields) > 1 {
			operation = strings.SplitN(fields[1], "(", 2)[0]
		}
		captureMutex.Lock()
		operationCounts[operation]++
		captureMutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var response string
		switch operation {
		case "UpsertRegionalAudibleBook":
			response = `{"data":{"upsert_book":{"id":77,"status":"created","book":{"id":42},"edition":{"id":84,"book_id":42,"reading_format_id":2},"edition_id":84,"errors":[]}}}`
		case "RegionalAudibleImport":
			response = `{"data":{"book_import_statuses":[{"status":"created","book_id":42,"edition_id":84,"external_id":"B0SOURCE12:us","platform_id":32}],"book_mappings":[{"id":77,"state":"created","book_id":42,"platform_id":32,"external_id":"B0SOURCE12:us","edition_id":84,"edition":{"id":84,"book_id":42,"reading_format_id":2}}]}}`
		case "GetEdition":
			captureMutex.Lock()
			editionRequest = request
			captureMutex.Unlock()
			response = `{"data":{"editions":[{"id":84,"book_id":42,"title":"Reviewed title","reading_format_id":2}]}}`
		case "GetCurrentUserID":
			response = `{"data":{"me":[{"id":7}]}}`
		case "GetUserBookByBookOnly", "GetUserBookByBook":
			captureMutex.Lock()
			userBookLookupRequest = request
			captureMutex.Unlock()
			response = `{"data":{"user_books":[{"id":300,"book_id":42,"edition_id":84}]}}`
		case "GetUserBook":
			captureMutex.Lock()
			userBookRequest = request
			captureMutex.Unlock()
			response = `{"data":{"user_books":[{"id":300,"book_id":42,"status_id":2,"book":{"id":42,"title":"Reviewed title"},"edition_id":84,"edition":{"id":84,"asin":"B0SOURCE12","book_mappings":[]}}]}}`
		case "GetUserBookReadsAll":
			captureMutex.Lock()
			readsRequest = request
			captureMutex.Unlock()
			response = `{"data":{"user_book_reads":[{"id":400,"user_book_id":300,"progress":10,"progress_seconds":100,"started_at":"2025-09-01","finished_at":null,"edition_id":84}]}}`
		case "UpdateUserBookRead":
			captureMutex.Lock()
			updateRequest = request
			captureMutex.Unlock()
			response = `{"data":{"update_user_book_read":{"id":400,"error":null,"user_book_read":{"id":400,"progress_seconds":300,"started_at":"2025-09-01","finished_at":null}}}}`
		default:
			t.Errorf("unexpected Hardcover GraphQL operation %q", operation)
			http.Error(w, "unexpected GraphQL operation", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(hardcoverServer.Close)
	fixture.config.Hardcover.BaseURL = hardcoverServer.URL

	response := postEditionCreate(t, fixture, fixture.owner,
		`{"run_id":"run-resync-concrete","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:us","resync":true}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data editionCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "created", body.Data.Status)
	require.NotNil(t, body.Data.Resync)
	require.True(t, body.Data.Resync.Attempted)
	require.Equal(t, "synced", body.Data.Resync.Outcome)
	require.Empty(t, body.Data.Resync.Error)
	require.EqualValues(t, 1, itemRequests.Load())
	require.EqualValues(t, 1, progressRequests.Load())
	captureMutex.Lock()
	upsertCalls := operationCounts["UpsertRegionalAudibleBook"]
	progressWriteCalls := operationCounts["UpdateUserBookRead"]
	verifiedEditionReadCalls := operationCounts["GetEdition"]
	userBookSnapshotReadCalls := operationCounts["GetUserBook"]
	queriedEdition := editionRequest
	lookedUpUserBook := userBookLookupRequest
	loadedUserBook := userBookRequest
	loadedReads := readsRequest
	updatedRead := updateRequest
	captureMutex.Unlock()
	require.EqualValues(t, 1, upsertCalls)
	require.EqualValues(t, 1, progressWriteCalls)
	require.EqualValues(t, 1, verifiedEditionReadCalls, "resync should reuse the edition freshly verified during creation")
	require.EqualValues(t, 1, userBookSnapshotReadCalls, "resync should reuse the fetched user-book snapshot")
	require.EqualValues(t, 84, queriedEdition.Variables["editionId"], "creation should verify the returned edition")
	require.EqualValues(t, 42, lookedUpUserBook.Variables["bookId"])
	require.EqualValues(t, 300, loadedUserBook.Variables["id"])
	require.EqualValues(t, 300, loadedReads.Variables["user_book_id"])
	require.NotEmpty(t, updatedRead.Query)
	require.EqualValues(t, 400, updatedRead.Variables["id"], "resync should update the existing read")
	updateObject, ok := updatedRead.Variables["object"].(map[string]interface{})
	require.True(t, ok)
	require.EqualValues(t, 300, updateObject["progress_seconds"])
	require.NotContains(t, updateObject, "edition_id", "resync must retain the existing read's edition")

	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "42", association.HardcoverBookID)
	require.Equal(t, "84", association.HardcoverEditionID)
	require.Equal(t, "audiobook", association.ReadingFormat)
	bookState, exists := stored.Books["abs-item-1:84"]
	require.True(t, exists, "successful resync should checkpoint progress under the associated edition")
	require.InDelta(t, 0.3, bookState.LastProgress, 0.000001)
	require.True(t, bookState.HasProgressSeconds)
}

func TestCheckEditionImportRecoveryWithConcreteClients(t *testing.T) {
	const itemJSON = `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"Reviewed title","authorName":"Author","asin":"B0SOURCE12","isbn":"9780306406157"},"duration":100,"numTracks":1}}`
	fixture := newEditionDraftTestFixture(t, itemJSON, "us")
	configureEditionCreateRoute(t, fixture)
	record := editionCreateRecord()
	record.Title = "Reviewed title"
	record.Author = "Author"
	addCompletedNeedsReviewRun(t, fixture, "run-concrete-recovery", record)

	type graphqlRequest struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}
	var upserts, statusReads, editionReads, mutations atomic.Int32
	var status string
	hardcoverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer hardcover-token" {
			t.Errorf("unexpected Hardcover request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var request graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode Hardcover request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		fields := strings.Fields(request.Query)
		operation := ""
		if len(fields) > 1 {
			operation = strings.SplitN(fields[1], "(", 2)[0]
		}
		w.Header().Set("Content-Type", "application/json")
		var response string
		switch operation {
		case "UpsertRegionalAudibleBook":
			upserts.Add(1)
			mutations.Add(1)
			response = `{"data":{"upsert_book":{"id":77,"status":"fetching","book":{"id":42},"errors":[]}}}`
		case "RegionalAudibleImport":
			statusReads.Add(1)
			if status == "ambiguous" {
				response = `{"errors":[{"message":"temporary status lookup failure"}]}`
			} else if status == "fetching" {
				response = `{"data":{"book_import_statuses":[{"status":"fetching","external_id":"B0SOURCE12:uk","platform_id":32}],"book_mappings":[]}}`
			} else {
				response = `{"data":{"book_import_statuses":[{"status":"created","book_id":42,"edition_id":84,"external_id":"B0SOURCE12:uk","platform_id":32}],"book_mappings":[{"id":77,"state":"normalized","book_id":42,"platform_id":32,"external_id":"B0SOURCE12:uk","edition_id":84,"edition":{"id":84,"book_id":42,"reading_format_id":2}}]}}`
			}
		case "GetEdition":
			editionReads.Add(1)
			response = `{"data":{"editions":[{"id":84,"book_id":42,"title":"Reviewed title","reading_format_id":2}]}}`
		default:
			t.Errorf("unexpected Hardcover GraphQL operation %q", operation)
			http.Error(w, "unexpected operation", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(hardcoverServer.Close)
	fixture.config.Hardcover.BaseURL = hardcoverServer.URL
	status = "ambiguous"

	created := postEditionCreate(t, fixture, fixture.owner, `{"run_id":"run-concrete-recovery","abs_item_id":"abs-item-1","audible_identifier":"B0SOURCE12:uk"}`)
	require.Equal(t, http.StatusBadGateway, created.Code, created.Body.String())
	var createEnvelope struct {
		ErrorCode string `json:"error_code"`
		Outcome   string `json:"outcome"`
		Data      struct {
			AudibleIdentifier string `json:"audible_identifier"`
			HardcoverBookID   string `json:"hardcover_book_id"`
			RecoveryToken     string `json:"recovery_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createEnvelope))
	require.Equal(t, "hardcover_import_unconfirmed", createEnvelope.ErrorCode)
	require.Equal(t, editionOutcomeUnconfirmed, createEnvelope.Outcome)
	require.Equal(t, "B0SOURCE12:uk", createEnvelope.Data.AudibleIdentifier)
	require.Equal(t, "42", createEnvelope.Data.HardcoverBookID)
	require.NotEmpty(t, createEnvelope.Data.RecoveryToken)
	stored, err := statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists := stored.GetAssociation("abs-item-1")
	require.False(t, exists)

	body := fmt.Sprintf(`{"run_id":"run-concrete-recovery","abs_item_id":"abs-item-1","audible_identifier":%q,"recovery_token":%q}`,
		createEnvelope.Data.AudibleIdentifier, createEnvelope.Data.RecoveryToken)
	statusReadsBeforeChecks := statusReads.Load()
	status = "fetching"
	pending := postEditionImportCheck(t, fixture, fixture.owner, body)
	require.Equal(t, http.StatusServiceUnavailable, pending.Code, pending.Body.String())
	var pendingEnvelope struct {
		ErrorCode string `json:"error_code"`
		Outcome   string `json:"outcome"`
		Data      struct {
			RecoveryToken string `json:"recovery_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(pending.Body.Bytes(), &pendingEnvelope))
	require.Equal(t, "hardcover_import_unconfirmed", pendingEnvelope.ErrorCode)
	require.Equal(t, editionOutcomeUnconfirmed, pendingEnvelope.Outcome)
	require.Equal(t, createEnvelope.Data.RecoveryToken, pendingEnvelope.Data.RecoveryToken)
	stored, err = statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	_, exists = stored.GetAssociation("abs-item-1")
	require.False(t, exists, "pending status must not save a guessed local association")

	status = "created"
	first := postEditionImportCheck(t, fixture, fixture.owner, body)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	second := postEditionImportCheck(t, fixture, fixture.owner, body)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.EqualValues(t, 1, upserts.Load(), "check-import must never submit another upsert")
	require.EqualValues(t, 1, mutations.Load(), "all checks remain read-only at the Hardcover boundary")
	require.EqualValues(t, 3, statusReads.Load()-statusReadsBeforeChecks, "each check performs one bounded status query")
	require.EqualValues(t, 2, editionReads.Load(), "each terminal check freshly verifies the edition")
	stored, err = statepkg.LoadState(editionCreateProfileStatePath(fixture))
	require.NoError(t, err)
	association, exists := stored.GetAssociation("abs-item-1")
	require.True(t, exists)
	require.Equal(t, "42", association.HardcoverBookID)
	require.Equal(t, "84", association.HardcoverEditionID)
	require.Equal(t, "B0SOURCE12:uk", association.RegionalExternalID)
}

func TestWriteEditionCreateErrorExplainsDailyQuotaBudgetWithoutRetryAfter(t *testing.T) {
	tests := []struct {
		name        string
		budgetCause error
		quotaCause  error
		wantMessage string
	}{
		{
			name:        "low quota in audiobook wrapper",
			budgetCause: errEditionCreateInsufficientBudget,
			quotaCause:  hardcover.ErrMutationDailyQuotaLow,
			wantMessage: "Hardcover's daily API quota is running low. Nothing was added to Hardcover. Please wait until Hardcover's daily API quota resets, then try again.",
		},
		{
			name:        "low quota in ebook wrapper",
			budgetCause: errors.Join(edition.ErrCreateEditionPreMutation, edition.ErrCreateEditionInsufficientMutationBudget),
			quotaCause:  hardcover.ErrMutationDailyQuotaLow,
			wantMessage: "Hardcover's daily API quota is running low. Nothing was added to Hardcover. Please wait until Hardcover's daily API quota resets, then try again.",
		},
		{
			name:        "exhausted quota in ebook wrapper",
			budgetCause: errors.Join(edition.ErrCreateEditionPreMutation, edition.ErrCreateEditionInsufficientMutationBudget),
			quotaCause:  hardcover.ErrMutationDailyQuotaExhausted,
			wantMessage: "Hardcover's daily API quota is exhausted. Nothing was added to Hardcover. Please wait until Hardcover's daily API quota resets, then try again.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &Handler{}
			response := httptest.NewRecorder()

			err := errors.Join(test.budgetCause, hardcover.ErrMutationInsufficientBudget, test.quotaCause)
			handler.writeEditionCreateError(response, "profile", err, nil)

			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.Empty(t, response.Header().Get("Retry-After"), "the server does not know the quota reset time")
			var payload struct {
				Error     string `json:"error"`
				ErrorCode string `json:"error_code"`
				Outcome   string `json:"outcome"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
			require.Equal(t, test.wantMessage, payload.Error)
			require.Equal(t, "edition_create_not_submitted", payload.ErrorCode)
			require.Equal(t, editionOutcomeNotSubmitted, payload.Outcome)
			require.NotContains(t, payload.Error, "may have processed")
		})
	}
}
