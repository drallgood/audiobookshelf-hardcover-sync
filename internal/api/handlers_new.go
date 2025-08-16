package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/types"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

// Handler provides HTTP handlers for the sync profile API
type Handler struct {
	multiUserService *multiuser.MultiUserService
	syncService syncService // Interface for sync service to allow for testing
	log        logger.Logger
}

// syncService defines the interface for the sync service
type syncService interface {
	GetSummary() *sync.SyncSummary
}

// NewHandler creates a new API handler
func NewHandler(multiUserService *multiuser.MultiUserService, syncSvc syncService, log *logger.Logger) *Handler {
	h := &Handler{
		multiUserService: multiUserService,
		syncService:      syncSvc,
	}
	
	// Initialize logger if provided
	if log != nil {
		h.log = *log
	} else {
		// Create a basic logger if none provided
		h.log = logger.Logger{} // Assuming logger.Logger has a zero-value that's usable
	}
	
	return h
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
	
	// Log the response before sending
	jsonBytes, err := json.Marshal(response)
	if err != nil {
		h.log.Error("Failed to marshal response to JSON", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		h.log.Debug("Sending JSON response", map[string]interface{}{
			"status_code": statusCode,
			"response":    string(jsonBytes),
		})
	}
	
	// Write the response
	if _, err := w.Write(jsonBytes); err != nil {
		h.log.Error("Failed to write JSON response", map[string]interface{}{
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
		h.log.Error("Failed to list sync profiles: " + err.Error())
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profiles")
		return
	}

	h.writeSuccessResponse(w, profiles)
}

// GetProfile handles GET /api/profiles/{id}
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	profileID := h.extractProfileID(r.URL.Path)
	h.log.Debug(fmt.Sprintf("GetProfile request for profileID: %s", profileID))
	
	if profileID == "" {
		h.log.Error("Profile ID extraction failed: Invalid profile ID")
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	profile, err := h.multiUserService.GetProfile(profileID)
	if err != nil {
		h.log.Error("Failed to get sync profile: " + err.Error())
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profile")
		return
	}

	if profile == nil {
		h.log.Error(fmt.Sprintf("Profile not found in database: %s", profileID))
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		return
	}

	h.log.Debug(fmt.Sprintf("Profile retrieved successfully: %s", profile.Profile.ID))
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
		h.log.Error("Failed to create sync profile: " + err.Error())
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to create sync profile")
		return
	}

	// Get the created profile to return in the response
	profile, err := h.multiUserService.GetProfile(req.ID)

	if err != nil {
		h.log.Error("Failed to create sync profile: " + err.Error())
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
			h.log.Error("Failed to update sync profile: " + err.Error())
			h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to update sync profile")
			return
		}
	}

	// Get updated profile
	profile, err := h.multiUserService.GetProfile(profileID)
	if err != nil {
		h.log.Error("Failed to get updated sync profile: " + err.Error())
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
		h.log.Error(fmt.Sprintf("Failed to get existing sync profile %s: %s", profileID, err.Error()))
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
		h.log.Error(fmt.Sprintf("Failed to update sync profile config %s: %s", profileID, err.Error()))
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to update sync profile configuration")
		return
	}

	// Get updated profile
	profile, err := h.multiUserService.GetProfile(profileID)
	if err != nil {
		h.log.Error("Failed to get updated sync profile: " + err.Error())
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
		h.log.Error(fmt.Sprintf("Failed to find sync profile %s for deletion: %s", profileID, err.Error()))
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		return
	}

	// Delete profile
	if err := h.multiUserService.DeleteProfile(profileID); err != nil {
		h.log.Error(fmt.Sprintf("Failed to delete sync profile %s: %s", profileID, err.Error()))
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
		h.log.Error("Failed to get all sync profile statuses: " + err.Error())
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
			h.log.Error(fmt.Sprintf("Failed to start sync for profile %s: %s", profileID, err.Error()))
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
		h.log.Error(fmt.Sprintf("Failed to cancel sync for profile %s: %s", profileID, err.Error()))
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
	h.writeSuccessResponse(w, map[string]interface{}{
		"id":   "current-user",
		"name": "Current User",
	})
}

// GetSyncSummary handles GET /api/profiles/{id}/summary
func (h *Handler) GetSyncSummary(w http.ResponseWriter, r *http.Request) {
	// Extract profile ID from URL
	profileID := h.extractProfileID(r.URL.Path)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}

	var summary *sync.SyncSummary

	// First try to get the sync service for this profile
	syncSvc, exists := h.multiUserService.GetSyncService(profileID)
	if exists && syncSvc != nil {
		// Get the sync summary from the active sync service
		summary = syncSvc.GetSummary()
	} else {
		// If no active sync service, try to get the last sync status
		status := h.multiUserService.GetProfileStatus(profileID)
		if status != nil && status.LastSync != nil {
			// Create a summary from the last sync status
			summary = &sync.SyncSummary{
				TotalBooksProcessed: int32(status.BooksTotal),
				BooksSynced:         int32(status.BooksSynced),
				BooksNotFound:       status.BooksNotFound,
				Mismatches:          status.Mismatches,
			}
			h.log.Debug("Created summary from profile status", map[string]interface{}{
				"total_books_processed": summary.TotalBooksProcessed,
				"books_synced":         summary.BooksSynced,
				"books_not_found_count": len(summary.BooksNotFound),
				"mismatches_count":     len(summary.Mismatches),
			})
		}
	}

	// If still no summary, return a default empty one
	if summary == nil {
		summary = &sync.SyncSummary{
			TotalBooksProcessed: 0,
			BooksSynced:         0,
			BooksNotFound:       []sync.BookNotFoundInfo{},
			Mismatches:          []mismatch.BookMismatch{},
		}
	}

	h.log.Debug("Sync summary from service", map[string]interface{}{
		"total_books_processed": summary.TotalBooksProcessed,
		"books_synced":         summary.BooksSynced,
		"books_not_found_count": len(summary.BooksNotFound),
		"mismatches_count":     len(summary.Mismatches),
	})

	// Log the summary we received from the service
	h.log.Debug("Processing sync summary from service", map[string]interface{}{
		"total_books_processed": summary.TotalBooksProcessed,
		"books_synced":         summary.BooksSynced,
		"books_not_found_count": len(summary.BooksNotFound),
		"mismatches_count":     len(summary.Mismatches),
	})

	// Convert to API response
	syncSummary := types.SyncSummaryResponse{
		TotalBooksProcessed: summary.TotalBooksProcessed, // Direct access is safe due to mutex in GetSummary()
		BooksSynced:         summary.BooksSynced,         // Direct access is safe due to mutex in GetSummary()
		BooksNotFound:       make([]types.BookNotFoundInfo, 0, len(summary.BooksNotFound)),
		Mismatches:          make([]mismatch.BookMismatch, 0, len(summary.Mismatches)),
	}

	h.log.Debug("Created response struct", map[string]interface{}{
		"total_books_processed": syncSummary.TotalBooksProcessed,
		"books_synced":         syncSummary.BooksSynced,
		"books_not_found_count": len(syncSummary.BooksNotFound),
		"mismatches_count":     len(syncSummary.Mismatches),
	})

	// Copy BooksNotFound
	for _, book := range summary.BooksNotFound {
		syncSummary.BooksNotFound = append(syncSummary.BooksNotFound, types.BookNotFoundInfo{
			Title:  book.Title,
			Author: book.Author,
		})
	}

	h.log.Debug("Copied BooksNotFound", map[string]interface{}{
		"count": len(syncSummary.BooksNotFound),
	})

	// Copy Mismatches
	h.log.Debug("Copying mismatches", map[string]interface{}{
		"source_mismatches_count": len(summary.Mismatches),
	})
	// Always copy mismatches, even if the slice is empty
	syncSummary.Mismatches = make([]mismatch.BookMismatch, len(summary.Mismatches))
	copy(syncSummary.Mismatches, summary.Mismatches)

	h.log.Debug("Copied Mismatches", map[string]interface{}{
		"count": len(syncSummary.Mismatches),
	})

	// Create the final response with user_id and total_books_processed at the top level
	// Always include mismatches in the response, even if empty
	response := map[string]interface{}{
		"user_id":              "default",
		"total_books_processed": syncSummary.TotalBooksProcessed,
		"books_synced":         syncSummary.BooksSynced,
		"books_not_found":      syncSummary.BooksNotFound,
		"mismatches":           syncSummary.Mismatches,
	}

	// Log the final response before sending
	h.log.Debug("Sending sync summary response", map[string]interface{}{
		"user_id":              "default",
		"total_books_processed": syncSummary.TotalBooksProcessed,
		"books_synced":         syncSummary.BooksSynced,
		"books_not_found_count": len(syncSummary.BooksNotFound),
		"mismatches_count":     len(syncSummary.Mismatches),
	})

	// Log the raw JSON response for debugging
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		h.log.Error("Failed to marshal response to JSON", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		h.log.Debug("Raw JSON response:\n" + string(jsonData))
	}

	h.writeSuccessResponse(w, response)
}
