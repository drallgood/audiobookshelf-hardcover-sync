package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// OIDCProvider implements OpenID Connect authentication using coreos/go-oidc
type OIDCProvider struct {
	name         string
	enabled      bool
	config       map[string]string
	clientID     string
	clientSecret string
	issuer       string
	redirectURI  string
	scopes       []string
	roleClaim    string
	
	// OIDC library components
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth2Config *oauth2.Config
	
	// PKCE state storage (in production, use Redis or database)
	pkceStates   map[string]string // state -> code_verifier
	statesMutex  sync.RWMutex
	
	// Logger for debug information
	logger       *logger.Logger
}

// OIDCClaims represents claims from OIDC ID token
type OIDCClaims struct {
	Subject           string                 `json:"sub"`
	Email             string                 `json:"email"`
	EmailVerified     bool                   `json:"email_verified"`
	Name              string                 `json:"name"`
	PreferredUsername string                 `json:"preferred_username"`
	GivenName         string                 `json:"given_name"`
	FamilyName        string                 `json:"family_name"`
	Locale            string                 `json:"locale"`
	RealmAccess       map[string]interface{} `json:"realm_access"`
	ResourceAccess    map[string]interface{} `json:"resource_access"`
	Groups            []string               `json:"groups"`
	Roles             []string               `json:"roles"`
}

// NewOIDCProvider creates a new OIDC authentication provider using coreos/go-oidc
func NewOIDCProvider(name string, config map[string]string, log *logger.Logger) (*OIDCProvider, error) {
	if log != nil {
		log.Debug("Creating OIDC provider", map[string]interface{}{
			"provider": name,
			"enabled":  true, // Provider is only created if enabled in initializeProviders
		})
	}

	// Validate required configuration
	issuer := config["issuer"]
	clientID := config["client_id"]
	clientSecret := config["client_secret"]
	redirectURI := config["redirect_uri"]

	if log != nil {
		log.Debug("OIDC configuration validation", map[string]interface{}{
			"provider":     name,
			"issuer":       issuer,
			"client_id":    clientID,
			"has_secret":   clientSecret != "",
			"redirect_uri": redirectURI,
		})
	}

	if issuer == "" || clientID == "" || clientSecret == "" || redirectURI == "" {
		if log != nil {
			log.Error("Missing required OIDC configuration", map[string]interface{}{
				"provider":      name,
				"has_issuer":    issuer != "",
				"has_client_id": clientID != "",
				"has_secret":    clientSecret != "",
				"has_redirect":  redirectURI != "",
			})
		}
		return nil, fmt.Errorf("missing required OIDC configuration: issuer, client_id, client_secret, redirect_uri")
	}

	// Parse scopes
	scopesStr := config["scopes"]
	if scopesStr == "" {
		scopesStr = "openid profile email"
	}
	scopes := strings.Fields(scopesStr)

	// Role claim path
	roleClaim := config["role_claim"]
	if roleClaim == "" {
		roleClaim = "realm_access.roles"
	}

	// Create OIDC provider using coreos/go-oidc
	ctx := context.Background()
	
	if log != nil {
		log.Debug("Attempting to create OIDC provider", map[string]interface{}{
			"provider": name,
			"issuer":   issuer,
		})
	}
	
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		if log != nil {
			log.Error("Failed to create OIDC provider", map[string]interface{}{
				"provider": name,
				"issuer":   issuer,
				"error":    err.Error(),
			})
		}
		return nil, fmt.Errorf("failed to create OIDC provider for %s: %w", issuer, err)
	}
	
	if log != nil {
		log.Debug("Successfully created OIDC provider", map[string]interface{}{
			"provider":              name,
			"issuer":                issuer,
			"authorization_endpoint": provider.Endpoint().AuthURL,
			"token_endpoint":        provider.Endpoint().TokenURL,
		})
	}

	// Create OAuth2 config
	oauth2Config := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}

	// Create ID token verifier
	verifier := provider.Verifier(&oidc.Config{
		ClientID: clientID,
	})

	oidcProvider := &OIDCProvider{
		name:         name,
		enabled:      true,
		config:       config,
		clientID:     clientID,
		clientSecret: clientSecret,
		issuer:       issuer,
		redirectURI:  redirectURI,
		scopes:       scopes,
		roleClaim:    roleClaim,
		provider:     provider,
		verifier:     verifier,
		oauth2Config: oauth2Config,
		pkceStates:   make(map[string]string),
		logger:       log,
	}
	
	if log != nil {
		log.Info("OIDC provider initialized successfully", map[string]interface{}{
			"provider":     name,
			"issuer":       issuer,
			"client_id":    clientID,
			"redirect_uri": redirectURI,
			"scopes":       scopes,
			"role_claim":   roleClaim,
		})
	}
	
	return oidcProvider, nil
}

