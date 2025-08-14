package api

import (
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

// SyncService defines the interface for the sync service
type SyncService interface {
	// GetSummary returns the current sync summary
	GetSummary() *sync.SyncSummary
}
