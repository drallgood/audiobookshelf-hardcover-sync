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

// ASINCacheEntry represents a cached ASIN lookup result with metadata
type ASINCacheEntry struct {
	ASIN      string                   `json:"asin"`
	Book      *models.HardcoverBook    `json:"book,omitempty"` // nil for failed lookups
	Timestamp time.Time                `json:"timestamp"`
	TTL       time.Duration            `json:"ttl"` // Time to live
}

// PersistentASINCache manages persistent ASIN cache storage
type PersistentASINCache struct {
	cacheFile string
	entries   map[string]*ASINCacheEntry
	defaultTTL time.Duration
}

// NewPersistentASINCache creates a new persistent ASIN cache
func NewPersistentASINCache(cacheDir string) *PersistentASINCache {
	cacheFile := filepath.Join(cacheDir, "asin_cache.json")
	return &PersistentASINCache{
		cacheFile:  cacheFile,
		entries:    make(map[string]*ASINCacheEntry),
		defaultTTL: 24 * time.Hour, // Cache entries for 24 hours by default
	}
}

// Load loads the cache from disk
func (c *PersistentASINCache) Load() error {
	// Ensure cache directory exists
	if err := os.MkdirAll(filepath.Dir(c.cacheFile), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Check if cache file exists
	if _, err := os.Stat(c.cacheFile); os.IsNotExist(err) {
		// Cache file doesn't exist, start with empty cache
		return nil
	}

	// Read cache file
	data, err := os.ReadFile(c.cacheFile)
	if err != nil {
		return fmt.Errorf("failed to read cache file: %w", err)
	}

	// Parse JSON
	var entries map[string]*ASINCacheEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to parse cache file: %w", err)
	}

	// Filter out expired entries
	now := time.Now()
	validEntries := make(map[string]*ASINCacheEntry)
	for asin, entry := range entries {
		if entry != nil && now.Sub(entry.Timestamp) < entry.TTL {
			validEntries[asin] = entry
		}
	}

	c.entries = validEntries
	return nil
}

