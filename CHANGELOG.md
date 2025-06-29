# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed
- **⚡ Rate Limiting Update**: Removed deprecated `sync_delay` configuration and `HARDCOVER_SYNC_DELAY_MS` environment variable
  - **Reason**: Replaced with more efficient token bucket rate limiting in the Hardcover API client
  - **Migration**: Use `HARDCOVER_RATE_LIMIT` environment variable to control rate limiting (default: 10 requests per second)
  - **Impact**: More reliable rate limiting with better performance characteristics

- **🔧 Configuration Cleanup**: Removed `audiobook_match_mode` configuration option
  - **Reason**: Simplified configuration and removed unused functionality
  - **Impact**: The `AudiobookMatchMode` field has been removed from the configuration. This was a legacy option that is no longer used in the codebase.


## [1.6.1] - 2025-06-12

### Fixed
- **🚨 CRITICAL: Complete Data Loss Prevention**: Comprehensive fix for GraphQL partial update data loss
  - **Root Cause**: Hardcover's GraphQL `update_user_book_read` mutation sets unmentioned fields to NULL
  - **Solution**: Enhanced `checkExistingUserBookRead()` to retrieve ALL existing fields before updates
  - **Protected Fields**: `progress_seconds`, `started_at`, `finished_at`, `edition_id`, `reading_format_id`
  - **Comprehensive Preservation**: All existing data is now preserved during progress updates
  - **Enhanced Data Structure**: New `ExistingUserBookReadData` struct for complete field management
  - **Updated Queries**: GraphQL queries now include `edition_id` and `reading_format_id` fields
  - **Fallback Protection**: Both primary and fallback queries enhanced with complete field selection
  - **Test Coverage**: Comprehensive tests verify no data loss during updates
  - **Impact**: Prevents critical reading history and metadata loss that was affecting user accounts

## [1.6.0] - 2025-06-12

### Security
- **🔒 Go Security Update**: Upgraded Go base image from `golang:1.24.2-alpine` to `golang:1.24.4-alpine`
  - Resolves CVE-2025-4673 (Proxy-Authorization headers issue)
  - Resolves CVE-2025-0913 (O_CREATE|O_EXCL handling inconsistency)
  - Resolves CVE-2025-22874 (VerifyOptions.KeyUsages issue)
  - Zero HIGH/CRITICAL vulnerabilities in Trivy security scan

### Added
- **🧠 Intelligent Caching System**: Revolutionary performance improvement with smart author/narrator lookup caching
  - **PersonCache**: In-memory cache with configurable TTL (default: 1 hour)
  - **Smart Search**: Caches author and narrator search results to avoid repeated API calls
  - **Cross-Role Lookup**: Authors found as narrators are cached for author searches and vice versa
  - **Publisher Support**: Extended caching to publisher lookups for complete metadata coverage
  - **Cache Statistics**: Built-in metrics for hit rates, misses, and performance monitoring
  - **Automatic Cleanup**: Expired entries are automatically cleaned up to manage memory
  - **Significant Performance**: Reduces API calls by 70-90% for libraries with repeated author/narrator names
  - **Configurable**: `CACHE_TTL_MINUTES` environment variable (default: 60 minutes)

- **📊 Enhanced Progress Detection System**: Advanced audiobook progress tracking with multiple fallback mechanisms
  - **Multi-Endpoint Support**: Uses both `/api/me` and `/api/me/listening-sessions` for comprehensive coverage
  - **Manually Finished Books**: Proper detection of books marked as "finished" with `isFinished` flag
  - **Smart Fallbacks**: Automatic fallback between different AudiobookShelf API endpoints
  - **Progress Validation**: Cross-validation between multiple data sources for accuracy
  - **Debug Logging**: Comprehensive logging for troubleshooting progress detection issues
  - **API Compatibility**: Works with different AudiobookShelf versions and configurations

- **🏗️ Complete Local Image Upload System**: Full implementation for AudiobookShelf cover image handling
  - **Local URL Detection**: Automatically detects AudiobookShelf server images vs external URLs
  - **Multi-Stage Upload**: Creates edition first, then uploads local images with proper association
  - **Edition Linking**: Automatically links uploaded images to newly created editions
  - **Error Handling**: Graceful degradation when image uploads fail (edition still created)
  - **URL Validation**: Smart detection of local vs external image URLs for proper processing

