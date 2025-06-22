package testutils

import (
	"testing"
	"time"
)

func TestPersonCache_BasicOperations(t *testing.T) {
	cache := NewPersonCache(5 * time.Minute)
	
	// Test data
	testResults := []PersonSearchResult{
		{ID: 1, Name: "John Doe"},
		{ID: 2, Name: "Jane Smith"},
	}
	
	// Test Put and Get
	cache.Put("John Doe", "author", testResults)
	
	results, found := cache.Get("John Doe", "author")
	if !found {
		t.Error("Expected cache hit, got miss")
	}
	
	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
	
	if results[0].ID != 1 || results[0].Name != "John Doe" {
		t.Errorf("Unexpected result: %+v", results[0])
	}
}

func TestPersonCache_CaseSensitivity(t *testing.T) {
	cache := NewPersonCache(5 * time.Minute)
	
	testResults := []PersonSearchResult{
		{ID: 1, Name: "John Doe"},
	}
	
	// Store with one case
	cache.Put("John Doe", "author", testResults)
	
	// Retrieve with different case
	results, found := cache.Get("JOHN DOE", "author")
	if !found {
		t.Error("Expected cache hit with different case, got miss")
	}
	
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestPersonCache_TTLExpiration(t *testing.T) {
	cache := NewPersonCache(100 * time.Millisecond)
	
	testResults := []PersonSearchResult{
		{ID: 1, Name: "John Doe"},
	}
	
	// Store data
	cache.Put("John Doe", "author", testResults)
	
	// Should be available immediately
	_, found := cache.Get("John Doe", "author")
	if !found {
		t.Error("Expected immediate cache hit")
	}
	
	// Wait for expiration
	time.Sleep(150 * time.Millisecond)
	
	// Should be expired
	_, found = cache.Get("John Doe", "author")
	if found {
		t.Error("Expected cache miss after TTL expiration")
	}
}

func TestPersonCache_CrossRoleLookup(t *testing.T) {
	cache := NewPersonCache(5 * time.Minute)
	
	testResults := []PersonSearchResult{
		{ID: 1, Name: "John Doe"},
	}
	
	// Store as author
	cache.Put("John Doe", "author", testResults)
	
	// Try to find as narrator using cross-role lookup
	results, foundAs, found := cache.GetCrossRole("John Doe", "narrator")
	if !found {
		t.Error("Expected cross-role cache hit")
	}
	
	if foundAs != "author" {
		t.Errorf("Expected foundAs='author', got '%s'", foundAs)
	}
	
	if len(results) != 1 || results[0].ID != 1 {
		t.Errorf("Unexpected cross-role results: %+v", results)
	}
}

func TestPersonCache_PublisherOperations(t *testing.T) {
	cache := NewPersonCache(5 * time.Minute)
	
	// Test publisher storage and retrieval
	cache.PutPublisher("Penguin Books", 123)
	
	publisherID, found := cache.GetPublisher("Penguin Books")
	if !found {
		t.Error("Expected publisher cache hit")
	}
	
	if publisherID != 123 {
		t.Errorf("Expected publisher ID 123, got %d", publisherID)
	}
	
	// Test case insensitive lookup
	publisherID, found = cache.GetPublisher("PENGUIN BOOKS")
	if !found {
		t.Error("Expected case-insensitive publisher cache hit")
	}
	
	if publisherID != 123 {
		t.Errorf("Expected publisher ID 123, got %d", publisherID)
	}
}

func TestPersonCache_CleanExpired(t *testing.T) {
	cache := NewPersonCache(100 * time.Millisecond)
	
	testResults := []PersonSearchResult{
		{ID: 1, Name: "John Doe"},
	}
	
	// Add several entries
	cache.Put("John Doe", "author", testResults)
	cache.Put("Jane Smith", "author", testResults)
	cache.PutPublisher("Penguin", 123)
	
	// Verify they exist
	stats := cache.Stats()
	if stats["total_entries"].(int) != 3 {
		t.Errorf("Expected 3 entries, got %d", stats["total_entries"])
	}
	
	// Wait for expiration
	time.Sleep(150 * time.Millisecond)
	
	// Clean expired
	cache.CleanExpired()
	
	// Verify they're gone
	stats = cache.Stats()
	if stats["total_entries"].(int) != 0 {
		t.Errorf("Expected 0 entries after cleanup, got %d", stats["total_entries"])
	}
}

func TestPersonCache_Stats(t *testing.T) {
	cache := NewPersonCache(5 * time.Minute)
	
	testResults := []PersonSearchResult{
		{ID: 1, Name: "John Doe"},
	}
	
	// Add different types of entries
	cache.Put("John Doe", "author", testResults)
	cache.Put("Jane Smith", "narrator", testResults)
	cache.PutPublisher("Penguin", 123)
	
	stats := cache.Stats()
	
	if stats["total_entries"].(int) != 3 {
		t.Errorf("Expected 3 total entries, got %d", stats["total_entries"])
	}
	
	if stats["authors"].(int) != 1 {
		t.Errorf("Expected 1 author entry, got %d", stats["authors"])
	}
	
	if stats["narrators"].(int) != 1 {
		t.Errorf("Expected 1 narrator entry, got %d", stats["narrators"])
	}
	
	if stats["publishers"].(int) != 1 {
		t.Errorf("Expected 1 publisher entry, got %d", stats["publishers"])
	}
	
	if stats["ttl_minutes"].(float64) != 5.0 {
		t.Errorf("Expected TTL 5.0 minutes, got %f", stats["ttl_minutes"])
	}
}

func TestSearchAuthorsCached(t *testing.T) {
	// This test requires mocking the searchAuthors function
	// For now, we'll test the caching behavior with a simple integration test
	
	// Reset cache for clean test
	personCache = NewPersonCache(30 * time.Minute)
	
	// First call should go to API and cache result
	// Note: This will fail if Hardcover API is not accessible, but that's expected in unit tests
	// In a real implementation, we'd mock the searchAuthors function
	
	t.Skip("Integration test - requires API access and mocking setup")
}

func TestSearchNarratorsCached(t *testing.T) {
	// Similar to authors test - would need mocking for proper unit testing
	t.Skip("Integration test - requires API access and mocking setup")
}

func TestSearchPublishersCached(t *testing.T) {
	// Similar to other search tests - would need mocking for proper unit testing
	t.Skip("Integration test - requires API access and mocking setup")
}

func TestCacheKeyGeneration(t *testing.T) {
	tests := []struct {
		name      string
		queryType string
		expected  string
	}{
		{"John Doe", "author", "author:john doe"},
		{"  Jane Smith  ", "narrator", "narrator:jane smith"},
		{"PENGUIN BOOKS", "publisher", "publisher:penguin books"},
		{"", "author", "author:"},
	}
	
	for _, test := range tests {
		result := generateCacheKey(test.name, test.queryType)
		if result != test.expected {
			t.Errorf("generateCacheKey(%q, %q) = %q, expected %q", 
				test.name, test.queryType, result, test.expected)
		}
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	cache := NewPersonCache(5 * time.Minute)
	
	testResults := []PersonSearchResult{
		{ID: 1, Name: "John Doe"},
	}
	
	// Test concurrent reads and writes
	done := make(chan bool, 10)
	
	// Start multiple goroutines doing cache operations
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				cache.Put("Author"+string(rune(id)), "author", testResults)
				cache.Get("Author"+string(rune(id)), "author")
				cache.Stats()
			}
			done <- true
		}(i)
	}
	
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				cache.GetCrossRole("Narrator"+string(rune(id)), "narrator")
				cache.PutPublisher("Publisher"+string(rune(id)), id*100)
				cache.GetPublisher("Publisher"+string(rune(id)))
			}
			done <- true
		}(i)
	}
	
	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
	
	// If we get here without deadlock or race conditions, the test passes
	stats := cache.Stats()
	if stats["total_entries"].(int) == 0 {
		t.Error("Expected some entries after concurrent operations")
	}
}