// GetName returns the provider name
func (p *OIDCProvider) GetName() string {
	return p.name
}

// GetType returns the provider type
func (p *OIDCProvider) GetType() string {
	return "oidc"
}

// IsEnabled returns whether the provider is enabled
func (p *OIDCProvider) IsEnabled() bool {
	return p.enabled
}

// Authenticate is not used for OIDC (uses OAuth flow instead)
func (p *OIDCProvider) Authenticate(ctx context.Context, credentials map[string]string) (*AuthUser, error) {
	return nil, fmt.Errorf("OIDC provider uses OAuth flow, not direct authentication")
}

// GetAuthURL generates an OAuth2 authorization URL with PKCE
func (p *OIDCProvider) GetAuthURL(state string) (string, error) {
	if !p.enabled {
		if p.logger != nil {
			p.logger.Warn("Attempted to get auth URL from disabled provider", map[string]interface{}{
				"provider": p.name,
				"state":    state,
			})
		}
		return "", fmt.Errorf("provider %s is disabled", p.name)
	}

	if p.logger != nil {
		p.logger.Debug("Generating OAuth2 authorization URL", map[string]interface{}{
			"provider": p.name,
			"state":    state,
			"scopes":   p.scopes,
		})
	}

	// Generate PKCE code verifier and challenge
	codeVerifier := generateCodeVerifier()
	codeChallenge := generateCodeChallenge(codeVerifier)

	if p.logger != nil {
		p.logger.Debug("Generated PKCE parameters", map[string]interface{}{
			"provider":       p.name,
			"state":          state,
			"code_verifier":  codeVerifier[:10] + "...", // Only log first 10 chars for security
			"code_challenge": codeChallenge[:10] + "...",
		})
	}

	// Store code verifier for later use in token exchange
	p.statesMutex.Lock()
	p.pkceStates[state] = codeVerifier
	p.statesMutex.Unlock()

	// Generate authorization URL with PKCE
	authURL := p.oauth2Config.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	if p.logger != nil {
		p.logger.Info("Generated OAuth2 authorization URL", map[string]interface{}{
			"provider": p.name,
			"state":    state,
			"auth_url": authURL,
		})
	}

	return authURL, nil
}