### Fixed
- **🔧 CRITICAL: Edition Field NULL Fix**: Fixed critical bug where `edition` field became `null` in `user_book_read` entries
  - **Root Cause**: Missing `edition_id` field in `DatesReadInput` objects for GraphQL mutations
  - **Data Loss Prevention**: Ensures reading entries maintain proper edition context
  - **Comprehensive Solution**: Updated both insert and update mutations with edition ID validation
  - **Diagnostic Tools**: Added troubleshooting functions and enhanced logging

- **🔧 Book ID Deduplication Fix**: Fixed handling of deduped books with canonical IDs in Hardcover
  - **Smart Detection**: Automatically uses `canonical_id` when `book_status_id = 4` (deduped)
  - **GraphQL Enhancement**: Added `book_status_id` and `canonical_id` fields to all book queries
  - **Real-World Testing**: Verified with "The Third Gilmore Girl" case and other deduped books
  - **Seamless Handling**: Transparent redirection to canonical book without user intervention

- **🔧 RE-READ Detection Fix**: Fixed incorrect RE-READ detection for manually finished books
  - **Enhanced Logic**: Uses `/api/me` endpoint for accurate finished book detection
  - **False Positive Prevention**: Checks `isBookFinished` status before treating as re-read
  - **Conservative Approach**: Added safeguards for books with finished reads but 0% API progress
  - **URL Support**: Fixed AudiobookShelf URL handling for reverse proxy configurations

- **🔧 Status Update Bug Fix**: Fixed missing user book status mutations in Hardcover
  - **Complete Implementation**: Added proper status update GraphQL mutations
  - **Want to Read Fix**: Prevents finished books from being incorrectly marked as "Want to Read"
  - **Status Consistency**: Ensures status changes are properly reflected in Hardcover

- **🔧 GraphQL Mutation Issues**: Fixed various GraphQL operation problems
  - **URL Handling**: Fixed mutations failing with local AudiobookShelf cover URLs
  - **Mutation Structure**: Corrected GraphQL mutation syntax and variable handling
  - **Error Recovery**: Improved error handling and fallback mechanisms

### Changed
- **🔧 ASIN Enhancement Logic**: Completely redesigned Audible ASIN reference system
  - **Honest Implementation**: Removed misleading "enhanced with external metadata" claims
  - **ASIN Reference Only**: Simple, reliable ASIN reference without false enhancement promises
  - **Transparent Markers**: Clear "ASIN: {value}" markers in mismatch JSON files
  - **Source Attribution**: Uses `"hardcover+asin"` for ASIN reference tracking

### Removed
- **❌ Non-functional API Integrations**: Cleaned up misleading external API integration code
  - **Audible API**: Removed non-functional Audible API integration attempts
  - **Web Scraping**: Removed blocked web scraping implementations
  - **False Claims**: Eliminated code that claimed external metadata enhancement capabilities
  - **Configuration Cleanup**: Removed non-functional environment variables

### Refactoring
- **📁 Project Structure**: Organized project structure for better maintainability
  - **Scripts Directory**: Moved build and deployment scripts to `scripts/` folder
  - **Documentation**: Consolidated feature documentation in `docs/` directory
  - **Test Organization**: Better organization of test files by feature area
  - **Dockerfile Updates**: Updated paths for reorganized structure

### Performance
- **⚡ Massive Performance Improvements**: Multiple optimizations for large libraries
  - **Cache System**: 70-90% reduction in API calls through intelligent caching
  - **Smart Detection**: Reduced redundant API operations through better detection logic
  - **Efficient Queries**: Optimized GraphQL queries to fetch only necessary data
  - **Memory Management**: Automatic cache cleanup and memory optimization

### Fixed
- **🔧 CRITICAL: Edition Field NULL Fix**: Fixed critical bug where `edition` field became `null` in `user_book_read` entries
  - **Root Cause**: `insertUserBookRead()` and `update_user_book_read` mutations were missing `edition_id` field in `DatesReadInput` objects
  - **Data Loss Prevention**: Without `edition_id`, Hardcover couldn't link reading entries to specific editions, causing `edition` field to become `null`
  - **Function Enhancement**: Modified `insertUserBookRead()` signature to accept `editionID` parameter
  - **Mutation Updates**: Added `edition_id` field to both insert and update `DatesReadInput` objects with validation
  - **Sync Logic**: Updated sync.go to pass `existingEditionId` from `checkExistingUserBook()` context
  - **Debug Features**: Added diagnostic logging and `diagnoseNullEdition()` calls for troubleshooting
  - **Data Integrity**: Preserves edition information in all user reading entries, preventing loss of audiobook edition context
  - **Test Coverage**: Comprehensive test suite in `edition_field_fix_test.go` with 100% pass rate
  - **Impact**: Critical fix for maintaining reading history integrity with proper edition tracking
