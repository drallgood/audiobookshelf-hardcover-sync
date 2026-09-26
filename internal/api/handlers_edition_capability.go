package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
)

// GetEditionCapability handles GET /api/profiles/{id}/edition-capability.
// It reports separate, read-only capability evidence for ebook and audiobook
// creation. A configured token is unverified because Hardcover does not expose
// a documented read-only scope introspection API.
func (h *Handler) GetEditionCapability(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}
	if _, authorized := h.authorizeProfileMetadata(w, r, profileID, true); !authorized {
		return
	}

	capability, err := h.multiUserService.EditionCapabilityForProfile(r.Context(), profileID)
	if err != nil {
		if errors.Is(err, multiuser.ErrProfileNotFound) {
			h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
			return
		}
		h.log.Error(fmt.Sprintf("Failed to report edition capability for profile %s: %s", profileID, err.Error()))
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to check edition capability")
		return
	}
	h.writeSuccessResponse(w, capability)
}
