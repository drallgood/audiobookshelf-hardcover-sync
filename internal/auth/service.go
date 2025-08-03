package auth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"gorm.io/gorm"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// AuthService provides high-level authentication operations
type AuthService struct {
	db             *gorm.DB
	repository     *AuthRepository
	sessionManager SessionManager
	providers      map[string]IAuthProvider
	config         AuthConfig
	enabled        bool
	logger         *logger.Logger
}

// NewAuthService creates a new authentication service
func NewAuthService(db *gorm.DB, config AuthConfig, log *logger.Logger) (*AuthService, error) {
	// Create repository
	repository := NewAuthRepository(db)
	
	// Create session manager
	sessionManager := NewSessionManager(db, config.Session)
	
	// Initialize providers
	providers := make(map[string]IAuthProvider)
	
	if log != nil {
		log.Debug("Creating authentication service", map[string]interface{}{
			"enabled":         config.Enabled,
			"provider_count":  len(config.Providers),
			"session_enabled": config.Session.Secret != "",
		})
	}

	service := &AuthService{
		db:             db,
		repository:     repository,
		sessionManager: sessionManager,
		providers:      providers,
		config:         config,
		enabled:        config.Enabled,
		logger:         log,
	}
	
	// Initialize providers
	if err := service.initializeProviders(); err != nil {
		return nil, fmt.Errorf("failed to initialize providers: %w", err)
	}
	
	return service, nil
}

// initializeProviders initializes authentication providers based on configuration
func (s *AuthService) initializeProviders() error {
	for _, providerConfig := range s.config.Providers {
		if !providerConfig.Enabled {
			continue
		}
		
		var provider IAuthProvider
		var err error
		
		switch providerConfig.Type {
		case "local":
			provider = NewLocalAuthProvider(providerConfig.Name, providerConfig.Config, s.logger, s.repository)
		case "oidc":
			provider, err = NewOIDCProvider(providerConfig.Name, providerConfig.Config, s.logger)
			if err != nil {
				return fmt.Errorf("failed to create OIDC provider %s: %w", providerConfig.Name, err)
			}
		default:
			return fmt.Errorf("unsupported provider type: %s", providerConfig.Type)
		}
		
		s.providers[providerConfig.Name] = provider
	}
	
	return nil
}

// IsEnabled returns whether authentication is enabled
func (s *AuthService) IsEnabled() bool {
	return s.enabled
}

// Login authenticates a user with the specified provider
func (s *AuthService) Login(ctx context.Context, providerName string, credentials map[string]string, r *http.Request) (*AuthResult, error) {
	if s.logger != nil {
		s.logger.Debug("Starting authentication login", map[string]interface{}{
			"provider": providerName,
			"username": credentials["username"], // Safe to log username
			"enabled":  s.enabled,
		})
	}

	if !s.enabled {
		if s.logger != nil {
			s.logger.Warn("Authentication login attempted but service is disabled", map[string]interface{}{
				"provider": providerName,
			})
		}
		return nil, fmt.Errorf("authentication is disabled")
	}
	
	provider, exists := s.providers[providerName]
	if !exists {
		if s.logger != nil {
			availableProviders := make([]string, 0, len(s.providers))
			for name := range s.providers {
				availableProviders = append(availableProviders, name)
			}
			s.logger.Error("Authentication login failed: provider not found", map[string]interface{}{
				"provider":           providerName,
				"available_providers": availableProviders,
			})
		}
		return nil, fmt.Errorf("provider %s not found", providerName)
	}
	
	if !provider.IsEnabled() {
		if s.logger != nil {
			s.logger.Warn("Authentication login failed: provider disabled", map[string]interface{}{
				"provider": providerName,
				"type":     provider.GetType(),
			})
		}
		return nil, fmt.Errorf("provider %s is disabled", providerName)
	}
	
	// For local authentication, we need to handle it specially
	if provider.GetType() == "local" {
		return s.handleLocalLogin(ctx, credentials, r)
	}
	
	// For other providers, use the provider's authenticate method
	user, err := provider.Authenticate(ctx, credentials)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	
	// Create or update user in database
	dbUser, err := s.createOrUpdateUser(ctx, user)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Failed to create user session",
		}, nil
	}
	
	// Create session
	session, err := s.sessionManager.CreateSession(ctx, dbUser.ID, r)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Failed to create session",
		}, nil
	}
	
	return &AuthResult{
		User:    dbUser,
		Token:   session.Token,
		Success: true,
	}, nil
}

