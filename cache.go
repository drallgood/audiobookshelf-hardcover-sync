package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// CacheEntry represents a cached search result with metadata
type CacheEntry struct {
	Results     []PersonSearchResult `json:"results"`
	PublisherId int                  `json:"publisher_id,omitempty"`
	Timestamp   time.Time            `json:"timestamp"`
	QueryType   string               `json:"query_type"` // "author", "narrator", or "publisher"
	OriginalQuery string             `json:"original_query"`
}

// PersonCache manages cached search results for authors and narrators
type PersonCache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	ttl     time.Duration
}

// Global cache instances
var (
	personCache = NewPersonCache(30 * time.Minute) // 30-minute TTL
)

// NewPersonCache creates a new person cache with specified TTL
func NewPersonCache(ttl time.Duration) *PersonCache {
	return &PersonCache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
	}
}

// generateCacheKey creates a consistent cache key from name and query type
func generateCacheKey(name, queryType string) string {
	// Normalize the name for consistent caching
	normalized := strings.ToLower(strings.TrimSpace(name))
	return fmt.Sprintf("%s:%s", queryType, normalized)
}

// Put stores search results in the cache
func (c *PersonCache) Put(name, queryType string, results []PersonSearchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	key := generateCacheKey(name, queryType)
	c.entries[key] = &CacheEntry{
		Results:       results,
		Timestamp:     time.Now(),
		QueryType:     queryType,
		OriginalQuery: name,
	}
	
	debugLog("Cache: Stored %d results for '%s' (%s)", len(results), name, queryType)
}

// PutPublisher stores a publisher ID in the cache
func (c *PersonCache) PutPublisher(name string, publisherId int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	key := generateCacheKey(name, "publisher")
	c.entries[key] = &CacheEntry{
		PublisherId:   publisherId,
		Timestamp:     time.Now(),
		QueryType:     "publisher",
		OriginalQuery: name,
	}
	
	debugLog("Cache: Stored publisher ID %d for '%s'", publisherId, name)
}

// Get retrieves search results from the cache
func (c *PersonCache) Get(name, queryType string) ([]PersonSearchResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	key := generateCacheKey(name, queryType)
	entry, exists := c.entries[key]
	
	if !exists {
		return nil, false
	}
	
	// Check if entry has expired
	if time.Since(entry.Timestamp) > c.ttl {
		delete(c.entries, key)
		debugLog("Cache: Expired entry for '%s' (%s)", name, queryType)
		return nil, false
	}
	
	debugLog("Cache: Hit for '%s' (%s) - %d results", name, queryType, len(entry.Results))
	return entry.Results, true
}

// GetPublisher retrieves a publisher ID from the cache
func (c *PersonCache) GetPublisher(name string) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	key := generateCacheKey(name, "publisher")
	entry, exists := c.entries[key]
	
	if !exists {
		return 0, false
	}
	
	// Check if entry has expired
	if time.Since(entry.Timestamp) > c.ttl {
		delete(c.entries, key)
		debugLog("Cache: Expired publisher entry for '%s'", name)
		return 0, false
	}
	
	debugLog("Cache: Hit for publisher '%s' - ID %d", name, entry.PublisherId)
	return entry.PublisherId, true
}

// GetCrossRole attempts to find a person in cache under a different role
// Returns: (results, foundInRole, cacheHit)
func (c *PersonCache) GetCrossRole(name, requestedRole string) ([]PersonSearchResult, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	// Define role pairs for cross-lookup
	var otherRole string
	switch requestedRole {
	case "author":
		otherRole = "narrator"
	case "narrator":
		otherRole = "author"
	default:
		return nil, "", false
	}
	
	// Check if we have this person cached under the other role
	otherKey := generateCacheKey(name, otherRole)
	entry, exists := c.entries[otherKey]
	
	if !exists {
		return nil, "", false
	}
	
	// Check if entry has expired
	if time.Since(entry.Timestamp) > c.ttl {
		delete(c.entries, otherKey)
		debugLog("Cache: Expired cross-role entry for '%s' (%s)", name, otherRole)
		return nil, "", false
	}
	
	debugLog("Cache: Cross-role hit for '%s' - found as %s, requested as %s", name, otherRole, requestedRole)
	return entry.Results, otherRole, true
}