// HandleCallback handles the OAuth callback from OIDC provider
func (p *OIDCProvider) HandleCallback(ctx context.Context, r *http.Request) (*AuthUser, error) {
	if !p.enabled {
		if p.logger != nil {
			p.logger.Warn("Attempted to handle callback from disabled provider", map[string]interface{}{
				"provider": p.name,
			})
		}
		return nil, fmt.Errorf("provider %s is disabled", p.name)
	}

	if p.logger != nil {
		p.logger.Debug("Handling OAuth2 callback", map[string]interface{}{
			"provider": p.name,
			"url":      r.URL.String(),
		})
	}

	// Get authorization code and state
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errorParam := r.URL.Query().Get("error")
	errorDesc := r.URL.Query().Get("error_description")

	if errorParam != "" {
		if p.logger != nil {
			p.logger.Error("OAuth2 callback returned error", map[string]interface{}{
				"provider":          p.name,
				"error":             errorParam,
				"error_description": errorDesc,
			})
		}
		return nil, fmt.Errorf("OAuth2 error: %s - %s", errorParam, errorDesc)
	}

	if code == "" {
		if p.logger != nil {
			p.logger.Error("Authorization code missing from callback", map[string]interface{}{
				"provider": p.name,
				"url":      r.URL.String(),
			})
		}
		return nil, fmt.Errorf("authorization code not found")
	}
	if state == "" {
		if p.logger != nil {
			p.logger.Error("State parameter missing from callback", map[string]interface{}{
				"provider": p.name,
				"url":      r.URL.String(),
			})
		}
		return nil, fmt.Errorf("state parameter not found")
	}

	if p.logger != nil {
		p.logger.Debug("Extracted callback parameters", map[string]interface{}{
			"provider": p.name,
			"state":    state,
			"code":     code[:10] + "...", // Only log first 10 chars for security
		})
	}

	// Get stored code verifier
	p.statesMutex.RLock()
	codeVerifier, exists := p.pkceStates[state]
	p.statesMutex.RUnlock()

	if !exists {
		if p.logger != nil {
			p.logger.Error("Invalid or expired state parameter", map[string]interface{}{
				"provider": p.name,
				"state":    state,
			})
		}
		return nil, fmt.Errorf("invalid or expired state parameter")
	}

	if p.logger != nil {
		p.logger.Debug("Retrieved PKCE code verifier", map[string]interface{}{
			"provider":      p.name,
			"state":         state,
			"code_verifier": codeVerifier[:10] + "...", // Only log first 10 chars for security
		})
	}

	// Clean up state
	p.statesMutex.Lock()
	delete(p.pkceStates, state)
	p.statesMutex.Unlock()

	// Exchange code for tokens with PKCE
	if p.logger != nil {
		p.logger.Debug("Exchanging authorization code for tokens", map[string]interface{}{
			"provider": p.name,
			"state":    state,
		})
	}

	token, err := p.oauth2Config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("Failed to exchange code for token", map[string]interface{}{
				"provider": p.name,
				"state":    state,
				"error":    err.Error(),
			})
		}
		return nil, fmt.Errorf("failed to exchange code for token: %w", err)
	}

	if p.logger != nil {
		p.logger.Debug("Successfully exchanged code for tokens", map[string]interface{}{
			"provider":    p.name,
			"state":       state,
			"token_type":  token.TokenType,
			"expires_in":  time.Until(token.Expiry).String(),
			"has_refresh": token.RefreshToken != "",
		})
	}

	// Extract ID token
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		if p.logger != nil {
			p.logger.Error("No ID token in OAuth2 response", map[string]interface{}{
				"provider": p.name,
				"state":    state,
			})
		}
		return nil, fmt.Errorf("no id_token in token response")
	}

	if p.logger != nil {
		p.logger.Debug("Extracted ID token from OAuth2 response", map[string]interface{}{
			"provider":    p.name,
			"state":       state,
			"id_token":    rawIDToken[:20] + "...", // Only log first 20 chars for security
		})
	}

	// Verify ID token
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("Failed to verify ID token", map[string]interface{}{
				"provider": p.name,
				"state":    state,
				"error":    err.Error(),
			})
		}
		return nil, fmt.Errorf("failed to verify ID token: %w", err)
	}

	if p.logger != nil {
		p.logger.Debug("Successfully verified ID token", map[string]interface{}{
			"provider": p.name,
			"state":    state,
			"subject":  idToken.Subject,
			"issuer":   idToken.Issuer,
			"audience": idToken.Audience,
			"expiry":   idToken.Expiry.String(),
		})
	}

	// Parse claims
	var claims OIDCClaims
	if err := idToken.Claims(&claims); err != nil {
		if p.logger != nil {
			p.logger.Error("Failed to parse ID token claims", map[string]interface{}{
				"provider": p.name,
				"state":    state,
				"error":    err.Error(),
			})
		}
		return nil, fmt.Errorf("failed to parse ID token claims: %w", err)
	}

	if p.logger != nil {
		p.logger.Debug("Successfully parsed ID token claims", map[string]interface{}{
			"provider":           p.name,
			"state":              state,
			"subject":            claims.Subject,
			"email":              claims.Email,
			"email_verified":     claims.EmailVerified,
			"preferred_username": claims.PreferredUsername,
			"name":               claims.Name,
			"given_name":         claims.GivenName,
			"family_name":        claims.FamilyName,
		})
	}

	// Map claims to AuthUser
	user := p.mapClaimsToUser(&claims)
	
	if p.logger != nil {
		p.logger.Info("Successfully authenticated user via OIDC", map[string]interface{}{
			"provider": p.name,
			"state":    state,
			"user_id":  user.ID,
			"username": user.Username,
			"email":    user.Email,
			"role":     user.Role,
		})
	}
	
	return user, nil
}