// handleLocalLogin handles local username/password authentication
func (s *AuthService) handleLocalLogin(ctx context.Context, credentials map[string]string, r *http.Request) (*AuthResult, error) {
	username, ok := credentials["username"]
	if !ok || username == "" {
		return &AuthResult{
			Success: false,
			Error:   "Username is required",
		}, nil
	}
	
	password, ok := credentials["password"]
	if !ok || password == "" {
		return &AuthResult{
			Success: false,
			Error:   "Password is required",
		}, nil
	}
	
	// Get user from database
	user, err := s.repository.GetUserByUsername(ctx, username)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Invalid username or password",
		}, nil
	}
	
	// Verify password
	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Invalid username or password",
		}, nil
	}
	
	// Check if user is active
	if !user.Active {
		return &AuthResult{
			Success: false,
			Error:   "User account is disabled",
		}, nil
	}
	
	// Create session
	session, err := s.sessionManager.CreateSession(ctx, user.ID, r)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Failed to create session",
		}, nil
	}
	
	return &AuthResult{
		User:    user,
		Token:   session.Token,
		Success: true,
	}, nil
}

// Logout logs out a user by destroying their session
func (s *AuthService) Logout(ctx context.Context, token string) error {
	if !s.enabled {
		return nil
	}
	
	return s.sessionManager.DestroySession(ctx, token)
}

// ValidateSession validates a session token and returns the user
func (s *AuthService) ValidateSession(ctx context.Context, token string) (*AuthUser, error) {
	if !s.enabled {
		return nil, fmt.Errorf("authentication is disabled")
	}
	
	return s.sessionManager.ValidateSession(ctx, token)
}

// CreateUser creates a new user
func (s *AuthService) CreateUser(ctx context.Context, username, email, password string, role UserRole, provider string) (*AuthUser, error) {
	if !s.enabled {
		return nil, fmt.Errorf("authentication is disabled")
	}
	
	// Check if user already exists
	exists, err := s.repository.UserExists(ctx, username, email)
	if err != nil {
		return nil, fmt.Errorf("failed to check user existence: %w", err)
	}
	
	if exists {
		return nil, fmt.Errorf("user with username or email already exists")
	}
	
	var user *AuthUser
	
	if provider == "local" {
		user, err = CreateLocalUser(username, email, password, role)
		if err != nil {
			return nil, fmt.Errorf("failed to create local user: %w", err)
		}
	} else {
		// For external providers, create user without password
		user = &AuthUser{
			ID:       generateUserID(),
			Username: username,
			Email:    email,
			Role:     string(role),
			Provider: provider,
			Active:   true,
		}
	}
	
	if err := s.repository.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to save user: %w", err)
	}
	
	return user, nil
}

// createOrUpdateUser creates or updates a user from external provider
func (s *AuthService) createOrUpdateUser(ctx context.Context, user *AuthUser) (*AuthUser, error) {
	// Try to find existing user by provider ID
	if user.ProviderID != "" {
		existingUser, err := s.repository.GetUserByProviderID(ctx, user.Provider, user.ProviderID)
		if err == nil {
			// Update existing user
			existingUser.Email = user.Email
			existingUser.Username = user.Username
			existingUser.Role = user.Role
			if err := s.repository.UpdateUser(ctx, existingUser); err != nil {
				return nil, err
			}
			return existingUser, nil
		}
	}
	
	// Try to find by email
	if user.Email != "" {
		existingUser, err := s.repository.GetUserByEmail(ctx, user.Email)
		if err == nil {
			// Update existing user with provider info
			existingUser.Provider = user.Provider
			existingUser.ProviderID = user.ProviderID
			existingUser.Role = user.Role
			if err := s.repository.UpdateUser(ctx, existingUser); err != nil {
				return nil, err
			}
			return existingUser, nil
		}
	}
	
	// Create new user
	if err := s.repository.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	
	return user, nil
}

// GetAuthURL gets the authentication URL for a provider
func (s *AuthService) GetAuthURL(providerName, state string) (string, error) {
	if !s.enabled {
		return "", fmt.Errorf("authentication is disabled")
	}
	
	provider, exists := s.providers[providerName]
	if !exists {
		return "", fmt.Errorf("provider %s not found", providerName)
	}
	
	return provider.GetAuthURL(state)
}

