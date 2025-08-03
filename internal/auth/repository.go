package auth

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// AuthRepository handles database operations for authentication
type AuthRepository struct {
	db *gorm.DB
}

// NewAuthRepository creates a new authentication repository
func NewAuthRepository(db *gorm.DB) *AuthRepository {
	return &AuthRepository{db: db}
}

// User operations

// CreateUser creates a new authentication user
func (r *AuthRepository) CreateUser(ctx context.Context, user *AuthUser) error {
	user.ID = generateUserID()
	if err := r.db.WithContext(ctx).Create(user).Error; err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// GetUserByID retrieves a user by ID
func (r *AuthRepository) GetUserByID(ctx context.Context, id string) (*AuthUser, error) {
	var user AuthUser
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &user, nil
}

// GetUserByUsername retrieves a user by username
func (r *AuthRepository) GetUserByUsername(ctx context.Context, username string) (*AuthUser, error) {
	var user AuthUser
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &user, nil
}

// GetUserByEmail retrieves a user by email
func (r *AuthRepository) GetUserByEmail(ctx context.Context, email string) (*AuthUser, error) {
	var user AuthUser
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &user, nil
}

// GetUserByProviderID retrieves a user by provider and provider ID
func (r *AuthRepository) GetUserByProviderID(ctx context.Context, provider, providerID string) (*AuthUser, error) {
	var user AuthUser
	err := r.db.WithContext(ctx).
		Where("provider = ? AND provider_id = ?", provider, providerID).
		First(&user).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &user, nil
}

// UpdateUser updates a user
func (r *AuthRepository) UpdateUser(ctx context.Context, user *AuthUser) error {
	if err := r.db.WithContext(ctx).Save(user).Error; err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}
	return nil
}

// DeleteUser deletes a user (soft delete)
func (r *AuthRepository) DeleteUser(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Model(&AuthUser{}).Where("id = ?", id).Update("active", false)
	if result.Error != nil {
		return fmt.Errorf("failed to delete user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// ListUsers lists all active users
func (r *AuthRepository) ListUsers(ctx context.Context) ([]AuthUser, error) {
	var users []AuthUser
	err := r.db.WithContext(ctx).Where("active = ?", true).Find(&users).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	return users, nil
}

// Session operations

// CreateSession creates a new session
func (r *AuthRepository) CreateSession(ctx context.Context, session *AuthSession) error {
	session.ID = generateUserID()
	if err := r.db.WithContext(ctx).Create(session).Error; err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// GetSessionByToken retrieves a session by token
func (r *AuthRepository) GetSessionByToken(ctx context.Context, token string) (*AuthSession, error) {
	var session AuthSession
	err := r.db.WithContext(ctx).
		Where("token = ? AND active = ? AND expires_at > ?", token, true, time.Now()).
		First(&session).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("session not found or expired")
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return &session, nil
}

// UpdateSession updates a session
func (r *AuthRepository) UpdateSession(ctx context.Context, session *AuthSession) error {
	if err := r.db.WithContext(ctx).Save(session).Error; err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}
	return nil
}

// DestroySession destroys a session
func (r *AuthRepository) DestroySession(ctx context.Context, token string) error {
	result := r.db.WithContext(ctx).
		Model(&AuthSession{}).
		Where("token = ?", token).
		Update("active", false)
	if result.Error != nil {
		return fmt.Errorf("failed to destroy session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

// DestroyUserSessions destroys all sessions for a user
func (r *AuthRepository) DestroyUserSessions(ctx context.Context, userID string) error {
	result := r.db.WithContext(ctx).
		Model(&AuthSession{}).
		Where("user_id = ?", userID).
		Update("active", false)
	if result.Error != nil {
		return fmt.Errorf("failed to destroy user sessions: %w", result.Error)
	}
	return nil
}

// CleanupExpiredSessions removes expired sessions
func (r *AuthRepository) CleanupExpiredSessions(ctx context.Context) error {
	result := r.db.WithContext(ctx).
		Where("expires_at < ? OR (active = ? AND last_activity < ?)",
			time.Now(),
			true,
			time.Now().Add(-24*time.Hour)).
		Delete(&AuthSession{})
	if result.Error != nil {
		return fmt.Errorf("failed to cleanup expired sessions: %w", result.Error)
	}
	return nil
}

// GetUserSessions gets all active sessions for a user
func (r *AuthRepository) GetUserSessions(ctx context.Context, userID string) ([]AuthSession, error) {
	var sessions []AuthSession
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND active = ? AND expires_at > ?", userID, true, time.Now()).
		Order("last_activity DESC").
		Find(&sessions).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get user sessions: %w", err)
	}
	return sessions, nil
}

// Provider operations

// CreateProvider creates a new authentication provider
func (r *AuthRepository) CreateProvider(ctx context.Context, provider *AuthProvider) error {
	provider.ID = generateUserID()
	if err := r.db.WithContext(ctx).Create(provider).Error; err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}
	return nil
}

// GetProviderByName retrieves a provider by name
func (r *AuthRepository) GetProviderByName(ctx context.Context, name string) (*AuthProvider, error) {
	var provider AuthProvider
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&provider).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("provider not found")
		}
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}
	return &provider, nil
}

// ListProviders lists all providers
func (r *AuthRepository) ListProviders(ctx context.Context) ([]AuthProvider, error) {
	var providers []AuthProvider
	err := r.db.WithContext(ctx).Find(&providers).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list providers: %w", err)
	}
	return providers, nil
}

