package server

import (
	"context"
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
	logger           *logger.Logger
}

// New creates a new HTTP server with multi-user and authentication support
func New(addr string, multiUserService *multiuser.MultiUserService, authService *auth.AuthService, log *logger.Logger) *Server {
	apiHandler := api.NewHandler(multiUserService, log)
	
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
		logger:           log,
	}

	// Set up routes
	handler := http.NewServeMux()
	
	// Health check (no auth required)
	handler.HandleFunc("GET /health", s.handleHealthCheck)
	
	// Authentication endpoints (no auth required for login)
	handler.HandleFunc("POST /auth/login", s.authHandlers.HandleLogin)
	handler.HandleFunc("GET /auth/callback", s.authHandlers.HandleOAuthCallback)
	handler.HandleFunc("POST /auth/logout", s.authHandlers.HandleLogout)
	handler.HandleFunc("GET /auth/me", s.handleAPICurrentUser)
	
	// Legacy sync endpoint (backwards compatibility, no auth for now)
	handler.HandleFunc("POST /sync", s.handleSync)

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
	apiMux.HandleFunc("GET /status", s.handleAPIStatus)

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
	// Extract profile ID from URL path
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid profile ID", http.StatusBadRequest)
		return
	}

	profileID := pathParts[2]
	if profileID == "" {
		http.Error(w, "Profile ID is required", http.StatusBadRequest)
		return
	}

	// Handle different resource types under /profiles/{id}
	if len(pathParts) > 3 {
		// Handle nested resources like /profiles/{id}/config, /profiles/{id}/status, etc.
		switch pathParts[3] {
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
	
	// Return a placeholder response for now
	// TODO: Implement proper user context handling
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"id":"system","name":"System User","email":""}`)); err != nil {
		s.logger.Errorf("Failed to write response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleStaticFiles serves static web UI files
func (s *Server) handleStaticFiles(w http.ResponseWriter, r *http.Request) {
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
	
	// Set content type based on file extension
	switch filepath.Ext(fullPath) {
	case ".html":
		w.Header().Set("Content-Type", "text/html")
	case ".css":
		w.Header().Set("Content-Type", "text/css")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript")
	case ".json":
		w.Header().Set("Content-Type", "application/json")
	default:
		w.Header().Set("Content-Type", "text/plain")
	}
	
	// Serve the file
	http.ServeFile(w, r, fullPath)
}