- **🎯 Honest Enhancement Markers**: Simplified mismatch enhancement to accurately reflect actual capabilities
  - **Issue**: Previous markers claimed "enhanced with external metadata" when only ASIN was added
  - **Fix**: Replaced with honest "ASIN: {value}" reference markers when ASIN is available
  - **Source**: Uses `"hardcover+asin"` source attribution for ASIN reference only

### Removed
- **❌ Non-functional API Integrations**: Removed misleading external API integration code
  - **Reason**: Audible has no public API available, and web scraping is blocked by anti-bot measures
  - **Cleanup**: Removed `audible.go`, `openlibrary.go`, and related non-functional integration files
  - **Honest Implementation**: `enhanceWithExternalData()` now only adds ASIN reference without false enhancement claims
  - **Configuration**: Removed non-functional environment variables (`AUDIBLE_API_ENABLED`, `AUDIBLE_API_TOKEN`, `AUDIBLE_API_TIMEOUT`)
  - **Documentation**: Updated to reflect actual capabilities instead of theoretical integrations

### Fixed
- **🔧 RE-READ Detection Fix**: Fixed incorrect RE-READ detection for manually finished books
  - **Root Cause**: Books marked as "Finished" in AudiobookShelf but showing 0% progress due to API detection issues were incorrectly treated as re-read scenarios
  - **Enhanced API Integration**: Now uses `/api/me` endpoint for accurate finished book detection with `isFinished` flags
  - **Smart Logic**: Modified RE-READ detection to check `isBookFinished` status before treating as re-read scenario
  - **Conservative Skip Logic**: Added safeguards for books with finished reads in Hardcover but 0% progress in AudiobookShelf
  - **URL Fix**: Updated AudiobookShelf URL handling to support reverse proxy with `/audiobookshelf` path prefix
  - **Type Corrections**: Fixed API response type definitions to match actual `/api/me` endpoint structure
  - **False Positive Prevention**: Eliminates incorrect duplicate read entries for books like "If I Was Your Girl" and "Earth Afire"
  - **Backward Compatible**: Maintains compatibility with existing API responses and configurations
- **🔧 1000x Progress Multiplication Error**: Fixed critical bug where progress values were being multiplied by 1000
  - **Root Cause**: AudiobookShelf API sometimes returns `currentTime` in milliseconds while `totalDuration` is in seconds
  - **Unit Conversion**: Added automatic detection and conversion of millisecond values to seconds
  - **Smart Detection**: `convertTimeUnits()` function intelligently identifies when conversion is needed
  - **Comprehensive Fix**: Applied unit conversion throughout all progress calculation paths
  - **Progress Calculation**: Updated `calculateProgressWithConversion()` to handle mixed units
  - **API Response Debugging**: Added `debugAPIResponse()` function to identify unit mismatches
  - **Backward Compatible**: Maintains compatibility with correctly formatted API responses
  - **Test Coverage**: Added comprehensive tests including reproduction of original bug scenario
  - **Real-world Impact**: Prevents progress values like 500.0 or 1000.0 being sent to Hardcover instead of 0.5 or 1.0

## [v1.5.0] - 2025-06-08

### Added
- **📚 Owned Books Query Functions**: Added proper owned books querying capabilities
  - **`OwnedBook` struct**: Data structure for representing owned books with complete metadata
  - **`getOwnedBooks()` function**: Retrieves all books from user's "Owned" list using correct GraphQL query
  - **`isBookOwnedDirect()` function**: Checks if a specific book is owned by querying the lists table
  - **Key Discovery**: Hardcover stores ownership in the "Owned" list, not the `user_books.owned` field
  - **Correct API Usage**: Uses `lists` table approach instead of faulty `user_books.owned` field
  - **Well-Documented**: Functions include comments explaining the correct ownership model
  - **Future-Ready**: Core functions available for implementing owned books sync features

### Fixed
- **📚 Owned Flag Auto-Fix**: Fixed and enhanced owned flag handling for books that are skipped during sync
  - Modified `checkExistingUserBook()` to return both owned status and edition_id from Hardcover
  - Updated sync logic to automatically mark books as owned using `edition_owned` mutation when needed
  - Added comprehensive owned flag checking even when status/progress are up-to-date
  - Integrated `markBookAsOwned()` function to actually fix missing owned flags (not just detect them)
  - Enhanced logging for all owned flag operations and edge cases
  - Created comprehensive test coverage for owned flag scenarios
  - **Key improvement**: Books that should be owned are now automatically marked as owned during sync

