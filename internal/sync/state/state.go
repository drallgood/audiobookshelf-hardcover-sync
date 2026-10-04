package state

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/audnexregion"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

const DefaultStateFile = "./data/sync_state.json"

// CurrentVersion is the explicit state schema version written by Save. Legacy
// v1 files only carried retired timestamp metadata; v2/v3 and unversioned
// files retain their checkpoint shape and need no field conversion.
const CurrentVersion = "4.0"

type State struct {
	Version              string                             `json:"version"`
	Books                map[string]Book                    `json:"books,omitempty"`
	PendingFinishedDates map[string]FinishedDateRestoration `json:"pendingFinishedDates,omitempty"`
	mu                   sync.RWMutex                       `json:"-"`
	dirty                bool                               `json:"-"`
}

// FinishedDateRestoration preserves completion dates across Hardcover's status
// mutation, which can replace an existing read's finished_at with today's date.
// Dates includes every existing read; an empty string represents null finished_at.
type FinishedDateRestoration struct {
	ABSItemID string           `json:"absItemId"`
	Dates     map[int64]string `json:"dates"`
}

// SetFinishedDateRestoration records an intent that must be saved before changing status.
func (s *State) SetFinishedDateRestoration(userBookID string, itemID string, dates map[int64]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.PendingFinishedDates == nil {
		s.PendingFinishedDates = make(map[string]FinishedDateRestoration)
	}
	copied := make(map[int64]string, len(dates))
	for id, date := range dates {
		copied[id] = date
	}
	s.PendingFinishedDates[userBookID] = FinishedDateRestoration{ABSItemID: itemID, Dates: copied}
	s.dirty = true
}

// GetFinishedDateRestoration returns a copy so callers cannot mutate state without locking.
func (s *State) GetFinishedDateRestoration(userBookID string) (map[int64]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.PendingFinishedDates[userBookID]
	copied := make(map[int64]string, len(intent.Dates))
	for id, date := range intent.Dates {
		copied[id] = date
	}
	return copied, ok
}

// HasFinishedDateRestoration keeps incomplete repairs eligible for incremental sync.
func (s *State) HasFinishedDateRestoration(itemID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, intent := range s.PendingFinishedDates {
		if intent.ABSItemID == itemID {
			return true
		}
	}
	return false
}

// GetItemFinishedDateRestorations returns independently owned date maps for an ABS item.
func (s *State) GetItemFinishedDateRestorations(itemID string) map[string]map[int64]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]map[int64]string)
	for userBookID, intent := range s.PendingFinishedDates {
		if intent.ABSItemID != itemID {
			continue
		}
		dates := make(map[int64]string, len(intent.Dates))
		for id, date := range intent.Dates {
			dates[id] = date
		}
		result[userBookID] = dates
	}
	return result
}

// ClearFinishedDateRestoration removes an intent only after its dates are verified.
func (s *State) ClearFinishedDateRestoration(userBookID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.PendingFinishedDates[userBookID]; ok {
		delete(s.PendingFinishedDates, userBookID)
		s.dirty = true
	}
}

type Book struct {
	LastProgress       float64      `json:"lastProgress"`
	LastUpdated        int64        `json:"lastUpdated"`
	Status             string       `json:"status,omitempty"`
	UserBookID         string       `json:"userBookID,omitempty"`
	HasProgressSeconds bool         `json:"hasProgressSeconds,omitempty"`
	Association        *Association `json:"association,omitempty"`
}

