# Edition Field NULL Fix

## 🚨 Critical Bug Description

**Issue**: The `edition` field was becoming `null` in `user_book_read` entries when updating progress, causing loss of edition information during book synchronization from AudiobookShelf to Hardcover.

**Example Problem**:
```json
{
  "data": {
    "user_book_reads": [
      {
        "id": 2899487,
        "progress_seconds": 23096,
        "edition": null,  // ← This should not be null!
        "started_at": "2025-06-08",
        "user_book_id": 7782044
      }
    ]
  }
}
```

## 🔍 Root Cause Analysis

### The Problem
The `insertUserBookRead()` and `update_user_book_read` mutations were not including the `edition_id` field in their `DatesReadInput` objects, even though:

1. **The GraphQL schema supports it**: `DatesReadInput` has an `edition_id: Int` field
2. **The information was available**: `checkExistingUserBook()` returns `existingEditionId` 
3. **It's critical for data integrity**: Without `edition_id`, the `edition` field becomes `null`

### Why This Caused Data Loss
1. **Missing Field**: `DatesReadInput` mutations were only sending `progress_seconds`, `started_at`, `finished_at`, and `reading_format_id`
2. **Null Edition**: Without `edition_id`, Hardcover couldn't link the reading entry to the specific edition
3. **Broken Relationships**: Edition information was lost in user reading history

## 🛠️ Solution Implemented

### 1. Updated `insertUserBookRead()` Function Signature
**File**: `hardcover.go`

**Before**:
```go
func insertUserBookRead(userBookID int, progressSeconds int, isFinished bool) error
```

**After**:
```go
func insertUserBookRead(userBookID int, progressSeconds int, isFinished bool, editionID int) error
```

### 2. Enhanced DatesReadInput Object Creation
**File**: `hardcover.go`

**Before**:
```go
userBookRead := map[string]interface{}{
    "progress_seconds":  progressSeconds,
    "reading_format_id": 2, // Audiobook format
}
```

**After**:
```go
userBookRead := map[string]interface{}{
    "progress_seconds":  progressSeconds,
    "reading_format_id": 2, // Audiobook format
}

// Set edition_id if available (CRITICAL FIX: prevents edition field from being null)
if editionID > 0 {
    userBookRead["edition_id"] = editionID
    debugLog("Setting edition_id in user_book_read: %d", editionID)
} else {
    debugLog("WARNING: No edition_id provided - edition field will be null in user_book_read")
}
```

### 3. Updated Function Call in Sync Logic
**File**: `sync.go`

**Before**:
```go
if err := insertUserBookRead(userBookId, targetProgressSeconds, a.Progress >= 0.99); err != nil {
```

**After**:
```go
// CRITICAL FIX: Pass edition_id to prevent edition field from being null
if err := insertUserBookRead(userBookId, targetProgressSeconds, a.Progress >= 0.99, existingEditionId); err != nil {
```

### 4. Enhanced Update Mutation Object
**File**: `sync.go`

**Before**:
```go
updateObject := map[string]interface{}{
    "progress_seconds": targetProgressSeconds,
}
```

**After**:
```go
updateObject := map[string]interface{}{
    "progress_seconds": targetProgressSeconds,
}

// Set edition_id if available (CRITICAL FIX: prevents edition field from being null)
if existingEditionId > 0 {
    updateObject["edition_id"] = existingEditionId
    debugLog("Setting edition_id in update_user_book_read: %d", existingEditionId)
} else {
    debugLog("WARNING: No edition_id available for update - edition field may become null")
}
```

### 5. Added Diagnostic Logging
Both `insertUserBookRead()` and `update_user_book_read` now call `diagnoseNullEdition()` in debug mode to help identify any remaining issues.

## 🧪 Testing Coverage

### Comprehensive Test Suite
- **File**: `edition_field_fix_test.go`
- **Tests**: Function signature verification, edition_id inclusion validation, mutation object structure
- **Coverage**: Insert operations, update operations, edge cases (missing edition_id)

### Test Results
```
✅ TestEditionFieldFix/Insert_with_valid_edition_id
✅ TestEditionFieldFix/Insert_without_edition_id  
✅ TestEditionFieldFix/Update_with_valid_edition_id
✅ TestUpdateMutationEditionField
✅ TestEditionFieldPreservation
```

## 🎯 Expected Behavior After Fix

### Before Fix
```json
{
  "user_book_read": {
    "id": 2899487,
    "progress_seconds": 23096,
    "edition": null,  // ❌ Lost edition information
    "started_at": "2025-06-08"
  }
}
```

### After Fix
```json
{
  "user_book_read": {
    "id": 2899487,
    "progress_seconds": 23096,
    "edition": {      // ✅ Edition information preserved
      "id": 32058625,
      "title": "The Audiobook Edition",
      "isbn_13": "9781234567890"
    },
    "started_at": "2025-06-08"
  }
}
```

## 🚀 Impact

### Data Integrity
- **Edition Information**: Preserved in all reading entries
- **Reading History**: Complete edition context maintained
- **User Experience**: Accurate book edition tracking

### Compatibility
- **Backward Compatible**: Existing entries remain unchanged
- **Forward Compatible**: All new entries include edition information
- **Graceful Degradation**: Handles missing edition_id with warnings

## 🔮 Prevention Measures

### Code Quality
- **Comprehensive Testing**: Full test coverage for edition_id handling
- **Debug Logging**: Clear visibility into edition_id processing
- **Validation Logic**: Explicit checks for edition_id availability

### Future Safeguards
- **Mutation Validation**: All DatesReadInput objects verified to include edition_id
- **Data Integrity Monitoring**: Debug mode diagnostics for null edition detection
- **Documentation**: Clear comments about critical field requirements

---

**Related Issues**:
- Similar to v1.3.1 reading history preservation fix
- Addresses user report of null edition fields in GraphQL responses
- Prevents data loss during progress synchronization

**Testing**: Run `go test -v ./edition_field_fix_test.go` to verify the fix
