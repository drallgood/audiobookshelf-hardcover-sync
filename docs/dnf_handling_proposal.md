# DNF (Did Not Finish) Handling Proposal

## Problem Statement
When users mark a book as "Did Not Finish" (DNF) in Audiobookshelf and then run the sync, the service currently:
1. Sees the book with 0% progress (since progress was discarded)
2. Creates a new read session or updates the status in Hardcover
3. Overwrites the existing DNF status in Hardcover

This is undesirable behavior as it loses the user's explicit DNF marking.

## Root Cause Analysis

### Audiobookshelf Behavior
- ABS doesn't have a dedicated DNF field
- When a user marks a book as DNF, ABS discards the progress:
  - `currentTime` becomes 0
  - `isFinished` becomes false
  - `startedAt` and `finishedAt` may be cleared

### Hardcover Status System
- Hardcover uses a `book_statuses` table with dynamic status values
- Known statuses:
  - ID 1: WANT_TO_READ
  - ID 2: READING/CURRENTLY_READING
  - ID 3: FINISHED/READ
  - Likely ID 4: DEDUPED (for canonical books)
  - Likely ID 5: DID_NOT_FINISH

### Current Sync Logic Issues
1. No awareness of DNF status in either system
2. Treats 0% progress as "not started" rather than "potentially DNF"
3. Creates new reads instead of preserving DNF status

## Proposed Solution

### 1. Detect DNF Scenarios
Add logic to detect when a book might be DNF:
- Book has 0% progress in ABS
- But has existing reads in Hardcover with progress > 0
- Or has DNF status in Hardcover

### 2. Preserve DNF Status
When DNF is detected:
- Check current status in Hardcover
- If it's DNF (status ID 5), skip syncing this book
- Log that DNF status is being preserved

### 3. Configuration Option
Add a config option to control DNF handling:
```yaml
sync:
  preserve_dnf: true  # Default: true
```

### 4. Implementation Changes

#### A. Query Hardcover for DNF Status
```go
// Add to findOrCreateUserBookID or sync logic
func (s *Service) isBookDNF(ctx context.Context, userBookID int64) (bool, error) {
    userBook, err := s.hardcover.GetUserBook(ctx, strconv.FormatInt(userBookID, 10))
    if err != nil {
        return false, err
    }
    return userBook != nil && userBook.BookStatusID == 5, nil
}
```

#### B. Check for Existing Progress
```go
// In handleInProgressBook, before creating new reads
if book.Progress.CurrentTime == 0 && !book.Progress.IsFinished {
    // Check if this might be a DNF scenario
    readStatuses, _ := s.hardcover.GetUserBookReads(ctx, hardcover.GetUserBookReadsInput{
        UserBookID: userBookID,
    })
    
    hasPreviousProgress := false
    for _, read := range readStatuses {
        if (read.ProgressSeconds != nil && *read.ProgressSeconds > 0) || read.Progress > 0 {
            hasPreviousProgress = true
            break
        }
    }
    
    if hasPreviousProgress && s.config.Sync.PreserveDNF {
        // Check current status
        isDNF, _ := s.isBookDNF(ctx, userBookID)
        if isDNF {
            log.Info("Preserving DNF status in Hardcover, skipping sync", logCtx)
            return nil
        }
    }
}
```

#### C. Add Config Option
```go
// In config.go
type SyncConfig struct {
    // ... existing fields ...
    PreserveDNF bool `yaml:"preserve_dnf" env:"SYNC_PRESERVE_DNF"`
}
```

### 5. Alternative Approaches

#### Option A: DNF Detection Heuristic
If Hardcover doesn't have DNF status:
- Detect DNF when: 0% progress + previous progress existed
- Ask user via log/config how to handle
- Options: skip, mark as WANT_TO_READ, or create new read

#### Option B: DNF Status Mapping
- Map ABS behavior to Hardcover DNF:
  - When progress goes from >0 to 0 between syncs
  - And user hasn't finished the book
  - Then mark as DNF in Hardcover

### 6. User Experience

#### Logging
- Clear logs when DNF is detected and preserved
- Example: "Book 'Title' appears to be DNF in Audiobookshelf (0% progress after previous reads). Preserving DNF status in Hardcover."

#### Configuration
- Default to preserving DNF status
- Allow users to disable if they want different behavior

## Implementation Priority

1. **High Priority**: Detect and preserve existing DNF status in Hardcover
2. **Medium Priority**: Add config option for DNF handling
3. **Low Priority**: Automatic DNF detection and status setting

## Testing Scenarios

1. Book marked DNF in Hardcover, 0% progress in ABS → Should preserve DNF
2. Book with progress in ABS, DNF in Hardcover → Should update (user is rereading)
3. Book 0% progress, no previous reads → Should create WANT_TO_READ (if enabled)
4. Book with previous reads, now 0% progress, not DNF in HC → User choice

## Backward Compatibility

- Default behavior preserves DNF status (safer)
- Users can opt-out if they want previous behavior
- No breaking changes to existing configs