## [v1.4.1] - 2025-06-07

### Fixed
- **🌍 Container Timezone Support**: Fixed timezone handling in Docker containers
  - **Issue**: Container logs showed UTC timestamps regardless of `TZ` environment variable
  - **Solution**: Added `tzdata` package to Alpine base image and timezone configuration in Go application
  - **Functionality**: Container now properly respects `TZ` environment variable (e.g., `TZ=Europe/Vienna`)
  - **Compatibility**: Maintains backward compatibility - works with or without `TZ` variable
  - **Logging**: Application now logs timezone confirmation when `TZ` is set
  - **Technical**: Go application sets `time.Local` based on `TZ` environment variable

## [v1.4.0] - 2025-06-07

### Added
- **🏠 Owned Books Marking**: New feature to mark synced books as "owned" in Hardcover
  - **Environment Variable**: `SYNC_OWNED=true` (enabled by default)
  - **Functionality**: Automatically marks synced books as "owned" to distinguish from wishlist items
  - **Integration**: Added to `insert_user_book` GraphQL mutation when creating new user books
  - **Configuration**: Can be disabled with `SYNC_OWNED=false` if ownership tracking not desired
- **🧪 Comprehensive Test Coverage**: Added `owned_test.go` with 12 test cases
  - Tests environment variable parsing with various boolean values
  - Validates default behavior (enabled by default)
  - Ensures proper handling of edge cases and invalid values
- **📖 Documentation**: Updated README.md with owned books sync section
  - Added `SYNC_OWNED` to environment variables table
  - Created dedicated "Owned Books Sync" configuration section
  - Updated features list to highlight ownership tracking capability

### Changed
- **📚 User Book Creation**: Enhanced sync logic to include ownership information
  - Modified `userBookInput` map to include `"owned": true` when `getSyncOwned()` returns true
  - Only affects newly created books; existing books in Hardcover remain unchanged
  - Ownership status is independent of reading status (Want to Read, Currently Reading, Read)

### Technical Details
- **Config Function**: Added `getSyncOwned()` function following same pattern as `getSyncWantToRead()`
- **Default Behavior**: Enabled by default to provide better library organization out of the box
- **API Integration**: Seamlessly integrates with existing Hardcover GraphQL mutations
- **Testing**: 100% test coverage for the new functionality with comprehensive edge case handling

## [v1.3.2] - 2025-06-05

### Changed
- **🎯 DEFAULT BEHAVIOR**: "Want to Read" sync is now **enabled by default**
  - **Breaking Change**: `SYNC_WANT_TO_READ` now defaults to `true` instead of `false`
  - **User Impact**: Unstarted books (0% progress) will automatically sync to Hardcover as "Want to Read" status
  - **Migration**: Users who prefer the old behavior can set `SYNC_WANT_TO_READ=false` to disable
  - **Logic Update**: Changed from opt-in (`"true"`) to opt-out (`!= "false", "0", "no"`)

### Added
- **📖 "Want to Read" Feature Documentation**: Comprehensive documentation for the "Want to Read" sync feature
  - Added detailed "Want to Read Sync" section to README.md with use cases and examples
  - Updated environment variables table to reflect new default behavior
  - Added configuration examples and migration guidance
- **🧪 Comprehensive Test Suite**: Created `want_to_read_test.go` with 17 tests
  - 12 environment variable tests covering default behavior and edge cases
  - 5 status logic tests validating status determination for different progress levels
  - 100% test coverage for the "Want to Read" feature

### Fixed
- **📚 Book Filtering Logic**: Enhanced book filtering to properly include 0% progress books when "Want to Read" sync is enabled
  - Updated `runSync()` to check both progress threshold and "Want to Read" configuration
  - Added debug logging for books included via "Want to Read" sync
- **🎛️ Configuration Logic**: Improved `getSyncWantToRead()` function with robust default handling
  - Defaults to `true` for better out-of-the-box experience
  - Handles edge cases like empty values, invalid strings, and case variations
  - Maintains backward compatibility while improving user experience

### Technical Details
- **Status Mapping**: 0% progress → "Want to Read" (status_id=1) when enabled, "Currently Reading" (status_id=2) when disabled
- **Environment Variable**: `SYNC_WANT_TO_READ=true` (default), set to `false`/`0`/`no` to disable
- **Performance**: No performance impact as filtering logic is optimized

