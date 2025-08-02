package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
)

// Handler provides HTTP handlers for the multi-user API
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

// CreateUserRequest represents the request body for creating a user
type CreateUserRequest struct {
	ID                  string                    `json:"id"`
	Name                string                    `json:"name"`
	AudiobookshelfURL   string                    `json:"audiobookshelf_url"`
	AudiobookshelfToken string                    `json:"audiobookshelf_token"`
	HardcoverToken      string                    `json:"hardcover_token"`
	SyncConfig          database.SyncConfigData   `json:"sync_config"`
}

// UpdateUserRequest represents the request body for updating a user
type UpdateUserRequest struct {
	Name string `json:"name"`
}

// UpdateUserConfigRequest represents the request body for updating user config
type UpdateUserConfigRequest struct {
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

// GetUsers handles GET /api/users
func (h *Handler) GetUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	users, err := h.multiUserService.GetUsers()
	if err != nil {
		h.logger.Error("Failed to get users", map[string]interface{}{
			"error": err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to get users")
		return
	}

	h.writeSuccessResponse(w, users)
}

// GetUser handles GET /api/users/{id}
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	userID := h.extractUserID(r.URL.Path)
	if userID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "User ID is required")
		return
	}

	user, err := h.multiUserService.GetUser(userID)
	if err != nil {
		h.logger.Error("Failed to get user", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		h.writeErrorResponse(w, http.StatusNotFound, "User not found")
		return
	}

	// Don't return tokens in the response for security
	response := map[string]interface{}{
		"user":               user.User,
		"audiobookshelf_url": user.AudiobookshelfURL,
		"sync_config":        user.SyncConfig,
	}

	h.writeSuccessResponse(w, response)
}

// CreateUser handles POST /api/users
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	// Validate required fields
	if req.ID == "" || req.Name == "" || req.AudiobookshelfURL == "" || req.AudiobookshelfToken == "" || req.HardcoverToken == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Missing required fields")
		return
	}

	err := h.multiUserService.CreateUser(req.ID, req.Name, req.AudiobookshelfURL, req.AudiobookshelfToken, req.HardcoverToken, req.SyncConfig)
	if err != nil {
		h.logger.Error("Failed to create user", map[string]interface{}{
			"user_id": req.ID,
			"error":   err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to create user")
		return
	}

	h.writeSuccessResponse(w, map[string]string{
		"message": "User created successfully",
		"user_id": req.ID,
	})
}

// UpdateUser handles PUT /api/users/{id}
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	userID := h.extractUserID(r.URL.Path)
	if userID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "User ID is required")
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if req.Name == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Name is required")
		return
	}

	err := h.multiUserService.UpdateUser(userID, req.Name)
	if err != nil {
		h.logger.Error("Failed to update user", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to update user")
		return
	}

	h.writeSuccessResponse(w, map[string]string{
		"message": "User updated successfully",
		"user_id": userID,
	})
}

// UpdateUserConfig handles PUT /api/users/{id}/config
func (h *Handler) UpdateUserConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	userID := h.extractUserIDFromConfigPath(r.URL.Path)
	if userID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "User ID is required")
		return
	}

	var req UpdateUserConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	// Validate required fields
	if req.AudiobookshelfURL == "" || req.AudiobookshelfToken == "" || req.HardcoverToken == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Missing required fields")
		return
	}

	err := h.multiUserService.UpdateUserConfig(userID, req.AudiobookshelfURL, req.AudiobookshelfToken, req.HardcoverToken, req.SyncConfig)
	if err != nil {
		h.logger.Error("Failed to update user config", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to update user config")
		return
	}

	h.writeSuccessResponse(w, map[string]string{
		"message": "User config updated successfully",
		"user_id": userID,
	})
}

// DeleteUser handles DELETE /api/users/{id}
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	userID := h.extractUserID(r.URL.Path)
	if userID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "User ID is required")
		return
	}

	err := h.multiUserService.DeleteUser(userID)
	if err != nil {
		h.logger.Error("Failed to delete user", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, "Failed to delete user")
		return
	}

	h.writeSuccessResponse(w, map[string]string{
		"message": "User deleted successfully",
		"user_id": userID,
	})
}

// GetUserStatus handles GET /api/users/{id}/status
func (h *Handler) GetUserStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	userID := h.extractUserIDFromStatusPath(r.URL.Path)
	if userID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "User ID is required")
		return
	}

	status := h.multiUserService.GetUserStatus(userID)
	h.writeSuccessResponse(w, status)
}

// GetAllUserStatuses handles GET /api/status
func (h *Handler) GetAllUserStatuses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	statuses := h.multiUserService.GetAllUserStatuses()
	h.writeSuccessResponse(w, statuses)
}

// StartSync handles POST /api/users/{id}/sync
func (h *Handler) StartSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	userID := h.extractUserIDFromSyncPath(r.URL.Path)
	if userID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "User ID is required")
		return
	}

	err := h.multiUserService.StartSync(userID)
	if err != nil {
		h.logger.Error("Failed to start sync", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeSuccessResponse(w, map[string]string{
		"message": "Sync started successfully",
		"user_id": userID,
	})
}

// CancelSync handles DELETE /api/users/{id}/sync
func (h *Handler) CancelSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	userID := h.extractUserIDFromSyncPath(r.URL.Path)
	if userID == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "User ID is required")
		return
	}

	err := h.multiUserService.CancelSync(userID)
	if err != nil {
		h.logger.Error("Failed to cancel sync", map[string]interface{}{
			"user_id": userID,
			"error":   err.Error(),
		})
		h.writeErrorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeSuccessResponse(w, map[string]string{
		"message": "Sync cancelled successfully",
		"user_id": userID,
	})
}

// Helper functions to extract user ID from URL paths

func (h *Handler) extractUserID(path string) string {
	// Extract user ID from /api/users/{id}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "users" {
		return parts[2]
	}
	return ""
}

func (h *Handler) extractUserIDFromConfigPath(path string) string {
	// Extract user ID from /api/users/{id}/config
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "users" && parts[3] == "config" {
		return parts[2]
	}
	return ""
}

func (h *Handler) extractUserIDFromStatusPath(path string) string {
	// Extract user ID from /api/users/{id}/status
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "users" && parts[3] == "status" {
		return parts[2]
	}
	return ""
}

func (h *Handler) extractUserIDFromSyncPath(path string) string {
	// Extract user ID from /api/users/{id}/sync
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "users" && parts[3] == "sync" {
		return parts[2]
	}
	return ""
}

// HandleCurrentUser returns information about the currently authenticated user
func (h *Handler) HandleCurrentUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get user from context (set by auth middleware)
	user, ok := auth.GetUserFromRequest(r)
	if !ok {
		h.writeErrorResponse(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	// Return user info (without sensitive data)
	userInfo := map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
		"role":     user.Role,
		"provider": user.Provider,
		"active":   user.Active,
	}

	response := APIResponse{
		Success: true,
		Data:    userInfo,
	}

	h.writeJSONResponse(w, http.StatusOK, response)
}
