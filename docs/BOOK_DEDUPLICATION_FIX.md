# Book ID Deduplication Fix

## Problem Description

When looking up books in Hardcover, some books were returning incorrect book IDs due to Hardcover's book deduplication system. Books that have been marked as "Deduped" (with `book_status_id: 4`) should use their `canonical_id` instead of their own `id` to get the correct book reference.

### Specific Example
- **Book**: "The Third Gilmore Girl" by Kelly Bishop
- **Original ID**: 1197329 (marked as deduped)
- **Canonical ID**: 1348061 (the correct ID to use)
- **Issue**: Mismatch files were showing book_id 1197329 instead of the canonical 1348061

## Root Cause

The GraphQL queries used for book lookups were missing two crucial fields:
- `book_status_id` - indicates if a book is deduped (status 4)
- `canonical_id` - the ID of the canonical book to use instead

Without these fields, the system couldn't detect when a book was deduped and needed to use the canonical ID.

## Solution Implemented

### 1. Enhanced GraphQL Queries

Modified all book lookup queries to include the deduplication fields:

#### hardcover.go - Title/Author Lookup
```graphql
query BooksByTitleAuthor($title: String!, $author: String!) {
  books(where: {title: {_eq: $title}, contributions: {author: {name: {_eq: $author}}}}) {
    id
    title
    book_status_id      # NEW: Added for deduplication detection
    canonical_id        # NEW: Added for canonical ID reference
    contributions {
      author {
        name
      }
    }
  }
}
```

#### sync.go - ASIN Lookup
```graphql
query BookByASIN($asin: String!) {
  books(where: { editions: { asin: { _eq: $asin }, reading_format_id: { _eq: 2 } } }, limit: 1) {
    id
    title
    book_status_id      # NEW: Added for deduplication detection
    canonical_id        # NEW: Added for canonical ID reference
    editions(where: { asin: { _eq: $asin }, reading_format_id: { _eq: 2 } }) {
      id
      asin
      isbn_13
      isbn_10
      reading_format_id
      audio_seconds
    }
  }
}
```

#### sync.go - ISBN Lookup
```graphql
query BookByISBN($isbn: String!) {
  books(where: { editions: { isbn_13: { _eq: $isbn } } }, limit: 1) {
    id
    title
    book_status_id      # NEW: Added for deduplication detection
    canonical_id        # NEW: Added for canonical ID reference
    editions(where: { isbn_13: { _eq: $isbn } }) {
      id
      isbn_13
      isbn_10
      asin
    }
  }
}
```

#### sync.go - ISBN10 Lookup
```graphql
query BookByISBN10($isbn10: String!) {
  books(where: { editions: { isbn_10: { _eq: $isbn10 } } }, limit: 1) {
    id
    title
    book_status_id      # NEW: Added for deduplication detection
    canonical_id        # NEW: Added for canonical ID reference
    editions(where: { isbn_10: { _eq: $isbn10 } }) {
      id
      isbn_13
      isbn_10
      asin
    }
  }
}
```

### 2. Updated Result Structs

Added the new fields to all result structures:
```go
type BookResult struct {
    ID           json.Number `json:"id"`
    BookStatusID int         `json:"book_status_id"`    // NEW
    CanonicalID  *int        `json:"canonical_id"`      // NEW
    // ... other fields
}
```

### 3. Implemented Deduplication Logic

Added logic in all book lookup result processing to handle deduped books:

```go
book := result.Data.Books[0]
bookId = book.ID.String()

// Handle deduped books: use canonical_id if book_status_id = 4 (deduped)
if book.BookStatusID == 4 && book.CanonicalID != nil {
    bookId = fmt.Sprintf("%d", *book.CanonicalID)
    debugLog("Book ID %s is deduped (status 4), using canonical_id %d instead", 
        book.ID.String(), *book.CanonicalID)
}
```

## Files Modified

1. **hardcover.go**
   - `lookupHardcoverBookIDRaw()` function
   - Added `book_status_id` and `canonical_id` to GraphQL query
   - Added deduplication logic and debug logging

2. **sync.go**
   - ASIN lookup query and result processing
   - ISBN lookup query and result processing
   - ISBN10 lookup query and result processing
   - Updated all result structs to include new fields
   - Added deduplication logic to all book ID assignments

3. **book_deduplication_test.go** (new)
   - Added tests to document and validate the fix
   - Provides regression prevention

## Expected Behavior

### Before Fix
```
Book lookup for "The Third Gilmore Girl":
- Returns book_id: 1197329 (wrong - this is the deduped book)
- Mismatch files show incorrect book_id: 1197329
```

### After Fix
```
Book lookup for "The Third Gilmore Girl":
- Detects book_status_id: 4 (deduped)
- Uses canonical_id: 1348061 (correct)
- Debug log: "Book ID 1197329 is deduped (status 4), using canonical_id 1348061 instead"
- Mismatch files show correct book_id: 1348061
```

## Testing

### Automated Tests
- `TestBookDeduplicationImplementation`: Validates that the fix is properly implemented
- `TestDeduplicationCaseDocumentation`: Documents the specific case that was fixed

### Manual Testing
To verify the fix works for the reported case:
1. Look up "The Third Gilmore Girl" by "Kelly Bishop"
2. Should return book_id 1348061 (not 1197329)
3. Debug logs should show deduplication occurred

## Impact

This fix ensures that:
- All book lookups correctly handle Hardcover's deduplication system
- Mismatch files contain the correct canonical book IDs
- Progress sync works with the proper book references
- Debug logs provide visibility into when deduplication occurs

## Hardcover Book Status Reference

- `book_status_id: 1` - Normal/Active book
- `book_status_id: 4` - Deduped book (should use canonical_id)
- Other statuses - Handled normally (use original id)

## Backwards Compatibility

This fix is fully backwards compatible:
- Books without deduplication (status != 4) work exactly as before
- Only deduped books with valid canonical_ids are affected
- No breaking changes to existing functionality
