package auth

import (
	"time"

	"gorm.io/gorm"
)

// AuthUser represents a user for authentication purposes
type AuthUser struct {
	ID           string    `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"uniqueIndex;not null" json:"username"`
	Email        string    `gorm:"uniqueIndex" json:"email,omitempty"`
	PasswordHash string    `gorm:"not null" json:"-"` // Never serialize password hash
	Role         string    `gorm:"not null;default:user" json:"role"`
	Provider     string    `gorm:"not null;default:local" json:"provider"`
	ProviderID   string    `gorm:"index" json:"provider_id,omitempty"`
	Active       bool      `gorm:"not null;default:true" json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

// AuthSession represents a user session
type AuthSession struct {
	ID           string    `gorm:"primaryKey" json:"id"`
	UserID       string    `gorm:"not null;index" json:"user_id"`
	Token        string    `gorm:"uniqueIndex;not null" json:"-"` // Don't expose in JSON
	ExpiresAt    time.Time `gorm:"not null;index" json:"expires_at"`
	UserAgent    string    `gorm:"type:text" json:"user_agent"`
	ClientIP     string    `gorm:"type:varchar(45)" json:"client_ip"`
	Active       bool      `gorm:"default:true;index" json:"active"`
	LastActivity time.Time `gorm:"index" json:"last_activity"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Relationships
	User AuthUser `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// AuthProvider represents an authentication provider configuration
type AuthProvider struct {
	ID       string    `gorm:"primaryKey" json:"id"`
	Name     string    `gorm:"uniqueIndex;not null" json:"name"`
	Type     string    `gorm:"not null" json:"type"` // local, oidc, oauth2
	Enabled  bool      `gorm:"default:true" json:"enabled"`
	Config   string    `gorm:"type:text" json:"config"` // JSON configuration
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserRole represents the available user roles
type UserRole string

const (
	RoleAdmin  UserRole = "admin"
	RoleUser   UserRole = "user"
	RoleViewer UserRole = "viewer"
)

// IsValid checks if the role is valid
func (r UserRole) IsValid() bool {
	switch r {
	case RoleAdmin, RoleUser, RoleViewer:
		return true
	default:
		return false
	}
}

// HasPermission checks if the role has a specific permission
func (r UserRole) HasPermission(permission Permission) bool {
	switch r {
	case RoleAdmin:
		return true // Admin has all permissions
	case RoleUser:
		return permission == PermissionReadOwn || permission == PermissionWriteOwn
	case RoleViewer:
		return permission == PermissionReadOwn
	default:
		return false
	}
}

// Permission represents different permission types
type Permission string

const (
	PermissionReadAll   Permission = "read_all"
	PermissionWriteAll  Permission = "write_all"
	PermissionReadOwn   Permission = "read_own"
	PermissionWriteOwn  Permission = "write_own"
	PermissionManageAuth Permission = "manage_auth"
)

// BeforeCreate hook for AuthUser
func (u *AuthUser) BeforeCreate(tx *gorm.DB) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = time.Now()
	}
	return nil
}

// BeforeUpdate hook for AuthUser
func (u *AuthUser) BeforeUpdate(tx *gorm.DB) error {
	u.UpdatedAt = time.Now()
	return nil
}

// BeforeCreate hook for AuthSession
func (s *AuthSession) BeforeCreate(tx *gorm.DB) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now()
	}
	return nil
}

// BeforeUpdate hook for AuthSession
func (s *AuthSession) BeforeUpdate(tx *gorm.DB) error {
	s.UpdatedAt = time.Now()
	return nil
}


