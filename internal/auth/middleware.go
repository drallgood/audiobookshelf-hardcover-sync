package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const userContextKey contextKey = "user"

// AuthMiddleware provides authentication middleware for HTTP handlers
type AuthMiddleware struct {
	sessionManager SessionManager
	config         AuthConfig
	enabled        bool
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(sessionManager SessionManager, config AuthConfig) *AuthMiddleware {
	return &AuthMiddleware{
		sessionManager: sessionManager,
		config:         config,
		enabled:        config.Enabled,
	}
}

// RequireAuth middleware that requires authentication
func (am *AuthMiddleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Debug logging
		logger.Get().Debug("RequireAuth middleware processing request", map[string]interface{}{
			"path":         r.URL.Path,
			"method":       r.Method,
			"auth_enabled": am.enabled,
			"has_cookies":  len(r.Cookies()) > 0,
		})

		if !am.enabled {
			// Authentication disabled, allow all requests
			logger.Get().Debug("Authentication disabled, allowing request", map[string]interface{}{
				"path": r.URL.Path,
			})
			next.ServeHTTP(w, r)
			return
		}

		user, err := am.authenticateRequest(r)
		if err != nil {
			logger.Get().Debug("Authentication failed", map[string]interface{}{
				"path":  r.URL.Path,
				"error": err.Error(),
			})
			am.handleAuthError(w, r, err)
			return
		}

		logger.Get().Debug("Authentication successful", map[string]interface{}{
			"path":     r.URL.Path,
			"user_id":  user.ID,
			"username": user.Username,
		})

		// Add user to request context
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole middleware that requires a specific role
func (am *AuthMiddleware) RequireRole(role UserRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !am.enabled {
				// Authentication disabled, allow all requests
				next.ServeHTTP(w, r)
				return
			}

			user, err := am.authenticateRequest(r)
			if err != nil {
				am.handleAuthError(w, r, err)
				return
			}

			userRole := UserRole(user.Role)
			if !userRole.HasPermission(Permission(role)) {
				am.handleForbidden(w, r)
				return
			}

			// Add user to request context
			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin middleware that requires admin role
func (am *AuthMiddleware) RequireAdmin(next http.Handler) http.Handler {
	return am.RequireRole(RoleAdmin)(next)
}

// OptionalAuth middleware that adds user to context if authenticated, but doesn't require it
func (am *AuthMiddleware) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !am.enabled {
			next.ServeHTTP(w, r)
			return
		}

		user, _ := am.authenticateRequest(r)
		if user != nil {
			ctx := context.WithValue(r.Context(), userContextKey, user)
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

// authenticateRequest authenticates a request and returns the user
func (am *AuthMiddleware) authenticateRequest(r *http.Request) (*AuthUser, error) {
	// Get session token from request
	token := am.getTokenFromRequest(r)
	if token == "" {
		return nil, &AuthError{Code: "no_token", Message: "No authentication token provided"}
	}

	// Validate session
	user, err := am.sessionManager.ValidateSession(r.Context(), token)
	if err != nil {
		return nil, &AuthError{Code: "invalid_token", Message: "Invalid or expired token"}
	}

	return user, nil
}

// getTokenFromRequest extracts authentication token from request
func (am *AuthMiddleware) getTokenFromRequest(r *http.Request) string {
	// Try session cookie first
	if cookie, err := r.Cookie(am.config.Session.CookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}

	// Try Authorization header
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
	}

	return ""
}

// handleAuthError handles authentication errors
func (am *AuthMiddleware) handleAuthError(w http.ResponseWriter, r *http.Request, err error) {
	// Check if this is an API request
	if am.isAPIRequest(r) {
		logger.Get().Debug("Returning 401 Unauthorized for API request", map[string]interface{}{
			"path":  r.URL.Path,
			"error": err.Error(),
		})
		am.writeJSONError(w, http.StatusUnauthorized, "authentication_required", err.Error())
		return
	}

	// For web requests, redirect to login
	logger.Get().Debug("Redirecting to login for web request", map[string]interface{}{
		"path": r.URL.Path,
	})
	http.Redirect(w, r, "/login?redirect="+url.QueryEscape(r.URL.Path), http.StatusFound)
}

// handleForbidden handles authorization errors
func (am *AuthMiddleware) handleForbidden(w http.ResponseWriter, r *http.Request) {
	if am.isAPIRequest(r) {
		am.writeJSONError(w, http.StatusForbidden, "insufficient_permissions", "Insufficient permissions")
		return
	}

	// For web requests, show forbidden page
	http.Error(w, "Forbidden", http.StatusForbidden)
}

// isAPIRequest checks if the request is for an API endpoint
func (am *AuthMiddleware) isAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/") ||
		strings.Contains(r.Header.Get("Accept"), "application/json") ||
		strings.Contains(r.Header.Get("Content-Type"), "application/json")
}

// writeJSONError writes a JSON error response
func (am *AuthMiddleware) writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	
	response := map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Log error but can't do much else at this point
		logger.Get().Error("Failed to encode middleware error response", map[string]interface{}{
			"error": err,
		})
	}
}

// GetUserFromContext extracts the authenticated user from request context
func GetUserFromContext(ctx context.Context) (*AuthUser, bool) {
	user, ok := ctx.Value(userContextKey).(*AuthUser)
	return user, ok
}

// GetUserFromRequest extracts the authenticated user from request context
func GetUserFromRequest(r *http.Request) (*AuthUser, bool) {
	return GetUserFromContext(r.Context())
}

// AuthError represents an authentication error
type AuthError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *AuthError) Error() string {
	return e.Message
}

// CSRF protection middleware
func (am *AuthMiddleware) CSRFProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !am.enabled {
			next.ServeHTTP(w, r)
			return
		}

		// Skip CSRF for GET, HEAD, OPTIONS requests
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		// Check CSRF token for state-changing requests
		token := r.Header.Get("X-CSRF-Token")
		if token == "" {
			token = r.FormValue("csrf_token")
		}

		if token == "" {
			if am.isAPIRequest(r) {
				am.writeJSONError(w, http.StatusForbidden, "csrf_token_missing", "CSRF token required")
			} else {
				http.Error(w, "CSRF token required", http.StatusForbidden)
			}
			return
		}

		// TODO: Implement proper CSRF token validation
		// For now, just check that a token is present

		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware handles CORS headers
func (am *AuthMiddleware) CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*") // Configure appropriately for production
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
