package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// SyncSnapshotJSON stores a serialized sync snapshot. Large libraries can
// produce snapshots that exceed MySQL's regular TEXT limit, while SQLite and
// PostgreSQL do not need a vendor-specific large-text type.
type SyncSnapshotJSON string

// GormDataType identifies the portable Go-side data type for GORM plugins and
// schema inspection.
func (SyncSnapshotJSON) GormDataType() string {
	return "string"
}

// GormDBDataType selects a database-native large text type. MariaDB uses the
// MySQL dialector in this project, so it receives the same LONGTEXT mapping.
func (SyncSnapshotJSON) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql" {
		return "LONGTEXT"
	}
	return "TEXT"
}

// SyncProfile represents a sync profile in the system
type SyncProfile struct {
	ID          string    `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"not null" json:"name"`
	OwnerUserID *string   `gorm:"index" json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Active      bool      `gorm:"default:true" json:"active"`

	// Relationships
	Config    *SyncProfileConfig `gorm:"foreignKey:ProfileID" json:"config,omitempty"`
	SyncState *ProfileSyncState  `gorm:"foreignKey:ProfileID" json:"sync_state,omitempty"`
}

// SyncProfileConfig holds the configuration for a specific sync profile
type SyncProfileConfig struct {
	ProfileID                    string    `gorm:"primaryKey;column:profile_id" json:"profile_id"`
	AudiobookshelfURL            string    `json:"audiobookshelf_url"`
	AudiobookshelfTokenEncrypted string    `json:"-"`                  // Hidden from JSON serialization
	HardcoverTokenEncrypted      string    `json:"-"`                  // Hidden from JSON serialization
	SyncConfig                   string    `gorm:"type:text" json:"-"` // JSON string (hidden from API responses)
	CreatedAt                    time.Time `json:"created_at"`
	UpdatedAt                    time.Time `json:"updated_at"`

	// Relationship
	Profile SyncProfile `gorm:"foreignKey:ProfileID" json:"-"`
}

// ProfileSyncState holds the sync state for a specific profile
type ProfileSyncState struct {
	ProfileID               string     `gorm:"primaryKey;column:profile_id" json:"profile_id"`
	LastAttemptedAt         *time.Time `json:"last_attempted_at"`
	LastAttemptedRunID      string     `json:"last_attempted_run_id"`
	LastAttemptedGeneration uint64     `gorm:"not null;default:0" json:"last_attempted_generation"`
	LastSuccessfulAt        *time.Time `json:"last_successful_at"`

	// Relationship
	Profile SyncProfile `gorm:"foreignKey:ProfileID" json:"profile,omitempty"`
}

// SyncRunReport is the durable, sanitized snapshot for one sync run.
//
// Run IDs are scoped to a profile. Generation is allocated monotonically per
// profile and is indexed to make newest-report queries inexpensive.
type SyncRunReport struct {
	ProfileID           string           `gorm:"primaryKey;column:profile_id;index:idx_sync_run_reports_profile_generation,priority:1" json:"profile_id"`
	RunID               string           `gorm:"primaryKey;column:run_id" json:"run_id"`
	Generation          uint64           `gorm:"not null;index:idx_sync_run_reports_profile_generation,priority:2" json:"generation"`
	Phase               string           `gorm:"not null" json:"phase"`
	DryRun              bool             `json:"dry_run"`
	QueuedAt            *time.Time       `json:"queued_at"`
	ProcessingStartedAt *time.Time       `json:"processing_started_at"`
	LastActivityAt      *time.Time       `json:"last_activity_at"`
	LastProcessedAt     *time.Time       `json:"last_processed_at"`
	FinishedAt          *time.Time       `json:"finished_at"`
	RunError            string           `gorm:"type:text" json:"run_error,omitempty"`
	SnapshotJSON        SyncSnapshotJSON `gorm:"column:snapshot_json" json:"snapshot_json"`
}

const (
	SyncRunPhaseQueued     = "queued"
	SyncRunPhaseRunning    = "running"
	SyncRunPhaseFinalizing = "finalizing"
	SyncRunPhaseCompleted  = "completed"
	SyncRunPhaseFailed     = "failed"
	SyncRunPhaseCanceled   = "canceled"
)

// SyncConfigData represents the structure of sync configuration
type SyncConfigData struct {
	Incremental        bool   `json:"incremental"`
	StateFile          string `json:"state_file"`
	MinChangeThreshold int    `json:"min_change_threshold"`
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
	AudnexusRegion     string  `json:"audnexus_region"`
	incrementalSet     bool
	syncWantToReadSet  bool
	processUnreadSet   bool
	syncOwnedSet       bool
	includeEbooksSet   bool
	dryRunSet          bool
	audnexusRegionSet  bool
}

// UnmarshalJSON records whether boolean and region fields were sent so profile
// updates can distinguish explicit zero values from omitted fields.
func (s *SyncConfigData) UnmarshalJSON(data []byte) error {
	type syncConfigAlias SyncConfigData
	decoded := syncConfigAlias(*s)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name, value := range fields {
		if isTrackedSyncConfigField(name) && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("sync config field %q cannot be null", name)
		}
	}
	*s = SyncConfigData(decoded)
	s.incrementalSet = false
	s.syncWantToReadSet = false
	s.processUnreadSet = false
	s.syncOwnedSet = false
	s.includeEbooksSet = false
	s.dryRunSet = false
	s.audnexusRegionSet = false
	for name := range fields {
		switch {
		case strings.EqualFold(name, "incremental"):
			s.incrementalSet = true
		case strings.EqualFold(name, "sync_want_to_read"):
			s.syncWantToReadSet = true
		case strings.EqualFold(name, "process_unread_books"):
			s.processUnreadSet = true
		case strings.EqualFold(name, "sync_owned"):
			s.syncOwnedSet = true
		case strings.EqualFold(name, "include_ebooks"):
			s.includeEbooksSet = true
		case strings.EqualFold(name, "dry_run"):
			s.dryRunSet = true
		case strings.EqualFold(name, "audnexus_region"):
			s.audnexusRegionSet = true
		}
	}
	return nil
}

func isTrackedSyncConfigField(name string) bool {
	switch {
	case strings.EqualFold(name, "incremental"),
		strings.EqualFold(name, "sync_want_to_read"),
		strings.EqualFold(name, "process_unread_books"),
		strings.EqualFold(name, "sync_owned"),
		strings.EqualFold(name, "include_ebooks"),
		strings.EqualFold(name, "dry_run"),
		strings.EqualFold(name, "audnexus_region"):
		return true
	default:
		return false
	}
}

// IsEmpty checks whether SyncConfigData contains no provided values.
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
		!s.incrementalSet &&
		!s.syncWantToReadSet &&
		!s.processUnreadSet &&
		!s.syncOwnedSet &&
		!s.includeEbooksSet &&
		!s.dryRunSet &&
		s.TestBookFilter == "" &&
		s.TestBookLimit == 0 &&
		s.AudnexusRegion == "" &&
		!s.audnexusRegionSet
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
