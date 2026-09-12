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
	dirty        bool               `json:"-"`
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
	s.mu.Lock()
	defer s.mu.Unlock()

	targetPath, err := resolveStatePath(path)
	if err != nil {
		return fmt.Errorf("failed to resolve state file: %w", err)
	}
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// CreateTemp uses 0600, so new state files do not expose book IDs or
	// progress details to other users. Existing files retain their permissions.
	fileMode := os.FileMode(0600)
	if info, err := os.Stat(targetPath); err == nil {
		fileMode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat state file: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	tempFile, err := os.CreateTemp(dir, ".sync-state-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary state file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if err := tempFile.Chmod(fileMode); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed to set temporary state file permissions: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed to write temporary state file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed to sync temporary state file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary state file: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("failed to replace state file: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("failed to sync state directory: %w", err)
	}
	s.dirty = false

	return nil
}

const maxStateSymlinkDepth = 255

// resolveStatePath follows the configured state path to the file that should
// be replaced. Resolving the path before the atomic rename keeps a configured
// symlink in place, including when its target does not exist yet. Components
// are resolved in filesystem order rather than cleaning the path first. This
// matters for paths such as "link/../state": the ".." is relative to the
// symlink target, not to the directory containing the symlink.
func resolveStatePath(path string) (string, error) {
	absPath, err := absoluteStatePath(path)
	if err != nil {
		return "", fmt.Errorf("failed to make state path absolute: %w", err)
	}

	base, components := splitStatePath(absPath)
	return resolveStatePathComponents(base, components, make(map[string]struct{}), 0)
}

func absoluteStatePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if path == "" {
		return workingDir, nil
	}
	// Do not use filepath.Join here: it cleans away ".." before the
	// component-by-component resolver can apply it after symlink expansion.
	return workingDir + string(filepath.Separator) + path, nil
}

func splitStatePath(path string) (string, []string) {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	if filepath.IsAbs(path) {
		root := volume + string(filepath.Separator)
		remainder = strings.TrimLeft(remainder, string(filepath.Separator))
		return root, strings.Split(remainder, string(filepath.Separator))
	}

	return "", strings.Split(remainder, string(filepath.Separator))
}

func resolveStatePathComponents(base string, components []string, visited map[string]struct{}, depth int) (string, error) {
	if len(components) == 0 {
		return base, nil
	}

	component := components[0]
	rest := components[1:]
	if component == "" || component == "." {
		return resolveStatePathComponents(base, rest, visited, depth)
	}
	if component == ".." {
		return resolveStatePathComponents(filepath.Dir(base), rest, visited, depth)
	}

	candidate := filepath.Join(base, component)
	info, err := os.Lstat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			if containsParentTraversal(rest) {
				return "", fmt.Errorf("cannot resolve state path through missing component %q", candidate)
			}
			// A missing component cannot contain a symlink below it. Keep the
			// remaining non-traversing components for MkdirAll and Save.
			return filepath.Join(append([]string{candidate}, rest...)...), nil
		}
		return "", fmt.Errorf("failed to inspect state path: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		if depth >= maxStateSymlinkDepth {
			return "", fmt.Errorf("state path exceeds maximum symlink depth")
		}
		if _, seen := visited[candidate]; seen {
			return "", fmt.Errorf("state path contains a symlink loop")
		}

		target, err := os.Readlink(candidate)
		if err != nil {
			return "", fmt.Errorf("failed to read state path symlink: %w", err)
		}
		if target == "" {
			return "", fmt.Errorf("state path symlink %q has an empty target", candidate)
		}

		targetBase, targetComponents := splitStatePath(target)
		if targetBase != "" {
			base = targetBase
		}
		// Keep this marker active while the symlink target is resolved so an
		// actual loop is rejected. Clear it before resolving the configured
		// path's remaining components: a path can legitimately encounter the
		// same symlink again after resolving ".." back to its parent.
		visited[candidate] = struct{}{}
		resolved, err := resolveStatePathComponents(base, targetComponents, visited, depth+1)
		delete(visited, candidate)
		if err != nil {
			return "", err
		}
		if len(rest) > 0 {
			info, err := os.Lstat(resolved)
			switch {
			case err == nil && !info.IsDir():
				return "", fmt.Errorf("state path component %q is not a directory", resolved)
			case err != nil && !os.IsNotExist(err):
				return "", fmt.Errorf("failed to inspect state path: %w", err)
			case os.IsNotExist(err) && containsParentTraversal(rest):
				return "", fmt.Errorf("cannot resolve state path through missing component %q", resolved)
			}
		}
		return resolveStatePathComponents(resolved, rest, visited, depth+1)
	}

	if len(rest) > 0 && !info.IsDir() {
		return "", fmt.Errorf("state path component %q is not a directory", candidate)
	}
	return resolveStatePathComponents(candidate, rest, visited, depth)
}