// CleanExpired removes expired entries from the cache
func (c *PersonCache) CleanExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	now := time.Now()
	expiredKeys := make([]string, 0)
	
	for key, entry := range c.entries {
		if now.Sub(entry.Timestamp) > c.ttl {
			expiredKeys = append(expiredKeys, key)
		}
	}
	
	for _, key := range expiredKeys {
		delete(c.entries, key)
	}
	
	if len(expiredKeys) > 0 {
		debugLog("Cache: Cleaned %d expired entries", len(expiredKeys))
	}
}

// Stats returns cache statistics
func (c *PersonCache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	stats := map[string]interface{}{
		"total_entries": len(c.entries),
		"ttl_minutes":   c.ttl.Minutes(),
	}
	
	// Count by type
	authorCount := 0
	narratorCount := 0
	publisherCount := 0
	
	for _, entry := range c.entries {
		switch entry.QueryType {
		case "author":
			authorCount++
		case "narrator":
			narratorCount++
		case "publisher":
			publisherCount++
		}
	}
	
	stats["authors"] = authorCount
	stats["narrators"] = narratorCount
	stats["publishers"] = publisherCount
	
	return stats
}

// Cached wrapper functions for the existing search functions

// searchAuthorsCached wraps searchAuthors with caching
func searchAuthorsCached(name string, limit int) ([]PersonSearchResult, error) {
	// Check cache first
	if results, found := personCache.Get(name, "author"); found {
		// Trim results to requested limit
		if len(results) > limit {
			results = results[:limit]
		}
		return results, nil
	}
	
	// Check cross-role cache (maybe this person is cached as narrator)
	if results, foundAs, found := personCache.GetCrossRole(name, "author"); found {
		debugLog("Cache: Found '%s' as %s while searching for author", name, foundAs)
		// Store in cache as author too for future requests
		personCache.Put(name, "author", results)
		
		// Trim results to requested limit
		if len(results) > limit {
			results = results[:limit]
		}
		return results, nil
	}
	
	// Cache miss - perform actual search
	results, err := searchAuthors(name, limit)
	if err != nil {
		return nil, err
	}
	
	// Store in cache
	personCache.Put(name, "author", results)
	
	return results, nil
}

// searchNarratorsCached wraps searchNarrators with caching
func searchNarratorsCached(name string, limit int) ([]PersonSearchResult, error) {
	// Check cache first
	if results, found := personCache.Get(name, "narrator"); found {
		// Trim results to requested limit
		if len(results) > limit {
			results = results[:limit]
		}
		return results, nil
	}
	
	// Check cross-role cache (maybe this person is cached as author)
	if results, foundAs, found := personCache.GetCrossRole(name, "narrator"); found {
		debugLog("Cache: Found '%s' as %s while searching for narrator", name, foundAs)
		// Store in cache as narrator too for future requests
		personCache.Put(name, "narrator", results)
		
		// Trim results to requested limit
		if len(results) > limit {
			results = results[:limit]
		}
		return results, nil
	}
	
	// Cache miss - perform actual search
	results, err := searchNarrators(name, limit)
	if err != nil {
		return nil, err
	}
	
	// Store in cache
	personCache.Put(name, "narrator", results)
	
	return results, nil
}

// searchPublishersCached wraps searchPublishers with caching
func searchPublishersCached(name string, limit int) ([]PublisherSearchResult, error) {
	// For publishers, we cache the first result's ID for exact matches
	// but still need to perform full searches for multiple results
	
	// Perform actual search (publishers are less frequently searched)
	results, err := searchPublishers(name, limit)
	if err != nil {
		return nil, err
	}
	
	// Cache exact matches for faster future lookups
	for _, result := range results {
		if strings.EqualFold(result.Name, name) {
			personCache.PutPublisher(name, result.ID)
			break
		}
	}
	
	return results, nil
}

// initCache initializes the caching system
func initCache() {
	// Start a goroutine to periodically clean expired entries
	go func() {
		ticker := time.NewTicker(15 * time.Minute) // Clean every 15 minutes
		defer ticker.Stop()
		
		for range ticker.C {
			personCache.CleanExpired()
		}
	}()
	
	debugLog("Cache: Initialized with %v TTL", personCache.ttl)
}

// getCacheStats returns cache statistics for debugging
func getCacheStats() map[string]interface{} {
	return personCache.Stats()
}
