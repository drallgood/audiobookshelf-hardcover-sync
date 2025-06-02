# Refactoring Plan for main.go

## Current State
- **File size**: 2113 lines
- **Status**: Monolithic structure with all functionality in one file
- **Issues**: Hard to maintain, test, and navigate

## Proposed Structure

### 1. `types.go` - Data Structures (~150 lines)
- BookMismatch struct
- Library, MediaMetadata, Media, UserProgress, Item, Audiobook structs
- All API response structures

### 2. `config.go` - Configuration & Environment (~100 lines)
- Environment variable getters (getAudiobookShelfURL, getHardcoverToken, etc.)
- Configuration validation
- HTTP client setup

### 3. `audiobookshelf.go` - AudiobookShelf API Client (~500 lines)
- fetchLibraries()
- fetchLibraryItems()
- fetchUserProgress()
- fetchItemProgress()
- fetchAudiobookShelfStats()
- isBookLikelyFinished()

### 4. `hardcover.go` - Hardcover API Client (~600 lines)
- lookupHardcoverBookID()
- lookupHardcoverBookIDRaw()
- checkExistingUserBook()
- checkExistingUserBookRead()
- checkRecentFinishedRead()
- insertUserBookRead()
- All GraphQL queries and mutations

### 5. `sync.go` - Core Sync Logic (~400 lines)
- syncToHardcover()
- runSync()
- Progress calculation and status logic

### 6. `mismatch.go` - Mismatch Collection Feature (~200 lines)
- addBookMismatch()
- printMismatchSummary()
- clearMismatches()
- BookMismatch related functions

### 7. `utils.go` - Utility Functions (~100 lines)
- normalizeTitle()
- toInt()
- min()
- debugLog()
- Helper functions

### 8. `main.go` - Entry Point & HTTP Server (~200 lines)
- main()
- HTTP handlers (/sync, /healthz)
- Command-line argument parsing
- Application lifecycle management

## Benefits of Refactoring

### ✅ **Improved Maintainability**
- Easier to find and modify specific functionality
- Clear separation of concerns
- Reduced cognitive load when working on specific features

### ✅ **Better Testing**
- Can test individual modules in isolation
- Easier to mock dependencies
- More focused test files

### ✅ **Enhanced Readability**
- Logical grouping of related functions
- Clear module boundaries
- Self-documenting structure

### ✅ **Easier Collaboration**
- Multiple developers can work on different modules
- Reduced merge conflicts
- Clear ownership of functionality

### ✅ **Future Development**
- Easier to add new features
- Better foundation for additional API integrations
- Cleaner interfaces for extending functionality

## Implementation Strategy

### Phase 1: Create New Files
1. Create all new `.go` files with proper package declaration
2. Move type definitions to `types.go`
3. Move configuration functions to `config.go`

### Phase 2: Move API Clients
1. Move AudiobookShelf functions to `audiobookshelf.go`
2. Move Hardcover functions to `hardcover.go`
3. Ensure proper imports and dependencies

### Phase 3: Extract Core Logic
1. Move sync functions to `sync.go`
2. Move mismatch functions to `mismatch.go`
3. Move utilities to `utils.go`

### Phase 4: Simplify Main
1. Keep only entry point logic in `main.go`
2. Add proper imports for all modules
3. Test that everything still works

### Phase 5: Verification
1. Run all tests to ensure functionality is preserved
2. Build and test the application
3. Update documentation as needed

## File Dependencies

```
main.go
├── config.go
├── types.go
├── utils.go
├── mismatch.go
├── audiobookshelf.go
├── hardcover.go
└── sync.go
    ├── audiobookshelf.go
    ├── hardcover.go
    └── mismatch.go
```

## Estimated Impact
- **Lines per file**: 100-600 (much more manageable)
- **Reduced complexity**: Each file has a single responsibility
- **Improved navigation**: IDE can better index and search functions
- **Better Git history**: Changes are isolated to relevant modules
