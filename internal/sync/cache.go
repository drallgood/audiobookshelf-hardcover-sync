package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// UserBookCacheEntry represents a cached user book lookup result
type UserBookCacheEntry struct {
	Key       string                `json:"key"`
	UserBook  *models.HardcoverBook `json:"user_book,omitempty"` // nil for failed lookups
	Timestamp time.Time             `json:"timestamp"`
	TTL       time.Duration         `json:"ttl"`
}

// PersistentUserBookCache manages persistent user book cache storage
type PersistentUserBookCache struct {
	cacheFile  string
	entries    map[string]*UserBookCacheEntry
	defaultTTL time.Duration
}

// NewPersistentUserBookCache creates a new persistent user book cache
func NewPersistentUserBookCache(cacheDir string) *PersistentUserBookCache {
	cacheFile := filepath.Join(cacheDir, "user_book_cache.json")
	return &PersistentUserBookCache{
		cacheFile:  cacheFile,
		entries:    make(map[string]*UserBookCacheEntry),
		defaultTTL: 6 * time.Hour, // Cache user books for 6 hours (moderate change frequency)
	}
}

// Load loads the user book cache from disk
func (c *PersistentUserBookCache) Load() error {
	if err := os.MkdirAll(filepath.Dir(c.cacheFile), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	if _, err := os.Stat(c.cacheFile); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(c.cacheFile)
	if err != nil {
		return fmt.Errorf("failed to read user book cache file: %w", err)
	}

	if err := json.Unmarshal(data, &c.entries); err != nil {
		return fmt.Errorf("failed to unmarshal user book cache: %w", err)
	}

	return nil
}

// Save saves the user book cache to disk
func (c *PersistentUserBookCache) Save() error {
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal user book cache: %w", err)
	}

	if err := os.WriteFile(c.cacheFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write user book cache file: %w", err)
	}

	return nil
}

// GetByUserBook retrieves a user book by user_book_id
func (c *PersistentUserBookCache) GetByUserBook(userBookID int) (*models.HardcoverBook, bool) {
	key := "ub:" + strconv.Itoa(userBookID)
	return c.get(key)
}

// GetByBookAndUser retrieves a user book by book_id and user_id
func (c *PersistentUserBookCache) GetByBookAndUser(bookID, userID int) (*models.HardcoverBook, bool) {
	key := fmt.Sprintf("bu:%d:%d", bookID, userID)
	return c.get(key)
}

// GetByEditionAndUser retrieves a user book by edition_id and user_id
func (c *PersistentUserBookCache) GetByEditionAndUser(editionID, userID int) (*models.HardcoverBook, bool) {
	key := fmt.Sprintf("eu:%d:%d", editionID, userID)
	return c.get(key)
}

// get is the internal method to retrieve from cache
func (c *PersistentUserBookCache) get(key string) (*models.HardcoverBook, bool) {
	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}

	// Check if entry is expired
	if time.Since(entry.Timestamp) > entry.TTL {
		delete(c.entries, key)
		return nil, false
	}

	return entry.UserBook, true
}

// SetByUserBook stores a user book by user_book_id
func (c *PersistentUserBookCache) SetByUserBook(userBookID int, userBook *models.HardcoverBook) {
	key := "ub:" + strconv.Itoa(userBookID)
	c.set(key, userBook)
}

// SetByBookAndUser stores a user book by book_id and user_id
func (c *PersistentUserBookCache) SetByBookAndUser(bookID, userID int, userBook *models.HardcoverBook) {
	key := fmt.Sprintf("bu:%d:%d", bookID, userID)
	c.set(key, userBook)
}

// SetByEditionAndUser stores a user book by edition_id and user_id
func (c *PersistentUserBookCache) SetByEditionAndUser(editionID, userID int, userBook *models.HardcoverBook) {
	key := fmt.Sprintf("eu:%d:%d", editionID, userID)
	c.set(key, userBook)
}

// set is the internal method to store in cache
func (c *PersistentUserBookCache) set(key string, userBook *models.HardcoverBook) {
	c.entries[key] = &UserBookCacheEntry{
		Key:       key,
		UserBook:  userBook,
		Timestamp: time.Now(),
		TTL:       c.defaultTTL,
	}
}

// Clear clears all entries from the cache
func (c *PersistentUserBookCache) Clear() {
	c.entries = make(map[string]*UserBookCacheEntry)
}

// InvalidateByUserBook removes a cached entry by user_book_id.
// Call after status mutations to prevent stale BookStatusID reads.
func (c *PersistentUserBookCache) InvalidateByUserBook(userBookID int) {
	key := "ub:" + strconv.Itoa(userBookID)
	delete(c.entries, key)
}

// Size returns the number of entries in the cache
func (c *PersistentUserBookCache) Size() int {
	return len(c.entries)
}

// Stats returns cache statistics
func (c *PersistentUserBookCache) Stats() (total, successful, failed int) {
	for _, entry := range c.entries {
		total++
		if entry.UserBook != nil {
			successful++
		} else {
			failed++
		}
	}
	return
}

// CleanExpired removes expired entries from the user book cache
func (c *PersistentUserBookCache) CleanExpired() int {
	now := time.Now()
	expiredCount := 0

	for key, entry := range c.entries {
		if now.Sub(entry.Timestamp) > entry.TTL {
			delete(c.entries, key)
			expiredCount++
		}
	}

	return expiredCount
}
