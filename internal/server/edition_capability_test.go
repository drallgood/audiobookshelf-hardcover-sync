package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/stretchr/testify/require"
)

func TestEditionCapabilityRouteDryRunReportsAllowedWithoutProbing(t *testing.T) {
	var hardcoverRequests atomic.Int32
	hardcoverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hardcoverRequests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(hardcoverServer.Close)
	fixture := newRouteTestFixtureWithHardcoverURL(t, hardcoverServer.URL)
	owner := newRouteSession(t, fixture, "capability-route-owner", auth.RoleUser)
	const profileID = "capability-profile"
	require.NoError(t, fixture.repo.CreateProfileForUser(
		profileID, "Capability profile", "http://abs.home", "abs-token", "hardcover-token",
		database.SyncConfigData{DryRun: true}, owner.user.ID,
	))

	response := fixture.requestWithCookies(
		http.MethodGet, "/api/profiles/"+profileID+"/edition-capability", nil, []*http.Cookie{owner.cookie},
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Ebook     multiuser.EditionCapabilityStatus `json:"ebook"`
			Audiobook multiuser.EditionCapabilityStatus `json:"audiobook"`
			DryRun    bool                              `json:"dry_run"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.True(t, payload.Data.DryRun)
	require.Equal(t, multiuser.EditionCapabilityInsertEdition, payload.Data.Ebook.Operation)
	require.Equal(t, multiuser.EditionCapabilityAllowed, payload.Data.Ebook.Status)
	require.True(t, payload.Data.Ebook.CanAttempt)
	require.Empty(t, payload.Data.Ebook.Warning)
	require.Equal(t, multiuser.EditionCapabilityUpsertBook, payload.Data.Audiobook.Operation)
	require.Equal(t, multiuser.EditionCapabilityAllowed, payload.Data.Audiobook.Status)
	require.True(t, payload.Data.Audiobook.CanAttempt)
	require.Empty(t, payload.Data.Audiobook.Warning)
	require.NotContains(t, response.Body.String(), "hardcover-token")
	require.Equal(t, int32(0), hardcoverRequests.Load(), "capability reporting must not probe a Hardcover write")
}

func TestEditionCapabilityRouteDoesNotDecryptAudiobookshelfToken(t *testing.T) {
	var hardcoverRequests atomic.Int32
	hardcoverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hardcoverRequests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(hardcoverServer.Close)
	fixture := newRouteTestFixtureWithHardcoverURL(t, hardcoverServer.URL)
	owner := newRouteSession(t, fixture, "capability-corrupt-abs-owner", auth.RoleUser)
	const profileID = "capability-corrupt-abs-profile"
	require.NoError(t, fixture.repo.CreateProfileForUser(
		profileID, "Capability profile", "http://abs.home", "abs-token", "hardcover-token",
		database.SyncConfigData{DryRun: true}, owner.user.ID,
	))
	require.NoError(t, fixture.db.GetDB().Model(&database.SyncProfileConfig{}).
		Where("profile_id = ?", profileID).
		Update("audiobookshelf_token_encrypted", "corrupt-ciphertext").Error)

	response := fixture.requestWithCookies(
		http.MethodGet, "/api/profiles/"+profileID+"/edition-capability", nil, []*http.Cookie{owner.cookie},
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			DryRun bool `json:"dry_run"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.True(t, payload.Data.DryRun)
	require.Equal(t, int32(0), hardcoverRequests.Load(), "capability reporting must not probe Hardcover")
}

func TestEditionCapabilityRouteUsesProfileWriteAuthorization(t *testing.T) {
	fixture := newRouteTestFixture(t, true)
	owner := newRouteSession(t, fixture, "capability-auth-owner", auth.RoleUser)
	foreign := newRouteSession(t, fixture, "capability-auth-foreign", auth.RoleUser)
	viewer := newRouteSession(t, fixture, "capability-auth-viewer", auth.RoleViewer)
	for _, profile := range []struct {
		id      string
		ownerID string
	}{
		{id: "capability-owner-profile", ownerID: owner.user.ID},
		{id: "capability-viewer-profile", ownerID: viewer.user.ID},
	} {
		require.NoError(t, fixture.repo.CreateProfileForUser(
			profile.id, profile.id, "http://abs.home", "abs-token", "hc-token",
			database.SyncConfigData{DryRun: true}, profile.ownerID,
		))
	}

	path := "/api/profiles/capability-owner-profile/edition-capability"
	unauthenticated := fixture.request(http.MethodGet, path, nil)
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())

	owned := fixture.requestWithCookies(http.MethodGet, path, nil, []*http.Cookie{owner.cookie})
	require.Equal(t, http.StatusOK, owned.Code, owned.Body.String())

	foreignResponse := fixture.requestWithCookies(http.MethodGet, path, nil, []*http.Cookie{foreign.cookie})
	require.Equal(t, http.StatusNotFound, foreignResponse.Code, foreignResponse.Body.String())

	viewerPath := "/api/profiles/capability-viewer-profile/edition-capability"
	viewerResponse := fixture.requestWithCookies(http.MethodGet, viewerPath, nil, []*http.Cookie{viewer.cookie})
	require.Equal(t, http.StatusForbidden, viewerResponse.Code, viewerResponse.Body.String())

	refreshPath := path + "/refresh"
	unauthenticatedRefresh := fixture.request(http.MethodPost, refreshPath, nil)
	require.Equal(t, http.StatusUnauthorized, unauthenticatedRefresh.Code, unauthenticatedRefresh.Body.String())
	ownedRefresh := fixture.requestWithCookies(http.MethodPost, refreshPath, nil, []*http.Cookie{owner.cookie})
	require.Equal(t, http.StatusOK, ownedRefresh.Code, ownedRefresh.Body.String())
	foreignRefresh := fixture.requestWithCookies(http.MethodPost, refreshPath, nil, []*http.Cookie{foreign.cookie})
	require.Equal(t, http.StatusNotFound, foreignRefresh.Code, foreignRefresh.Body.String())
	viewerRefresh := fixture.requestWithCookies(http.MethodPost, viewerPath+"/refresh", nil, []*http.Cookie{viewer.cookie})
	require.Equal(t, http.StatusForbidden, viewerRefresh.Code, viewerRefresh.Body.String())
}

