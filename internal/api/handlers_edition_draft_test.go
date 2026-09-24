package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/stretchr/testify/require"
)

const editionDraftItemPath = "/api/profiles/draft-profile/edition-drafts/source/abs-item-1"

type editionDraftTestFixture struct {
	routes            http.Handler
	handler           *Handler
	authService       *auth.AuthService
	db                *database.Database
	owner             *auth.AuthUser
	absRequests       *atomic.Int32
	hardcoverRequests *atomic.Int32
}

func newEditionDraftTestFixture(t *testing.T, itemJSON, preferredRegion string) *editionDraftTestFixture {
	return newEditionDraftTestFixtureWithABSDelay(t, itemJSON, preferredRegion, 0)
}

func newEditionDraftTestFixtureWithABSDelay(t *testing.T, itemJSON, preferredRegion string, absDelay time.Duration) *editionDraftTestFixture {
	t.Helper()
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})
	dataDir := t.TempDir()
	absRequests := &atomic.Int32{}
	hardcoverRequests := &atomic.Int32{}
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		absRequests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/items/abs-item-1" || r.URL.Query().Get("expanded") != "1" || r.Header.Get("Authorization") != "Bearer abs-token" {
			t.Errorf("unexpected Audiobookshelf request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if absDelay > 0 {
			timer := time.NewTimer(absDelay)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(itemJSON))
	}))
	t.Cleanup(absServer.Close)
	hardcoverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hardcoverRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hardcoverServer.Close)

	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "edition-draft.db"),
	}, logger.Get())
	require.NoError(t, err)
	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())
	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	cfg.Hardcover.BaseURL = hardcoverServer.URL
	cfg.RateLimit.Rate = time.Nanosecond
	cfg.RateLimit.MaxConcurrent = 1
	multiUserService := multiuser.NewMultiUserService(repo, cfg, logger.Get())

	authConfig := auth.DefaultAuthConfig()
	authConfig.Enabled = true
	authService, err := auth.NewAuthService(db.GetDB(), authConfig, logger.Get())
	require.NoError(t, err)
	owner, err := authService.CreateUser(context.Background(), "draft-owner", "draft-owner@example.invalid", "password", auth.RoleUser, "local")
	require.NoError(t, err)
	const profileID = "draft-profile"
	require.NoError(t, multiUserService.CreateProfileForUser(
		profileID, "Draft profile", absServer.URL, "abs-token", "hardcover-token",
		database.SyncConfigData{DryRun: true, AudnexusRegion: preferredRegion}, owner.ID,
	))

	handler := NewHandler(multiUserService, logger.Get())
	handler.SetAuthEnabled(true)
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/profiles/{id}/edition-drafts/source/{itemID}", handler.GetEditionSourceDraft)
	routes := auth.NewAuthMiddleware(authService.GetSessionManager(), authConfig).RequireAuth(apiMux)
	t.Cleanup(func() {
		require.NoError(t, multiUserService.Shutdown(context.Background()))
		require.NoError(t, db.Close())
	})
	return &editionDraftTestFixture{
		routes: routes, handler: handler, authService: authService, owner: owner,
		db: db, absRequests: absRequests, hardcoverRequests: hardcoverRequests,
	}
}

func (f *editionDraftTestFixture) sessionCookie(t *testing.T, user *auth.AuthUser) *http.Cookie {
	t.Helper()
	session, err := f.authService.GetSessionManager().CreateSession(
		context.Background(), user.ID, httptest.NewRequest(http.MethodGet, "/", nil),
	)
	require.NoError(t, err)
	return &http.Cookie{Name: "abs-hc-session", Value: session.Token}
}

func (f *editionDraftTestFixture) request(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	f.routes.ServeHTTP(response, req)
	return response
}

func (f *editionDraftTestFixture) requestWithoutAuth(path string) *httptest.ResponseRecorder {
	f.handler.SetAuthEnabled(false)
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/profiles/{id}/edition-drafts/source/{itemID}", f.handler.GetEditionSourceDraft)
	response := httptest.NewRecorder()
	apiMux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}

type editionDraftDiscoveryFunc func(context.Context, string, string) (*audnex.Book, string, error)

type editionDraftDiscoveryStub struct {
	discover editionDraftDiscoveryFunc
}

func (s editionDraftDiscoveryStub) DiscoverBookByASIN(ctx context.Context, asin, region string) (*audnex.Book, string, error) {
	return s.discover(ctx, asin, region)
}

func (f *editionDraftTestFixture) setDiscovery(discovery editionDraftDiscoveryFunc) {
	f.handler.editionDraftAudnexClientFactory = func() editionDraftAudnexDiscoverer {
		return editionDraftDiscoveryStub{discover: discovery}
	}
}

