package state

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// CurrentVersion is the current version of the sync state format
	CurrentVersion = "2.0"
	// DefaultStateFile is the default path for the sync state file
	DefaultStateFile = "./data/sync_state.json"
)

// State represents the current sync state
type State struct {
	Version      string             `json:"version"`
	LastSync     int64              `json:"lastSync"`
	LastFullSync int64              `json:"lastFullSync"`
	Libraries    map[string]Library `json:"libraries,omitempty"`
	Books        map[string]Book    `json:"books,omitempty"`
	mu           sync.RWMutex       `json:"-"`
}

// Library represents the sync state of a library
type Library struct {
	LastUpdated int64 `json:"lastUpdated"`
}

// Book represents the sync state of a book
type Book struct {
	LastProgress float64 `json:"lastProgress"`
	LastUpdated  int64   `json:"lastUpdated"`
	Status       string  `json:"status,omitempty"` // e.g., "WANT_TO_READ", "IN_PROGRESS", "FINISHED"
}

// NewState creates a new empty state with current version
func NewState() *State {
	return &State{
		Version:      CurrentVersion,
		LastSync:     0,
		LastFullSync: 0,
		Libraries:    make(map[string]Library),
		Books:        make(map[string]Book),
	}
}

// LoadState loads the sync state from a file, migrating if necessary
func LoadState(path string) (*State, error) {
	if path == "" {
		path = DefaultStateFile
	}

	targetDir := filepath.Dir(path)

	// Ensure directory exists with proper permissions
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory %q: %w", targetDir, err)
	}

	// Read file if it exists
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return new state if file doesn't exist
			state := NewState()
			// Save the new state file to ensure the directory is writable
			if err := state.Save(path); err != nil {
				return nil, fmt.Errorf("failed to initialize new state file at %q: %w", path, err)
			}
			return state, nil
		}
		return nil, fmt.Errorf("failed to read state file at %q: %w", path, err)
	}

	// Try to detect version
	var version struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return nil, fmt.Errorf("invalid state file format: %w", err)
	}

	var state *State
	switch version.Version {
	case "", "1.0":
		// Migrate from v1 to v2
		var v1 v1State
		if err := json.Unmarshal(data, &v1); err != nil {
			return nil, fmt.Errorf("failed to parse v1 state: %w", err)
		}
		state = migrateV1ToV2(v1)
	case CurrentVersion:
		// Current version - initialize with empty maps first
		state = &State{
			Libraries: make(map[string]Library),
			Books:     make(map[string]Book),
		}
		if err := json.Unmarshal(data, state); err != nil {
			return nil, fmt.Errorf("failed to parse state: %w", err)
		}
		// Ensure maps are not nil after unmarshal
		if state.Libraries == nil {
			state.Libraries = make(map[string]Library)
		}
		if state.Books == nil {
			state.Books = make(map[string]Book)
		}
	default:
		return nil, fmt.Errorf("unsupported state version: %s", version.Version)
	}

	return state, nil
}

// Save writes the state to a file
func (s *State) Save(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if path == "" {
		path = DefaultStateFile
	}

	targetDir := filepath.Dir(path)

	// Ensure directory exists with proper permissions
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory %q: %w", targetDir, err)
	}

	// Create temp file in the same directory as the target file
	tmpFile, err := os.CreateTemp(targetDir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %q: %w", targetDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		if _, err := os.Stat(tmpPath); err == nil {
			os.Remove(tmpPath)
		}
	}()

	// Write JSON with indentation
	encoder := json.NewEncoder(tmpFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s); err != nil {
		return fmt.Errorf("failed to encode state: %w", err)
	}

	// Ensure data is written to disk
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync state file: %w", err)
	}

	// Close the file before renaming (required on Windows)
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// On Windows, we need to make sure the target file doesn't exist before renaming
	if _, err := os.Stat(path); err == nil {
		// File exists, remove it first
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove existing state file: %w", err)
		}
	}

	// Rename temp file to final path
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file to %q: %w", path, err)
	}

	// Ensure the file has the correct permissions
	if err := os.Chmod(path, 0644); err != nil {
		return fmt.Errorf("failed to set permissions on state file: %w", err)
	}

	return nil
}

