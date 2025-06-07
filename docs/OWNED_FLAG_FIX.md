# Owned Flag Fix

## Problem
Books that were already up-to-date in terms of status and progress were being skipped entirely during sync, which meant their "owned" flag was never checked or updated in Hardcover. This resulted in books that should be marked as owned remaining unmarked.

## Root Cause
The sync logic in `sync.go` would return early if a book's status and progress were already up-to-date, without checking if the owned flag needed updating:

```go
if !statusChanged && !progressChanged {
    debugLog("Book '%s' already up-to-date in Hardcover - skipping", a.Title)
    return nil
}
```

## Solution
Modified the sync logic to check and **actually fix** the owned flag even when books are otherwise up-to-date:

### 1. Enhanced `checkExistingUserBook` Function
- **File**: `hardcover.go`
- **Change**: Added `owned` and `edition_id` fields to GraphQL queries and updated return signature
- **Before**: `func checkExistingUserBook(bookId string) (int, int, int, error)`
- **After**: `func checkExistingUserBook(bookId string) (int, int, int, bool, int, error)`

The function now returns:
1. Current owned status from Hardcover's user_books table
2. Edition ID needed for the `edition_owned` mutation

### 2. Updated Sync Logic with Actual Fix
- **File**: `sync.go`
- **Change**: Modified conditional sync logic to detect AND fix owned flag differences

```go
// Check if owned flag needs updating
targetOwned := getSyncOwned()
ownedChanged := targetOwned != existingOwned

if statusChanged || progressChanged {
    needsSync = true
    // ... existing sync logic
} else if ownedChanged {
    // Handle owned flag change separately
    if targetOwned && !existingOwned && existingEditionId > 0 {
        // ACTUALLY FIX IT - mark as owned using edition_owned mutation
        if err := markBookAsOwned(fmt.Sprintf("%d", existingEditionId)); err != nil {
            debugLog("Failed to mark book '%s' as owned: %v", a.Title, err)
        } else {
            debugLog("Successfully marked book '%s' as owned", a.Title)
        }
    } else if targetOwned && !existingOwned && existingEditionId == 0 {
        debugLog("Book '%s' needs to be marked as owned but no edition_id available", a.Title)
    }
    return nil // Skip regular sync since we handled the owned flag
}
```

### 3. Integrated `markBookAsOwned` Function
- **File**: `hardcover.go`
- **Function**: Uses Hardcover's `edition_owned` GraphQL mutation
- **Integration**: Called automatically when books need to be marked as owned
- **Requirements**: Requires edition ID (retrieved from existing user_book)

## How It Works Now

**Before:** Books that were up-to-date in status/progress were skipped entirely
```go
if !statusChanged && !progressChanged {
    return nil  // Skip without checking owned flag
}
```

**After:** Owned flag is checked and **automatically fixed** when books are otherwise up-to-date
```go
if statusChanged || progressChanged {
    needsSync = true  // Regular sync
} else if ownedChanged && targetOwned && !existingOwned && hasEditionId {
    markBookAsOwned(editionId)  // ACTUALLY FIX THE OWNED FLAG!
    return nil
} else if ownedChanged {
    // Log why we can't fix it (no edition_id, or trying to unmark)
    return nil
} else {
    // Truly up-to-date, including owned flag
    return nil
}
```

## Capabilities

✅ **Detects** owned flag mismatches  
✅ **Automatically fixes** missing owned flags (when edition_id available)  
✅ **Logs** detailed information about owned flag operations  
✅ **Handles** cases where edition_id is not available  
⚠️ **Cannot unmark** owned flags (no API support for this)  

## Scenarios Handled

1. **Book needs to be marked as owned + has edition_id**: ✅ **FIXED AUTOMATICALLY**
2. **Book needs to be marked as owned + no edition_id**: ⚠️ Detected and logged, cannot fix
3. **Book should not be owned but is**: ⚠️ Detected and logged, cannot unmark
4. **Book ownership is correct**: ✅ Skipped with confirmation

## Testing
- All existing tests continue to pass
- Added comprehensive owned flag logic tests
- Tests cover all scenarios: detection, fixing, edge cases

## Configuration
Uses the existing `SYNC_OWNED` environment variable to determine the target owned status via the `getSyncOwned()` function in `config.go`.

## Benefits
1. **Automatic Fix**: Books that should be owned are now automatically marked as owned
2. **No Breaking Changes**: Existing functionality remains unchanged
3. **Comprehensive Logging**: Clear visibility into all owned flag operations
4. **Edge Case Handling**: Graceful handling of missing edition_ids and unsupported operations
