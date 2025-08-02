package auth

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"fmt"
	mathRand "math/rand"
	"net/http"
	"time"
)

// IAuthProvider interface defines the contract for authentication providers
type IAuthProvider interface {
	// GetName returns the provider name
	GetName() string
	
	// GetType returns the provider type (local, oidc, oauth2)
	GetType() string
	
	// IsEnabled returns whether the provider is enabled
	IsEnabled() bool
	
	// Authenticate authenticates a user with the given credentials
	Authenticate(ctx context.Context, credentials map[string]string) (*AuthUser, error)
	
	// HandleCallback handles OAuth/OIDC callbacks (if applicable)
	HandleCallback(ctx context.Context, r *http.Request) (*AuthUser, error)
	
	// GetAuthURL returns the authentication URL for redirect-based auth (if applicable)
	GetAuthURL(state string) (string, error)
	
	// ValidateToken validates a token and returns user info (if applicable)
	ValidateToken(ctx context.Context, token string) (*AuthUser, error)
}

// AuthResult represents the result of an authentication attempt
type AuthResult struct {
	User    *AuthUser `json:"user"`
	Token   string    `json:"token,omitempty"`
	Success bool      `json:"success"`
	Error   string    `json:"error,omitempty"`
}

// SessionManager handles user sessions
type SessionManager interface {
	// CreateSession creates a new session for a user
	CreateSession(ctx context.Context, userID string, r *http.Request) (*AuthSession, error)
	
	// GetSession retrieves a session by token
	GetSession(ctx context.Context, token string) (*AuthSession, error)
	
	// ValidateSession validates a session and returns the user
	ValidateSession(ctx context.Context, token string) (*AuthUser, error)
	
	// DestroySession destroys a session
	DestroySession(ctx context.Context, token string) error
	
	// CleanupExpiredSessions removes expired sessions
	CleanupExpiredSessions(ctx context.Context) error
}

// AuthConfig represents authentication configuration
type AuthConfig struct {
	Enabled   bool                       `yaml:"enabled" json:"enabled"`
	Providers []AuthProviderConfig       `yaml:"providers" json:"providers"`
	Session   SessionConfig              `yaml:"session" json:"session"`
}

// AuthProviderConfig represents a provider configuration
type AuthProviderConfig struct {
	Type    string            `yaml:"type" json:"type"`
	Name    string            `yaml:"name" json:"name"`
	Enabled bool              `yaml:"enabled" json:"enabled"`
	Config  map[string]string `yaml:"config" json:"config"`
}

// SessionConfig represents session configuration
type SessionConfig struct {
	Secret     string `yaml:"secret" json:"-"`
	MaxAge     int    `yaml:"max_age" json:"max_age"`         // Session duration in seconds
	CookieName string `yaml:"cookie_name" json:"cookie_name"`
	Secure     bool   `yaml:"secure" json:"secure"`
	HttpOnly   bool   `yaml:"http_only" json:"http_only"`
	SameSite   string `yaml:"same_site" json:"same_site"`
}

// DefaultAuthConfig returns default authentication configuration
func DefaultAuthConfig() AuthConfig {
	return AuthConfig{
		Enabled: false,
		Providers: []AuthProviderConfig{
			{
				Type:    "local",
				Name:    "local",
				Enabled: true,
				Config:  map[string]string{},
			},
		},
		Session: SessionConfig{
			Secret:     generateSessionSecret(),
			MaxAge:     86400, // 24 hours
			CookieName: "abs-hc-session",
			Secure:     true,
			HttpOnly:   true,
			SameSite:   "Lax",
		},
	}
}

// generateSessionSecret generates a random session secret
func generateSessionSecret() string {
	bytes := make([]byte, 32)
	if _, err := cryptoRand.Read(bytes); err != nil {
		// Fallback to a default secret if random generation fails
		return "default-session-secret-change-in-production"
	}
	return hex.EncodeToString(bytes)
}

// generateSessionToken generates a random session token
func generateSessionToken() string {
	bytes := make([]byte, 32)
	if _, err := cryptoRand.Read(bytes); err != nil {
		// This should not happen, but provide a fallback
		r := mathRand.New(mathRand.NewSource(time.Now().UnixNano()))
		return fmt.Sprintf("session-%d", r.Int63())
	}
	return hex.EncodeToString(bytes)
}

// generateUserID generates a unique user ID
func generateUserID() string {
	bytes := make([]byte, 16)
	if _, err := cryptoRand.Read(bytes); err != nil {
		// Fallback to timestamp-based ID
		r := mathRand.New(mathRand.NewSource(time.Now().UnixNano()))
		return fmt.Sprintf("user-%d", r.Int63())
	}
	return hex.EncodeToString(bytes)
}
