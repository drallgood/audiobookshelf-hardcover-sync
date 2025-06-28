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

### Issues Found (Text Format)
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

### JSON Output Format (LOG_FORMAT=json)
When running with `LOG_FORMAT=json`, the output is structured as JSON for easier parsing and integration with monitoring systems:

```json
{
  "level": "warn",
  "time": "2025-06-28T19:50:00+02:00",
  "message": "Book matching issues found",
  "mismatch_count": 2,
  "mismatches": [
    {
      "title": "The Midnight Library",
      "author": "Matt Haig",
      "isbn": "9781786892720",
      "asin": "B08FF8Z1XR",
      "book_id": "123456",
      "issue": "ASIN lookup failed for ASIN B08FF8Z1XR, using fallback book matching",
      "time": "2025-06-02T18:30:15+02:00"
    },
    {
      "title": "Atomic Habits",
      "author": "James Clear",
      "isbn": "9780735211292",
      "book_id": "789012",
      "issue": "No audiobook edition found using ISBN 9780735211292, using general book matching",
      "time": "2025-06-02T18:30:22+02:00"
    }
  ],
  "recommendations": [
    "Check if the Hardcover Book ID corresponds to the correct audiobook edition",
    "Verify progress syncing is working correctly for these books",
    "Consider updating book metadata if ISBN/ASIN is missing or incorrect",
    "Set AUDIOBOOK_MATCH_MODE=skip or AUDIOBOOK_MATCH_MODE=fail to change behavior"
  ]
}
```

### Logging Configuration

The mismatch collection feature respects the global logging configuration:

```bash
# JSON format (default, recommended for production)
export LOG_FORMAT=json

# Text format (human-readable)
export LOG_FORMAT=text

# Enable debug logging for more detailed output
export DEBUG=true
```

## Benefits

1. **Visibility**: Clear summary of all books that may need manual review
2. **Actionable**: Provides specific recommendations for each issue
3. **Persistent**: Collects all mismatches during sync instead of logging individually
4. **Informative**: Includes book details, Hardcover IDs, and timestamps
5. **User-Friendly**: Formatted output that's easy to read and act upon

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `AUDIOBOOK_MATCH_MODE` | Controls how book matching issues are handled | `continue` |
| `LOG_FORMAT` | Output format for logs (`json` or `text`) | `json` |
| `DEBUG` | Enable debug logging | `false` |

### AUDIOBOOK_MATCH_MODE Options

- `continue` (default): 
  - Collects mismatches in the background
  - Continues syncing with fallback matching when possible
  - Best for automated environments where some mismatches are acceptable

- `skip`:
  - Collects mismatches for review
  - Skips books that can't be matched to correct audiobook editions
  - Prevents syncing progress to potentially wrong editions
  - Best for maintaining data quality

- `fail`:
  - Fails immediately on first mismatch
  - No summary is generated
  - Best for strict validation scenarios

### Example Configuration

```bash
# Strict mode - fail on any mismatch
export AUDIOBOOK_MATCH_MODE=fail

# Debug mode with JSON output
export DEBUG=true
export LOG_FORMAT=json
```

## Technical Details

- **Global Collection**: Uses global `bookMismatches` slice during sync
- **Thread-Safe**: Collection is sequential during sync operations
- **Memory Efficient**: Only stores essential information for each mismatch
- **Debug Integration**: Maintains existing debug logging alongside collection
- **Logging**: Integrates with the application's logging system
  - Respects `LOG_FORMAT` environment variable
  - Supports both structured (JSON) and human-readable output
  - Includes timestamps and log levels for better traceability

### Data Flow

1. **Detection**: Mismatches are detected during sync operations
2. **Collection**: Mismatch details are added to the global collection
3. **Processing**: Collection is processed at the end of the sync
4. **Output**: Results are formatted according to `LOG_FORMAT` setting
5. **Cleanup**: Collection is cleared for the next sync operation

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
