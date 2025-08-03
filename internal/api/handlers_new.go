package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
)

// Handler provides HTTP handlers for the sync profile API
type Handler struct {
	multiUserService *multiuser.MultiUserService
	logger           *logger.Logger
}

// NewHandler creates a new API handler
func NewHandler(multiUserService *multiuser.MultiUserService, log *logger.Logger) *Handler {
	return &Handler{
		multiUserService: multiUserService,
		logger:           log,
	}
}

// CreateProfileRequest represents the request body for creating a sync profile
type CreateProfileRequest struct {
	ID                  string                    `json:"id"`
	Name                string                    `json:"name"`
	AudiobookshelfURL   string                    `json:"audiobookshelf_url"`
	AudiobookshelfToken string                    `json:"audiobookshelf_token"`
	HardcoverToken      string                    `json:"hardcover_token"`
	SyncConfig          database.SyncConfigData   `json:"sync_config"`
}

// UpdateProfileRequest represents the request body for updating a sync profile
type UpdateProfileRequest struct {
	Name string `json:"name"`
}

// UpdateProfileConfigRequest represents the request body for updating sync profile config
type UpdateProfileConfigRequest struct {
	AudiobookshelfURL   string                  `json:"audiobookshelf_url"`
	AudiobookshelfToken string                  `json:"audiobookshelf_token"`
	HardcoverToken      string                  `json:"hardcover_token"`
	SyncConfig          database.SyncConfigData `json:"sync_config"`
}

// APIResponse represents a standard API response
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// writeJSONResponse writes a JSON response
func (h *Handler) writeJSONResponse(w http.ResponseWriter, statusCode int, response APIResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode JSON response", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

// writeErrorResponse writes an error response
func (h *Handler) writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	h.writeJSONResponse(w, statusCode, APIResponse{
		Success: false,
		Error:   message,
	})
}

// writeSuccessResponse writes a success response
func (h *Handler) writeSuccessResponse(w http.ResponseWriter, data interface{}) {
	h.writeJSONResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
	})
}

