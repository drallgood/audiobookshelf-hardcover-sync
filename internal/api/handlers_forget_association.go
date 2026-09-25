package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
)

// ForgetEditionAssociation handles DELETE
// /api/profiles/{id}/edition-associations/{itemID}.
func (h *Handler) ForgetEditionAssociation(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	itemID := strings.TrimSpace(r.PathValue("itemID"))
	if profileID == "" || itemID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID and Audiobookshelf item ID are required")
		return
	}
	if !h.authorizeProfile(w, r, profileID, true) {
		return
	}

	result, err := h.multiUserService.ForgetEditionAssociation(profileID, itemID)
	if err != nil {
		switch {
		case errors.Is(err, multiuser.ErrProfileNotFound):
			h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		case errors.Is(err, multiuser.ErrSyncAlreadyActive):
			h.writeErrorResponse(w, http.StatusConflict, "Sync already in progress")
		case errors.Is(err, multiuser.ErrProfileStateBusy):
			h.writeErrorResponse(w, http.StatusConflict, "Sync state is busy")
		case errors.Is(err, multiuser.ErrProfileDeleting):
			h.writeErrorResponse(w, http.StatusConflict, "Sync profile is being deleted")
		case errors.Is(err, multiuser.ErrServiceShuttingDown):
			h.writeErrorResponse(w, http.StatusServiceUnavailable, "Service is shutting down")
		default:
			h.log.Error("Failed to forget edition association: " + err.Error())
			h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to forget edition association")
		}
		return
	}

	h.writeSuccessResponse(w, result)
}
