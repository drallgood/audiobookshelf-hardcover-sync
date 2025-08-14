package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/auth"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
)

// Server represents the HTTP server
type Server struct {
	server           *http.Server
	multiUserService *multiuser.MultiUserService
	apiHandler       *api.Handler
	authService      *auth.AuthService
	authHandlers     *auth.AuthHandlers
	authMiddleware   *auth.AuthMiddleware
	syncService      api.SyncService
	logger           *logger.Logger
}

// New creates a new HTTP server with multi-user and authentication support
func New(addr string, multiUserService *multiuser.MultiUserService, authService *auth.AuthService, syncService api.SyncService, log *logger.Logger) *Server {
	apiHandler := api.NewHandler(multiUserService, syncService, log)
	
	// Initialize authentication handlers and middleware
	authHandlers := auth.NewAuthHandlers(authService, log)
	authMiddleware := authService.GetMiddleware()
	
	s := &Server{
		server: &http.Server{
			Addr: addr,
		},
		multiUserService: multiUserService,
		apiHandler:       apiHandler,
		authService:      authService,
		authHandlers:     authHandlers,
		authMiddleware:   authMiddleware,
		syncService:      syncService,
		logger:           log,
	}

	// Set up routes
	handler := http.NewServeMux()
	
	// Health check (no auth required)
	handler.HandleFunc("GET /health", s.handleHealthCheck)
	
	// Authentication endpoints (no auth required for login)
	handler.HandleFunc("GET /login", s.authHandlers.HandleLogin)  // Serve login page
	handler.HandleFunc("POST /api/auth/login", s.authHandlers.HandleLogin)  // Handle login form submission
	handler.HandleFunc("GET /api/auth/me", s.handleAPICurrentUser)  // Check auth status (no auth required)
	handler.HandleFunc("GET /auth/callback/{provider}", s.authHandlers.HandleOAuthCallback)
	handler.HandleFunc("GET /auth/oauth/{provider}", s.authHandlers.HandleOAuthLogin)
	handler.HandleFunc("POST /api/auth/logout", s.authHandlers.HandleLogout)
	
	// Public API endpoints (no auth required)
	handler.HandleFunc("GET /api/status", s.handleAPIStatus)  // General status check
	handler.HandleFunc("POST /api/sync", s.handleSync)  // Legacy sync endpoint

	// API v1 routes with authentication
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /profiles", s.handleAPIProfiles)
	apiMux.HandleFunc("POST /profiles", s.handleAPIProfiles)
	apiMux.HandleFunc("GET /profiles/{id}", s.handleAPIProfilesWithID)
	apiMux.HandleFunc("PUT /profiles/{id}", s.handleAPIProfilesWithID)
	apiMux.HandleFunc("DELETE /profiles/{id}", s.handleAPIProfilesWithID)
	apiMux.HandleFunc("PUT /profiles/{id}/config", s.handleAPIProfilesWithID)
	apiMux.HandleFunc("GET /profiles/{id}/status", s.handleAPIProfilesWithID)
	apiMux.HandleFunc("POST /profiles/{id}/sync", s.handleAPIProfilesWithID)
	apiMux.HandleFunc("DELETE /profiles/{id}/sync", s.handleAPIProfilesWithID)

	// Mount API routes under /api with auth middleware
	handler.Handle("/api/", s.authMiddleware.RequireAuth(http.StripPrefix("/api", apiMux)))
	
	// Static web UI files (no auth required)
	handler.Handle("/", http.HandlerFunc(s.handleStaticFiles))

	// Add middleware chain: CORS -> Auth -> Logger
	var finalHandler http.Handler = handler
	finalHandler = s.authMiddleware.CORSMiddleware(finalHandler)
	finalHandler = logger.HTTPMiddleware(finalHandler)
	s.server.Handler = finalHandler

	// Set timeouts
	s.server.ReadTimeout = 10 * time.Second
	s.server.WriteTimeout = 30 * time.Second
	s.server.IdleTimeout = 120 * time.Second

	return s
}

// Start starts the HTTP server
func (s *Server) Start() error {
	logger.Get().Info("Starting HTTP server", map[string]interface{}{
		"addr": s.server.Addr,
	})

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	logger.Get().Info("Shutting down HTTP server", nil)
	return s.server.Shutdown(ctx)
}

// handleHealthCheck handles health check requests
func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ok"}`)
}

