# Conditional Sync Implementation

## Overview

The AudioBookShelf to Hardcover sync tool has been enhanced with conditional sync logic to avoid unnecessary API calls and updates. The sync now only inserts/updates progress data in Hardcover if:

1. The book isn't already in the user's Hardcover library, OR
2. The progress status has actually changed significantly

## Key Changes

### New Function: `checkExistingUserBook()`

```go
func checkExistingUserBook(bookId string) (int, int, int, error)
```

This function queries Hardcover to check if the user already has a book in their library and returns:
- `userBookId`: The existing user book ID (0 if not found)
- `currentStatusId`: Current reading status (2=currently reading, 3=read)
- `currentProgressSeconds`: Current progress in seconds
- `error`: Any error that occurred

### Enhanced `syncToHardcover()` Logic

The sync function now follows this flow:

1. **Book Lookup**: Find the book in Hardcover (unchanged)
2. **Existing Book Check**: Query user's library for this book
3. **Status Comparison**: Calculate target status and progress
4. **Conditional Sync**: Only sync if needed
5. **Progress Update**: Only update if progress changed significantly

### Sync Conditions

A book will be synced if:

- **Book doesn't exist**: New book will be added to user's library
- **Status changed**: Reading status changed (e.g., currently reading → read)
- **Progress changed significantly**: Progress difference > 30 seconds OR > 10% of target progress

### Skip Conditions

A book will be skipped if:

- **Already up-to-date**: Same status and similar progress already exists
- **Minimal progress change**: Progress difference is less than threshold

## Example Debug Output

### New Book (Will Sync)
```
[DEBUG] No existing user book found for bookId=123456
[DEBUG] Book 'Example Audiobook' not found in user's Hardcover library - will create
[DEBUG] Creating new user book for 'Example Audiobook' with status_id=3
```

### Existing Book with Changed Progress (Will Sync)
```
[DEBUG] Found existing user book: userBookId=789, statusId=2, progressSeconds=1800
[DEBUG] Book 'Example Audiobook' needs update - status changed: false (2->2), progress changed: true (1800s->5400s)
[DEBUG] Syncing progress for 'Example Audiobook': 5400 seconds (75.00%)
```

### Existing Book Up-to-Date (Will Skip)
```
[DEBUG] Found existing user book: userBookId=789, statusId=3, progressSeconds=5380
[DEBUG] Book 'Example Audiobook' already up-to-date in Hardcover (status: 3, progress: 5380s) - skipping
```

## Benefits

1. **Reduced API Calls**: Eliminates unnecessary requests to Hardcover
2. **Faster Sync**: Skip books that are already synchronized
3. **Rate Limit Friendly**: Reduces chance of hitting API rate limits
4. **Accurate Status**: Only updates when there are meaningful changes
5. **Progress Threshold**: Avoids tiny progress updates that aren't meaningful

## Backward Compatibility

- All existing functionality remains unchanged
- All tests continue to pass
- Environment variables and configuration unchanged
- Same command-line interface

## Testing

The implementation includes comprehensive debug logging to monitor the conditional sync behavior. Set `DEBUG_MODE=true` in your `.env` file to see detailed sync decisions.