// ListEnabledProviders lists all enabled providers
func (r *AuthRepository) ListEnabledProviders(ctx context.Context) ([]AuthProvider, error) {
	var providers []AuthProvider
	err := r.db.WithContext(ctx).Where("enabled = ?", true).Find(&providers).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list enabled providers: %w", err)
	}
	return providers, nil
}

// UpdateProvider updates a provider
func (r *AuthRepository) UpdateProvider(ctx context.Context, provider *AuthProvider) error {
	if err := r.db.WithContext(ctx).Save(provider).Error; err != nil {
		return fmt.Errorf("failed to update provider: %w", err)
	}
	return nil
}

// DeleteProvider deletes a provider
func (r *AuthRepository) DeleteProvider(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Delete(&AuthProvider{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete provider: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("provider not found")
	}
	return nil
}

// Utility methods

// UserExists checks if a user exists by username or email
func (r *AuthRepository) UserExists(ctx context.Context, username, email string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&AuthUser{}).
		Where("username = ? OR email = ?", username, email).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("failed to check user existence: %w", err)
	}
	return count > 0, nil
}

// GetUserCount returns the total number of active users
func (r *AuthRepository) GetUserCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&AuthUser{}).Where("active = ?", true).Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("failed to get user count: %w", err)
	}
	return count, nil
}

// CreateDefaultAdminUser creates a default admin user if no users exist
func (r *AuthRepository) CreateDefaultAdminUser(ctx context.Context, username, email, password string) error {
	// Check if any users exist
	count, err := r.GetUserCount(ctx)
	if err != nil {
		return err
	}
	
	if count > 0 {
		// If admin user exists, update its password to match config
		var existingUser AuthUser
		err := r.db.WithContext(ctx).Where("username = ? AND role = ?", username, "admin").First(&existingUser).Error
		if err == nil {
			// Admin user exists, update password
			user, err := CreateLocalUser(username, email, password, RoleAdmin)
			if err != nil {
				return fmt.Errorf("failed to create updated admin user: %w", err)
			}
			existingUser.PasswordHash = user.PasswordHash
			return r.db.WithContext(ctx).Save(&existingUser).Error
		}
		return nil // Users exist but no admin found
	}
	
	// Create default admin user
	user, err := CreateLocalUser(username, email, password, RoleAdmin)
	if err != nil {
		return fmt.Errorf("failed to create default admin user: %w", err)
	}
	
	return r.CreateUser(ctx, user)
}