// UpdateBook updates the state for a book if there are actual changes
// Returns true if the state was updated, false if no changes were needed
// bookID should be in the format "bookID:editionID" to handle multiple editions
func (s *State) UpdateBook(bookID string, progress float64, status string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	debugLog := false
	
	// Check if we already have state for this book
	if existing, exists := s.Books[bookID]; exists {
		// Calculate if progress has changed significantly (more than 0.1%)
		progressDiff := math.Abs(existing.LastProgress - progress)
		progressChanged := progressDiff > 0.001
		statusChanged := existing.Status != status
		
		// Debug logging for Scrum book
		if strings.Contains(strings.ToLower(bookID), "scrum") {
			debugLog = true
			log.Printf("DEBUG - UpdateBook for Scrum - ID: %s, Progress: %.4f -> %.4f (diff: %.4f), Status: %s -> %s",
				bookID, existing.LastProgress, progress, progressDiff, existing.Status, status)
		}
		
		// Only update if something has changed
		if !progressChanged && !statusChanged {
			if debugLog {
				log.Printf("DEBUG - No update needed for book %s - no significant changes", bookID)
			}
			return false
		}
		
		// Update only the changed fields
		s.Books[bookID] = Book{
			LastProgress: progress,
			LastUpdated:  now,
			Status:       status,
		}
		
		if debugLog {
			log.Printf("DEBUG - Updated book %s state - progress: %.4f, status: %s", bookID, progress, status)
		}
	} else {
		// New book, always update
		s.Books[bookID] = Book{
			LastProgress: progress,
			LastUpdated:  now,
			Status:       status,
		}
		
		if strings.Contains(strings.ToLower(bookID), "scrum") {
			log.Printf("DEBUG - Created new state for Scrum book %s - progress: %.4f, status: %s", bookID, progress, status)
		}
	}
	
	s.LastSync = now
	return true
}

// UpdateLibrary updates the state for a library
func (s *State) UpdateLibrary(libraryID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	s.Libraries[libraryID] = Library{
		LastUpdated: now,
	}
	s.LastSync = now
}

// SetFullSync updates the last full sync timestamp
func (s *State) SetFullSync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.LastFullSync = time.Now().Unix()
}

// NeedsSync checks if a book needs syncing based on changes since last sync
// Returns true if the book should be processed, false if it can be skipped
func (s *State) NeedsSync(bookID string, currentProgress float64, currentStatus string, minChangeThreshold float64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	lastBook, exists := s.Books[bookID]
	if !exists {
		// New book, needs sync
		return true
	}
	
	// Check if status changed
	if lastBook.Status != currentStatus {
		return true
	}
	
	// Check if progress changed significantly
	progressDiff := math.Abs(currentProgress - lastBook.LastProgress)
	if progressDiff >= minChangeThreshold {
		return true
	}
	
	// No significant changes
	return false
}

// GetBookState returns the last known state of a book
func (s *State) GetBookState(bookID string) (Book, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	book, exists := s.Books[bookID]
	return book, exists
}

// GetStaleBooks returns books that haven't been updated in a while and might need refresh
func (s *State) GetStaleBooks(maxAge time.Duration) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	cutoff := time.Now().Add(-maxAge).Unix()
	var staleBooks []string
	
	for bookID, book := range s.Books {
		if book.LastUpdated < cutoff {
			staleBooks = append(staleBooks, bookID)
		}
	}
	
	return staleBooks
}

// v1State represents the version 1.0 state format
// This is used for migration purposes only
type v1State struct {
	LastSyncTimestamp int64  `json:"lastSyncTimestamp"`
	LastFullSync      int64  `json:"lastFullSync"`
	Version           string `json:"version"`
}

// migrateV1ToV2 migrates a v1 state to v2
func migrateV1ToV2(v1 v1State) *State {
	return &State{
		Version:      CurrentVersion,
		LastSync:     v1.LastSyncTimestamp / 1000, // Convert ms to s
		LastFullSync: v1.LastFullSync / 1000,      // Convert ms to s
		Libraries:    make(map[string]Library),
		Books:        make(map[string]Book),
	}
}
