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
	CurrentVersion   = "2.0"
	DefaultStateFile = "./data/sync_state.json"
)

type State struct {
	Version      string             `json:"version"`
	LastSync     int64              `json:"lastSync"`
	LastFullSync int64              `json:"lastFullSync"`
	Libraries    map[string]Library `json:"libraries,omitempty"`
	Books        map[string]Book    `json:"books,omitempty"`
	mu           sync.RWMutex       `json:"-"`
}

type Library struct {
	LastUpdated int64 `json:"lastUpdated"`
}

type Book struct {
	LastProgress       float64 `json:"lastProgress"`
	LastUpdated        int64   `json:"lastUpdated"`
	Status             string  `json:"status,omitempty"`
	UserBookID         string  `json:"userBookID,omitempty"`
	HasProgressSeconds bool    `json:"hasProgressSeconds,omitempty"`
}

func NewState() *State {
	return &State{
		Version:      CurrentVersion,
		LastSync:     0,
		LastFullSync: 0,
		Libraries:    make(map[string]Library),
		Books:        make(map[string]Book),
	}
}

func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewState(), nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	version, _ := raw["version"].(string)
	if version == "" || version == "1.0" {
		log.Println("INFO - Migrating state from v1 to v2")
		var v1 v1State
		if err := json.Unmarshal(data, &v1); err != nil {
			return nil, fmt.Errorf("failed to parse v1 state: %w", err)
		}
		return migrateV1ToV2(v1), nil
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state: %w", err)
	}

	if state.Books == nil {
		state.Books = make(map[string]Book)
	}
	if state.Libraries == nil {
		state.Libraries = make(map[string]Library)
	}

	return &state, nil
}

func (s *State) Save(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

func (s *State) UpdateBook(bookID string, progress float64, status string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	debugLog := false
	if strings.Contains(strings.ToLower(bookID), "scrum") {
		debugLog = true
	}

	now := time.Now().Unix()
	normalizedProgress := normalizeProgress(progress)

	updated := false

	if existing, exists := s.Books[bookID]; exists {
		storedProgress := existing.LastProgress
		if storedProgress > 1.0 {
			storedProgress = storedProgress / 100.0
		}

		progressDiff := math.Abs(storedProgress - normalizedProgress)
		progressChanged := progressDiff >= 0.001
		statusChanged := existing.Status != status

		if debugLog {
			log.Printf("DEBUG - UpdateBook for %s (existing) - stored: %.4f, new: %.4f, storedStatus: %s, newStatus: %s, progressChanged: %v, statusChanged: %v",
				bookID, storedProgress, normalizedProgress, existing.Status, status, progressChanged, statusChanged)
		}

		if !progressChanged && !statusChanged {
			if debugLog {
				log.Printf("DEBUG - No update needed for book %s - no significant changes", bookID)
			}
		} else {
			oldBook := s.Books[bookID]
			s.Books[bookID] = Book{
				LastProgress:       normalizedProgress,
				LastUpdated:        now,
				Status:             status,
				UserBookID:         oldBook.UserBookID,
				HasProgressSeconds: oldBook.HasProgressSeconds,
			}
			updated = true
			if debugLog {
				log.Printf("DEBUG - Updated book %s state - progress: %.4f, status: %s", bookID, normalizedProgress, status)
			}
		}
	} else {
		s.Books[bookID] = Book{
			LastProgress: normalizedProgress,
			LastUpdated:  now,
			Status:       status,
		}
		updated = true

		if strings.Contains(strings.ToLower(bookID), "scrum") {
			log.Printf("DEBUG - Created new state for Scrum book %s - progress: %.4f, status: %s", bookID, normalizedProgress, status)
		}
	}

	if baseID := strings.SplitN(bookID, ":", 2)[0]; baseID != "" && baseID != bookID {
		if existing, exists := s.Books[baseID]; exists {
			storedProgress := existing.LastProgress
			if storedProgress > 1.0 {
				storedProgress = storedProgress / 100.0
			}
			progressDiff := math.Abs(storedProgress - normalizedProgress)
			statusChanged := existing.Status != status

			if progressDiff >= 0.001 || statusChanged {
				oldBook := s.Books[baseID]
				s.Books[baseID] = Book{
					LastProgress:       normalizedProgress,
					LastUpdated:        now,
					Status:             status,
					UserBookID:         oldBook.UserBookID,
					HasProgressSeconds: oldBook.HasProgressSeconds,
				}
			}
		} else {
			s.Books[baseID] = Book{
				LastProgress: normalizedProgress,
				LastUpdated:  now,
				Status:       status,
			}
		}
	}

	s.LastSync = now
	return updated
}

func (s *State) UpdateLibrary(libraryID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	s.Libraries[libraryID] = Library{
		LastUpdated: now,
	}
	s.LastSync = now
}

func (s *State) SetFullSync() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.LastFullSync = time.Now().Unix()
}

func (s *State) NeedsSync(bookID string, currentProgress float64, currentStatus string, minChangeThreshold float64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lastBook, exists := s.Books[bookID]
	if !exists {
		return true
	}

	if !lastBook.HasProgressSeconds {
		return true
	}

	if lastBook.Status != currentStatus {
		return true
	}

	storedProgress := lastBook.LastProgress
	if storedProgress > 1.0 {
		storedProgress = storedProgress / 100.0
	}
	normalizedCurrent := currentProgress
	if normalizedCurrent > 1.0 {
		normalizedCurrent = normalizedCurrent / 100.0
	}

	progressDiff := math.Abs(normalizedCurrent - storedProgress)
	if progressDiff >= minChangeThreshold {
		return true
	}

	return false
}

func (s *State) GetBookState(bookID string) (Book, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	book, exists := s.Books[bookID]
	return book, exists
}

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

func (s *State) UpdateBookWithUserBookID(bookID string, progress float64, status string, userBookID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	normalizedProgress := normalizeProgress(progress)

	oldBook, exists := s.Books[bookID]
	hasProgressSeconds := false
	if exists {
		hasProgressSeconds = oldBook.HasProgressSeconds
	}

	s.Books[bookID] = Book{
		LastProgress:       normalizedProgress,
		LastUpdated:        now,
		Status:             status,
		UserBookID:         userBookID,
		HasProgressSeconds: hasProgressSeconds,
	}

	s.LastSync = now
}

func normalizeProgress(progress float64) float64 {
	if progress > 1.0 {
		return progress / 100.0
	}
	if progress < 0 {
		return 0
	}
	return progress
}

type v1State struct {
	LastSyncTimestamp int64  `json:"lastSyncTimestamp"`
	LastFullSync      int64  `json:"lastFullSync"`
	Version           string `json:"version"`
}

func migrateV1ToV2(v1 v1State) *State {
	return &State{
		Version:      CurrentVersion,
		LastSync:     v1.LastSyncTimestamp / 1000,
		LastFullSync: v1.LastFullSync / 1000,
		Libraries:    make(map[string]Library),
		Books:        make(map[string]Book),
	}
}

func (s *State) SetHasProgressSeconds(bookID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if book, exists := s.Books[bookID]; exists {
		book.HasProgressSeconds = true
		s.Books[bookID] = book
	}

	if baseID := strings.SplitN(bookID, ":", 2)[0]; baseID != "" && baseID != bookID {
		if book, exists := s.Books[baseID]; exists {
			book.HasProgressSeconds = true
			s.Books[baseID] = book
		}
	}
}