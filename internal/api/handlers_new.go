package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

const maxNewProfileIDBytes = 244

// Handler provides HTTP handlers for the sync profile API
type Handler struct {
	multiUserService *multiuser.MultiUserService
	log              logger.Logger
	authEnabled      bool
}

// NewHandler creates a new API handler.
//
// Sync services are owned by MultiUserService for profile-scoped operations;
// the HTTP layer does not need a separate single-user service reference.
func NewHandler(multiUserService *multiuser.MultiUserService, log *logger.Logger) *Handler {
	h := &Handler{
		multiUserService: multiUserService,
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

// SetAuthEnabled configures whether profile handlers require request user
// context and enforce ownership. It is set by the mounted HTTP server; direct
// handler callers retain the legacy authentication-disabled behavior.
func (h *Handler) SetAuthEnabled(enabled bool) {
	h.authEnabled = enabled
}

// CreateProfileRequest represents the request body for creating a sync profile
type CreateProfileRequest struct {
	ID                  string                  `json:"id"`
	Name                string                  `json:"name"`
	AudiobookshelfURL   string                  `json:"audiobookshelf_url"`
	AudiobookshelfToken string                  `json:"audiobookshelf_token"`
	HardcoverToken      string                  `json:"hardcover_token"`
	SyncConfig          database.SyncConfigData `json:"sync_config"`
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

// aggregateSnapshotResponse is intentionally separate from sync.SyncSnapshot:
// aggregate polling must not serialize the potentially large per-book arrays.
type aggregateSnapshotResponse struct {
	RunID               string             `json:"run_id,omitempty"`
	QueuedAt            time.Time          `json:"queued_at,omitempty"`
	ProcessingStartedAt time.Time          `json:"processing_started_at,omitempty"`
	LastActivityAt      time.Time          `json:"last_activity_at,omitempty"`
	LastProcessedAt     time.Time          `json:"last_processed_at,omitempty"`
	FinishedAt          time.Time          `json:"finished_at,omitempty"`
	DryRun              bool               `json:"dry_run"`
	State               string             `json:"state,omitempty"`
	UnattemptedCount    int32              `json:"unattempted_count"`
	BooksTotal          int32              `json:"books_total"`
	ProcessedSoFar      int32              `json:"processed_so_far"`
	OutcomeCounts       sync.OutcomeCounts `json:"outcome_counts"`
}

type aggregateStatusResponse struct {
	ProfileID        string                     `json:"profile_id"`
	ProfileName      string                     `json:"profile_name"`
	LastAttemptedAt  *time.Time                 `json:"last_attempted_at,omitempty"`
	LastSuccessfulAt *time.Time                 `json:"last_successful_at,omitempty"`
	Snapshot         *aggregateSnapshotResponse `json:"snapshot,omitempty"`
}

// writeJSONResponse writes a JSON response
func (h *Handler) writeJSONResponse(w http.ResponseWriter, statusCode int, response APIResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	// Do not log the serialized response: profile responses contain decrypted
	// credentials and status responses may contain sensitive book metadata.
	jsonBytes, err := json.Marshal(response)
	if err != nil {
		h.log.Error("Failed to marshal response to JSON", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		h.log.Debug("Sending JSON response", map[string]interface{}{
			"status_code": statusCode,
		})
	}

	// Write the response
	if _, err := w.Write(jsonBytes); err != nil {
		h.log.Error("Failed to write JSON response", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

// authorizeProfile enforces profile ownership after authentication middleware
// has populated the request context. Metadata is loaded without decrypting
// tokens, and all denial responses intentionally use 404 for foreign profiles.
func (h *Handler) authorizeProfile(w http.ResponseWriter, r *http.Request, profileID string, mutation bool) bool {
	_, authorized := h.authorizeProfileMetadata(w, r, profileID, mutation)
	return authorized
}

// authorizeProfileMetadata is the metadata-returning form of authorizeProfile
// for handlers that need to confirm the profile exists without loading its
// encrypted configuration.
func (h *Handler) authorizeProfileMetadata(w http.ResponseWriter, r *http.Request, profileID string, mutation bool) (*database.SyncProfile, bool) {
	if !h.authEnabled {
		return nil, true
	}
	user, authenticated := auth.GetUserFromRequest(r)
	if !authenticated || user == nil {
		h.writeErrorResponse(w, http.StatusUnauthorized, "Authentication required")
		return nil, false
	}

	profile, err := h.multiUserService.GetProfileMetadata(profileID)
	if err != nil {
		h.log.Error("Failed to authorize sync profile: " + err.Error())
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profile")
		return nil, false
	}
	if profile == nil {
		h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		return nil, false
	}

	if auth.UserRole(user.Role) != auth.RoleAdmin {
		if profile.OwnerUserID == nil || *profile.OwnerUserID != user.ID {
			h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
			return nil, false
		}
		if mutation && !auth.UserRole(user.Role).HasPermission(auth.PermissionWriteOwn) {
			h.writeErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
			return nil, false
		}
	}
	return profile, true
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

func (h *Handler) buildProfileResponse(p *database.ProfileWithTokens) map[string]interface{} {
	if p == nil {
		return map[string]interface{}{}
	}
	prof := p.Profile
	response := map[string]interface{}{
		"profile": map[string]interface{}{
			"id":         prof.ID,
			"name":       prof.Name,
			"created_at": prof.CreatedAt,
			"updated_at": prof.UpdatedAt,
			"active":     prof.Active,
		},
		"audiobookshelf_url": p.AudiobookshelfURL,
		"sync_config":        p.SyncConfig,
	}
	return response
}

// GetProfiles handles GET /api/profiles
func (h *Handler) GetProfiles(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetUserFromRequest(r)
	authEnabled := h.authEnabled
	if authEnabled && user == nil {
		h.writeErrorResponse(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	admin := authEnabled && auth.UserRole(user.Role) == auth.RoleAdmin
	var profiles []database.SyncProfile
	var err error
	if authEnabled {
		profiles, err = h.multiUserService.ListProfilesForUser(user.ID, admin, true)
	} else {
		profiles, err = h.multiUserService.ListProfiles()
	}
	if err != nil {
		h.log.Error("Failed to list sync profiles: " + err.Error())
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profiles")
		return
	}

	// Include the canonical last-successful timestamp used by the web UI.
	resp := make([]map[string]interface{}, 0, len(profiles))
	for _, p := range profiles {
		item := map[string]interface{}{
			"id":         p.ID,
			"name":       p.Name,
			"active":     p.Active,
			"created_at": p.CreatedAt,
			"updated_at": p.UpdatedAt,
		}
		var lastSuccessful interface{} = nil
		if p.SyncState != nil && p.SyncState.LastSuccessfulAt != nil {
			lastSuccessful = p.SyncState.LastSuccessfulAt
		}
		item["last_successful_at"] = lastSuccessful
		resp = append(resp, item)
	}

	h.writeSuccessResponse(w, resp)
}

// GetProfile handles GET /api/profiles/{id}
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	h.log.Debug(fmt.Sprintf("GetProfile request for profileID: %s", profileID))

	if profileID == "" {
		h.log.Error("Profile ID extraction failed: Invalid profile ID")
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}
	if !h.authorizeProfile(w, r, profileID, false) {
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
	h.writeSuccessResponse(w, h.buildProfileResponse(profile))
}

// CreateProfile handles POST /api/profiles
func (h *Handler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	var user *auth.AuthUser
	if h.authEnabled {
		user, _ = auth.GetUserFromRequest(r)
		if user == nil {
			h.writeErrorResponse(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		if !auth.UserRole(user.Role).HasPermission(auth.PermissionWriteOwn) {
			h.writeErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
			return
		}
	}

	var req CreateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if !isValidNewProfileID(req.ID) {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid profile ID")
		return
	}

	if req.Name == "" || req.AudiobookshelfURL == "" || req.AudiobookshelfToken == "" || req.HardcoverToken == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Missing required fields")
		return
	}

	ownerUserID := ""
	if user != nil {
		ownerUserID = user.ID
	}
	err := h.multiUserService.CreateProfileForUser(
		req.ID,
		req.Name,
		req.AudiobookshelfURL,
		req.AudiobookshelfToken,
		req.HardcoverToken,
		req.SyncConfig,
		ownerUserID,
	)
	if err != nil {
		if errors.Is(err, multiuser.ErrProfileStateFileNameTooLong) ||
			errors.Is(err, multiuser.ErrProfileStateFilePathNotAllowed) {
			h.writeErrorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
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

	h.writeSuccessResponse(w, h.buildProfileResponse(profile))
}

// UpdateProfile handles PUT /api/profiles/{id}
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}
	if !h.authorizeProfile(w, r, profileID, true) {
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

	h.writeSuccessResponse(w, h.buildProfileResponse(profile))
}

// UpdateProfileConfig handles PUT /api/profiles/{id}/config
func (h *Handler) UpdateProfileConfig(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}
	if !h.authorizeProfile(w, r, profileID, true) {
		return
	}

	var req UpdateProfileConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// At least one field must be provided
	if req.AudiobookshelfURL == "" && req.AudiobookshelfToken == "" && req.HardcoverToken == "" {
		// Check if sync config has any actual values set
		hasSyncConfig := !req.SyncConfig.IsEmpty() ||
			!req.SyncConfig.ProcessUnreadBooks || // Explicitly set to false
			req.SyncConfig.Incremental || // Explicitly set to true
			req.SyncConfig.SyncWantToRead || // Explicitly set to true
			req.SyncConfig.SyncOwned || // Explicitly set to true
			req.SyncConfig.IncludeEbooks || // Explicitly set to true
			req.SyncConfig.DryRun // Explicitly set to true

		if !hasSyncConfig {
			h.writeErrorResponse(w, http.StatusBadRequest, "At least one field must be provided")
			return
		}
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
		if errors.Is(err, multiuser.ErrProfileStateFileNameTooLong) ||
			errors.Is(err, multiuser.ErrProfileStateFilePathNotAllowed) {
			h.writeErrorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
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

	h.writeSuccessResponse(w, h.buildProfileResponse(profile))
}

// DeleteProfile handles DELETE /api/profiles/{id}
func (h *Handler) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}
	if !h.authorizeProfile(w, r, profileID, true) {
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

func aggregateSnapshotFrom(snapshot *sync.SyncSnapshot) *aggregateSnapshotResponse {
	if snapshot == nil {
		return nil
	}
	return &aggregateSnapshotResponse{
		RunID:               snapshot.RunID,
		QueuedAt:            snapshot.QueuedAt,
		ProcessingStartedAt: snapshot.ProcessingStartedAt,
		LastActivityAt:      snapshot.LastActivityAt,
		LastProcessedAt:     snapshot.LastProcessedAt,
		FinishedAt:          snapshot.FinishedAt,
		DryRun:              snapshot.DryRun,
		State:               snapshot.State,
		UnattemptedCount:    snapshot.UnattemptedCount,
		BooksTotal:          snapshot.BooksTotal,
		ProcessedSoFar:      snapshot.ProcessedSoFar,
		OutcomeCounts:       snapshot.OutcomeCounts,
	}
}

// GetRunDetails handles GET /api/profiles/{id}/runs/{runID}/details. Details
// are deliberately run-scoped: an active run is served only when its ID
// matches, otherwise the exact retained report is consulted. Unknown,
// cross-profile, and evicted runs never fall through to a newer snapshot.
func (h *Handler) GetRunDetails(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	runID := r.PathValue("runID")
	if profileID == "" || runID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID and run ID are required")
		return
	}
	profileMetadata, authorized := h.authorizeProfileMetadata(w, r, profileID, false)
	if !authorized {
		return
	}

	// Authentication-enabled requests already loaded metadata above. Preserve
	// the legacy authentication-disabled existence check without decrypting
	// profile credentials before consulting the in-memory run snapshot.
	if profileMetadata == nil {
		var err error
		profileMetadata, err = h.multiUserService.GetProfileMetadata(profileID)
		if err != nil {
			h.log.Error("Failed to get sync profile for run details: " + err.Error())
			h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profile")
			return
		}
		if profileMetadata == nil {
			h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
			return
		}
	}

	snapshot, err := h.multiUserService.GetSyncRunSnapshot(profileID, runID)
	if err != nil {
		h.log.Error("Failed to get sync run details: " + err.Error())
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync run details")
		return
	}
	if snapshot == nil || snapshot.RunID != runID ||
		(snapshot.UserID != "" && snapshot.UserID != profileID) {
		h.writeErrorResponse(w, http.StatusNotFound, "Sync run not found")
		return
	}

	h.writeSuccessResponse(w, snapshot)
}

// GetAllProfileStatuses handles GET /api/status
func (h *Handler) GetAllProfileStatuses(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.multiUserService.GetAllProfileStatuses()
	if err != nil {
		h.log.Error("Failed to get all sync profile statuses: " + err.Error())
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve sync profile statuses")
		return
	}

	responses := make([]aggregateStatusResponse, 0, len(statuses))
	for _, status := range statuses {
		if status == nil {
			continue
		}
		response := aggregateStatusResponse{
			ProfileID:        status.ProfileID,
			ProfileName:      status.ProfileName,
			LastAttemptedAt:  status.LastAttemptedAt,
			LastSuccessfulAt: status.LastSuccessfulAt,
		}
		if status.Snapshot != nil {
			response.Snapshot = aggregateSnapshotFrom(status.Snapshot)
		}
		responses = append(responses, response)
	}

	h.writeSuccessResponse(w, responses)
}

// StartSync handles POST /api/profiles/{id}/sync
func (h *Handler) StartSync(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}
	if !h.authorizeProfile(w, r, profileID, true) {
		return
	}

	// StartSyncWithAcceptedRun returns the identity from the same durable
	// reservation that installed the queued run before worker launch. The
	// response may be written after worker processing has begun.
	accepted, err := h.multiUserService.StartSyncWithAcceptedRun(profileID)
	if err != nil {
		switch {
		case errors.Is(err, multiuser.ErrProfileNotFound):
			h.writeErrorResponse(w, http.StatusNotFound, "Sync profile not found")
		case errors.Is(err, multiuser.ErrSyncAlreadyActive):
			h.writeErrorResponse(w, http.StatusConflict, "Sync already in progress")
		default:
			h.log.Error(fmt.Sprintf("Failed to start sync for profile %s: %s", profileID, err.Error()))
			h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to start sync")
		}
		return
	}

	h.writeJSONResponse(w, http.StatusAccepted, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"message":   "Sync started",
			"run_id":    accepted.RunID,
			"state":     accepted.State,
			"queued_at": accepted.QueuedAt,
			"dry_run":   accepted.DryRun,
		},
	})
}

// CancelSync handles DELETE /api/profiles/{id}/sync
func (h *Handler) CancelSync(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromRequest(r)
	if profileID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Profile ID is required")
		return
	}
	if !h.authorizeProfile(w, r, profileID, true) {
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

// profileIDFromRequest reads the path variable installed by the ServeMux
// profile routes. Mounted routes preserve encoded legacy IDs containing route
// delimiters because PathValue returns the unescaped path segment.
func profileIDFromRequest(r *http.Request) string {
	return r.PathValue("id")
}

// isValidNewProfileID accepts up to 244 bytes of RFC 3986 unreserved ASCII
// characters. Existing profiles are not revalidated at read/update/delete
// time so legacy IDs remain addressable when their delimiters are URL-encoded.
func isValidNewProfileID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > maxNewProfileIDBytes {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' || r == '~' {
			continue
		}
		return false
	}
	return true
}