func TestGetEditionSourceDraftKeepsBareASINAndUsesDiscoveredRegionDate(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"ABS Title","subtitle":"ABS Subtitle","authorName":"ABS Author","narratorName":"ABS Narrator","publisher":"Publisher","description":"ABS description","publishedDate":"2020-02-03","publishedYear":"2021","isbn":"978-0-306-40615-7","asin":" B0SOURCE123 ","language":"English","abridged":true},
			"duration":3660.6,"numTracks":1
		}}`, "UK")
	fixture.setDiscovery(func(_ context.Context, sourceASIN, preferred string) (*audnex.Book, string, error) {
		require.Equal(t, "B0SOURCE123", sourceASIN)
		require.Equal(t, "uk", preferred)
		return &audnex.Book{ASIN: sourceASIN, Title: "Different Audnex title", ReleaseDate: "2023-08-09"}, "ca", nil
	})

	response := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Success bool                 `json:"success"`
		Data    editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	draft := envelope.Data
	require.Equal(t, "audiobook", draft.ReadingFormat)
	require.True(t, draft.DryRun)
	require.True(t, draft.Eligible)
	require.Equal(t, "B0SOURCE123", draft.SourceIdentifiers.ASIN)
	require.Equal(t, "978-0-306-40615-7", draft.SourceIdentifiers.ISBN)
	require.Equal(t, "confirmed", draft.RegionStatus)
	require.Equal(t, "ca", draft.ConfirmedRegion)
	require.Equal(t, "B0SOURCE123", draft.AudibleIdentifierCandidate.ASIN)
	require.Equal(t, "ca", draft.AudibleIdentifierCandidate.Region)
	require.True(t, draft.AudibleIdentifierCandidate.CorrectionAllowed)
	require.NotNil(t, draft.MetadataPreview)
	require.Nil(t, draft.EbookCandidate)
	require.Equal(t, "ABS Title", draft.MetadataPreview.Title)
	require.Equal(t, "ABS Author", draft.MetadataPreview.Author)
	require.Equal(t, "ABS Narrator", draft.MetadataPreview.Narrator)
	require.Equal(t, "2023-08-09", draft.MetadataPreview.ReleaseDate)
	require.Equal(t, "Abridged", draft.MetadataPreview.EditionInformation)
	require.Equal(t, 3661, draft.MetadataPreview.AudioSeconds)
	require.Equal(t, "0306406152", draft.MetadataPreview.ISBN10)
	var previewEnvelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &previewEnvelope))
	var preview map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(previewEnvelope.Data["metadata_preview"], &preview))
	require.NotContains(t, preview, "asin")
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestGetEditionSourceDraftReportsRetryableAudnexWarningAndABSDateFallback(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "rate limited", err: audnex.ErrRateLimited},
		{name: "transient failure", err: audnex.ErrTransient},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, `{
				"id":"abs-item-1","mediaType":"book","media":{
					"metadata":{"title":"Fallback","authorName":"Author","publishedYear":"2008","asin":"B0SOURCE123","language":"Spanish"},
					"duration":3600,"numTracks":1
				}}`, "br")
			fixture.setDiscovery(func(_ context.Context, sourceASIN, preferred string) (*audnex.Book, string, error) {
				require.Equal(t, "B0SOURCE123", sourceASIN)
				require.Equal(t, "us", preferred)
				return nil, "", test.err
			})

			response := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var envelope struct {
				Data editionDraftResponse `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			draft := envelope.Data
			require.Equal(t, "temporarily_unavailable", draft.RegionStatus)
			require.Equal(t, "2008-01-01", draft.MetadataPreview.ReleaseDate)
			require.Equal(t, "B0SOURCE123", draft.AudibleIdentifierCandidate.ASIN)
			require.Empty(t, draft.AudibleIdentifierCandidate.Region)
			warning := editionDraftWarningByCode(draft, "audnex_temporarily_unavailable")
			require.NotNil(t, warning)
			require.True(t, warning.Retryable)
			require.True(t, hasEditionDraftWarning(draft, "language_defaults_to_english"))
			require.Zero(t, fixture.hardcoverRequests.Load())
		})
	}
}

