package database

import (
	"time"

	"gorm.io/gorm"
)

// User represents a user in the multi-user system
type User struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"not null" json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Active    bool      `gorm:"default:true" json:"active"`

	// Relationships
	Config    *UserConfig `gorm:"foreignKey:UserID" json:"config,omitempty"`
	SyncState *SyncState  `gorm:"foreignKey:UserID" json:"sync_state,omitempty"`
}

// UserConfig holds the configuration for a specific user
type UserConfig struct {
	UserID                     string `gorm:"primaryKey" json:"user_id"`
	AudiobookshelfURL          string `json:"audiobookshelf_url"`
	AudiobookshelfTokenEncrypted string `json:"-"` // Hidden from JSON serialization
	HardcoverTokenEncrypted    string `json:"-"` // Hidden from JSON serialization
	SyncConfig                 string `gorm:"type:text" json:"sync_config"` // JSON string
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`

	// Relationship
	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// SyncState holds the sync state for a specific user
type SyncState struct {
	UserID    string     `gorm:"primaryKey" json:"user_id"`
	StateData string     `gorm:"type:text" json:"state_data"` // JSON string
	LastSync  *time.Time `json:"last_sync"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`

	// Relationship
	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// SyncConfigData represents the structure of sync configuration
type SyncConfigData struct {
	Incremental        bool     `json:"incremental"`
	StateFile          string   `json:"state_file"`
	MinChangeThreshold int      `json:"min_change_threshold"`
	Libraries          struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"libraries"`
	SyncInterval    string  `json:"sync_interval"`
	MinimumProgress float64 `json:"minimum_progress"`
	SyncWantToRead  bool    `json:"sync_want_to_read"`
	SyncOwned       bool    `json:"sync_owned"`
	DryRun          bool    `json:"dry_run"`
	TestBookFilter  string  `json:"test_book_filter"`
	TestBookLimit   int     `json:"test_book_limit"`
}

// BeforeCreate hook for User
func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = time.Now()
	}
	return nil
}

// BeforeUpdate hook for User
func (u *User) BeforeUpdate(tx *gorm.DB) error {
	u.UpdatedAt = time.Now()
	return nil
}

// BeforeCreate hook for UserConfig
func (uc *UserConfig) BeforeCreate(tx *gorm.DB) error {
	if uc.CreatedAt.IsZero() {
		uc.CreatedAt = time.Now()
	}
	if uc.UpdatedAt.IsZero() {
		uc.UpdatedAt = time.Now()
	}
	return nil
}

// BeforeUpdate hook for UserConfig
func (uc *UserConfig) BeforeUpdate(tx *gorm.DB) error {
	uc.UpdatedAt = time.Now()
	return nil
}

// BeforeCreate hook for SyncState
func (ss *SyncState) BeforeCreate(tx *gorm.DB) error {
	if ss.CreatedAt.IsZero() {
		ss.CreatedAt = time.Now()
	}
	if ss.UpdatedAt.IsZero() {
		ss.UpdatedAt = time.Now()
	}
	return nil
}

// BeforeUpdate hook for SyncState
func (ss *SyncState) BeforeUpdate(tx *gorm.DB) error {
	ss.UpdatedAt = time.Now()
	return nil
}
