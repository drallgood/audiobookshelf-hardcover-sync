package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/stretchr/testify/require"
)

type routeTestFixture struct {
	dataDir string
	repo    *database.Repository
	server  *Server
}

func newRouteTestFixture(t *testing.T, authEnabled bool) *routeTestFixture {
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
	multiUserService := multiuser.NewMultiUserService(repo, cfg, logger.Get())

	authConfig := auth.DefaultAuthConfig()
	authConfig.Enabled = authEnabled
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

func (f *routeTestFixture) request(method, path string, body []byte) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	f.server.server.Handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *routeTestFixture) requestWithCookies(method, path string, body []byte, cookies []*http.Cookie) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	f.server.server.Handler.ServeHTTP(recorder, request)
	return recorder
}

func TestServerRoutesPreserveEncodedLegacyProfileIDForCRUD(t *testing.T) {
	fixture := newRouteTestFixture(t, false)
	legacyID := "legacy/profile;id"
	require.NoError(t, fixture.repo.CreateProfile(
		legacyID,
		"Legacy profile",
		"http://audiobookshelf.invalid",
		"abs-token",
		"hardcover-token",
		database.SyncConfigData{ProcessUnreadBooks: true, DryRun: true},
	))
	escapedID := url.PathEscape(legacyID)

	profileResponse := fixture.request(http.MethodGet, "/api/profiles/"+escapedID, nil)
	require.Equal(t, http.StatusOK, profileResponse.Code, profileResponse.Body.String())
	var profilePayload struct {
		Success bool `json:"success"`
		Data    struct {
			Profile struct {
				ID string `json:"id"`
			} `json:"profile"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(profileResponse.Body.Bytes(), &profilePayload))
	require.True(t, profilePayload.Success)
	require.Equal(t, legacyID, profilePayload.Data.Profile.ID)

	statusResponse := fixture.request(http.MethodGet, "/api/profiles/"+escapedID+"/status", nil)
	require.Equal(t, http.StatusOK, statusResponse.Code, statusResponse.Body.String())
	var statusPayload struct {
		Success bool `json:"success"`
		Data    struct {
			ProfileID string `json:"profile_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(statusResponse.Body.Bytes(), &statusPayload))
	require.True(t, statusPayload.Success)
	require.Equal(t, legacyID, statusPayload.Data.ProfileID)

	detailsResponse := fixture.request(http.MethodGet, "/api/profiles/"+escapedID+"/runs/unknown/details", nil)
	require.Equal(t, http.StatusNotFound, detailsResponse.Code, detailsResponse.Body.String())

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "profile update",
			method: http.MethodPut,
			path:   "/api/profiles/" + escapedID,
			body:   `{"name":"Updated profile"}`,
		},
		{
			name:   "config update",
			method: http.MethodPut,
			path:   "/api/profiles/" + escapedID + "/config",
			body:   `{"audiobookshelf_url":"http://updated.invalid","sync_config":{"process_unread_books":true}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.request(test.method, test.path, []byte(test.body))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		})
	}

	deleteResponse := fixture.request(http.MethodDelete, "/api/profiles/"+escapedID, nil)
	require.Equal(t, http.StatusOK, deleteResponse.Code, deleteResponse.Body.String())
	missingResponse := fixture.request(http.MethodGet, "/api/profiles/"+escapedID, nil)
	require.Equal(t, http.StatusNotFound, missingResponse.Code, missingResponse.Body.String())
}

func TestServerProfileRoutesUseAuthMiddleware(t *testing.T) {
	fixture := newRouteTestFixture(t, true)
	response := fixture.request(http.MethodGet, "/api/profiles/example", nil)
	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
}

func TestServerAggregateOmitsErrorWhileAuthenticatedStatusRetainsIt(t *testing.T) {
	fixture := newRouteTestFixture(t, true)
	profileID := "error-profile"
	sentinel := "sensitive-run-error"
	statePath := filepath.Join(fixture.dataDir, sentinel+".json")
	profileStatePath := strings.TrimSuffix(statePath, ".json") + "." + profileID
	require.NoError(t, os.Mkdir(profileStatePath, 0o755))
	require.NoError(t, fixture.repo.CreateProfile(
		profileID,
		"Error profile",
		"http://audiobookshelf.invalid",
		"abs-token",
		"hardcover-token",
		database.SyncConfigData{StateFile: statePath, ProcessUnreadBooks: true, DryRun: true},
	))
	_, err := fixture.server.multiUserService.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)
	fixture.server.multiUserService.WaitForSyncs()

	terminal := fixture.server.multiUserService.GetProfileStatus(profileID)
	require.NotNil(t, terminal)
	require.Equal(t, "error", terminal.Status)
	require.Contains(t, terminal.Error, sentinel)

	publicResponse := fixture.request(http.MethodGet, "/api/status", nil)
	require.Equal(t, http.StatusOK, publicResponse.Code, publicResponse.Body.String())
	require.NotContains(t, publicResponse.Body.String(), sentinel)
	var publicEnvelope struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(publicResponse.Body.Bytes(), &publicEnvelope))
	var publicProfile map[string]json.RawMessage
	for _, profile := range publicEnvelope.Data {
		var id string
		require.NoError(t, json.Unmarshal(profile["profile_id"], &id))
		if id == profileID {
			publicProfile = profile
			break
		}
	}
	require.NotNil(t, publicProfile)
	_, hasError := publicProfile["error"]
	require.False(t, hasError)

	loginResponse := fixture.request(
		http.MethodPost,
		"/api/auth/login",
		[]byte(`{"provider":"local","credentials":{"username":"admin","password":"admin"}}`),
	)
	require.Equal(t, http.StatusOK, loginResponse.Code, loginResponse.Body.String())
	cookies := loginResponse.Result().Cookies()
	require.NotEmpty(t, cookies)
	statusResponse := fixture.requestWithCookies(http.MethodGet, "/api/profiles/"+profileID+"/status", nil, cookies)
	require.Equal(t, http.StatusOK, statusResponse.Code, statusResponse.Body.String())
	var authenticatedStatus struct {
		Success bool                        `json:"success"`
		Data    multiuser.SyncProfileStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(statusResponse.Body.Bytes(), &authenticatedStatus))
	require.True(t, authenticatedStatus.Success)
	require.Equal(t, terminal.Error, authenticatedStatus.Data.Error)
	require.Contains(t, statusResponse.Body.String(), sentinel)
}
