package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// LocalAuthProvider implements local username/password authentication
type LocalAuthProvider struct {
	name       string
	enabled    bool
	config     map[string]string
	logger     *logger.Logger
	repository *AuthRepository
}

// NewLocalAuthProvider creates a new local authentication provider
func NewLocalAuthProvider(name string, config map[string]string, log *logger.Logger, repository *AuthRepository) *LocalAuthProvider {
	provider := &LocalAuthProvider{
		name:       name,
		enabled:    true,
		config:     config,
		logger:     log,
		repository: repository,
	}
	
	if log != nil {
		log.Info("Local authentication provider initialized", map[string]interface{}{
			"provider": name,
			"enabled":  true,
		})
	}
	
	return provider
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
	if p.logger != nil {
		p.logger.Debug("Starting local authentication", map[string]interface{}{
			"provider": p.name,
			"username": credentials["username"], // Safe to log username
		})
	}

	username, ok := credentials["username"]
	if !ok || username == "" {
		if p.logger != nil {
			p.logger.Warn("Local authentication failed: missing username", map[string]interface{}{
				"provider": p.name,
			})
		}
		return nil, fmt.Errorf("username is required")
	}

	password, ok := credentials["password"]
	if !ok || password == "" {
		if p.logger != nil {
			p.logger.Warn("Local authentication failed: missing password", map[string]interface{}{
				"provider": p.name,
				"username": username,
			})
		}
		return nil, fmt.Errorf("password is required")
	}

	if p.logger != nil {
		p.logger.Debug("Credentials validated, attempting database lookup", map[string]interface{}{
			"provider": p.name,
			"username": username,
		})
	}

	// Look up user in database
	user, err := p.repository.GetUserByUsername(ctx, username)
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("Local authentication failed: user not found", map[string]interface{}{
				"provider": p.name,
				"username": username,
				"error":    err.Error(),
			})
		}
		return nil, fmt.Errorf("invalid username or password")
	}

	// Check if user is active
	if !user.Active {
		if p.logger != nil {
			p.logger.Warn("Local authentication failed: user inactive", map[string]interface{}{
				"provider": p.name,
				"username": username,
				"user_id":  user.ID,
			})
		}
		return nil, fmt.Errorf("user account is inactive")
	}

	// Check if user is from local provider
	if user.Provider != "local" {
		if p.logger != nil {
			p.logger.Warn("Local authentication failed: user from different provider", map[string]interface{}{
				"provider":      p.name,
				"username":      username,
				"user_id":       user.ID,
				"user_provider": user.Provider,
			})
		}
		return nil, fmt.Errorf("user is not configured for local authentication")
	}

	// Verify password
	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		if p.logger != nil {
			p.logger.Warn("Local authentication failed: invalid password", map[string]interface{}{
				"provider": p.name,
				"username": username,
				"user_id":  user.ID,
			})
		}
		return nil, fmt.Errorf("invalid username or password")
	}

	// Update last login time
	now := time.Now()
	user.LastLoginAt = &now
	if err := p.repository.UpdateUser(ctx, user); err != nil {
		if p.logger != nil {
			p.logger.Warn("Failed to update last login time", map[string]interface{}{
				"provider": p.name,
				"username": username,
				"user_id":  user.ID,
				"error":    err.Error(),
			})
		}
		// Don't fail authentication just because we couldn't update login time
	}

	if p.logger != nil {
		p.logger.Info("Local authentication successful", map[string]interface{}{
			"provider": p.name,
			"username": username,
			"user_id":  user.ID,
			"role":     user.Role,
		})
	}

	return user, nil
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
