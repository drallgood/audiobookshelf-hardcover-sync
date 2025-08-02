package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
)

// DefaultSessionManager implements SessionManager interface
type DefaultSessionManager struct {
	db     *gorm.DB
	config SessionConfig
}

// NewSessionManager creates a new session manager
func NewSessionManager(db *gorm.DB, config SessionConfig) *DefaultSessionManager {
	return &DefaultSessionManager{
		db:     db,
		config: config,
	}
}

// CreateSession creates a new session for a user
func (sm *DefaultSessionManager) CreateSession(ctx context.Context, userID string, r *http.Request) (*AuthSession, error) {
	// Generate session token
	token := generateSessionToken()
	
	// Get user agent and IP
	userAgent := r.UserAgent()
	clientIP := getClientIP(r)
	
	// Calculate expiry
	expiresAt := time.Now().Add(time.Duration(sm.config.MaxAge) * time.Second)
	
	session := &AuthSession{
		ID:        generateUserID(), // Reuse the same ID generation function
		UserID:    userID,
		Token:     token,
		ExpiresAt: expiresAt,
		UserAgent: userAgent,
		ClientIP:  clientIP,
		Active:    true,
	}
	
	// Save to database
	if err := sm.db.WithContext(ctx).Create(session).Error; err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	
	return session, nil
}

// GetSession retrieves a session by token
func (sm *DefaultSessionManager) GetSession(ctx context.Context, token string) (*AuthSession, error) {
	var session AuthSession
	err := sm.db.WithContext(ctx).
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

// ValidateSession validates a session and returns the user
func (sm *DefaultSessionManager) ValidateSession(ctx context.Context, token string) (*AuthUser, error) {
	// Get session
	session, err := sm.GetSession(ctx, token)
	if err != nil {
		return nil, err
	}
	
	// Get user
	var user AuthUser
	err = sm.db.WithContext(ctx).
		Where("id = ? AND active = ?", session.UserID, true).
		First(&user).Error
	
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found or inactive")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	
	// Update session last activity
	session.LastActivity = time.Now()
	sm.db.WithContext(ctx).Save(session)
	
	return &user, nil
}

// DestroySession destroys a session
func (sm *DefaultSessionManager) DestroySession(ctx context.Context, token string) error {
	result := sm.db.WithContext(ctx).
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

// CleanupExpiredSessions removes expired sessions
func (sm *DefaultSessionManager) CleanupExpiredSessions(ctx context.Context) error {
	result := sm.db.WithContext(ctx).
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

// DestroyUserSessions destroys all sessions for a user
func (sm *DefaultSessionManager) DestroyUserSessions(ctx context.Context, userID string) error {
	result := sm.db.WithContext(ctx).
		Model(&AuthSession{}).
		Where("user_id = ?", userID).
		Update("active", false)
	
	if result.Error != nil {
		return fmt.Errorf("failed to destroy user sessions: %w", result.Error)
	}
	
	return nil
}

// GetUserSessions gets all active sessions for a user
func (sm *DefaultSessionManager) GetUserSessions(ctx context.Context, userID string) ([]AuthSession, error) {
	var sessions []AuthSession
	err := sm.db.WithContext(ctx).
		Where("user_id = ? AND active = ? AND expires_at > ?", userID, true, time.Now()).
		Order("last_activity DESC").
		Find(&sessions).Error
	
	if err != nil {
		return nil, fmt.Errorf("failed to get user sessions: %w", err)
	}
	
	return sessions, nil
}

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// Check for X-Forwarded-For header (proxy/load balancer)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		if idx := len(xff); idx > 0 {
			if commaIdx := 0; commaIdx < idx {
				for i, c := range xff {
					if c == ',' {
						commaIdx = i
						break
					}
				}
				if commaIdx > 0 {
					return xff[:commaIdx]
				}
			}
			return xff
		}
	}
	
	// Check for X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	
	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// SetSessionCookie sets the session cookie on the response
func (sm *DefaultSessionManager) SetSessionCookie(w http.ResponseWriter, token string) {
	cookie := &http.Cookie{
		Name:     sm.config.CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   sm.config.MaxAge,
		HttpOnly: sm.config.HttpOnly,
		Secure:   sm.config.Secure,
	}
	
	// Set SameSite attribute
	switch sm.config.SameSite {
	case "Strict":
		cookie.SameSite = http.SameSiteStrictMode
	case "Lax":
		cookie.SameSite = http.SameSiteLaxMode
	case "None":
		cookie.SameSite = http.SameSiteNoneMode
	default:
		cookie.SameSite = http.SameSiteLaxMode
	}
	
	http.SetCookie(w, cookie)
}

// ClearSessionCookie clears the session cookie
func (sm *DefaultSessionManager) ClearSessionCookie(w http.ResponseWriter) {
	cookie := &http.Cookie{
		Name:     sm.config.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: sm.config.HttpOnly,
		Secure:   sm.config.Secure,
	}
	
	http.SetCookie(w, cookie)
}

// GetSessionFromRequest extracts session token from request
func (sm *DefaultSessionManager) GetSessionFromRequest(r *http.Request) string {
	// Try cookie first
	if cookie, err := r.Cookie(sm.config.CookieName); err == nil {
		return cookie.Value
	}
	
	// Try Authorization header as fallback
	if auth := r.Header.Get("Authorization"); auth != "" {
		if len(auth) > 7 && auth[:7] == "Bearer " {
			return auth[7:]
		}
	}
	
	return ""
}
