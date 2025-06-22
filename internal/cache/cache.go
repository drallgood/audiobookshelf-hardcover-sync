package cache

import (
	"sync"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Cache defines the interface for a generic cache
// that can store and retrieve values with a TTL
// and supports different key types (string or int)
type Cache[K comparable, V any] interface {
	// Set stores a value in the cache with the specified TTL
	Set(key K, value V, ttl time.Duration)
	// Get retrieves a value from the cache and a boolean indicating if it was found
	Get(key K) (V, bool)
	// Delete removes a value from the cache
	Delete(key K)
	// Clear removes all values from the cache
	Clear()
}

// entry represents a cache entry with its expiration time
type entry[V any] struct {
	value    V
	expiresAt time.Time
}

// memoryCache is an in-memory implementation of the Cache interface
type memoryCache[K comparable, V any] struct {
	items map[K]entry[V]
	mu    sync.RWMutex
	log   *logger.Logger
}

// NewMemoryCache creates a new in-memory cache with the provided logger
func NewMemoryCache[K comparable, V any](log *logger.Logger) Cache[K, V] {
	return &memoryCache[K, V]{
		items: make(map[K]entry[V]),
		log:   log,
	}
}

// Set stores a value in the cache with the specified TTL
func (c *memoryCache[K, V]) Set(key K, value V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	} else {
		expiresAt = time.Time{} // Zero time means no expiration
	}

	c.items[key] = entry[V]{
		value:    value,
		expiresAt: expiresAt,
	}

	c.log.Debug().
		Interface("key", key).
		Int("cache_size", len(c.items)).
		Msg("Item added to cache")
}

// Get retrieves a value from the cache and a boolean indicating if it was found
func (c *memoryCache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, found := c.items[key]
	if !found {
		var zero V
		return zero, false
	}

	// Check if the item has expired
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		c.log.Debug().
			Interface("key", key).
			Msg("Cache item expired")
		var zero V
		return zero, false
	}

	c.log.Debug().
		Interface("key", key).
		Msg("Cache hit")

	return item.value, true
}

// Delete removes a value from the cache
func (c *memoryCache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, key)

	c.log.Debug().
		Interface("key", key).
		Int("remaining_items", len(c.items)).
		Msg("Item removed from cache")
}

// Clear removes all values from the cache
func (c *memoryCache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[K]entry[V])

	c.log.Info().
		Msg("Cache cleared")
}

// WithTTL returns a wrapper that automatically applies a TTL to all Set operations
func WithTTL[K comparable, V any](cache Cache[K, V], ttl time.Duration) Cache[K, V] {
	return &ttlWrapper[K, V]{
		cache: cache,
		ttl:   ttl,
	}
}

type ttlWrapper[K comparable, V any] struct {
	cache Cache[K, V]
	ttl   time.Duration
}

func (w *ttlWrapper[K, V]) Set(key K, value V, _ time.Duration) {
	w.cache.Set(key, value, w.ttl)
}

func (w *ttlWrapper[K, V]) Get(key K) (V, bool) {
	return w.cache.Get(key)
}

func (w *ttlWrapper[K, V]) Delete(key K) {
	w.cache.Delete(key)
}

func (w *ttlWrapper[K, V]) Clear() {
	w.cache.Clear()
}