// Association records a confirmed mapping from one Audiobookshelf item to a
// Hardcover book and edition. It is stored with that item's checkpoint so a
// checkpoint cannot persist independently from its association.
type Association struct {
	ABSItemID               string    `json:"absItemId"`
	SourceASIN              string    `json:"sourceAsin,omitempty"`
	SourceISBN10            string    `json:"sourceIsbn10,omitempty"`
	SourceISBN13            string    `json:"sourceIsbn13,omitempty"`
	Correction              string    `json:"correction,omitempty"`
	RegionalExternalID      string    `json:"regionalExternalId,omitempty"`
	AudnexusConfirmedRegion string    `json:"audnexusConfirmedRegion,omitempty"`
	AudnexusConfirmedAt     time.Time `json:"audnexusConfirmedAt,omitzero"`
	HardcoverBookID         string    `json:"hardcoverBookId"`
	HardcoverEditionID      string    `json:"hardcoverEditionId"`
	ReadingFormat           string    `json:"readingFormat"`
	Provenance              string    `json:"provenance"`
	// OwnershipVerifiedAt is the Unix time Hardcover's Owned list was last
	// confirmed to include this book and edition. It lets the next syncs skip the
	// ownership request, and is dropped with the association when it is replaced
	// or forgotten.
	OwnershipVerifiedAt int64 `json:"ownershipVerifiedAt,omitempty"`
	// OwnershipTokenFingerprint scopes a confirmation to the Hardcover account
	// that produced it. It stores a SHA-256 fingerprint, never the raw token.
	OwnershipTokenFingerprint string `json:"ownershipTokenFingerprint,omitempty"`
}

// NewAudibleImportAssociation creates the durable match recorded after a
// confirmed unanchored Audible import has been read back from Hardcover.
func NewAudibleImportAssociation(item *models.AudiobookshelfBook, result *hardcover.RegionalAudiobookResult, correction string, confirmedAt time.Time) (Association, error) {
	if item == nil || item.ID == "" || item.ReadingFormat() != models.ReadingFormatAudiobook {
		return Association{}, fmt.Errorf("an Audiobookshelf audiobook item is required")
	}
	if result == nil || result.BookID <= 0 || result.EditionID <= 0 ||
		(result.Status != hardcover.RegionalAudiobookLoaded && result.Status != hardcover.RegionalAudiobookCreated) ||
		result.ReadingFormatID != models.ReadingFormatID(models.ReadingFormatAudiobook) {
		return Association{}, fmt.Errorf("a verified regional Hardcover audiobook result is required")
	}
	externalID := strings.ToUpper(strings.TrimSpace(result.RegionalExternalID))
	parts := strings.Split(externalID, ":")
	if len(parts) != 2 {
		return Association{}, fmt.Errorf("a confirmed regional Audible identifier is required")
	}
	asin, validASIN := audnex.CanonicalASIN(parts[0])
	region := strings.ToLower(strings.TrimSpace(parts[1]))
	if !validASIN || !audnexregion.IsRegion(region) {
		return Association{}, fmt.Errorf("a confirmed regional Audible identifier is required")
	}
	canonicalExternalID := asin + ":" + region
	if !strings.EqualFold(strings.TrimSpace(result.RegionalExternalID), canonicalExternalID) {
		return Association{}, fmt.Errorf("the verified regional Audible identifier did not match the requested identifier")
	}
	if confirmedAt.IsZero() {
		return Association{}, fmt.Errorf("the Audnexus confirmation time is required")
	}
	sourceASIN, sourceISBN10, sourceISBN13 := SourceIdentifiers(item.Media.Metadata.ASIN, item.Media.Metadata.ISBN)
	return Association{
		ABSItemID: item.ID, SourceASIN: sourceASIN, SourceISBN10: sourceISBN10, SourceISBN13: sourceISBN13,
		Correction: strings.TrimSpace(correction), RegionalExternalID: canonicalExternalID,
		AudnexusConfirmedRegion: region, AudnexusConfirmedAt: confirmedAt.UTC(),
		HardcoverBookID: strconv.Itoa(result.BookID), HardcoverEditionID: strconv.Itoa(result.EditionID),
		ReadingFormat: models.ReadingFormatAudiobook, Provenance: "audible_import_unanchored",
	}, nil
}

