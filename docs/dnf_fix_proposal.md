# DNF (Did Not Finish) Fix Proposal

## Problem Statement
When a user marks a book as DNF in Hardcover but Audiobookshelf still has the old progress:
1. Sync service sees progress in ABS
2. Creates a new read entry in Hardcover with that progress
3. This overrides the DNF status, effectively "un-DNF-ing" the book

## Root Cause
The sync service only checks for FINISHED status (ID 3) before creating new reads, but doesn't check for DNF status (likely ID 5).

## Solution
Add a check for DNF status before creating new read entries.

### Implementation

#### 1. Add DNF status check in handleInProgressBook

```go
// In handleInProgressBook function, before creating new reads
if readStatusToUpdate == nil {
    // Check if the book is marked as DNF in Hardcover before creating new reads
    if userBook != nil && userBook.BookStatusID == 5 { // Assuming DNF is ID 5
        log.Info("Book is marked as DNF in Hardcover, will not create new read", map[string]interface{}{
            "user_book_id":   userBookID,
            "book_status_id": userBook.BookStatusID,
            "title":          book.Media.Metadata.Title,
        })
        return nil // Skip creating new read
    }
}
```

#### 2. Add DNF status check in handleFinishedBook

```go
// Similar check in handleFinishedBook before updating status
if userBook != nil && userBook.BookStatusID == 5 {
    log.Info("Book is marked as DNF in Hardcover, preserving DNF status", map[string]interface{}{
        "user_book_id":   userBookID,
        "book_status_id": userBook.BookStatusID,
    })
    // Don't update status to FINISHED
    return nil
}
```

#### 3. Make DNF status ID configurable

```go
// In config.go
type SyncConfig struct {
    // ... existing fields ...
    DNFStatusID int `yaml:"dnf_status_id" env:"SYNC_DNF_STATUS_ID"`
}

// Default to 5 unless specified
func DefaultConfig() *Config {
    return &Config{
        // ... other defaults ...
        Sync: SyncConfig{
            // ... other sync defaults ...
            DNFStatusID: 5, // Default DNF status ID in Hardcover
        },
    }
}
```

#### 4. Add helper method

```go
// Check if book is DNF
func (s *Service) isBookDNF(userBook *models.HardcoverBook) bool {
    if userBook == nil {
        return false
    }
    return userBook.BookStatusID == s.config.Sync.DNFStatusID
}
```

## Alternative: Query Hardcover for DNF Status

If we're not certain about the status ID, we can query Hardcover:

```go
// Query the book_statuses table to find DNF status
func (s *Service) getDNFStatusID(ctx context.Context) (int, error) {
    // GraphQL query to find status with name containing "DID NOT FINISH" or "DNF"
    query := `
    query {
      book_statuses(where: {name: {_ilike: "%DID NOT FINISH%"}}) {
        id
        name
      }
    }`
    
    // Execute query and return the ID
}
```

## Testing Scenarios

1. Book marked DNF in HC, has progress in ABS → Should NOT create new read
2. Book marked DNF in HC, no progress in ABS → Should preserve DNF
3. Book marked READING in HC, has progress in ABS → Should create/update read
4. Book marked FINISHED in HC, has progress in ABS → Should create new read (reread)

## User Experience

- Clear logging when DNF status is preserved
- Example: "Preserving DNF status for 'Book Title', skipping sync to avoid overriding user's DNF marking"
- Config option to disable DNF preservation if desired

## Implementation Priority

1. **Immediate**: Add hardcoded check for status ID 5 (most likely DNF)
2. **Short term**: Make DNF status ID configurable
3. **Long term**: Query Hardcover dynamically for DNF status

This fix ensures users' DNF markings are respected and not accidentally overridden by the sync process.