func TestEditionCapabilityRouteDistinguishesMissingProfileFromStorageFailure(t *testing.T) {
	const path = "/api/profiles/capability-profile/edition-capability"
	fixture := newRouteTestFixture(t, false)

	missing := fixture.request(http.MethodGet, path, nil)
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())

	require.NoError(t, fixture.repo.CreateProfile(
		"capability-profile", "Capability profile", "http://abs.home", "abs-token", "hc-token",
		database.SyncConfigData{},
	))
	require.NoError(t, fixture.db.GetDB().Exec("ALTER TABLE sync_profiles RENAME TO unavailable_sync_profiles").Error)
	failed := fixture.request(http.MethodGet, path, nil)
	require.Equal(t, http.StatusInternalServerError, failed.Code, failed.Body.String())
	require.Contains(t, failed.Body.String(), "Failed to check edition capability")
}

func TestEditionCapabilityRefreshRejectsCrossOriginFormAndPreservesCache(t *testing.T) {
	var hardcoverRequests atomic.Int32
	hardcoverServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hardcoverRequests.Add(1)
		var payload struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid GraphQL request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(payload.Query, "upsert_book") {
			_, _ = w.Write([]byte(`{"errors":[{"message":"missing required field 'book'","extensions":{"path":"$.selectionSet.upsert_book.args.book","code":"validation-failed"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"errors":[{"message":"missing required field 'book_id'","extensions":{"path":"$.selectionSet.insert_edition.args.book_id","code":"validation-failed"}}]}`))
	}))
	t.Cleanup(hardcoverServer.Close)
	fixture := newRouteTestFixtureWithHardcoverURL(t, hardcoverServer.URL)
	owner := newRouteSession(t, fixture, "csrf-capability-owner", auth.RoleUser)
	const profileID = "csrf-capability-profile"
	require.NoError(t, fixture.repo.CreateProfileForUser(
		profileID, "Capability profile", "http://abs.home", "abs-token", "hardcover-token",
		database.SyncConfigData{}, owner.user.ID,
	))
	capabilityPath := "/api/profiles/" + profileID + "/edition-capability"
	refreshPath := capabilityPath + "/refresh"

	primed := fixture.requestWithCookies(http.MethodGet, capabilityPath, nil, []*http.Cookie{owner.cookie})
	require.Equal(t, http.StatusOK, primed.Code, primed.Body.String())
	require.Equal(t, int32(2), hardcoverRequests.Load())

	rejected := serveRouteTestRequest(t, fixture, http.MethodPost, "http://app.example.test"+refreshPath,
		"csrf_token=forged", map[string]string{
			"Content-Type":   "application/x-www-form-urlencoded",
			"Origin":         "https://attacker.example.test",
			"Sec-Fetch-Site": "cross-site",
			"Authorization":  "Bearer " + owner.cookie.Value,
		}, []*http.Cookie{owner.cookie})
	requireCSRFRequestRejected(t, rejected)
	require.Equal(t, int32(2), hardcoverRequests.Load(), "a rejected refresh must not probe Hardcover")

	cached := fixture.requestWithCookies(http.MethodGet, capabilityPath, nil, []*http.Cookie{owner.cookie})
	require.Equal(t, http.StatusOK, cached.Code, cached.Body.String())
	require.Equal(t, int32(2), hardcoverRequests.Load(), "a rejected refresh must preserve cached capability evidence")

	foreignSafeGet := serveRouteTestRequest(t, fixture, http.MethodGet, "http://app.example.test"+capabilityPath,
		"", map[string]string{
			"Origin":         "https://attacker.example.test",
			"Sec-Fetch-Site": "cross-site",
		}, []*http.Cookie{owner.cookie})
	require.Equal(t, http.StatusOK, foreignSafeGet.Code, foreignSafeGet.Body.String())
	require.Equal(t, "*", foreignSafeGet.Header().Get("Access-Control-Allow-Origin"))
	require.Empty(t, foreignSafeGet.Header().Get("Access-Control-Allow-Credentials"))
	require.Equal(t, int32(2), hardcoverRequests.Load())

	preflight := serveRouteTestRequest(t, fixture, http.MethodOptions, "http://app.example.test"+refreshPath, "", map[string]string{
		"Origin":                        "https://attacker.example.test",
		"Sec-Fetch-Site":                "cross-site",
		"Access-Control-Request-Method": "POST",
	}, nil)
	require.Equal(t, http.StatusOK, preflight.Code, preflight.Body.String())
	require.Equal(t, "*", preflight.Header().Get("Access-Control-Allow-Origin"))
	require.Empty(t, preflight.Header().Get("Access-Control-Allow-Credentials"))

	sameOriginRefresh := serveRouteTestRequest(t, fixture, http.MethodPost, "http://app.example.test"+refreshPath,
		"", map[string]string{
			"Origin":         "http://app.example.test",
			"Sec-Fetch-Site": "same-origin",
		}, []*http.Cookie{owner.cookie})
	require.Equal(t, http.StatusOK, sameOriginRefresh.Code, sameOriginRefresh.Body.String())
	require.Equal(t, int32(4), hardcoverRequests.Load())

	cliRefresh := serveRouteTestRequest(t, fixture, http.MethodPost, "http://app.example.test"+refreshPath,
		"", map[string]string{"Authorization": "Bearer " + owner.cookie.Value}, nil)
	require.Equal(t, http.StatusOK, cliRefresh.Code, cliRefresh.Body.String())
	require.Equal(t, int32(6), hardcoverRequests.Load())
}