func TestCacheInitialization(t *testing.T) {
	// Test that initCache doesn't panic and sets up background cleanup
	// This is mainly a smoke test since the cleanup goroutine runs in background
	
	// Store original cache
	originalCache := personCache
	defer func() { personCache = originalCache }()
	
	// Reset cache
	personCache = NewPersonCache(100 * time.Millisecond)
	
	// Call initCache
	initCache()
	
	// Add some data
	testResults := []PersonSearchResult{{ID: 1, Name: "Test"}}
	personCache.Put("Test", "author", testResults)
	
	// Verify data exists
	_, found := personCache.Get("Test", "author")
	if !found {
		t.Error("Expected cache hit after initialization")
	}
	
	// Wait for potential cleanup cycle
	time.Sleep(200 * time.Millisecond)
	
	// Data should be expired and cleaned
	_, found = personCache.Get("Test", "author")
	if found {
		t.Error("Expected cache miss after TTL expiration and cleanup")
	}
}

func TestGetCacheStats(t *testing.T) {
	// Create a new cache for testing
	personCache := NewPersonCache(30 * time.Minute)
	
	// Add some test data to the cache
	testResults := []PersonSearchResult{{ID: 1, Name: "Test"}}
	personCache.Put("Test Author", "author", testResults)
	personCache.Put("Test Narrator", "narrator", testResults)
	personCache.PutPublisher("Test Publisher", 123)
	
	// Get stats from the cache
	stats := personCache.Stats()
	
	// Check if the stats are as expected
	if stats["total_entries"].(int) != 3 {
		t.Errorf("Expected 3 entries in stats, got %d", stats["total_entries"])
	}
	
	if stats["authors"].(int) != 1 {
		t.Errorf("Expected 1 author in stats, got %d", stats["authors"])
	}
	
	if stats["narrators"].(int) != 1 {
		t.Errorf("Expected 1 narrator in stats, got %d", stats["narrators"])
	}
	
	if stats["publishers"].(int) != 1 {
		t.Errorf("Expected 1 publisher in stats, got %d", stats["publishers"])
	}
}

// Benchmark tests for performance validation
func BenchmarkCachePut(b *testing.B) {
	cache := NewPersonCache(30 * time.Minute)
	testResults := []PersonSearchResult{{ID: 1, Name: "Test"}}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Put("Test Author", "author", testResults)
	}
}

func BenchmarkCacheGet(b *testing.B) {
	cache := NewPersonCache(30 * time.Minute)
	testResults := []PersonSearchResult{{ID: 1, Name: "Test"}}
	cache.Put("Test Author", "author", testResults)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get("Test Author", "author")
	}
}

func BenchmarkCacheCrossRoleLookup(b *testing.B) {
	cache := NewPersonCache(30 * time.Minute)
	testResults := []PersonSearchResult{{ID: 1, Name: "Test"}}
	cache.Put("Test Author", "author", testResults)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.GetCrossRole("Test Author", "narrator")
	}
}
