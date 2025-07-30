package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	removed := 0
	
	for asin, entry := range c.entries {
		if now.Sub(entry.Timestamp) >= entry.TTL {
			delete(c.entries, asin)
			removed++
		}
	}
	
	return removed
}