// GetProfiles handles GET /api/profiles
func (h *Handler) GetProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.multiUserService.ListProfiles()
	if err != nil {
		h.logger.Error("Failed to list sync profiles", map[string]interface{}{
			"error": err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profiles")
		return
	}

	h.writeSuccessResponse(w, profiles)
}

// GetProfile handles GET /api/profiles/{id}
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	profileID := h.extractProfileID(r.URL.Path)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	profile, err := h.multiUserService.GetProfile(profileID)
	if err != nil {
		h.logger.Error("Failed to get sync profile", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profile")
		return
	}

	if profile == nil {
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		return
	}

	h.writeSuccessResponse(w, profile)
}

// CreateProfile handles POST /api/profiles
func (h *Handler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	var req CreateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.ID == "" || req.Name == "" || req.AudiobookshelfURL == "" || req.AudiobookshelfToken == "" || req.HardcoverToken == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Missing required fields")
		return
	}

	err := h.multiUserService.CreateProfile(
		req.ID,
		req.Name,
		req.AudiobookshelfURL,
		req.AudiobookshelfToken,
		req.HardcoverToken,
		req.SyncConfig,
	)
	if err != nil {
		h.logger.Error("Failed to create sync profile", map[string]interface{}{
			"error": err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to create sync profile")
		return
	}

	// Get the created profile to return in the response
	profile, err := h.multiUserService.GetProfile(req.ID)

	if err != nil {
		h.logger.Error("Failed to create sync profile", map[string]interface{}{
			"error": err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to create sync profile")
		return
	}

	h.writeSuccessResponse(w, profile)
}

// UpdateProfile handles PUT /api/profiles/{id}
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	profileID := h.extractProfileID(r.URL.Path)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	var req UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Update profile name if provided
	if req.Name != "" {
		if err := h.multiUserService.UpdateProfile(profileID, req.Name); err != nil {
			h.logger.Error("Failed to update sync profile", map[string]interface{}{
				"profile_id": profileID,
				"error":      err.Error(),
			})
			h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to update sync profile")
			return
		}
	}

	// Get updated profile
	profile, err := h.multiUserService.GetProfile(profileID)
	if err != nil {
		h.logger.Error("Failed to get updated sync profile", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve updated sync profile")
		return
	}

	h.writeSuccessResponse(w, profile)
}

// UpdateProfileConfig handles PUT /api/profiles/{id}/config
func (h *Handler) UpdateProfileConfig(w http.ResponseWriter, r *http.Request) {
	profileID := h.extractProfileIDFromConfigPath(r.URL.Path)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	var req UpdateProfileConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// At least one field must be provided
	if req.AudiobookshelfURL == "" && req.AudiobookshelfToken == "" && req.HardcoverToken == "" && req.SyncConfig.IsEmpty() {
		h.writeErrorResponse(w, http.StatusBadRequest, "At least one field must be provided")
		return
	}

	// Get existing profile to preserve tokens if not provided
	existingProfile, err := h.multiUserService.GetProfile(profileID)
	if err != nil || existingProfile == nil {
		h.logger.Error("Failed to get existing sync profile", map[string]interface{}{
			"profile_id": profileID,
			"error":      err,
		})
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		return
	}

	// Use existing tokens if not provided in request
	audiobookshelfToken := req.AudiobookshelfToken
	if audiobookshelfToken == "" {
		audiobookshelfToken = existingProfile.AudiobookshelfToken
	}

	hardcoverToken := req.HardcoverToken
	if hardcoverToken == "" {
		hardcoverToken = existingProfile.HardcoverToken
	}

	// Update profile config
	if err := h.multiUserService.UpdateProfileConfig(
		profileID,
		req.AudiobookshelfURL,
		audiobookshelfToken,
		hardcoverToken,
		req.SyncConfig,
	); err != nil {
		h.logger.Error("Failed to update sync profile config", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to update sync profile configuration")
		return
	}

	// Get updated profile
	profile, err := h.multiUserService.GetProfile(profileID)
	if err != nil {
		h.logger.Error("Failed to get updated sync profile", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve updated sync profile")
		return
	}

	h.writeSuccessResponse(w, profile)
}

// DeleteProfile handles DELETE /api/profiles/{id}
func (h *Handler) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	profileID := h.extractProfileID(r.URL.Path)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	// Check if profile exists
	_, err := h.multiUserService.GetProfile(profileID)
	if err != nil {
		h.logger.Error("Failed to find sync profile for deletion", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		return
	}

	// Delete profile
	if err := h.multiUserService.DeleteProfile(profileID); err != nil {
		h.logger.Error("Failed to delete sync profile", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to delete sync profile")
		return
	}

	h.writeSuccessResponse(w, nil)
}

// GetProfileStatus handles GET /api/profiles/{id}/status
func (h *Handler) GetProfileStatus(w http.ResponseWriter, r *http.Request) {
	profileID := h.extractProfileIDFromStatusPath(r.URL.Path)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	status := h.multiUserService.GetProfileStatus(profileID)
	h.writeSuccessResponse(w, status)
}

// GetAllProfileStatuses handles GET /api/status
func (h *Handler) GetAllProfileStatuses(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.multiUserService.GetAllProfileStatuses()
	if err != nil {
		h.logger.Error("Failed to get all sync profile statuses", map[string]interface{}{
			"error": err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profile statuses")
		return
	}

	h.writeSuccessResponse(w, statuses)
}

// StartSync handles POST /api/profiles/{id}/sync
func (h *Handler) StartSync(w http.ResponseWriter, r *http.Request) {
	profileID := h.extractProfileIDFromSyncPath(r.URL.Path)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	// Start sync in a goroutine
	go func() {
		if err := h.multiUserService.StartSync(profileID); err != nil {
			h.logger.Error("Failed to start sync", map[string]interface{}{
				"profile_id": profileID,
				"error":      err.Error(),
			})
		}
	}()

	h.writeSuccessResponse(w, map[string]string{
		"message": "Sync started",
	})
}

// CancelSync handles DELETE /api/profiles/{id}/sync
func (h *Handler) CancelSync(w http.ResponseWriter, r *http.Request) {
	profileID := h.extractProfileIDFromSyncPath(r.URL.Path)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	if err := h.multiUserService.CancelSync(profileID); err != nil {
		h.logger.Error("Failed to cancel sync", map[string]interface{}{
			"profile_id": profileID,
			"error":      err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to cancel sync")
		return
	}

	h.writeSuccessResponse(w, map[string]string{
		"message": "Sync cancelled",
	})
}

// Helper functions to extract profile ID from URL paths

func (h *Handler) extractProfileID(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "profiles" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func (h *Handler) extractProfileIDFromConfigPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "profiles" && i+2 < len(parts) && parts[i+2] == "config" {
			return parts[i+1]
		}
	}
	return ""
}

func (h *Handler) extractProfileIDFromStatusPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "profiles" && i+2 < len(parts) && parts[i+2] == "status" {
			return parts[i+1]
		}
	}
	return ""
}

func (h *Handler) extractProfileIDFromSyncPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "profiles" && i+2 < len(parts) && parts[i+2] == "sync" {
			return parts[i+1]
		}
	}
	return ""
}

// HandleCurrentUser returns information about the current sync profile
// This is a placeholder for future authentication integration
func (h *Handler) HandleCurrentUser(w http.ResponseWriter, r *http.Request) {
	// For now, return an error indicating authentication is not implemented
	h.writeErrorResponse(w, http.StatusNotImplemented, "Authentication is not yet implemented")
}
