package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/stretchr/testify/require"
)

type routeSession struct {
	user   *auth.AuthUser
	cookie *http.Cookie
}

func newRouteSession(t *testing.T, fixture *routeTestFixture, username string, role auth.UserRole) routeSession {
	t.Helper()
	user, err := fixture.server.authService.CreateUser(
		context.Background(), username, username+"@example.invalid", "password", role, "local",
	)
	require.NoError(t, err)
	session, err := fixture.server.authService.GetSessionManager().CreateSession(
		context.Background(), user.ID, httptest.NewRequest(http.MethodGet, "/", nil),
	)
	require.NoError(t, err)
	return routeSession{
		user:   user,
		cookie: &http.Cookie{Name: "abs-hc-session", Value: session.Token},
	}
}

func createRouteProfile(t *testing.T, fixture *routeTestFixture, session routeSession, id, token string) {
	createRouteProfileAtURL(t, fixture, session, id, "http://audiobookshelf.invalid", token)
}

func createRouteProfileAtURL(t *testing.T, fixture *routeTestFixture, session routeSession, id, audiobookshelfURL, token string) {
	t.Helper()
	response := fixture.requestWithCookies(http.MethodPost, "/api/profiles", []byte(`{
        "id": "`+id+`",
        "name": "`+id+`",
        "audiobookshelf_url": "`+audiobookshelfURL+`",
        "audiobookshelf_token": "`+token+`",
        "hardcover_token": "hardcover-`+token+`",
        "sync_config": {"process_unread_books": true, "dry_run": true}
    }`), []*http.Cookie{session.cookie})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func newRouteTestFixtureWithHardcoverURL(t *testing.T, hardcoverURL string) *routeTestFixture {
	t.Helper()
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})

	dataDir := t.TempDir()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "server-route-test.db"),
	}, logger.Get())
	require.NoError(t, err)

	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())
	cfg := config.DefaultConfig()
	cfg.Paths.DataDir = dataDir
	cfg.Hardcover.BaseURL = hardcoverURL
	cfg.RateLimit.Rate = time.Nanosecond
	cfg.RateLimit.MaxConcurrent = 1
	multiUserService := multiuser.NewMultiUserService(repo, cfg, logger.Get())

	authConfig := auth.DefaultAuthConfig()
	authConfig.Enabled = true
	authService, err := auth.NewAuthService(db.GetDB(), authConfig, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() {
		multiUserService.WaitForSyncs()
		require.NoError(t, db.Close())
	})

	return &routeTestFixture{
		dataDir: dataDir,
		repo:    repo,
		server:  New("", multiUserService, authService, logger.Get()),
	}
}