func TestGetEditionSourceDraftReportsUnknownRegionWithoutGuessing(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Unknown region","authorName":"Author","publishedDate":"2020-04-05","asin":"B0SOURCE123","language":"English"},
			"duration":100,"numTracks":1
		}}`, "uk")
	fixture.setDiscovery(func(_ context.Context, sourceASIN, preferred string) (*audnex.Book, string, error) {
		require.Equal(t, "B0SOURCE123", sourceASIN)
		require.Equal(t, "uk", preferred)
		return nil, "", nil
	})

	response := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, "unknown", envelope.Data.RegionStatus)
	require.Empty(t, envelope.Data.ConfirmedRegion)
	require.Empty(t, envelope.Data.AudibleIdentifierCandidate.Region)
	require.Equal(t, "2020-04-05", envelope.Data.MetadataPreview.ReleaseDate)
	require.True(t, envelope.Data.Eligible)
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestGetEditionSourceDraftBuildsEbookCandidateWithoutAudnex(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"ebook","media":{
			"metadata":{"title":"Ebook","subtitle":"Subtitle","authorName":"Writer","publishedDate":"2024-02-03","isbn":"0306406152","language":"English"},
			"ebookFile":{},"ebookFormat":"epub"
		}}`, "us")
	var discoveryCalls atomic.Int32
	fixture.setDiscovery(func(context.Context, string, string) (*audnex.Book, string, error) {
		discoveryCalls.Add(1)
		return nil, "", nil
	})

	response := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	draft := envelope.Data
	require.Equal(t, "ebook", draft.ReadingFormat)
	require.True(t, draft.Eligible)
	require.Nil(t, draft.MetadataPreview)
	require.Nil(t, draft.AudibleIdentifierCandidate)
	require.Equal(t, "2024-02-03", draft.EbookCandidate.ReleaseDate)
	require.Equal(t, "Ebook", draft.EbookCandidate.EditionFormat)
	require.Equal(t, "Ebook", draft.EbookCandidate.Title)
	require.Equal(t, "0306406152", draft.EbookCandidate.ISBN10)
	require.Equal(t, "9780306406157", draft.EbookCandidate.ISBN13)
	require.NotNil(t, draft.EbookCandidate.ISBN10Valid)
	require.True(t, *draft.EbookCandidate.ISBN10Valid)
	require.NotNil(t, draft.EbookCandidate.ISBN13Valid)
	require.True(t, *draft.EbookCandidate.ISBN13Valid)
	require.Empty(t, draft.EbookCandidate.CorrectedISBN)
	require.Zero(t, discoveryCalls.Load())
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestGetEditionSourceDraftKeepsBadChecksumISBNWithWarning(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"ebook","media":{
			"metadata":{"title":"Ebook","authorName":"Writer","isbn":"9780306406158","language":"English"},
			"ebookFile":{},"ebookFormat":"epub"
		}}`, "us")
	response := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	draft := envelope.Data
	require.True(t, draft.Eligible)
	require.Equal(t, "9780306406158", draft.EbookCandidate.ISBN13)
	require.NotNil(t, draft.EbookCandidate.ISBN13Valid)
	require.False(t, *draft.EbookCandidate.ISBN13Valid)
	require.True(t, hasEditionDraftWarning(draft, "invalid_isbn"))
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestGetEditionSourceDraftMarksItemsWithoutASINOrISBNIneligible(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"No identifiers","authorName":"Writer","publishedDate":"Unknown date"},
			"duration":10,"numTracks":1
		}}`, "us")
	var discoveryCalls atomic.Int32
	fixture.setDiscovery(func(context.Context, string, string) (*audnex.Book, string, error) {
		discoveryCalls.Add(1)
		return nil, "", nil
	})

	response := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, fixture.owner))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.False(t, envelope.Data.Eligible)
	require.Contains(t, envelope.Data.IneligibleReason, "ASIN or ISBN")
	require.True(t, hasEditionDraftWarning(envelope.Data, "missing_identifier"))
	require.True(t, hasEditionDraftWarning(envelope.Data, "date_unavailable"))
	require.True(t, hasEditionDraftWarning(envelope.Data, "published_date_unrecognized"))
	require.Equal(t, "not_applicable", envelope.Data.RegionStatus)
	require.Empty(t, envelope.Data.AudibleIdentifierCandidate.ASIN)
	require.True(t, envelope.Data.AudibleIdentifierCandidate.CorrectionAllowed)
	require.Zero(t, discoveryCalls.Load())
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestGetEditionSourceDraftRequiresAuthenticationAndProfileOwnership(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"Book"},"duration":1}}`, "us")
	unauthenticated := fixture.request(editionDraftItemPath, nil)
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())

	foreignUser, err := fixture.authService.CreateUser(context.Background(), "foreign-user", "foreign@example.invalid", "password", auth.RoleUser, "local")
	require.NoError(t, err)
	foreign := fixture.request(editionDraftItemPath, fixture.sessionCookie(t, foreignUser))
	require.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
	require.Zero(t, fixture.absRequests.Load())
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestGetEditionSourceDraftDistinguishesMissingProfileFromRepositoryError(t *testing.T) {
	t.Run("missing profile returns not found when auth is disabled", func(t *testing.T) {
		fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"Book"},"duration":1}}`, "us")
		response := fixture.requestWithoutAuth("/api/profiles/missing-profile/edition-drafts/source/abs-item-1")

		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		var envelope APIResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require.False(t, envelope.Success)
		require.Equal(t, "Sync profile not found", envelope.Error)
		require.Zero(t, fixture.absRequests.Load())
	})

	t.Run("repository error remains internal server error", func(t *testing.T) {
		fixture := newEditionDraftTestFixture(t, `{"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"Book"},"duration":1}}`, "us")
		require.NoError(t, fixture.db.GetDB().Exec("ALTER TABLE sync_profiles RENAME TO unavailable_sync_profiles").Error)
		response := fixture.requestWithoutAuth(editionDraftItemPath)

		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		var envelope APIResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require.False(t, envelope.Success)
		require.Equal(t, "Failed to retrieve sync profile", envelope.Error)
		require.Zero(t, fixture.absRequests.Load())
	})
}

