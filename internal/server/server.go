package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Server represents the HTTP server
type Server struct {
	server *http.Server
}

// New creates a new HTTP server
func New(addr string) *Server {
	s := &Server{
		server: &http.Server{
			Addr: addr,
		},
	}

	// Set up routes
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", s.handleHealthCheck)
	handler.HandleFunc("/sync", s.handleSync)

	// Add middleware
	s.server.Handler = logger.HTTPMiddleware(handler)

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

// handleSync handles sync requests
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if !(r.Method == http.MethodGet || r.Method == http.MethodPost) {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: Implement sync logic
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	fmt.Fprint(w, `{"status":"not implemented"}`)
}