// HandleCallback handles OAuth/OIDC callbacks
func (s *AuthService) HandleCallback(ctx context.Context, providerName string, r *http.Request) (*AuthResult, error) {
	if !s.enabled {
		return nil, fmt.Errorf("authentication is disabled")
	}
	
	provider, exists := s.providers[providerName]
	if !exists {
		return nil, fmt.Errorf("provider %s not found", providerName)
	}
	
	user, err := provider.HandleCallback(ctx, r)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	
	// Create or update user in database
	dbUser, err := s.createOrUpdateUser(ctx, user)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Failed to create user",
		}, nil
	}
	
	// Create session
	session, err := s.sessionManager.CreateSession(ctx, dbUser.ID, r)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Failed to create session",
		}, nil
	}
	
	return &AuthResult{
		User:    dbUser,
		Token:   session.Token,
		Success: true,
	}, nil
}

// GetProviders returns all enabled providers
func (s *AuthService) GetProviders() map[string]IAuthProvider {
	if !s.enabled {
		return make(map[string]IAuthProvider)
	}
	
	enabled := make(map[string]IAuthProvider)
	for name, provider := range s.providers {
		if provider.IsEnabled() {
			enabled[name] = provider
		}
	}
	return enabled
}

// InitializeDefaultUser creates a default admin user if no users exist
func (s *AuthService) InitializeDefaultUser(ctx context.Context) error {
	if !s.enabled {
		return nil
	}
	
	// Check if any users exist
	count, err := s.repository.GetUserCount(ctx)
	if err != nil {
		return fmt.Errorf("failed to check user count: %w", err)
	}
	
	if count > 0 {
		return nil // Users already exist
	}
	
	// Get default admin credentials from environment
	username := os.Getenv("AUTH_DEFAULT_ADMIN_USERNAME")
	if username == "" {
		username = "admin"
	}
	
	email := os.Getenv("AUTH_DEFAULT_ADMIN_EMAIL")
	if email == "" {
		email = "admin@localhost"
	}
	
	password := os.Getenv("AUTH_DEFAULT_ADMIN_PASSWORD")
	if password == "" {
		password = "admin" // This should be changed in production
	}
	
	// Create default admin user
	return s.repository.CreateDefaultAdminUser(ctx, username, email, password)
}

// GetMiddleware returns authentication middleware
func (s *AuthService) GetMiddleware() *AuthMiddleware {
	return NewAuthMiddleware(s.sessionManager, s.config)
}

// LoadConfigFromEnv loads authentication configuration from environment variables
func LoadConfigFromEnv() AuthConfig {
	config := DefaultAuthConfig()
	
	// Check if auth is enabled
	if enabled := os.Getenv("AUTH_ENABLED"); enabled != "" {
		config.Enabled = strings.ToLower(enabled) == "true"
	}
	
	// Session configuration
	if secret := os.Getenv("AUTH_SESSION_SECRET"); secret != "" {
		config.Session.Secret = secret
	}
	
	if cookieName := os.Getenv("AUTH_COOKIE_NAME"); cookieName != "" {
		config.Session.CookieName = cookieName
	}
	
	// OIDC/Keycloak configuration
	if issuer := os.Getenv("KEYCLOAK_ISSUER"); issuer != "" {
		oidcProvider := AuthProviderConfig{
			Type:    "oidc",
			Name:    "keycloak",
			Enabled: true,
			Config: map[string]string{
				"issuer":        issuer,
				"client_id":     os.Getenv("KEYCLOAK_CLIENT_ID"),
				"client_secret": os.Getenv("KEYCLOAK_CLIENT_SECRET"),
				"redirect_uri":  os.Getenv("KEYCLOAK_REDIRECT_URI"),
				"scopes":        os.Getenv("KEYCLOAK_SCOPES"),
				"role_claim":    os.Getenv("KEYCLOAK_ROLE_CLAIM"),
			},
		}
		
		// Replace or add OIDC provider
		found := false
		for i, provider := range config.Providers {
			if provider.Type == "oidc" {
				config.Providers[i] = oidcProvider
				found = true
				break
			}
		}
		if !found {
			config.Providers = append(config.Providers, oidcProvider)
		}
	}
	
	return config
}