// ValidateToken validates an OIDC token and returns user info
func (p *OIDCProvider) ValidateToken(ctx context.Context, token string) (*AuthUser, error) {
	if !p.enabled {
		return nil, fmt.Errorf("OIDC provider %s is not enabled", p.name)
	}

	// Verify the token
	idToken, err := p.verifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to verify token: %w", err)
	}

	// Parse claims
	var claims OIDCClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to parse token claims: %w", err)
	}

	return p.mapClaimsToUser(&claims), nil
}

// mapClaimsToUser maps OIDC claims to AuthUser
func (p *OIDCProvider) mapClaimsToUser(claims *OIDCClaims) *AuthUser {
	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	if username == "" {
		username = claims.Subject
	}

	// Extract roles from claims
	role := p.extractRoleFromClaims(claims)

	return &AuthUser{
		ID:         claims.Subject,
		Username:   username,
		Email:      claims.Email,
		Role:       string(role),
		Provider:   p.name,
		ProviderID: claims.Subject,
		Active:     true,
	}
}

// extractRoleFromClaims extracts user role from OIDC claims
func (p *OIDCProvider) extractRoleFromClaims(claims *OIDCClaims) UserRole {
	// Check for admin roles first
	if p.hasRole(claims, "admin", "administrator", "realm-admin") {
		return RoleAdmin
	}

	// Check for user roles
	if p.hasRole(claims, "user", "member") {
		return RoleUser
	}

	// Default to user role
	return RoleUser
}

// hasRole checks if user has any of the specified roles
func (p *OIDCProvider) hasRole(claims *OIDCClaims, roles ...string) bool {
	// Check direct roles array
	for _, userRole := range claims.Roles {
		for _, checkRole := range roles {
			if strings.EqualFold(userRole, checkRole) {
				return true
			}
		}
	}

	// Check realm_access.roles (Keycloak format)
	if realmAccess, ok := claims.RealmAccess["roles"].([]interface{}); ok {
		for _, roleInterface := range realmAccess {
			if roleStr, ok := roleInterface.(string); ok {
				for _, checkRole := range roles {
					if strings.EqualFold(roleStr, checkRole) {
						return true
					}
				}
			}
		}
	}

	// Check groups
	for _, group := range claims.Groups {
		for _, checkRole := range roles {
			if strings.EqualFold(group, checkRole) {
				return true
			}
		}
	}

	return false
}

// generateCodeVerifier generates a PKCE code verifier
func generateCodeVerifier() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		// This should never happen with crypto/rand, but handle it gracefully
		panic(fmt.Sprintf("failed to generate random bytes: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// generateCodeChallenge generates a PKCE code challenge
func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}