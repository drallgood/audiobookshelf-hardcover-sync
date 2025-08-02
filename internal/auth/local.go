package auth

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// LocalAuthProvider implements local username/password authentication
type LocalAuthProvider struct {
	name    string
	enabled bool
	config  map[string]string
}

// NewLocalAuthProvider creates a new local authentication provider
func NewLocalAuthProvider(name string, config map[string]string) *LocalAuthProvider {
	return &LocalAuthProvider{
		name:    name,
		enabled: true,
		config:  config,
	}
}

// GetName returns the provider name
func (p *LocalAuthProvider) GetName() string {
	return p.name
}

// GetType returns the provider type
func (p *LocalAuthProvider) GetType() string {
	return "local"
}

// IsEnabled returns whether the provider is enabled
func (p *LocalAuthProvider) IsEnabled() bool {
	return p.enabled
}

// Authenticate authenticates a user with username and password
func (p *LocalAuthProvider) Authenticate(ctx context.Context, credentials map[string]string) (*AuthUser, error) {
	username, ok := credentials["username"]
	if !ok || username == "" {
		return nil, fmt.Errorf("username is required")
	}

	password, ok := credentials["password"]
	if !ok || password == "" {
		return nil, fmt.Errorf("password is required")
	}

	// This would typically query the database to find the user
	// For now, we'll return an error indicating this needs to be implemented
	// with the actual database integration
	return nil, fmt.Errorf("local authentication requires database integration")
}

// HandleCallback is not applicable for local authentication
func (p *LocalAuthProvider) HandleCallback(ctx context.Context, r *http.Request) (*AuthUser, error) {
	return nil, fmt.Errorf("callback not supported for local authentication")
}

// GetAuthURL is not applicable for local authentication
func (p *LocalAuthProvider) GetAuthURL(state string) (string, error) {
	return "", fmt.Errorf("auth URL not supported for local authentication")
}

// ValidateToken validates a session token (not applicable for local auth)
func (p *LocalAuthProvider) ValidateToken(ctx context.Context, token string) (*AuthUser, error) {
	return nil, fmt.Errorf("token validation not supported for local authentication")
}

// HashPassword hashes a password using bcrypt
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(bytes), nil
}

// VerifyPassword verifies a password against a bcrypt hash
func VerifyPassword(password, hash string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		return fmt.Errorf("invalid password")
	}
	return nil
}

// CreateLocalUser creates a new local user with hashed password
func CreateLocalUser(username, email, password string, role UserRole) (*AuthUser, error) {
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}

	if password == "" {
		return nil, fmt.Errorf("password is required")
	}

	if !role.IsValid() {
		role = RoleUser // Default to user role
	}

	hashedPassword, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &AuthUser{
		ID:           generateUserID(),
		Username:     username,
		Email:        email,
		PasswordHash: hashedPassword,
		Role:         string(role),
		Provider:     "local",
		Active:       true,
	}

	return user, nil
}
