# DNF (Did Not Finish) Status Handling

## Overview

The sync service now supports preserving books marked as "Did Not Finish" (DNF) in Hardcover. This prevents accidentally overriding a user's DNF marking when syncing with Audiobookshelf.

## How It Works

When a book is marked as DNF in Hardcover (status ID 5):
- The sync service will detect this status before making any updates
- If `preserve_dnf` is enabled (default), the sync will be skipped for that book
- The DNF status remains intact in Hardcover
- A clear log message indicates the DNF status was preserved

## Configuration

Add to your `config.yaml`:

```yaml
sync:
  preserve_dnf: true  # Default: true
```

Or set environment variable:
```bash
export SYNC_PRESERVE_DNF=true
```

## Behavior Scenarios

### Scenario 1: DNF in Hardcover, Progress in Audiobookshelf
- **Before fix**: Sync would create a new read entry, overriding DNF
- **After fix**: Sync detects DNF and skips, preserving the DNF status

### Scenario 2: DNF in Hardcover, No Progress in Audiobookshelf
- Sync detects DNF and skips, preserving the DNF status

### Scenario 3: User wants to resume a DNF book
- User must first change the status in Hardcover from DNF to "Currently Reading" or "Want to Read"
- Then sync will work normally

### Scenario 4: DNF preservation disabled
- If `preserve_dnf: false`, the sync will update DNF books like any other
- This allows users who want different behavior to opt out

## Hardcover Status IDs

For reference, Hardcover uses these status IDs:
- 1: Want to Read
- 2: Currently Reading
- 3: Read (Finished)
- 4: Paused
- 5: Did Not Finish
- 6: Ignored

## Log Messages

When DNF status is preserved, you'll see:
```
INFO Book is marked as DNF in Hardcover, preserving DNF status and skipping sync {"user_book_id": 123, "book_status_id": 5, "title": "Book Title"}
```

## Implementation Details

The DNF check is performed in:
1. `handleInProgressBook` - Before creating/updating reads
2. `HandleFinishedBook` - Before marking books as finished
3. The `isBookDNF()` helper method checks for status ID 5

This ensures DNF books are protected from all sync operations that might override their status.
