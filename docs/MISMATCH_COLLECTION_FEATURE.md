# Book Matching Mismatch Collection Feature

## Overview

The AudioBookShelf to Hardcover sync tool now includes a comprehensive mismatch collection feature that tracks and reports books that may need manual verification for correct audiobook edition matching.

## Problem Solved

Previously, warnings about potential book matching issues were only logged during sync and could easily be missed in the output. Users had no easy way to:
- Track which books might need manual review
- Get a summary of all problematic books after sync
- Access actionable recommendations for resolving issues

## Solution Implemented

### Data Structure

```go
type BookMismatch struct {
    Title      string
    Author     string
    ISBN       string
    ASIN       string
    BookID     string
    EditionID  string
    Reason     string
    Timestamp  time.Time
}
```

### Core Functions

1. **`addBookMismatch()`**: Collects mismatch information during sync
2. **`printMismatchSummary()`**: Displays formatted summary at end of sync
3. **`clearMismatches()`**: Clears collected mismatches (useful for testing)

### Integration Points

- **`syncToHardcover()`**: Replaces existing warning logs with structured mismatch collection
- **`runSync()`**: Calls `printMismatchSummary()` at the end of sync operations

## When Mismatches Are Collected

Mismatches are collected in the following scenarios:

### 1. Complete Book Lookup Failure (with AUDIOBOOK_MATCH_MODE=skip)
- Book not found in Hardcover database using ASIN, ISBN, or title/author search
- Book is skipped but mismatch is collected for manual review

### 2. Audiobook Edition Matching Failure (with AUDIOBOOK_MATCH_MODE=skip)  
- Book found in Hardcover but no audiobook edition available
- Only non-audiobook editions (ebook/physical) found
- Book is skipped but mismatch is collected for manual review

### 3. Fallback Book Matching (with AUDIOBOOK_MATCH_MODE=continue)
- ASIN lookup fails and fallback book matching is used
- No audiobook edition found for ISBN and general book matching is used
- System cannot guarantee the matched book is the correct audiobook edition
- Book sync continues but mismatch is collected for manual review

## Example Output

### No Issues Found
```
✅ No book matching issues found during sync
```

### Issues Found
```
⚠️  MANUAL REVIEW NEEDED: Found 2 book(s) that may need verification
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
1. Title: The Midnight Library
   Author: Matt Haig
   ISBN: 9781786892720
   ASIN: B08FF8Z1XR
   Hardcover Book ID: 123456
   Issue: ASIN lookup failed for ASIN B08FF8Z1XR, using fallback book matching. Progress may not sync correctly if this isn't the audiobook edition.
   Time: 2025-06-02 18:30:15
   ──────────────────────────────────────────────────────────────────────
2. Title: Atomic Habits
   Author: James Clear
   ISBN: 9780735211292
   Hardcover Book ID: 789012
   Issue: No audiobook edition found using ISBN 9780735211292, using general book matching. Progress may not sync correctly if this isn't the audiobook edition.
   Time: 2025-06-02 18:30:22
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
💡 RECOMMENDATIONS:
   1. Check if the Hardcover Book ID corresponds to the correct audiobook edition
   2. Verify progress syncing is working correctly for these books
   3. Consider updating book metadata if ISBN/ASIN is missing or incorrect
   4. Set AUDIOBOOK_MATCH_MODE=skip or AUDIOBOOK_MATCH_MODE=fail to change behavior
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

## Benefits

1. **Visibility**: Clear summary of all books that may need manual review
2. **Actionable**: Provides specific recommendations for each issue
3. **Persistent**: Collects all mismatches during sync instead of logging individually
4. **Informative**: Includes book details, Hardcover IDs, and timestamps
5. **User-Friendly**: Formatted output that's easy to read and act upon

## Environment Variable Integration

The feature works with existing `AUDIOBOOK_MATCH_MODE` settings:
- `continue` (default): Collects mismatches and continues syncing with fallback matching
- `skip`: Books are skipped to avoid wrong editions, but mismatches are still collected for review
- `fail`: Sync fails immediately on mismatch, no summary needed (process stops)

## Technical Details

- **Global Collection**: Uses global `bookMismatches` slice during sync
- **Thread-Safe**: Collection is sequential during sync operations
- **Memory Efficient**: Only stores essential information for each mismatch
- **Debug Integration**: Maintains existing debug logging alongside collection

## Future Enhancements

Potential improvements for future versions:
- Export mismatches to JSON/CSV for external analysis
- Web dashboard integration for mismatch review
- Automatic retry mechanisms for failed ASIN lookups
- Integration with external book metadata services

## Testing

The feature has been tested to ensure:
- ✅ Builds successfully without compilation errors
- ✅ Maintains backward compatibility with existing functionality
- ✅ Properly integrates with existing debug logging
- ✅ Handles edge cases (no mismatches, multiple mismatches)
- ✅ Works with all `AUDIOBOOK_MATCH_MODE` settings
