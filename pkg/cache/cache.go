package cache

import (
	"sync"
	"time"
)

// Cache represents a thread-safe in-memory cache with TTL support
type Cache struct {
	items map[string]cacheItem
	mu    sync.RWMutex
}

type cacheItem struct {
	value      interface{}
	expiration int64
}

// New creates a new cache instance
func New() *Cache {
	return &Cache{
		items: make(map[string]cacheItem),
	}
}

// Set adds an item to the cache with the specified TTL (in seconds)
func (c *Cache) Set(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = cacheItem{
		value:      value,
		expiration: time.Now().Add(ttl).UnixNano(),
	}
}

// Get retrieves an item from the cache
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, found := c.items[key]
	if !found {
		return nil, false
	}

	// Check if item has expired
	if item.expiration > 0 && time.Now().UnixNano() > item.expiration {
		delete(c.items, key)
		return nil, false
	}

	return item.value, true
}

// Delete removes an item from the cache
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, key)
}

// Clear removes all items from the cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]cacheItem)
}

// ItemCount returns the number of items in the cache
func (c *Cache) ItemCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// GetWithFunc retrieves an item from the cache or calls the provided function to get it
func (c *Cache) GetWithFunc(key string, ttl time.Duration, fn func() (interface{}, error)) (interface{}, error) {
	// Try to get from cache first
	if val, found := c.Get(key); found {
		return val, nil
	}

	// Call the function to get the value
	val, err := fn()
	if err != nil {
		return nil, err
	}

	// Cache the result
	c.Set(key, val, ttl)

	return val, nil
}
