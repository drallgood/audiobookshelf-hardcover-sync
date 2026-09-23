package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
)

// GetEditionDraft handles
// GET /api/profiles/{id}/runs/{runID}/books/{bookID}/edition-draft.
//
// It returns a previewable Hardcover edition built from the Audiobookshelf item
// behind a needs-review record. The target Hardcover book comes from the run
// record, never from the request.
func (h *Handler) GetEditionDraft(w http.ResponseWriter, r *http.Request) {
	profileID, runID, bookID, ok := h.editionRequestIDs(w, r)
	if !ok {
		return
	}

	draft, err := h.multiUserService.PrepareEditionDraft(r.Context(), profileID, runID, bookID)
	if err != nil {
		h.writeEditionError(w, "prepare edition draft", profileID, err)
		return
	}
	h.writeSuccessResponse(w, draft)
}

// editionRequestIDs validates the path identifiers and authorizes the caller
// for the profile. The edition workflow requires write access even though
// preparing a draft itself is read-only.
func (h *Handler) editionRequestIDs(w http.ResponseWriter, r *http.Request) (profileID, runID, bookID string, ok bool) {
	profileID = profileIDFromRequest(r)
	runID = r.PathValue("runID")
	bookID = r.PathValue("bookID")
	if profileID == "" || runID == "" || bookID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID, run ID, and book ID are required")
		return "", "", "", false
	}
	if _, authorized := h.authorizeProfileMetadata(w, r, profileID, true); !authorized {
		return "", "", "", false
	}
	return profileID, runID, bookID, true
}

// writeEditionError maps edition service errors to HTTP responses. Upstream and
// internal failures are logged and reported generically so remote details and
// credentials never reach the client.
func (h *Handler) writeEditionError(w http.ResponseWriter, action, profileID string, err error) {
	var upstream *multiuser.EditionUpstreamError
	errors.As(err, &upstream)
	switch {
	case errors.Is(err, multiuser.ErrProfileNotFound):
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
	case errors.Is(err, multiuser.ErrEditionNotFound):
		h.writeErrorResponse(w, http.StatusNotFound, "Sync run or book not found")
	case errors.Is(err, multiuser.ErrEditionItemNotFound):
		h.writeErrorResponse(w, http.StatusNotFound, "Audiobookshelf item not found")
	case errors.Is(err, multiuser.ErrEditionNotEligible):
		h.writeErrorResponse(w, http.StatusConflict, "Only needs-review books that matched a Hardcover book can get a new edition")
	case errors.Is(err, multiuser.ErrEditionNoIdentifier):
		h.writeErrorResponse(w, http.StatusConflict, "This book has no ASIN or ISBN in Audiobookshelf, so an edition created for it could not be matched by a sync. Add an ASIN or ISBN in Audiobookshelf first.")
	case errors.Is(err, multiuser.ErrProfileDeleting):
		h.writeErrorResponse(w, http.StatusConflict, "Sync profile is being deleted")
	case errors.Is(err, multiuser.ErrServiceShuttingDown):
		h.writeErrorResponse(w, http.StatusServiceUnavailable, "Service is shutting down")
	case upstream == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)):
		// The request context ended; the client is no longer waiting for an
		// upstream failure response, so avoid logging it as one.
		return
	case upstream != nil:
		h.log.Error(fmt.Sprintf("Failed to %s for profile %s: %s", action, profileID, err.Error()))
		h.writeErrorResponse(w, http.StatusBadGateway, fmt.Sprintf("Could not complete the request with %s", upstream.Service))
	default:
		h.log.Error(fmt.Sprintf("Failed to %s for profile %s: %s", action, profileID, err.Error()))
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to "+action)
	}
}
