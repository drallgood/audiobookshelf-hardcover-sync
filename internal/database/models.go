package database

import (
	"time"

	"gorm.io/gorm"
)

// SyncProfile represents a sync profile in the system
type SyncProfile struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"not null" json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Active    bool      `gorm:"default:true" json:"active"`

	// Relationships
	Config    *SyncProfileConfig `gorm:"foreignKey:ProfileID" json:"config,omitempty"`
	SyncState *ProfileSyncState  `gorm:"foreignKey:ProfileID" json:"sync_state,omitempty"`
}

// SyncProfileConfig holds the configuration for a specific sync profile
type SyncProfileConfig struct {
	ProfileID                  string `gorm:"primaryKey;column:profile_id" json:"profile_id"`
	AudiobookshelfURL          string `json:"audiobookshelf_url"`
	AudiobookshelfTokenEncrypted string `json:"-"` // Hidden from JSON serialization
	HardcoverTokenEncrypted    string `json:"-"` // Hidden from JSON serialization
	SyncConfig                 string `gorm:"type:text" json:"-"` // JSON string (hidden from API responses)
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`

	// Relationship
	Profile SyncProfile `gorm:"foreignKey:ProfileID" json:"-"`
}

// ProfileSyncState holds the sync state for a specific profile
type ProfileSyncState struct {
	ProfileID string     `gorm:"primaryKey;column:profile_id" json:"profile_id"`
	StateData string     `gorm:"type:text" json:"state_data"` // JSON string
	LastSync  *time.Time `json:"last_sync"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`

	// Relationship
	Profile SyncProfile `gorm:"foreignKey:ProfileID" json:"profile,omitempty"`
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
	SyncInterval       string  `json:"sync_interval"`
	MinimumProgress    float64 `json:"minimum_progress"`
	SyncWantToRead     bool    `json:"sync_want_to_read"`
	ProcessUnreadBooks bool    `json:"process_unread_books"`
	SyncOwned          bool    `json:"sync_owned"`
	IncludeEbooks      bool    `json:"include_ebooks"`
	DryRun             bool    `json:"dry_run"`
	TestBookFilter     string  `json:"test_book_filter"`
	TestBookLimit      int     `json:"test_book_limit"`
}

// IsEmpty checks if the SyncConfigData is empty (all fields at their zero values)
func (s SyncConfigData) IsEmpty() bool {
	return !s.Incremental &&
		s.StateFile == "" &&
		s.MinChangeThreshold == 0 &&
		len(s.Libraries.Include) == 0 &&
		len(s.Libraries.Exclude) == 0 &&
		s.SyncInterval == "" &&
		s.MinimumProgress == 0 &&
		!s.SyncWantToRead &&
		!s.ProcessUnreadBooks &&
		!s.SyncOwned &&
		!s.IncludeEbooks &&
		!s.DryRun &&
		s.TestBookFilter == "" &&
		s.TestBookLimit == 0
}

// BeforeCreate hook for SyncProfile
func (p *SyncProfile) BeforeCreate(tx *gorm.DB) error {
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	return nil
}

// BeforeUpdate hook for SyncProfile
func (p *SyncProfile) BeforeUpdate(tx *gorm.DB) error {
	p.UpdatedAt = time.Now()
	return nil
}

// BeforeCreate hook for SyncProfileConfig
func (c *SyncProfileConfig) BeforeCreate(tx *gorm.DB) error {
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	return nil
}

// BeforeUpdate hook for SyncProfileConfig
func (c *SyncProfileConfig) BeforeUpdate(tx *gorm.DB) error {
	c.UpdatedAt = time.Now()
	return nil
}

// BeforeCreate hook for ProfileSyncState
func (s *ProfileSyncState) BeforeCreate(tx *gorm.DB) error {
	now := time.Now()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = now
	}
	return nil
}

// BeforeUpdate hook for ProfileSyncState
func (s *ProfileSyncState) BeforeUpdate(tx *gorm.DB) error {
	s.UpdatedAt = time.Now()
	return nil
}
