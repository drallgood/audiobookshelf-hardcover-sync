package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	"github.com/stretchr/testify/require"
)

func TestRunDetailsEditionRecoveryCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name       string
		role       auth.UserRole
		issuedAt   time.Time
		signingKey string
		wantToken  bool
		wantManual bool
	}{
		{"owner", auth.RoleUser, time.Now(), "hardcover-token", true, false},
		{"admin", auth.RoleAdmin, time.Now(), "hardcover-token", true, false},
		{"viewer", auth.RoleViewer, time.Now(), "hardcover-token", false, false},
		{"expired", auth.RoleUser, time.Now().Add(-49 * time.Hour), "hardcover-token", false, true},
		{"rotated", auth.RoleUser, time.Now(), "old-hardcover-token", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, `{}`, "us")
			configureEditionCreateRoute(t, fixture)
			require.NoError(t, fixture.db.GetDB().Model(&auth.AuthUser{}).Where("id = ?", fixture.owner.ID).Update("role", tc.role).Error)
			const runID = "pending-details"
			addCompletedNeedsReviewRun(t, fixture, runID, editionCreateRecord())
			claims := editionRecoveryClaims{ProfileID: "draft-profile", RunID: runID, ABSItemID: "abs-item-1", HardcoverBookID: "42", AudibleIdentifier: "B0SOURCE12:us"}
			token := signEditionRecoveryTokenAt(tc.signingKey, claims, tc.issuedAt)
			action := sync.EditionActionRecord{
				Outcome:       editionOutcomeUnconfirmed,
				SubmittedBody: &sync.EditionActionSubmittedBody{RunID: runID, ABSItemID: "abs-item-1"},
				Data:          &sync.EditionActionData{HardcoverBookID: "42", AudibleIdentifier: claims.AudibleIdentifier, RecoveryToken: token},
			}
			require.NoError(t, fixture.multiUserService.SaveEditionAction("draft-profile", runID, "abs-item-1", action))
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/profiles/{id}/runs/{runID}/details", fixture.handler.GetRunDetails)
			cfg := auth.DefaultAuthConfig()
			cfg.Enabled = true
			fixture.routes = auth.NewAuthMiddleware(fixture.authService.GetSessionManager(), cfg).RequireAuth(mux)
			response := fixture.request("/api/profiles/draft-profile/runs/"+runID+"/details", fixture.sessionCookie(t, fixture.owner))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body struct {
				Data sync.SyncSnapshot `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Len(t, body.Data.BookOutcomes, 1)
			projected := body.Data.BookOutcomes[0].EditionAction
			require.NotNil(t, projected)
			require.Equal(t, tc.wantToken, projected.Data.RecoveryToken != "")
			if tc.wantManual {
				require.Equal(t, editionOutcomeTransportUnknown, projected.Outcome)
				require.NotEmpty(t, projected.Data.Guidance)
			} else {
				require.Equal(t, editionOutcomeUnconfirmed, projected.Outcome)
			}
			stored, found, err := fixture.multiUserService.GetEditionAction("draft-profile", runID, "abs-item-1")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, token, stored.Data.RecoveryToken, "read projection must not modify the saved capability")
			require.Equal(t, editionOutcomeUnconfirmed, stored.Outcome)
		})
	}
}
