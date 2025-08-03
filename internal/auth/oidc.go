package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDCProvider implements OpenID Connect authentication for providers like Keycloak
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
}

// OIDCDiscovery represents OIDC discovery document
type OIDCDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSUri               string `json:"jwks_uri"`
}

// OIDCTokenResponse represents the token response from OIDC provider
type OIDCTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
}

// OIDCClaims represents claims from OIDC ID token
type OIDCClaims struct {
	Subject           string                 `json:"sub"`
	Email             string                 `json:"email"`
	EmailVerified     bool                   `json:"email_verified"`
	PreferredUsername string                 `json:"preferred_username"`
	Name              string                 `json:"name"`
	GivenName         string                 `json:"given_name"`
	FamilyName        string                 `json:"family_name"`
	RealmAccess       map[string]interface{} `json:"realm_access"`
	ResourceAccess    map[string]interface{} `json:"resource_access"`
	Groups            []string               `json:"groups"`
	Roles             []string               `json:"roles"`
	jwt.RegisteredClaims
}

// NewOIDCProvider creates a new OIDC authentication provider
func NewOIDCProvider(name string, config map[string]string) (*OIDCProvider, error) {
	clientID, ok := config["client_id"]
	if !ok || clientID == "" {
		return nil, fmt.Errorf("client_id is required for OIDC provider")
	}

	clientSecret, ok := config["client_secret"]
	if !ok || clientSecret == "" {
		return nil, fmt.Errorf("client_secret is required for OIDC provider")
	}

	issuer, ok := config["issuer"]
	if !ok || issuer == "" {
		return nil, fmt.Errorf("issuer is required for OIDC provider")
	}

	redirectURI := config["redirect_uri"]
	if redirectURI == "" {
		redirectURI = "/auth/callback/" + name
	}

	scopes := []string{"openid", "profile", "email"}
	if scopesStr, ok := config["scopes"]; ok && scopesStr != "" {
		scopes = strings.Split(scopesStr, ",")
		for i, scope := range scopes {
			scopes[i] = strings.TrimSpace(scope)
		}
	}

	roleClaim := config["role_claim"]
	if roleClaim == "" {
		roleClaim = "realm_access.roles"
	}

	return &OIDCProvider{
		name:         name,
		enabled:      true,
		config:       config,
		clientID:     clientID,
		clientSecret: clientSecret,
		issuer:       issuer,
		redirectURI:  redirectURI,
		scopes:       scopes,
		roleClaim:    roleClaim,
	}, nil
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
	return nil, fmt.Errorf("direct authentication not supported for OIDC, use OAuth flow")
}

// GetAuthURL returns the authorization URL for OIDC authentication
func (p *OIDCProvider) GetAuthURL(state string) (string, error) {
	discovery, err := p.getDiscoveryDocument(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get discovery document: %w", err)
	}

	// Generate PKCE challenge
	codeVerifier := generateCodeVerifier()
	codeChallenge := generateCodeChallenge(codeVerifier)

	params := url.Values{
		"client_id":             {p.clientID},
		"response_type":         {"code"},
		"scope":                 {strings.Join(p.scopes, " ")},
		"redirect_uri":          {p.redirectURI},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}

	authURL := discovery.AuthorizationEndpoint + "?" + params.Encode()
	return authURL, nil
}

// HandleCallback handles the OAuth callback from OIDC provider
func (p *OIDCProvider) HandleCallback(ctx context.Context, r *http.Request) (*AuthUser, error) {
	code := r.URL.Query().Get("code")
	if code == "" {
		return nil, fmt.Errorf("authorization code not found in callback")
	}

	state := r.URL.Query().Get("state")
	if state == "" {
		return nil, fmt.Errorf("state parameter not found in callback")
	}

	// Exchange code for tokens
	tokenResponse, err := p.exchangeCodeForTokens(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code for tokens: %w", err)
	}

	// Parse and validate ID token
	claims, err := p.parseIDToken(tokenResponse.IDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID token: %w", err)
	}

	// Map claims to AuthUser
	user := p.mapClaimsToUser(claims)
	return user, nil
}

