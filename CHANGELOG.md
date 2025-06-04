# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v1.2.2] - 2025-06-04

### Fixed
- **Runtime Fix**: Fixed JSON unmarshaling error in `getCurrentUser()` function where Hardcover's `me` query returns an array instead of expected object
- **Added**: Debug logging for `getCurrentUser()` response to aid troubleshooting
- **Added**: Validation for empty user data response in `getCurrentUser()`
- **Note**: This patch enables the security features introduced in v1.2.1 to work properly

## [v1.2.1] - 2025-06-04

### Security
- **CRITICAL**: Fixed GraphQL security vulnerability where API returned data from other users
- **Fixed**: GraphQL variable type mismatch (`String!` vs `citext!`) causing query failures
- **Added**: Explicit user filtering to all GraphQL queries to prevent cross-user data leakage
- **Added**: `getCurrentUser()` function for authenticated user validation
- **Enhanced**: Defense-in-depth security with relationship-based filtering in `user_book_reads` queries
- **Fixed**: Enhanced query strategy with user-scoped fallback queries

### Technical  
- Updated `checkExistingUserBook()` with proper user filtering and improved ordering
- Added user validation to `checkExistingUserBookRead()` and `checkExistingFinishedRead()`
- Changed GraphQL ordering from invalid `created_at` to valid `started_at` field
- All queries now include `user: { username: { _eq: $username } }` filtering

## [v1.2.0] - 2025-06-03

### Added
- **Enhanced Mismatch Collection**: Upgraded book mismatch tracking system with rich metadata
  - Added detailed metadata fields: subtitle, narrator, publisher, published year/date, duration
  - Duration display in human-readable hours format (e.g., "18.1 hours")
  - Enhanced `Audiobook` struct to carry full metadata through sync process
  - Comprehensive mismatch summaries with all available book information
  - Improved manual review process with better identification data
- **Backward Compatibility**: Preserved original `addBookMismatch()` function for existing integrations

### Technical
- Extended `BookMismatch` struct with 7 additional metadata fields
- Created `addBookMismatchWithMetadata()` function with metadata processing
- Updated sync process to use enhanced metadata collection at all mismatch points
- Added metadata flow from `fetchAudiobookShelfStats()` through to mismatch collection

## [v1.1.0] - 2025-06-02

### Added
- **Book Matching Mismatch Collection**: New comprehensive system to track and report books that may need manual verification
  - Collects three types of mismatches: complete lookup failures, audiobook edition failures, and fallback matches
  - Provides detailed summaries after sync with actionable recommendations
  - Helps users identify books requiring manual review
- **Configurable Audiobook Edition Matching**: New `AUDIOBOOK_MATCH_MODE` environment variable
  - `continue` (default): Log warning and sync with available book data
  - `skip`: Skip problematic books to avoid wrong edition syncs  
  - `fail`: Stop sync immediately when audiobook edition cannot be verified
- **Conditional Sync Logic**: Smart API usage to avoid unnecessary Hardcover API calls
  - Checks existing read status before making changes
  - Only syncs when progress has actually changed
  - Reduces API load and improves performance
- **Version Injection**: Build system now properly injects version information
- **Comprehensive Documentation**: Organized feature docs in `docs/` folder

### Fixed
- **Critical**: Duplicate user_book_read entries spam in Hardcover feed
- **Critical**: Progress data mapping bug in AudioBookShelf sync
- **Critical**: Detection of manually finished books in AudiobookShelf
- **Critical**: Include 100% completed books in sync process (was excluding them)
- **Critical**: `getMinimumProgressThreshold()` returning 0.0 instead of correct default 0.01
- GraphQL mutation errors with user_book_read operations
- GraphQL query syntax issues in book lookup functions
- Improved _ilike to _eq for more precise GraphQL queries
- Enhanced response validation for GraphQL mutations
- Input validation for configuration threshold values

### Changed
- **Major Code Refactoring**: Extracted sync functionality into separate modules for improved maintainability
  - Split monolithic `main.go` (~774 lines) into focused modules
  - Created `sync.go` (630 lines) for core sync logic
  - Created `audiobookshelf.go` for AudiobookShelf API interactions
  - Created `hardcover.go` for Hardcover API interactions
  - Created `config.go` for configuration management
  - Created `types.go` for data structure definitions
  - Created `utils.go` for utility functions
  - Created `mismatch.go` for book matching mismatch handling
  - Reduced `main.go` to 144 lines (startup, HTTP endpoints, lifecycle management)

### Technical Improvements
- Better separation of concerns between application lifecycle and business logic
- Improved code organization and modularity
- Enhanced maintainability for future development
- Easier testing of individual components
- Multi-architecture Docker builds (amd64, arm64)
- Build reproducibility and cache optimization
- All existing functionality preserved and tested

### Notes
- This is a major feature and refactoring release with **no breaking changes**
- All existing features and API compatibility maintained
- No configuration changes required for existing users
- New environment variables are optional with sensible defaults

## [v1.0.0] - Previous Release
- Initial stable release with full sync functionality between AudiobookShelf and Hardcover
