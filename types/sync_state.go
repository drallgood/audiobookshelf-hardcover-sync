package types

import "time"

// SyncState represents the persistent sync state
type SyncState struct {
	// Timestamp fields
	LastSyncTimestamp int64     `json:"lastSyncTimestamp"` // Unix timestamp in milliseconds
	LastFullSync      int64     `json:"lastFullSync"`      // Unix timestamp of last full sync
	LastSync          time.Time `json:"last_sync"`         // Time of last sync
	
	// Sync metadata
	Version     string `json:"version"`      // State file version for compatibility
	SyncMode    string `json:"sync_mode"`    // Current sync mode (full/incremental)
	SyncCount   int    `json:"sync_count"`   // Total number of syncs completed
	LastSyncID  string `json:"last_sync_id"` // ID of last sync operation
	SyncStatus  string `json:"sync_status"`  // Status of last sync (success/failed)
	
	// Statistics
	BooksProcessed int `json:"books_processed"` // Number of books processed in last sync
	BooksSynced    int `json:"books_synced"`    // Number of books successfully synced
	BooksFailed    int `json:"books_failed"`    // Number of books that failed to sync
}

// State file version
const (
	StateVersion   = "1.0"
)

// Default concurrency settings
const (
	DefaultBatchSize = 10
	DefaultRPS      = 10
)