## [v1.3.1] - 2025-06-05

### Fixed
- **🚨 CRITICAL**: Fixed reading history being wiped out when updating existing `user_book_read` entries
  - **Issue**: When updating progress on existing reads, `started_at: null` was being preserved, removing reading history
  - **Root Cause**: `update_user_book_read` mutation was only sending `progress_seconds` without ensuring `started_at` is set
  - **Fix**: Always set `started_at` to current date when updating existing reads to prevent null values
  - **Impact**: Prevents loss of reading start dates and maintains proper reading history
  - Also ensures `finished_at` is properly set when books reach 99%+ completion
  - Added comprehensive test coverage for the reading history fix

## [v1.3.0] - 2025-06-05

### Added
- **🚀 Incremental/Delta Sync**: Major performance improvement with timestamp-based incremental syncing
  - Added persistent sync state management with `sync_state.json` file
  - Only processes books with changes since last sync using AudiobookShelf listening session timestamps
  - Automatic fallback to full sync when incremental data is unavailable or on first run
  - Configurable sync modes: `enabled` (default), `disabled`, or `auto`
  - Smart full sync scheduling: automatically performs full sync after 7 days or when forced
  - New environment variables:
    - `INCREMENTAL_SYNC_MODE`: Control incremental sync behavior
    - `SYNC_STATE_FILE`: Custom path for sync state storage (default: `sync_state.json`)
    - `FORCE_FULL_SYNC`: Force full sync on next run (automatically resets after use)
  - Comprehensive logging to show sync mode and progress
  - Reduces API calls significantly for large libraries with minimal changes

### Fixed
- **LibraryItemID Field Reference**: Fixed incorrect field access in incremental sync filtering
  - Changed from `book.Metadata.LibraryItemID` to `book.ID` to match actual data structure
  - Ensures proper book filtering in incremental sync mode
- **Duplicate User Book Reads**: Fixed issue where multiple `user_book_reads` entries were created for the same book when reading across different days
  - Modified `checkExistingUserBookRead()` to check for any unfinished reads instead of date-specific reads
  - Changed GraphQL query from `started_at: { _eq: $targetDate }` to `finished_at: { _is_null: true }`
  - Added ordering by `started_at desc` to get the most recent unfinished read
  - Prevents duplicate read tracking entries when continuing books on different days
- **Panic Fix**: Fixed runtime panic when Hardcover returns `user_book_reads` with null `started_at` values
  - Added nil check for `userBookRead.StartedAt` before dereferencing in debug logs
  - Use "null" string when `StartedAt` is nil instead of causing application crash
  - Prevents `invalid memory address or nil pointer dereference` error

### Changed
- **Performance Optimization**: Reduced `getCurrentUser()` API calls from 3 per book to 1 per sync run
  - Added caching mechanism for current user authentication
  - Cache is cleared at start of each sync run to ensure fresh authentication
  - Significantly reduces API load and improves sync performance for large libraries
  - For 100 books: reduces from 300 API calls to 1 per sync session

### Technical Details
- Added `incremental.go` with sync state management functions
- Enhanced `fetchRecentListeningSessions()` to query AudiobookShelf sessions API with timestamp filtering
- Modified `runSync()` to integrate incremental sync logic with existing sync workflow
- Added comprehensive test coverage for incremental sync functionality
- Backward compatible: existing setups continue to work without configuration changes

## [v1.2.4] - 2025-06-04

### Added
- **Enhanced Formatting**: Improved readability of duration and date displays in mismatch reports
  - Added `formatDuration()` function to convert decimal hours to human-readable "Xh Ym Zs" format
  - Added `formatReleaseDate()` function with support for multiple date formats (YYYY-MM-DD, MM/DD/YYYY, etc.)
  - Enhanced mismatch collection to use new formatting functions for better user experience
  - Comprehensive test coverage for new formatting functions with example outputs
- **Build Improvements**: Added test binaries to .gitignore to keep repository clean

### Technical
- Refactored mismatch metadata handling to use centralized formatting functions
- Added support for parsing various date formats including partial dates (year/month only)
- Improved duration display consistency across all mismatch reporting

## [v1.2.3] - 2025-06-04

### Fixed
- **API Fix**: Corrected Hardcover GraphQL API URL in `checkExistingFinishedRead()` function from `https://hardcover.app/api/graphql` to `https://api.hardcover.app/v1/graphql`
- **Resolved**: 404 error warning "Failed to check existing finished reads" that was appearing in production logs
- **Cleanup**: Removed empty test files that were causing build issues

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