func TestCrossOriginProtectionRejectsSameSiteSiblingWithAuthDisabled(t *testing.T) {
	for _, authEnabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "authentication-enabled", false: "authentication-disabled"}[authEnabled], func(t *testing.T) {
			fixture := newRouteTestFixture(t, authEnabled)
			var cookies []*http.Cookie
			var authorization string
			if authEnabled {
				session := newRouteSession(t, fixture, "csrf-sibling-owner", auth.RoleUser)
				cookies = []*http.Cookie{session.cookie}
				authorization = "Bearer " + session.cookie.Value
			}
			response := serveRouteTestRequest(t, fixture, http.MethodPost,
				"http://app.example.test/api/profiles/missing/edition-capability/refresh", "", map[string]string{
					"Origin":         "https://books.example.test",
					"Sec-Fetch-Site": "same-site",
					"Authorization":  authorization,
				}, cookies)
			requireCSRFRequestRejected(t, response)
		})
	}
}

func serveRouteTestRequest(t *testing.T, fixture *routeTestFixture, method, target, body string, headers map[string]string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	for name, value := range headers {
		if value != "" {
			request.Header.Set(name, value)
		}
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	fixture.server.server.Handler.ServeHTTP(recorder, request)
	return recorder
}

func requireCSRFRequestRejected(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, "csrf_request_rejected", payload.Error.Code)
}
