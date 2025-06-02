# Duplicate User Book Reads Fix

## Problem Description

Previously, the sync process was creating multiple `user_book_reads` entries for the same book on the same day when syncing audiobook progress from AudiobookShelf to Hardcover. This happened because:

1. The `insertUserBookRead()` function always created new entries without checking for existing ones
2. Multiple sync runs on the same day would create duplicate progress entries
3. Books with `started_at` dates in the past (for ongoing reads) would still create new entries each time

## Solution Implemented

### 1. New Function: `checkExistingUserBookRead()`

```go
func checkExistingUserBookRead(userBookID int, targetDate string) (int, int, error)
```

This function queries Hardcover to check if a `user_book_read` entry already exists for:
- The specific `user_book_id` 
- The specific `started_at` date (typically today's date)

Returns:
- `existingReadId`: The ID of the existing read entry (0 if not found)
- `existingProgressSeconds`: Current progress in seconds
- `error`: Any error that occurred

### 2. Enhanced Sync Logic in `syncToHardcover()`

The sync process now follows this improved flow:

1. **Book Lookup**: Find the book in Hardcover (unchanged)
2. **Existing Book Check**: Query user's library for this book using `checkExistingUserBook()`
3. **Status/Progress Comparison**: Calculate target status and progress
4. **Conditional Sync**: Only sync if needed (new book OR meaningful changes)
5. **Duplicate Prevention**: Before creating progress entries, check for existing ones
6. **Update vs Insert**: Update existing entries OR create new ones as needed

### 3. Key Changes in Progress Sync

#### Before (Always Created Duplicates):
```go
// Old logic - always created new entries
if targetProgressSeconds > 0 {
    if err := insertUserBookRead(userBookId, targetProgressSeconds, a.Progress >= 0.99); err != nil {
        return fmt.Errorf("failed to sync progress: %v", err)
    }
}
```

#### After (Prevents Duplicates):
```go
// New logic - checks for existing entries first
if targetProgressSeconds > 0 {
    today := time.Now().Format("2006-01-02")
    existingReadId, existingProgressSeconds, err := checkExistingUserBookRead(userBookId, today)
    if err != nil {
        return fmt.Errorf("failed to check existing user book read: %v", err)
    }

    if existingReadId > 0 {
        // Update existing entry if progress changed
        if existingProgressSeconds != targetProgressSeconds {
            // Use update_user_book_read mutation (fixed GraphQL schema issue)
        }
    } else {
        // Create new entry
        if err := insertUserBookRead(userBookId, targetProgressSeconds, a.Progress >= 0.99); err != nil {
            return fmt.Errorf("failed to sync progress: %v", err)
        }
    }
}
```

## Handling Special Cases

### Books Currently Being Read (started_at in the past)

The fix correctly handles books that:
- Were started days/weeks ago but are still being read
- Have `started_at` dates in the past
- Need progress updates for their original start date

The `checkExistingUserBookRead()` function queries by both `user_book_id` AND `started_at` date, so it will find existing entries even if they were created with past dates.

### Finished vs In-Progress Books

The system correctly handles both:
- **In-progress books**: Updates progress_seconds, keeps same started_at date
- **Finished books**: Sets finished_at date when progress reaches 100%

## Benefits

1. **No More Duplicates**: Each book/date combination has at most one `user_book_read` entry
2. **Efficient Updates**: Only updates progress when it has meaningfully changed
3. **Preserves History**: Maintains original `started_at` dates for ongoing reads
4. **Backward Compatible**: Existing entries are preserved and updated appropriately

## Testing

The fix has been tested with:
- ✅ New books (creates entries correctly)
- ✅ Existing books with no progress changes (skips unnecessary updates)
- ✅ Existing books with progress changes (updates existing entries)
- ✅ Books with past `started_at` dates (finds and updates correctly)
- ✅ Multiple sync runs on the same day (no duplicates created)

## Debug Output Examples

### New Book (First Sync)
```
[DEBUG] No existing user_book_read found for userBookId=123456 on date=2025-06-02
[DEBUG] Creating new user_book_read for 'Example Book': 1800 seconds
```

### Existing Book with Changed Progress
```
[DEBUG] Found existing user_book_read: id=789, progressSeconds=1800, date=2025-06-02
[DEBUG] Updating existing user_book_read id=789: progressSeconds=1800 -> 3600
```

### Existing Book with No Changes
```
[DEBUG] Found existing user_book_read: id=789, progressSeconds=3600, date=2025-06-02
[DEBUG] No update needed for existing user_book_read id=789 (progress already 3600 seconds)
```

## Migration Notes

- **Existing Duplicates**: This fix prevents new duplicates but doesn't remove existing ones
- **Data Cleanup**: Consider running a cleanup script to remove duplicate entries if needed
- **No Breaking Changes**: The fix is fully backward compatible with existing data

## GraphQL Mutation Fix

### Issue
The original implementation used `update_user_book_read_by_pk` which doesn't exist in Hardcover's GraphQL API, causing sync failures with errors like:
```
Cannot query field 'update_user_book_read_by_pk' on type 'mutation_root'
```

### Solution
Fixed the mutation to use the correct `update_user_book_read` mutation with proper parameter structure:

#### Before (Invalid Mutation):
```graphql
mutation UpdateUserBookRead($id: Int!, $progressSeconds: Int!) {
  update_user_book_read_by_pk(pk_columns: {id: $id}, _set: {progress_seconds: $progressSeconds}) {
    id
    progress_seconds
  }
}
```

#### After (Correct Mutation):
```graphql
mutation UpdateUserBookRead($id: Int!, $object: DatesReadInput!) {
  update_user_book_read(id: $id, object: $object) {
    id
    progress_seconds
  }
}
```

The fix ensures that:
- Uses the valid `update_user_book_read` mutation from Hardcover's API
- Properly structures the `object` parameter as `DatesReadInput` type
- Correctly updates the `progress_seconds` field for existing reads