func NewState() *State {
	return &State{
		Version: CurrentVersion,
		Books:   make(map[string]Book),
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

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state: %w", err)
	}
	if state.Version != "" && state.Version != "1.0" && state.Version != "2.0" && state.Version != "3.0" && state.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported state version %q", state.Version)
	}
	// Unversioned legacy Books files and v1/v2/v3 checkpoint files are compatible
	// with the current shape. Normalize their in-memory version so the next
	// persistence writes an explicit current schema version. v1 timestamp-only
	// files intentionally remain empty so the first run rebuilds checkpoints.
	state.Version = CurrentVersion

	if state.Books == nil {
		state.Books = make(map[string]Book)
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
	directoriesToSync, err := stateDirectorySyncPaths(dir)
	if err != nil {
		return fmt.Errorf("failed to inspect state directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
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
	for _, directory := range directoriesToSync {
		if err := syncDirectory(directory); err != nil {
			return fmt.Errorf("failed to sync state directory %q: %w", directory, err)
		}
	}
	s.dirty = false

	return nil
}

// stateDirectorySyncPaths returns the destination directory and every missing
// parent up to the nearest directory that existed before MkdirAll. Flushing
// each of these directories after the state file rename makes the complete
// newly-created path durable across a power loss.
func stateDirectorySyncPaths(dir string) ([]string, error) {
	var paths []string
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		switch {
		case err == nil:
			if !info.IsDir() {
				return nil, fmt.Errorf("state path component %q is not a directory", current)
			}
			return append(paths, current), nil
		case !os.IsNotExist(err):
			return nil, fmt.Errorf("failed to inspect state directory %q: %w", current, err)
		}

		paths = append(paths, current)
		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf("no existing ancestor for state directory %q", dir)
		}
	}
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

// SourceIdentifiers splits an Audiobookshelf item's reported ASIN and ISBN
// into an association's source identifier fields. The ISBN is recorded as
// ISBN-10 when it normalizes to ten characters and otherwise as ISBN-13. A
// malformed or empty ISBN keeps its reported value so a later source
// correction invalidates the association.
func SourceIdentifiers(rawASIN, rawISBN string) (asin, isbn10, isbn13 string) {
	asin = strings.TrimSpace(rawASIN)
	rawISBN = strings.TrimSpace(rawISBN)
	if len(isbn.Normalize(rawISBN)) == 10 {
		return asin, rawISBN, ""
	}
	return asin, "", rawISBN
}

// SetAssociation stores a confirmed Hardcover resolution with the ABS item's
// base checkpoint. Checkpoint updates preserve this field, and atomic Save
// persists both together.
func (s *State) SetAssociation(association Association) error {
	if strings.TrimSpace(association.ABSItemID) == "" {
		return fmt.Errorf("association ABS item ID is required")
	}
	if strings.TrimSpace(association.HardcoverBookID) == "" || strings.TrimSpace(association.HardcoverEditionID) == "" {
		return fmt.Errorf("association Hardcover book and edition IDs are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	book := s.Books[association.ABSItemID]
	if book.Association != nil {
		// Provenance describes how the match was confirmed, not a different
		// match or account. Preserve ownership when only this metadata changes.
		previous := *book.Association
		previous.OwnershipVerifiedAt = association.OwnershipVerifiedAt
		previous.OwnershipTokenFingerprint = association.OwnershipTokenFingerprint
		previous.Provenance = association.Provenance
		if previous == association {
			association.OwnershipVerifiedAt = book.Association.OwnershipVerifiedAt
			association.OwnershipTokenFingerprint = book.Association.OwnershipTokenFingerprint
			if *book.Association == association {
				return nil
			}
		}
	}
	book.Association = &association
	s.Books[association.ABSItemID] = book
	s.dirty = true
	return nil
}

// GetAssociation returns the confirmed association stored for an ABS item.
func (s *State) GetAssociation(itemID string) (Association, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	book, exists := s.Books[itemID]
	if !exists || book.Association == nil {
		return Association{}, false
	}
	return *book.Association, true
}

// RecordOwnershipVerified remembers that the Hardcover account identified by
// tokenFingerprint's SHA-256 fingerprint had the item's saved book and edition
// on its Owned list at the given time. It does nothing when the item has no
// association for exactly that book and edition.
func (s *State) RecordOwnershipVerified(itemID, hardcoverBookID, hardcoverEditionID, tokenFingerprint string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	book, exists := s.Books[itemID]
	if !exists || book.Association == nil ||
		book.Association.HardcoverBookID != hardcoverBookID || book.Association.HardcoverEditionID != hardcoverEditionID {
		return
	}
	association := *book.Association
	association.OwnershipVerifiedAt = at.Unix()
	association.OwnershipTokenFingerprint = tokenFingerprint
	book.Association = &association
	s.Books[itemID] = book
	s.dirty = true
}

// OwnershipVerifiedSince reports whether the Hardcover account identified by
// tokenFingerprint's SHA-256 fingerprint had the item's saved book and edition
// on its Owned list at or after the cutoff. Empty fingerprints never reuse a
// confirmation.
func (s *State) OwnershipVerifiedSince(itemID, hardcoverBookID, hardcoverEditionID, tokenFingerprint string, cutoff time.Time) bool {
	if tokenFingerprint == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	book, exists := s.Books[itemID]
	if !exists || book.Association == nil ||
		book.Association.HardcoverBookID != hardcoverBookID || book.Association.HardcoverEditionID != hardcoverEditionID {
		return false
	}
	return book.Association.OwnershipVerifiedAt != 0 &&
		book.Association.OwnershipVerifiedAt >= cutoff.Unix() &&
		book.Association.OwnershipTokenFingerprint == tokenFingerprint
}

// InvalidateItemCheckpoints removes an item's base and edition-specific
// incremental checkpoints. If the base entry already carries an association,
// it is retained without checkpoint data.
func (s *State) InvalidateItemCheckpoints(itemID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	if book, exists := s.Books[itemID]; exists {
		if book.Association == nil {
			delete(s.Books, itemID)
			changed = true
		} else {
			associationOnly := Book{Association: book.Association}
			if book != associationOnly {
				s.Books[itemID] = associationOnly
				changed = true
			}
		}
	}

	for key := range s.Books {
		if strings.HasPrefix(key, itemID+":") {
			delete(s.Books, key)
			changed = true
		}
	}
	if changed {
		s.dirty = true
	}
}

// RemoveAssociation forgets an ABS item's association and all incremental
// checkpoints for that item. Missing associations are a safe no-op, so an old
// request cannot erase checkpoints created after a prior forget operation.
func (s *State) RemoveAssociation(itemID string) (Association, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	book, exists := s.Books[itemID]
	if !exists || book.Association == nil {
		return Association{}, false
	}
	previous := *book.Association
	delete(s.Books, itemID)
	for key := range s.Books {
		if strings.HasPrefix(key, itemID+":") {
			delete(s.Books, key)
		}
	}
	s.dirty = true
	return previous, true
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
	var debugMessages []string

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
			debugMessages = append(debugMessages, fmt.Sprintf(
				"DEBUG - UpdateBook for %s (existing) - stored: %.4f, new: %.4f, storedStatus: %s, newStatus: %s, progressChanged: %v, statusChanged: %v",
				bookID, storedProgress, normalizedProgress, existing.Status, status, progressChanged, statusChanged,
			))
		}

		if !progressChanged && !statusChanged {
			if debugLog {
				debugMessages = append(debugMessages, fmt.Sprintf("DEBUG - No update needed for book %s - no significant changes", bookID))
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
				Association:        oldBook.Association,
			}
			updated = true
			if debugLog {
				debugMessages = append(debugMessages, fmt.Sprintf("DEBUG - Updated book %s state - progress: %.4f, status: %s", bookID, normalizedProgress, status))
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
			debugMessages = append(debugMessages, fmt.Sprintf("DEBUG - Created new state for Scrum book %s - progress: %.4f, status: %s", bookID, normalizedProgress, status))
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
					Association:        oldBook.Association,
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
		s.dirty = true
	}
	s.mu.Unlock()
	for _, message := range debugMessages {
		log.Print(message)
	}
	return updated
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

func (s *State) UpdateBookWithUserBookID(bookID string, progress float64, status string, userBookID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	normalizedProgress := normalizeProgress(progress)

	oldBook, exists := s.Books[bookID]
	updated := Book{
		LastProgress:       normalizedProgress,
		Status:             status,
		UserBookID:         userBookID,
		HasProgressSeconds: oldBook.HasProgressSeconds,
		Association:        oldBook.Association,
	}

	if exists {
		// LastUpdated records the last semantic state change, so it must not
		// make an otherwise identical update dirty merely because time passed.
		updated.LastUpdated = oldBook.LastUpdated
		if oldBook == updated {
			return
		}
	}
	updated.LastUpdated = now
	s.Books[bookID] = updated
	s.dirty = true
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
