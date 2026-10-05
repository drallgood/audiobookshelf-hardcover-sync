package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	"github.com/stretchr/testify/require"
)

func TestRunDetailsEditionRecoveryCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name         string
		role         auth.UserRole
		issuedAt     time.Time
		signingKey   string
		wantToken    bool
		wantManual   bool
		clearToken   bool
		omitRecovery bool
		fallback     bool
	}{
		{"owner", auth.RoleUser, time.Now(), "hardcover-token", true, false, false, false, false},
		{"admin", auth.RoleAdmin, time.Now(), "hardcover-token", true, false, false, false, false},
		{"viewer", auth.RoleViewer, time.Now(), "hardcover-token", false, false, false, false, false},
		{"viewer without recovery", auth.RoleViewer, time.Now(), "hardcover-token", false, false, false, true, false},
		{"expired", auth.RoleUser, time.Now().Add(-49 * time.Hour), "hardcover-token", false, true, false, false, false},
		{"rotated", auth.RoleUser, time.Now(), "old-hardcover-token", false, true, false, false, false},
		{"missing Hardcover token", auth.RoleUser, time.Now(), "hardcover-token", false, true, true, false, false},
		{"edition ASIN fallback owner after resolved result", auth.RoleUser, time.Now(), "hardcover-token", true, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newEditionDraftTestFixture(t, `{}`, "us")
			configureEditionCreateRoute(t, fixture)
			require.NoError(t, fixture.db.GetDB().Model(&auth.AuthUser{}).Where("id = ?", fixture.owner.ID).Update("role", tc.role).Error)
			if tc.clearToken {
				require.NoError(t, fixture.db.GetDB().Model(&database.SyncProfileConfig{}).
					Where("profile_id = ?", "draft-profile").Update("hardcover_token_encrypted", "").Error)
			}
			const runID = "pending-details"
			record := editionCreateRecord()
			claims := editionRecoveryClaims{ProfileID: "draft-profile", RunID: runID, ABSItemID: "abs-item-1", HardcoverBookID: "42", AudibleIdentifier: "B0SOURCE12:us"}
			if tc.fallback {
				record = audiobookIdentifierFallbackRecord(sync.OutcomeAlreadyCurrent, "edition_asin")
				claims.HardcoverBookID = ""
			}
			addCompletedNeedsReviewRun(t, fixture, runID, record)
			token := signEditionRecoveryTokenAt(tc.signingKey, claims, tc.issuedAt)
			if tc.omitRecovery {
				token = ""
			}
			editedTitle := "User edited title"
			action := sync.EditionActionRecord{
				Outcome:       editionOutcomeUnconfirmed,
				SubmittedBody: &sync.EditionActionSubmittedBody{RunID: runID, ABSItemID: "abs-item-1", Title: &editedTitle},
				Data:          &sync.EditionActionData{HardcoverBookID: "42", AudibleIdentifier: claims.AudibleIdentifier, RecoveryToken: token},
			}
			if tc.fallback {
				action.Data.HardcoverBookID = "73"
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
			if tc.role == auth.RoleViewer {
				require.Nil(t, projected.SubmittedBody)
				require.NotContains(t, response.Body.String(), "submitted_body")
			} else {
				require.Equal(t, action.SubmittedBody, projected.SubmittedBody)
			}
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
			require.Equal(t, action.SubmittedBody, stored.SubmittedBody, "read projection must not modify the saved retry fields")
		})
	}
}