func TestProfileAuthorizationOwnershipAndAdminOverride(t *testing.T) {
	fixture := newRouteTestFixture(t, true)
	ownerA := newRouteSession(t, fixture, "owner-a", auth.RoleUser)
	ownerB := newRouteSession(t, fixture, "owner-b", auth.RoleUser)
	adminResponse := fixture.request(
		http.MethodPost,
		"/api/auth/login",
		[]byte(`{"provider":"local","credentials":{"username":"admin","password":"admin"}}`),
	)
	require.Equal(t, http.StatusOK, adminResponse.Code, adminResponse.Body.String())
	admin := routeSession{cookie: adminResponse.Result().Cookies()[0]}

	createRouteProfile(t, fixture, ownerA, "owned-a", "owner-a-sentinel-token")
	createRouteProfile(t, fixture, ownerB, "owned-b", "owner-b-sentinel-token")
	require.NoError(t, fixture.repo.CreateProfile(
		"legacy-ownerless", "Legacy", "http://audiobookshelf.invalid", "legacy-token", "legacy-hc-token", structSyncConfig(),
	))
	metadata, err := fixture.repo.GetProfileMetadata("owned-a")
	require.NoError(t, err)
	require.NotNil(t, metadata)
	require.NotNil(t, metadata.OwnerUserID)
	require.Equal(t, ownerA.user.ID, *metadata.OwnerUserID)

	for _, test := range []struct {
		name       string
		session    routeSession
		wantIDs    []string
		absentBody []string
	}{
		{name: "owner A", session: ownerA, wantIDs: []string{"owned-a"}, absentBody: []string{"owned-b", "legacy-ownerless"}},
		{name: "owner B", session: ownerB, wantIDs: []string{"owned-b"}, absentBody: []string{"owned-a", "legacy-ownerless"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.requestWithCookies(http.MethodGet, "/api/profiles", nil, []*http.Cookie{test.session.cookie})
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var payload struct {
				Data []map[string]interface{} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
			require.Len(t, payload.Data, len(test.wantIDs))
			for _, id := range test.wantIDs {
				require.Contains(t, response.Body.String(), id)
			}
			for _, id := range test.absentBody {
				require.NotContains(t, response.Body.String(), id)
			}
			require.NotContains(t, response.Body.String(), "owner_user_id")
		})
	}

	ownerResponse := fixture.requestWithCookies(http.MethodGet, "/api/profiles/owned-a", nil, []*http.Cookie{ownerA.cookie})
	require.Equal(t, http.StatusOK, ownerResponse.Code, ownerResponse.Body.String())
	require.Contains(t, ownerResponse.Body.String(), "owner-a-sentinel-token")

	foreignResponse := fixture.requestWithCookies(http.MethodGet, "/api/profiles/owned-b", nil, []*http.Cookie{ownerA.cookie})
	require.Equal(t, http.StatusNotFound, foreignResponse.Code, foreignResponse.Body.String())
	require.NotContains(t, foreignResponse.Body.String(), "owner-b-sentinel-token")

	legacyResponse := fixture.requestWithCookies(http.MethodGet, "/api/profiles/legacy-ownerless", nil, []*http.Cookie{ownerA.cookie})
	require.Equal(t, http.StatusNotFound, legacyResponse.Code, legacyResponse.Body.String())
	adminLegacyResponse := fixture.requestWithCookies(http.MethodGet, "/api/profiles/legacy-ownerless", nil, []*http.Cookie{admin.cookie})
	require.Equal(t, http.StatusOK, adminLegacyResponse.Code, adminLegacyResponse.Body.String())

	adminListResponse := fixture.requestWithCookies(http.MethodGet, "/api/profiles", nil, []*http.Cookie{admin.cookie})
	require.Equal(t, http.StatusOK, adminListResponse.Code, adminListResponse.Body.String())
	require.Contains(t, adminListResponse.Body.String(), "owned-a")
	require.Contains(t, adminListResponse.Body.String(), "owned-b")
	require.Contains(t, adminListResponse.Body.String(), "legacy-ownerless")
}

func TestViewerProfileAuthorizationIsReadOnly(t *testing.T) {
	fixture := newRouteTestFixture(t, true)
	viewer := newRouteSession(t, fixture, "viewer", auth.RoleViewer)
	require.NoError(t, fixture.repo.CreateProfileForUser(
		"viewer-owned",
		"viewer-owned",
		"http://audiobookshelf.invalid",
		"viewer-sentinel-token",
		"hardcover-viewer-sentinel-token",
		database.SyncConfigData{ProcessUnreadBooks: true},
		viewer.user.ID,
	))

	listResponse := fixture.requestWithCookies(http.MethodGet, "/api/profiles", nil, []*http.Cookie{viewer.cookie})
	require.Equal(t, http.StatusOK, listResponse.Code, listResponse.Body.String())
	require.Contains(t, listResponse.Body.String(), "viewer-owned")
	require.NotContains(t, listResponse.Body.String(), "viewer-sentinel-token")
	require.NotContains(t, listResponse.Body.String(), "hardcover-viewer-sentinel-token")

	createDenied := fixture.requestWithCookies(
		http.MethodPost,
		"/api/profiles",
		[]byte(`{"id":"viewer-create-denied","name":"Denied","audiobookshelf_url":"http://audiobookshelf.invalid","audiobookshelf_token":"denied-token","hardcover_token":"denied-hc-token"}`),
		[]*http.Cookie{viewer.cookie},
	)
	require.Equal(t, http.StatusForbidden, createDenied.Code, createDenied.Body.String())
	deniedMetadata, err := fixture.repo.GetProfileMetadata("viewer-create-denied")
	require.NoError(t, err)
	require.Nil(t, deniedMetadata)

	readResponse := fixture.requestWithCookies(http.MethodGet, "/api/profiles/viewer-owned", nil, []*http.Cookie{viewer.cookie})
	require.Equal(t, http.StatusOK, readResponse.Code, readResponse.Body.String())
	require.Contains(t, readResponse.Body.String(), "viewer-owned")
	require.Contains(t, readResponse.Body.String(), "audiobookshelf.invalid")
	require.NotContains(t, readResponse.Body.String(), "viewer-sentinel-token")
	require.NotContains(t, readResponse.Body.String(), "hardcover-viewer-sentinel-token")

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "profile update", method: http.MethodPut, path: "/api/profiles/viewer-owned", body: `{"name":"changed"}`},
		{name: "config update", method: http.MethodPut, path: "/api/profiles/viewer-owned/config", body: `{"audiobookshelf_url":"http://changed.invalid"}`},
		{name: "delete profile", method: http.MethodDelete, path: "/api/profiles/viewer-owned"},
		{name: "start sync", method: http.MethodPost, path: "/api/profiles/viewer-owned/sync"},
		{name: "cancel sync", method: http.MethodDelete, path: "/api/profiles/viewer-owned/sync"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.requestWithCookies(test.method, test.path, []byte(test.body), []*http.Cookie{viewer.cookie})
			require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		})
	}

	metadata, err := fixture.repo.GetProfileMetadata("viewer-owned")
	require.NoError(t, err)
	require.NotNil(t, metadata)
	require.True(t, metadata.Active)
	require.Equal(t, "viewer-owned", metadata.Name)
	_, running := fixture.server.multiUserService.GetSyncService("viewer-owned")
	require.False(t, running)
}