// handleSync handles sync requests (legacy endpoint for backwards compatibility)
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: Implement legacy sync logic or redirect to multi-user API
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"status": "sync started"}`)); err != nil {
		s.logger.Error("Failed to write sync response", map[string]interface{}{
			"error": err,
		})
	}
}

// handleAPIStatus handles /api/status endpoint
func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.apiHandler.GetAllProfileStatuses(w, r)
}

// handleAPIProfiles handles /api/profiles endpoint
func (s *Server) handleAPIProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.apiHandler.GetProfiles(w, r)
	case http.MethodPost:
		s.apiHandler.CreateProfile(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAPIProfilesWithID handles /api/profiles/{id} and related endpoints
func (s *Server) handleAPIProfilesWithID(w http.ResponseWriter, r *http.Request) {
	// Debug logging for path parsing
	s.logger.Debug("handleAPIProfilesWithID processing request", map[string]interface{}{
		"original_path": r.URL.Path,
		"method":        r.Method,
	})

	// Extract profile ID from URL path
	trimmedPath := strings.Trim(r.URL.Path, "/")
	pathParts := strings.Split(trimmedPath, "/")
	
	s.logger.Debug("Path parsing details", map[string]interface{}{
		"original_path":  r.URL.Path,
		"trimmed_path":   trimmedPath,
		"path_parts":     pathParts,
		"parts_count":    len(pathParts),
		"parts_needed":   2,
	})

	// After StripPrefix("/api"), path becomes "/profiles/{id}", so we need at least 2 parts
	if len(pathParts) < 2 {
		s.logger.Debug("Returning 400: Invalid profile ID", map[string]interface{}{
			"path_parts":  pathParts,
			"parts_count": len(pathParts),
		})
		http.Error(w, "Invalid profile ID", http.StatusBadRequest)
		return
	}

	profileID := pathParts[1]
	if profileID == "" {
		http.Error(w, "Profile ID is required", http.StatusBadRequest)
		return
	}

	// Handle different resource types under /profiles/{id}
	if len(pathParts) > 2 {
		// Handle nested resources like /profiles/{id}/config, /profiles/{id}/status, etc.
		switch pathParts[2] {
		case "config":
			if r.Method == http.MethodPut {
				s.apiHandler.UpdateProfileConfig(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		case "status":
			if r.Method == http.MethodGet {
				s.apiHandler.GetProfileStatus(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		case "sync":
			switch r.Method {
			case http.MethodPost:
				s.apiHandler.StartSync(w, r)
			case http.MethodDelete:
				s.apiHandler.CancelSync(w, r)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
		return
	}

	// Handle direct profile access (/profiles/{id})
	switch r.Method {
	case http.MethodGet:
		s.apiHandler.GetProfile(w, r)
	case http.MethodPut:
		s.apiHandler.UpdateProfile(w, r)
	case http.MethodDelete:
		s.apiHandler.DeleteProfile(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAPICurrentUser handles /auth/me endpoint
func (s *Server) handleAPICurrentUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set content type
	w.Header().Set("Content-Type", "application/json")

	// Check if authentication is enabled
	if !s.authService.IsEnabled() {
		// Authentication is disabled
		response := map[string]interface{}{
			"auth_enabled":  false,
			"authenticated": false,
			"user":          nil,
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			s.logger.Error("Failed to encode auth disabled response", map[string]interface{}{
				"error": err.Error(),
			})
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Authentication is enabled - validate session cookie directly
	// Since this endpoint is outside auth middleware, we need to check session manually
	sessionManager := s.authService.GetSessionManager().(*auth.DefaultSessionManager)
	token := sessionManager.GetSessionFromRequest(r)
	
	if token == "" {
		// No session token found
		s.logger.Debug("Auth status check: no session token found", nil)
		response := map[string]interface{}{
			"auth_enabled":  true,
			"authenticated": false,
			"user":          nil,
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			s.logger.Error("Failed to encode no token response", map[string]interface{}{
				"error": err.Error(),
			})
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Validate session token
	user, err := s.authService.ValidateSession(r.Context(), token)
	if err != nil {
		// Session validation failed
		s.logger.Debug("Auth status check: session validation failed", map[string]interface{}{
			"error": err.Error(),
		})
		response := map[string]interface{}{
			"auth_enabled":  true,
			"authenticated": false,
			"user":          nil,
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			s.logger.Error("Failed to encode validation failed response", map[string]interface{}{
				"error": err.Error(),
			})
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	// User is authenticated
	s.logger.Debug("Auth status check: user authenticated", map[string]interface{}{
		"user_id": user.ID,
		"username": user.Username,
	})
	response := map[string]interface{}{
		"auth_enabled":  true,
		"authenticated": true,
		"user": map[string]interface{}{
			"id":       user.ID,
			"username": user.Username,
			"email":    user.Email,
			"role":     user.Role,
			"provider": user.Provider,
		},
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode authenticated user response", map[string]interface{}{
			"user_id": user.ID,
			"error":   err.Error(),
		})
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleStaticFiles serves static web UI files
func (s *Server) handleStaticFiles(w http.ResponseWriter, r *http.Request) {
	// Skip if this is an API route
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}

	// Serve static files from web/static directory
	staticDir := "./web/static"
	
	// Default to index.html for root path
	filePath := r.URL.Path
	if filePath == "/" {
		filePath = "/index.html"
	}
	
	// Security: prevent directory traversal
	if strings.Contains(filePath, "..") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	
	fullPath := filepath.Join(staticDir, filePath)
	
	// Set content type and cache control headers based on file extension
	switch filepath.Ext(fullPath) {
	case ".html":
		w.Header().Set("Content-Type", "text/html")
		// HTML files should not be cached to ensure updates are seen immediately
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
	case ".css":
		w.Header().Set("Content-Type", "text/css")
		// CSS files should not be cached during development
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript")
		// JavaScript files should not be cached during development
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	case ".json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	default:
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	}
	
	// Serve the file
	http.ServeFile(w, r, fullPath)
}