func TestGetEditionSourceDraftStopsOnRequestCancellation(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{"metadata":{"title":"Book","asin":"B0SOURCE123"},"duration":1,"numTracks":1}
	}`, "us")
	started := make(chan struct{})
	fixture.setDiscovery(func(ctx context.Context, _ string, _ string) (*audnex.Book, string, error) {
		close(started)
		<-ctx.Done()
		return nil, "", ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, editionDraftItemPath, nil).WithContext(ctx)
	req.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		fixture.routes.ServeHTTP(response, req)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("region discovery did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not stop after cancellation")
	}
	require.Empty(t, strings.TrimSpace(response.Body.String()))
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestGetEditionSourceDraftFinishesBeforeHTTPWriteTimeout(t *testing.T) {
	fixture := newEditionDraftTestFixtureWithABSDelay(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Slow ABS","authorName":"Author","publishedYear":"2020","asin":"B0SOURCE123"},
			"duration":10,"numTracks":1
		}}`, "us", 60*time.Millisecond)
	fixture.handler.editionDraftRequestTimeout = 120 * time.Millisecond
	fixture.setDiscovery(func(ctx context.Context, _, _ string) (*audnex.Book, string, error) {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			return nil, "", audnex.ErrTransient
		}
		return nil, "", ctx.Err()
	})

	server := httptest.NewUnstartedServer(fixture.routes)
	server.Config.WriteTimeout = 300 * time.Millisecond
	server.Start()
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 2 * time.Second}
	request, err := http.NewRequest(http.MethodGet, server.URL+editionDraftItemPath, nil)
	require.NoError(t, err)
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response, err := client.Do(request)
	require.NoError(t, err, "the draft must return before the server write deadline")
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var envelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.Equal(t, "temporarily_unavailable", envelope.Data.RegionStatus)
	warning := editionDraftWarningByCode(envelope.Data, "audnex_temporarily_unavailable")
	require.NotNil(t, warning)
	require.True(t, warning.Retryable)
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func TestGetEditionSourceDraftMapsAudnex408ToRetryableWarning(t *testing.T) {
	fixture := newEditionDraftTestFixture(t, `{
		"id":"abs-item-1","mediaType":"book","media":{
			"metadata":{"title":"Audnex timeout","authorName":"Author","asin":"B0SOURCE123"},
			"duration":10,"numTracks":1
		}}`, "us")
	fixture.handler.editionDraftRequestTimeout = 700 * time.Millisecond
	audnexRequests := &atomic.Int32{}
	audnexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		audnexRequests.Add(1)
		w.WriteHeader(http.StatusRequestTimeout)
	}))
	t.Cleanup(audnexServer.Close)
	fixture.handler.editionDraftAudnexClientFactory = func() editionDraftAudnexDiscoverer {
		return audnex.NewClientForTesting(audnexServer.URL, logger.Get())
	}

	server := httptest.NewUnstartedServer(fixture.routes)
	server.Config.WriteTimeout = 2 * time.Second
	server.Start()
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 3 * time.Second}
	request, err := http.NewRequest(http.MethodGet, server.URL+editionDraftItemPath, nil)
	require.NoError(t, err)
	request.AddCookie(fixture.sessionCookie(t, fixture.owner))
	response, err := client.Do(request)
	require.NoError(t, err, "an Audnex 408 should produce a completed retryable draft response")
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var envelope struct {
		Data editionDraftResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.Equal(t, "temporarily_unavailable", envelope.Data.RegionStatus)
	warning := editionDraftWarningByCode(envelope.Data, "audnex_temporarily_unavailable")
	require.NotNil(t, warning)
	require.True(t, warning.Retryable)
	require.Positive(t, audnexRequests.Load())
	require.Zero(t, fixture.hardcoverRequests.Load())
}

func hasEditionDraftWarning(draft editionDraftResponse, code string) bool {
	return editionDraftWarningByCode(draft, code) != nil
}

func editionDraftWarningByCode(draft editionDraftResponse, code string) *editionDraftWarning {
	for _, warning := range draft.Warnings {
		if warning.Code == code {
			return &warning
		}
	}
	return nil
}
