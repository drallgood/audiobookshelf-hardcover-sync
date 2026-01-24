# DNF (Did Not Finish) Implementation Summary

## Problem
When users marked a book as "Did Not Finish" (DNF) in Hardcover but Audiobookshelf still had the old progress, the sync service would:
1. See the progress in Audiobookshelf
2. Create a new read entry in Hardcover with that progress
3. Override the DNF status, effectively "un-DNF-ing" the book

## Solution Implemented

### 1. Configuration Option
Added `preserve_dnf` configuration option:
- Location: `sync.preserve_dnf`
- Type: boolean
- Default: `true`
- Environment variable: `SYNC_PRESERVE_DNF`

### 2. DNF Detection
Added helper method `isBookDNF()` that checks if a book has status ID 5 (DNF) in Hardcover.

### 3. Status Preservation
Modified sync logic to check for DNF status before making updates:
- In `handleInProgressBook()` - Before creating/updating reads
- In `HandleFinishedBook()` - Before marking books as finished

When DNF is detected and preservation is enabled:
- Sync is skipped for that book
- DNF status remains intact
- Clear log message indicates preservation

### 4. Code Changes

#### Configuration (`internal/config/config.go`)
```go
// PreserveDNF controls whether books marked as DNF in Hardcover should be protected from sync updates
PreserveDNF bool `yaml:"preserve_dnf" env:"SYNC_PRESERVE_DNF"`
```

#### Service (`internal/sync/service.go`)
```go
// isBookDNF checks if the book is marked as DNF (Did Not Finish) in Hardcover
func (s *Service) isBookDNF(userBook *models.HardcoverBook) bool {
    if userBook == nil {
        return false
    }
    // DNF status ID is 5 according to Hardcover API documentation
    return userBook.BookStatusID == 5
}
```

DNF checks added before sync operations:
```go
// Check if the book is marked as DNF in Hardcover
if s.config.Sync.PreserveDNF && s.isBookDNF(hcBook) {
    log.Info("Book is marked as DNF in Hardcover, preserving DNF status and skipping sync", ...)
    return nil
}
```

### 5. Testing
Created comprehensive tests in `internal/sync/dnf_test.go`:
- `TestIsBookDNF` - Tests the helper method
- `TestHandleInProgressBook_DNFStatus` - Tests DNF preservation for in-progress books
- `TestHandleInProgressBook_DNFDisabled` - Tests behavior when preservation is disabled
- `TestHandleFinishedBook_DNFStatus` - Tests DNF preservation for finished books

### 6. Documentation
- Created `docs/DNF_HANDLING.md` with detailed explanation
- Updated README.md with configuration option
- Updated config.example.yaml with default setting

## Behavior

### With DNF Preservation (default)
- Books marked DNF in Hardcover are skipped during sync
- Progress in Audiobookshelf is ignored for DNF books
- User must manually change status in Hardcover to resume syncing

### Without DNF Preservation
- DNF books are treated like any other book
- Sync will update status and create reads based on Audiobookshelf progress
- Previous behavior is maintained

## Log Examples

### DNF Preserved
```
INFO Book is marked as DNF in Hardcover, preserving DNF status and skipping sync {"user_book_id": 123, "book_status_id": 5, "title": "Book Title"}
```

### DNF Updated (when disabled)
```
INFO Successfully created new read status in Hardcover
INFO Updating book status to CURRENTLY_READING
```

## Impact
- Fixes the issue where DNF markings were accidentally lost
- Provides user control via configuration
- Maintains backward compatibility
- No breaking changes to existing configurations
