package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/stretchr/testify/require"
)

func TestEditionCapabilityRouteReportsSeparateUnverifiedStatuses(t *testing.T) {
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
	require.Equal(t, multiuser.EditionCapabilityUnverified, payload.Data.Ebook.Status)
	require.True(t, payload.Data.Ebook.CanAttempt)
	require.Equal(t, "permission_unverified", payload.Data.Ebook.Warning)
	require.Equal(t, multiuser.EditionCapabilityUpsertBook, payload.Data.Audiobook.Operation)
	require.Equal(t, multiuser.EditionCapabilityUnverified, payload.Data.Audiobook.Status)
	require.True(t, payload.Data.Audiobook.CanAttempt)
	require.Equal(t, "permission_unverified", payload.Data.Audiobook.Warning)
	require.NotContains(t, response.Body.String(), "hardcover-token")
	require.Equal(t, int32(0), hardcoverRequests.Load(), "capability reporting must not probe a Hardcover write")
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
			database.SyncConfigData{}, profile.ownerID,
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
}
