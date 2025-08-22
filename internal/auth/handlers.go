package auth

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// simpleTitle provides a simple title case conversion
// This replaces the deprecated strings.Title function
func simpleTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// AuthHandlers provides HTTP handlers for authentication
type AuthHandlers struct {
	service *AuthService
	logger  *logger.Logger
}

// NewAuthHandlers creates new authentication handlers
func NewAuthHandlers(service *AuthService, log *logger.Logger) *AuthHandlers {
	return &AuthHandlers{
		service: service,
		logger:  log,
	}
}

// LoginRequest represents a login request
type LoginRequest struct {
	Provider    string            `json:"provider"`
	Username    string            `json:"username,omitempty"`
	Password    string            `json:"password,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	Success   bool      `json:"success"`
	User      *AuthUser `json:"user,omitempty"`
	Token     string    `json:"token,omitempty"`
	Error     string    `json:"error,omitempty"`
	RedirectURL string  `json:"redirect_url,omitempty"`
}

// HandleLogin handles login requests
func (h *AuthHandlers) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if !h.service.IsEnabled() {
		h.writeError(w, http.StatusServiceUnavailable, "authentication_disabled", "Authentication is disabled")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleLoginPage(w, r)
	case http.MethodPost:
		h.handleLoginSubmit(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleLoginPage serves the login page
func (h *AuthHandlers) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	// Get available providers
	providers := h.service.GetProviders()
	
	// Always serve login page - don't check authentication status here
	// The frontend will handle redirects after successful authentication
	h.logger.Debug("Serving login page", nil)
	h.serveLoginHTML(w, r, providers)
}

// handleLoginSubmit handles login form submission
func (h *AuthHandlers) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	
	// Parse request based on content type
	contentType := r.Header.Get("Content-Type")
	isJSONRequest := strings.Contains(contentType, "application/json")
	if isJSONRequest {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			// For API clients, return JSON; for browser forms, redirect with error
			if strings.Contains(r.Header.Get("Accept"), "application/json") || isJSONRequest {
				h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON request")
				return
			}
			http.Redirect(w, r, "/login?error=invalid_request", http.StatusFound)
			return
		}
	} else {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			if strings.Contains(r.Header.Get("Accept"), "application/json") {
				h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid form data")
				return
			}
			http.Redirect(w, r, "/login?error=invalid_request", http.StatusFound)
			return
		}
		
		req.Provider = r.FormValue("provider")
		req.Username = r.FormValue("username")
		req.Password = r.FormValue("password")
		
		if req.Credentials == nil {
			req.Credentials = make(map[string]string)
		}
		req.Credentials["username"] = req.Username
		req.Credentials["password"] = req.Password
	}

	// Default to local provider if username/password are supplied without explicit provider
	if req.Provider == "" && (req.Username != "" || req.Password != "") {
		req.Provider = "local"
	}
	// Validate request
	if req.Provider == "" {
		if strings.Contains(r.Header.Get("Accept"), "application/json") || isJSONRequest {
			h.writeError(w, http.StatusBadRequest, "missing_provider", "Provider is required")
			return
		}
		// Redirect back to login page with friendly error message
		http.Redirect(w, r, "/login?error=missing_provider", http.StatusFound)
		return
	}

	// Attempt login
	result, err := h.service.Login(r.Context(), req.Provider, req.Credentials, r)
	if err != nil {
		h.logger.Error("Login attempt failed", map[string]interface{}{
			"provider": req.Provider,
			"username": req.Username,
			"error":    err.Error(),
		})
		if strings.Contains(r.Header.Get("Accept"), "application/json") || isJSONRequest {
			h.writeError(w, http.StatusInternalServerError, "login_failed", "Login failed")
			return
		}
		// Redirect back with generic error
		redirect := r.FormValue("redirect")
		if redirect == "" {
			redirect = "/"
		}
		http.Redirect(w, r, "/login?error=login_failed&redirect="+url.QueryEscape(redirect), http.StatusFound)
		return
	}

	if !result.Success {
		if strings.Contains(r.Header.Get("Accept"), "application/json") || isJSONRequest {
			h.writeError(w, http.StatusUnauthorized, "authentication_failed", result.Error)
			return
		}
		// Redirect back with auth_failed for a friendly UI message
		redirect := r.FormValue("redirect")
		if redirect == "" {
			redirect = "/"
		}
		http.Redirect(w, r, "/login?error=auth_failed&redirect="+url.QueryEscape(redirect), http.StatusFound)
		return
	}

	// Set session cookie
	sessionManager := h.service.sessionManager.(*DefaultSessionManager)
	sessionManager.SetSessionCookie(w, result.Token)

	// Log successful login
	h.logger.Info("User logged in successfully", map[string]interface{}{
		"user_id":  result.User.ID,
		"username": result.User.Username,
		"provider": req.Provider,
	})

	// Handle response based on content type
	if strings.Contains(r.Header.Get("Accept"), "application/json") || isJSONRequest {
		response := LoginResponse{
			Success: true,
			User:    result.User,
			Token:   result.Token,
		}
		h.writeJSON(w, response)
	} else {
		// Redirect to original URL or dashboard
		redirectURL := r.FormValue("redirect")
		if redirectURL == "" {
			redirectURL = "/"
		}
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

// HandleLogout handles logout requests
func (h *AuthHandlers) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if !h.service.IsEnabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// Get session token
	sessionManager := h.service.sessionManager.(*DefaultSessionManager)
	token := sessionManager.GetSessionFromRequest(r)
	
	if token != "" {
		// Destroy session
		if err := h.service.Logout(r.Context(), token); err != nil {
			h.logger.Error("Failed to destroy session", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	// Clear session cookie
	sessionManager.ClearSessionCookie(w)

	// Handle response based on content type
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		h.writeJSON(w, map[string]interface{}{"success": true})
	} else {
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// HandleOAuthCallback handles OAuth/OIDC callbacks
func (h *AuthHandlers) HandleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !h.service.IsEnabled() {
		http.Error(w, "Authentication disabled", http.StatusServiceUnavailable)
		return
	}

	// Extract provider from URL path
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid callback URL", http.StatusBadRequest)
		return
	}
	
	providerName := pathParts[2] // /auth/callback/{provider}

	// Handle callback
	result, err := h.service.HandleCallback(r.Context(), providerName, r)
	if err != nil {
		h.logger.Error("OAuth callback failed", map[string]interface{}{
			"provider": providerName,
			"error":    err.Error(),
		})
		http.Redirect(w, r, "/login?error=callback_failed", http.StatusFound)
		return
	}

	if !result.Success {
		h.logger.Error("OAuth authentication failed", map[string]interface{}{
			"provider": providerName,
			"error":    result.Error,
		})
		http.Redirect(w, r, "/login?error=auth_failed", http.StatusFound)
		return
	}

	// Set session cookie
	sessionManager := h.service.sessionManager.(*DefaultSessionManager)
	sessionManager.SetSessionCookie(w, result.Token)

	// Log successful login
	h.logger.Info("User logged in via OAuth", map[string]interface{}{
		"user_id":  result.User.ID,
		"username": result.User.Username,
		"provider": providerName,
	})

	// Redirect to original URL or dashboard
	state := r.URL.Query().Get("state")
	redirectURL := "/"
	if state != "" {
		// Decode state to get original redirect URL
		if decoded, err := url.QueryUnescape(state); err == nil {
			redirectURL = decoded
		}
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// HandleOAuthLogin initiates OAuth login
func (h *AuthHandlers) HandleOAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !h.service.IsEnabled() {
		http.Error(w, "Authentication disabled", http.StatusServiceUnavailable)
		return
	}

	// Extract provider from URL path
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid OAuth URL", http.StatusBadRequest)
		return
	}
	
	providerName := pathParts[2] // /auth/oauth/{provider}

	// Get redirect URL from query parameter
	redirectURL := r.URL.Query().Get("redirect")
	if redirectURL == "" {
		redirectURL = "/"
	}

	// Generate state parameter (encode redirect URL)
	state := url.QueryEscape(redirectURL)

	// Get auth URL from provider
	authURL, err := h.service.GetAuthURL(providerName, state)
	if err != nil {
		h.logger.Error("Failed to get OAuth URL", map[string]interface{}{
			"provider": providerName,
			"error":    err.Error(),
		})
		http.Error(w, "Failed to initiate OAuth", http.StatusInternalServerError)
		return
	}

	// Redirect to provider
	http.Redirect(w, r, authURL, http.StatusFound)
}

// serveLoginHTML serves the login page HTML
func (h *AuthHandlers) serveLoginHTML(w http.ResponseWriter, r *http.Request, providers map[string]IAuthProvider) {
    // Build dynamic sections based on available providers
    hasLocal := false
    for _, p := range providers {
        if p.GetType() == "local" && p.IsEnabled() {
            hasLocal = true
            break
        }
    }

    // Simple login page HTML (no provider dropdown; local form + separate OAuth buttons)
    pageHTML := `<!DOCTYPE html>
<html>
<head>
    <title>Login - Audiobookshelf Hardcover Sync</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            margin: 0;
            padding: 0;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .login-container {
            background: white;
            padding: 2rem;
            border-radius: 10px;
            box-shadow: 0 10px 25px rgba(0,0,0,0.1);
            width: 100%;
            max-width: 400px;
        }
        .login-header {
            text-align: center;
            margin-bottom: 2rem;
        }
        .login-header h1 {
            color: #333;
            margin: 0 0 0.5rem 0;
        }
        .login-header p {
            color: #666;
            margin: 0;
        }
        .form-group {
            margin-bottom: 1rem;
        }
        label {
            display: block;
            margin-bottom: 0.5rem;
            color: #333;
            font-weight: 500;
        }
        input[type="text"], input[type="password"], select {
            width: 100%;
            padding: 0.75rem;
            border: 1px solid #ddd;
            border-radius: 5px;
            font-size: 1rem;
            box-sizing: border-box;
        }
        input[type="text"]:focus, input[type="password"]:focus, select:focus {
            outline: none;
            border-color: #667eea;
            box-shadow: 0 0 0 2px rgba(102, 126, 234, 0.2);
        }
        .btn {
            width: 100%;
            padding: 0.75rem;
            background: #667eea;
            color: white;
            border: none;
            border-radius: 5px;
            font-size: 1rem;
            cursor: pointer;
            transition: background 0.2s;
        }
        .btn:hover {
            background: #5a6fd8;
        }
        .oauth-providers {
            margin-top: 1.5rem;
            padding-top: 1.5rem;
            border-top: 1px solid #eee;
        }
        .oauth-btn {
            display: block;
            width: 100%;
            padding: 0.75rem;
            margin-bottom: 0.5rem;
            background: #f8f9fa;
            color: #333;
            text-decoration: none;
            border-radius: 5px;
            text-align: center;
            transition: background 0.2s;
        }
        .oauth-btn:hover {
            background: #e9ecef;
        }
        .error {
            background: #f8d7da;
            color: #721c24;
            padding: 0.75rem;
            border-radius: 5px;
            margin-bottom: 1rem;
        }
    </style>
</head>
<body>
    <div class="login-container">
        <div class="login-header">
            <h1>Login</h1>
            <p>Audiobookshelf Hardcover Sync</p>
        </div>
        
        %s
        
        %s
        
        <div class="oauth-providers">
            %s
        </div>
    </div>
    
    <script>
        // No provider dropdown; local form is shown when available.
    </script>
</body>
</html>`

    // Build error message
    errorMsg := ""
	if errorParam := r.URL.Query().Get("error"); errorParam != "" {
		switch errorParam {
		case "callback_failed":
			errorMsg = `<div class="error">OAuth callback failed. Please try again.</div>`
		case "auth_failed":
			errorMsg = `<div class="error">Authentication failed. Please check your credentials.</div>`
		case "invalid_request":
			errorMsg = `<div class="error">Invalid request. Please try again.</div>`
		case "missing_provider":
			errorMsg = `<div class="error">Login provider missing. Please try again.</div>`
		case "login_failed":
			errorMsg = `<div class="error">Login failed due to a server error. Please try again.</div>`
		default:
			errorMsg = `<div class="error">Login failed. Please try again.</div>`
		}
	}

	// Keep error message as HTML (safe because it's static text we control)

	    oauthLinks := ""
    
    for name, provider := range providers {
        if provider.GetType() != "local" {
            redirectURL := r.URL.Query().Get("redirect")
            if redirectURL == "" {
                redirectURL = "/"
            }
            oauthURL := fmt.Sprintf("/auth/oauth/%s?redirect=%s", name, url.QueryEscape(redirectURL))
            oauthLinks += fmt.Sprintf(`<a href="%s" class="oauth-btn">Login with %s</a>`, oauthURL, simpleTitle(name))
        }
    }

	redirectURL := r.URL.Query().Get("redirect")
	if redirectURL == "" {
		redirectURL = "/"
	}

	// Escape redirect value for safe HTML embedding
	escapedRedirect := html.EscapeString(redirectURL)

	    // Build local form (only if local provider is available)
    localForm := ""
    if hasLocal {
        localForm = fmt.Sprintf(`
        <form method="post" action="/api/auth/login">
            <input type="hidden" name="redirect" value="%s">
            <input type="hidden" name="provider" value="local">
            <div class="form-group">
                <label for="username">Username:</label>
                <input type="text" name="username" id="username" required>
            </div>
            <div class="form-group">
                <label for="password">Password:</label>
                <input type="password" name="password" id="password" required>
            </div>
            <button type="submit" class="btn">Login</button>
        </form>
        `, escapedRedirect)
    }

    // Render HTML by replacing placeholders (error, localForm, oauthLinks)
    finalHTML := pageHTML
    finalHTML = strings.Replace(finalHTML, "%s", errorMsg, 1)
    finalHTML = strings.Replace(finalHTML, "%s", localForm, 1)
    finalHTML = strings.Replace(finalHTML, "%s", oauthLinks, 1)
    
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.WriteHeader(http.StatusOK)
    if _, err := w.Write([]byte(finalHTML)); err != nil {
        h.logger.Error("Failed to write HTML response", map[string]interface{}{
			"error": err,
		})
	}
}

// writeJSON writes a JSON response
func (h *AuthHandlers) writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("Failed to encode JSON response", map[string]interface{}{
			"error": err,
		})
	}
}

// writeError writes an error response
func (h *AuthHandlers) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	
	response := map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode error response", map[string]interface{}{
			"error": err,
		})
	}
}