// Save saves the cache to disk
func (c *PersistentASINCache) Save() error {
	// Ensure cache directory exists
	if err := os.MkdirAll(filepath.Dir(c.cacheFile), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	// Write to file
	if err := os.WriteFile(c.cacheFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	return nil
}

// Get retrieves an entry from the cache
func (c *PersistentASINCache) Get(asin string) (*models.HardcoverBook, bool) {
	entry, exists := c.entries[asin]
	if !exists {
		return nil, false
	}

	// Check if entry is expired
	if time.Since(entry.Timestamp) >= entry.TTL {
		delete(c.entries, asin)
		return nil, false
	}

	return entry.Book, true
}

// Set stores an entry in the cache
func (c *PersistentASINCache) Set(asin string, book *models.HardcoverBook) {
	c.entries[asin] = &ASINCacheEntry{
		ASIN:      asin,
		Book:      book,
		Timestamp: time.Now(),
		TTL:       c.defaultTTL,
	}
}

// SetWithTTL stores an entry in the cache with custom TTL
func (c *PersistentASINCache) SetWithTTL(asin string, book *models.HardcoverBook, ttl time.Duration) {
	c.entries[asin] = &ASINCacheEntry{
		ASIN:      asin,
		Book:      book,
		Timestamp: time.Now(),
		TTL:       ttl,
	}
}

// Clear clears all entries from the cache
func (c *PersistentASINCache) Clear() {
	c.entries = make(map[string]*ASINCacheEntry)
}

// Size returns the number of entries in the cache
func (c *PersistentASINCache) Size() int {
	return len(c.entries)
}

// Stats returns cache statistics
func (c *PersistentASINCache) Stats() (total, successful, failed int) {
	for _, entry := range c.entries {
		total++
		if entry.Book != nil {
			successful++
		} else {
			failed++
		}
	}
	return
}

// CleanExpired removes expired entries from the cache
func (c *PersistentASINCache) CleanExpired() int {
	now := time.Now()
	expiredCount := 0
	
	for asin, entry := range c.entries {
		if now.Sub(entry.Timestamp) > entry.TTL {
			delete(c.entries, asin)
			expiredCount++
		}
	}
	
	return expiredCount
}

// EditionCacheEntry represents a cached edition lookup result
type EditionCacheEntry struct {
	EditionID int             `json:"edition_id"`
	Edition   *models.Edition `json:"edition,omitempty"` // nil for failed lookups
	Timestamp time.Time       `json:"timestamp"`
	TTL       time.Duration   `json:"ttl"`
}

// PersistentEditionCache manages persistent edition cache storage
type PersistentEditionCache struct {
	cacheFile  string
	entries    map[int]*EditionCacheEntry
	defaultTTL time.Duration
}

// NewPersistentEditionCache creates a new persistent edition cache
func NewPersistentEditionCache(cacheDir string) *PersistentEditionCache {
	cacheFile := filepath.Join(cacheDir, "edition_cache.json")
	return &PersistentEditionCache{
		cacheFile:  cacheFile,
		entries:    make(map[int]*EditionCacheEntry),
		defaultTTL: 7 * 24 * time.Hour, // Cache editions for 7 days (they change rarely)
	}
}

// Load loads the edition cache from disk
func (c *PersistentEditionCache) Load() error {
	if err := os.MkdirAll(filepath.Dir(c.cacheFile), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	if _, err := os.Stat(c.cacheFile); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(c.cacheFile)
	if err != nil {
		return fmt.Errorf("failed to read edition cache file: %w", err)
	}

	if err := json.Unmarshal(data, &c.entries); err != nil {
		return fmt.Errorf("failed to unmarshal edition cache: %w", err)
	}

	return nil
}

// Save saves the edition cache to disk
func (c *PersistentEditionCache) Save() error {
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal edition cache: %w", err)
	}

	if err := os.WriteFile(c.cacheFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write edition cache file: %w", err)
	}

	return nil
}

// Get retrieves an edition from the cache
func (c *PersistentEditionCache) Get(editionID int) (*models.Edition, bool) {
	entry, exists := c.entries[editionID]
	if !exists {
		return nil, false
	}

	// Check if entry is expired
	if time.Since(entry.Timestamp) > entry.TTL {
		delete(c.entries, editionID)
		return nil, false
	}

	return entry.Edition, true
}

// Set stores an edition in the cache
func (c *PersistentEditionCache) Set(editionID int, edition *models.Edition) {
	c.entries[editionID] = &EditionCacheEntry{
		EditionID: editionID,
		Edition:   edition,
		Timestamp: time.Now(),
		TTL:       c.defaultTTL,
	}
}

// Clear clears all entries from the cache
func (c *PersistentEditionCache) Clear() {
	c.entries = make(map[int]*EditionCacheEntry)
}

// Size returns the number of entries in the cache
func (c *PersistentEditionCache) Size() int {
	return len(c.entries)
}

// Stats returns cache statistics
func (c *PersistentEditionCache) Stats() (total, successful, failed int) {
	for _, entry := range c.entries {
		total++
		if entry.Edition != nil {
			successful++
		} else {
			failed++
		}
	}
	return
}

// CleanExpired removes expired entries from the edition cache
func (c *PersistentEditionCache) CleanExpired() int {
	now := time.Now()
	expiredCount := 0

	for editionID, entry := range c.entries {
		if now.Sub(entry.Timestamp) > entry.TTL {
			delete(c.entries, editionID)
			expiredCount++
		}
	}

	return expiredCount
}

// UserBookCacheEntry represents a cached user book lookup result
type UserBookCacheEntry struct {
	Key       string                 `json:"key"`
	UserBook  *models.HardcoverBook  `json:"user_book,omitempty"` // nil for failed lookups
	Timestamp time.Time              `json:"timestamp"`
	TTL       time.Duration          `json:"ttl"`
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