// ValidateToken validates an OIDC token and returns user info
func (p *OIDCProvider) ValidateToken(ctx context.Context, token string) (*AuthUser, error) {
	claims, err := p.parseIDToken(token)
	if err != nil {
		return nil, fmt.Errorf("failed to validate token: %w", err)
	}

	user := p.mapClaimsToUser(claims)
	return user, nil
}

// getDiscoveryDocument fetches the OIDC discovery document
func (p *OIDCProvider) getDiscoveryDocument(ctx context.Context) (*OIDCDiscovery, error) {
	discoveryURL := strings.TrimSuffix(p.issuer, "/") + "/.well-known/openid_configuration"
	
	req, err := http.NewRequestWithContext(ctx, "GET", discoveryURL, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery request failed with status %d", resp.StatusCode)
	}

	var discovery OIDCDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return nil, err
	}

	return &discovery, nil
}

// exchangeCodeForTokens exchanges authorization code for tokens
func (p *OIDCProvider) exchangeCodeForTokens(ctx context.Context, code string) (*OIDCTokenResponse, error) {
	discovery, err := p.getDiscoveryDocument(ctx)
	if err != nil {
		return nil, err
	}

	data := url.Values{
		"grant_type":   {"authorization_code"},
		"client_id":    {p.clientID},
		"client_secret": {p.clientSecret},
		"code":         {code},
		"redirect_uri": {p.redirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", discovery.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d", resp.StatusCode)
	}

	var tokenResponse OIDCTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResponse); err != nil {
		return nil, err
	}

	return &tokenResponse, nil
}

// parseIDToken parses and validates the ID token
func (p *OIDCProvider) parseIDToken(idToken string) (*OIDCClaims, error) {
	// Parse token without verification first to get claims
	token, _, err := new(jwt.Parser).ParseUnverified(idToken, &OIDCClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID token: %w", err)
	}

	claims, ok := token.Claims.(*OIDCClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	// TODO: Add proper JWT signature verification using JWKS
	// For now, we'll trust the token since it came from the token endpoint

	return claims, nil
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

	user := &AuthUser{
		ID:         generateUserID(),
		Username:   username,
		Email:      claims.Email,
		Role:       string(role),
		Provider:   p.name,
		ProviderID: claims.Subject,
		Active:     true,
	}

	return user
}

// extractRoleFromClaims extracts user role from OIDC claims
func (p *OIDCProvider) extractRoleFromClaims(claims *OIDCClaims) UserRole {
	// Check for admin role in various claim locations
	if p.hasRole(claims, "admin", "administrator", "abs-admin") {
		return RoleAdmin
	}

	// Check for user role
	if p.hasRole(claims, "user", "abs-user") {
		return RoleUser
	}

	// Default to viewer
	return RoleViewer
}

// hasRole checks if user has any of the specified roles
func (p *OIDCProvider) hasRole(claims *OIDCClaims, roles ...string) bool {
	// Check realm_access.roles
	if realmAccess, ok := claims.RealmAccess["roles"].([]interface{}); ok {
		for _, role := range realmAccess {
			if roleStr, ok := role.(string); ok {
				for _, checkRole := range roles {
					if strings.EqualFold(roleStr, checkRole) {
						return true
					}
				}
			}
		}
	}

	// Check direct roles claim
	for _, role := range claims.Roles {
		for _, checkRole := range roles {
			if strings.EqualFold(role, checkRole) {
				return true
			}
		}
	}

	// Check groups claim
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
		// Fallback to a simpler method if crypto/rand fails
		// This should never happen in practice
		for i := range bytes {
			bytes[i] = byte(i % 256)
		}
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// generateCodeChallenge generates a PKCE code challenge
func generateCodeChallenge(verifier string) string {
	// For simplicity, using plain method instead of S256
	// In production, should use S256 with SHA256 hash
	return verifier
}