func TestForeignProfileAuthorizationReturnsNotFoundForEveryRoute(t *testing.T) {
	const attentionSentinel = "foreign-attention-sentinel"
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			_, _ = w.Write([]byte(`{"mediaProgress":[],"listeningSessions":[]}`))
		case "/api/libraries":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"libraries": []map[string]string{{"id": "library", "name": "Library"}},
			})
		case "/api/libraries/library/items":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []map[string]interface{}{{
					"id":        "foreign-attention-book",
					"libraryId": "library",
					"mediaType": "book",
					"media": map[string]interface{}{
						"metadata": map[string]string{
							"title":      attentionSentinel,
							"authorName": "Foreign Author",
						},
						"duration": 100,
					},
				}},
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(absServer.Close)
	hardcoverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"search": map[string]interface{}{
					"error":   "",
					"results": map[string]interface{}{"hits": []interface{}{}},
				},
			},
		})
	}))
	t.Cleanup(hardcoverServer.Close)
	fixture := newRouteTestFixtureWithHardcoverURL(t, hardcoverServer.URL)
	owner := newRouteSession(t, fixture, "route-owner", auth.RoleUser)
	foreign := newRouteSession(t, fixture, "route-foreign", auth.RoleUser)
	createRouteProfileAtURL(t, fixture, owner, "foreign-target", absServer.URL, "foreign-target-token")

	require.NoError(t, fixture.server.multiUserService.StartSync("foreign-target"))
	fixture.server.multiUserService.WaitForSyncs()
	status := fixture.server.multiUserService.GetProfileStatus("foreign-target")
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Len(t, status.Snapshot.AttentionRecords, 1)
	require.Equal(t, attentionSentinel, status.Snapshot.AttentionRecords[0].Title)
	runDetailsPath := "/api/profiles/foreign-target/runs/" + status.Snapshot.RunID + "/details"

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "get profile", method: http.MethodGet, path: "/api/profiles/foreign-target"},
		{name: "update profile", method: http.MethodPut, path: "/api/profiles/foreign-target", body: `{"name":"stolen"}`},
		{name: "delete profile", method: http.MethodDelete, path: "/api/profiles/foreign-target"},
		{name: "update config", method: http.MethodPut, path: "/api/profiles/foreign-target/config", body: `{"hardcover_token":"stolen"}`},
		{name: "status", method: http.MethodGet, path: "/api/profiles/foreign-target/status"},
		{name: "summary", method: http.MethodGet, path: "/api/profiles/foreign-target/summary"},
		{name: "run details", method: http.MethodGet, path: runDetailsPath},
		{name: "start sync", method: http.MethodPost, path: "/api/profiles/foreign-target/sync"},
		{name: "cancel sync", method: http.MethodDelete, path: "/api/profiles/foreign-target/sync"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.requestWithCookies(test.method, test.path, []byte(test.body), []*http.Cookie{foreign.cookie})
			require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "foreign-target-token")
			require.NotContains(t, response.Body.String(), attentionSentinel)
		})
	}

	metadata, err := fixture.repo.GetProfileMetadata("foreign-target")
	require.NoError(t, err)
	require.NotNil(t, metadata)
	require.True(t, metadata.Active)
	_, running := fixture.server.multiUserService.GetSyncService("foreign-target")
	require.False(t, running)
}

// Keep the test fixture's legacy profile creation concise without exposing the
// fixture's implementation details in every test case.
func structSyncConfig() database.SyncConfigData {
	return database.SyncConfigData{ProcessUnreadBooks: true}
}
