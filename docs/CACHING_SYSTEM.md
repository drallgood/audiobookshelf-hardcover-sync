# Caching System Implementation

## Overview
The AudiobookShelf-Hardcover sync tool now includes a comprehensive caching system to improve performance by avoiding redundant API calls during author, narrator, and publisher lookups.

## Features

### 1. Thread-Safe Caching
- **Thread-safe operations**: All cache operations are protected with read/write mutexes
- **Concurrent access**: Multiple goroutines can safely access the cache simultaneously
- **Memory efficient**: Uses pointer-based entries to minimize memory overhead

### 2. TTL-Based Expiration
- **Configurable TTL**: Cache entries expire after 30 minutes by default
- **Automatic cleanup**: Background goroutine removes expired entries every 10 minutes
- **Fresh data**: Ensures API data doesn't become stale over long sync operations

### 3. Cross-Role Lookup Capability
- **Smart discovery**: Can find people under different roles (author vs narrator)
- **Reduced API calls**: If someone is cached as an author, narrator searches will find them
- **Bidirectional**: Works both ways - authors can be found when searching for narrators

### 4. Comprehensive Coverage
- **Authors**: Caches `searchAuthors()` results
- **Narrators**: Caches `searchNarrators()` results  
- **Publishers**: Caches `searchPublishers()` results
- **All lookups**: Both interactive CLI and mismatch processing use cached versions

## Implementation Details

### Cache Structure
```go
type CacheEntry struct {
    Results       []PersonSearchResult `json:"results"`
    PublisherId   int                  `json:"publisher_id,omitempty"`
    Timestamp     time.Time            `json:"timestamp"`
    QueryType     string               `json:"query_type"`
    OriginalQuery string               `json:"original_query"`
}
```

### Cached Functions
- `searchAuthorsCached()` - Cached wrapper for `searchAuthors()`
- `searchNarratorsCached()` - Cached wrapper for `searchNarrators()`
- `searchPublishersCached()` - Cached wrapper for `searchPublishers()`

### Integration Points
- **Mismatch Processing**: `processAuthorsWithLookup()`, `processNarratorsWithLookup()`, `processPublisherWithLookup()`
- **CLI Tools**: All interactive lookup commands (`--lookup-author`, `--lookup-narrator`, etc.)
- **Bulk Operations**: Bulk lookup commands for processing multiple names

## Performance Benefits

### Benchmark Results
```
BenchmarkCachePut-10           6,780,279    177.4 ns/op    200 B/op    7 allocs/op
BenchmarkCacheGet-10           8,322,330    144.1 ns/op    104 B/op    6 allocs/op
BenchmarkCacheCrossRole-10     7,859,952    152.7 ns/op    120 B/op    7 allocs/op
```

### Real-World Impact
- **Reduced API calls**: Eliminates duplicate lookups for the same person
- **Faster sync times**: Subsequent lookups are nearly instantaneous
- **Lower rate limiting**: Fewer API calls means less chance of hitting Hardcover's 60/minute limit
- **Improved reliability**: Less dependent on network conditions during long sync operations

## Usage Statistics

### Sync Logging
The cache provides detailed statistics during sync operations:

**At Sync Start:**
```
[CACHE] Starting sync with cache stats: 0 total entries (0 authors, 0 narrators, 0 publishers)
```

**At Sync Completion:**
```
[CACHE] Final cache stats: 45 total entries (28 authors, 12 narrators, 5 publishers)
```

### Debug Information
When debug logging is enabled, the cache provides detailed hit/miss information:
```
Cache: Hit for 'John Smith' (author) - 3 results
Cache: Cross-role hit for 'Jane Doe' - found as narrator, requested as author
Cache: Miss for 'New Author' (author) - performing API search
```

## Configuration

### Environment Variables
No additional environment variables are required. The caching system is automatically enabled and configured with sensible defaults:

- **TTL**: 30 minutes
- **Cleanup interval**: 10 minutes
- **Automatic initialization**: Starts with the main application

### Tuning Options
If needed, cache parameters can be adjusted in `cache.go`:
```go
var (
    personCache = NewPersonCache(30 * time.Minute) // Adjust TTL here
)

// In initCache() function:
go func() {
    ticker := time.NewTicker(10 * time.Minute) // Adjust cleanup interval
    // ...
}()
```

## Testing

### Test Coverage
The caching system includes comprehensive tests:
- **Unit tests**: Basic cache operations (put, get, expiration)
- **Concurrency tests**: Thread-safety validation
- **Cross-role tests**: Verification of cross-role lookup functionality
- **Performance tests**: Benchmarks for cache operations
- **Integration tests**: Validation with existing sync workflow

### Running Tests
```bash
# Run cache-specific tests
go test -v -run TestCache

# Run performance benchmarks
go test -bench=BenchmarkCache

# Run all tests
go test -v ./...
```

## Monitoring and Debugging

### Cache Statistics
Cache statistics are logged at the start and end of each sync operation, providing visibility into:
- Total number of cached entries
- Breakdown by type (authors, narrators, publishers)
- Cache effectiveness over time

### Debug Logging
Enable debug logging to see detailed cache hit/miss information:
```bash
export DEBUG=true
./main
```

## Future Enhancements

### Potential Improvements
1. **Hit/Miss Statistics**: Add counters to track cache effectiveness
2. **Persistent Caching**: Save cache to disk between runs
3. **Cache Size Limits**: Implement LRU eviction for memory management
4. **Configurable TTL**: Make TTL configurable via environment variables
5. **Cache Warming**: Pre-populate cache with common authors/narrators

### Performance Monitoring
Consider adding metrics export for monitoring systems:
- Cache hit rate
- Average lookup time
- Memory usage
- API call reduction percentage

## Conclusion

The caching system significantly improves the performance and reliability of the AudiobookShelf-Hardcover sync tool by:
- Reducing redundant API calls
- Providing fast lookups for repeated queries
- Supporting cross-role discovery
- Maintaining thread safety in concurrent operations
- Offering comprehensive monitoring and debugging capabilities

The implementation follows Go best practices and integrates seamlessly with the existing codebase while providing substantial performance benefits for users syncing large libraries.