func containsParentTraversal(components []string) bool {
	for _, component := range components {
		if component == ".." {
			return true
		}
	}
	return false
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
			// Even when nothing changed, fix up HasProgressSeconds for FINISHED books
			// so the incremental NeedsSync check skips them on subsequent runs.
			if status == "FINISHED" && !s.Books[bookID].HasProgressSeconds {
				old := s.Books[bookID]
				old.HasProgressSeconds = true
				s.Books[bookID] = old
				updated = true
			}
		} else {
			oldBook := s.Books[bookID]
			s.Books[bookID] = Book{
				LastProgress:       normalizedProgress,
				LastUpdated:        now,
				Status:             status,
				UserBookID:         oldBook.UserBookID,
				HasProgressSeconds: oldBook.HasProgressSeconds || status == "FINISHED",
			}
			updated = true
			if debugLog {
				log.Printf("DEBUG - Updated book %s state - progress: %.4f, status: %s", bookID, normalizedProgress, status)
			}
		}
	} else {
		s.Books[bookID] = Book{
			LastProgress:       normalizedProgress,
			LastUpdated:        now,
			Status:             status,
			HasProgressSeconds: status == "FINISHED",
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
					HasProgressSeconds: oldBook.HasProgressSeconds || status == "FINISHED",
				}
				updated = true
			} else if !existing.HasProgressSeconds && status == "FINISHED" {
				existing.HasProgressSeconds = true
				s.Books[baseID] = existing
				updated = true
			}
		} else {
			s.Books[baseID] = Book{
				LastProgress:       normalizedProgress,
				LastUpdated:        now,
				Status:             status,
				HasProgressSeconds: status == "FINISHED",
			}
			updated = true
		}
	}

	if updated {
		s.LastSync = now
		s.dirty = true
	}
	return updated
}

func (s *State) UpdateLibrary(libraryID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	updated := Library{
		LastUpdated: now,
	}
	if existing, exists := s.Libraries[libraryID]; !exists || existing != updated {
		s.Libraries[libraryID] = updated
		s.LastSync = now
		s.dirty = true
	}
}

func (s *State) SetFullSync() {
	s.mu.Lock()
	defer s.mu.Unlock()

	lastFullSync := time.Now().Unix()
	if s.LastFullSync != lastFullSync {
		s.LastFullSync = lastFullSync
		s.dirty = true
	}
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
	return progressDiff >= minChangeThreshold
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

	updated := Book{
		LastProgress:       normalizedProgress,
		LastUpdated:        now,
		Status:             status,
		UserBookID:         userBookID,
		HasProgressSeconds: hasProgressSeconds,
	}

	if !exists || oldBook != updated {
		s.Books[bookID] = updated
		s.LastSync = now
		s.dirty = true
	}
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
		if !book.HasProgressSeconds {
			book.HasProgressSeconds = true
			s.Books[bookID] = book
			s.dirty = true
		}
	}

	if baseID := strings.SplitN(bookID, ":", 2)[0]; baseID != "" && baseID != bookID {
		if book, exists := s.Books[baseID]; exists {
			if !book.HasProgressSeconds {
				book.HasProgressSeconds = true
				s.Books[baseID] = book
				s.dirty = true
			}
		}
	}
}

// IsDirty reports whether the state has changes that have not been persisted.
func (s *State) IsDirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.dirty
}
