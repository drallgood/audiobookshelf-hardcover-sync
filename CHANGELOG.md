# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **Process Unread Books**: Fixed `process_unread_books` setting not being respected in both single-user and multi-user modes (#90)
  - Single-user: Updated config.example.yaml to show correct default value (true)
  - Multi-user: Added missing field assignments in sync config application
  - API: Fixed validation that incorrectly rejected updates when setting to false
  - Migration: Added missing ProcessUnreadBooks field when migrating from single-user config
- **Incremental Sync**: Fixed incremental sync not working correctly in multi-user mode (#90)
  - State file paths are now profile-specific to avoid conflicts between users
  - Each user now has their own sync state file: `./data/sync_state.{profileID}.json`
- **Edition Format Detection**: Edition format logic updated to be more precise:
  - "Audible Audio" format is only applied when the book was purchased from Audible/Amazon (detected by presence of ASIN)
  - "libro.fm" format is applied for libro.fm publishers
  - Generic audiobooks now leave the format field empty (since the type is already "audiobook")
  - Previously, generic audiobooks incorrectly defaulted to "Audiobook" format

## [v3.2.0] - 2025-12-17

### Fixed
- **Finished Date Sync**: Use actual completion date from Audiobookshelf instead of sync date when marking books as finished in Hardcover (#74)
 - **Encryption Key Persistence & Logging**: Store the AES-256 encryption key in the same persistent data directory as the database and add detailed logging and hints for token decryption failures to diagnose encryption key and volume mismatches in multi-user setups (#58)

## [v3.1.0] - 2025-11-30

### Added
- **Include Ebooks Configuration**: New option to include items with media type "ebook" in sync
  - Web UI: Add/Edit Profile checkbox "Include Ebooks"
  - Backend: Config field `include_ebooks` persisted in `SyncConfigData`
  - Migration: Carries `IncludeEbooks` from single-user config to multi-profile DB
  - Environment: `SYNC_INCLUDE_EBOOKS=true/false` supported for legacy single-user mode

### Fixed
- **Hardcover Schema Compatibility**: Updated `GetBookByID` GraphQL query to request `publisher` only from editions, matching the current Hardcover schema.
- **Mismatch Export Safety**: Improved mismatch JSON export so `book_id` now uses the Hardcover book ID only when known, and stays empty/zero otherwise, preventing wrong imports when no canonical Hardcover ID exists.
- **Mismatch Edition Export Fields**: Fixed mismatch edition JSON so `isbn_13` is correctly consumed by the edition import tool and `image_url` now prefers Audiobookshelf cover URLs, only falling back to Hardcover covers when no ABS image is available.
 - **Incremental Sync State & Finished Books**: Normalized stored progress units, aligned state keys between `NeedsSync` and `UpdateBook`, and now persist finished state so unchanged finished books are correctly skipped and no longer cause repeated Hardcover activity entries.

## [v3.0.0] - 2025-08-22

### Fixed
- **Authentication**: Fixed default admin user creation to properly read credentials from local provider config
- **Helm Chart YAML Syntax**: Fixed critical YAML syntax errors in Helm templates
  - Fixed authentication configuration structure in `values.yaml`
  - Corrected secret template with proper Helm conditionals
  - Fixed deployment template environment variable references
  - All Helm lint validations now pass successfully
- **Helm Chart Documentation**: Enhanced web UI configuration and documentation
  - Added comprehensive web UI access instructions
  - Documented authentication-aware features and endpoints
  - Added ingress configuration examples for session management
  - Updated chart README with complete setup guide
- **Go Code Linting**: Fixed all Go linting and staticcheck errors
  - Added proper error checking for `os.MkdirAll`, `w.Write`, `json.Encoder.Encode`, `rand.Read`, and `CancelSync` calls
  - Replaced deprecated `strings.Title` with custom `simpleTitle` function
  - Fixed context key usage with custom `contextKey` type to avoid collisions
  - Resolved format string parsing issue in HTML template rendering
  - All linting checks now pass cleanly

- **Sync Configuration**: Fixed `sync_want_to_read` flag not being respected for books with 0% progress
  - Now properly skips syncing "Want to Read" books when `sync_want_to_read` is disabled
  - Added comprehensive test coverage for all sync scenarios
  - Improved test reliability by properly initializing service configuration

### Added
- **🎉 MAJOR: Multi-User Support**: Complete multi-user system with web interface and secure token management
  - **Multi-User Database**: SQLite backend with encrypted token storage using AES-256-GCM encryption
  - **Web Management Interface**: Modern, responsive web UI accessible at `http://localhost:8080`
    - **Multi-Tab Interface**: Users, Sync Status, and Add User tabs for comprehensive management
    - **Real-Time Monitoring**: Live sync status updates with auto-refresh every 5 seconds
    - **User Management**: Create, edit, and delete users with individual configurations
    - **Professional UI**: Modern design with toast notifications, modal dialogs, and responsive layout
  - **REST API**: Complete RESTful API for programmatic user and sync management
    - `GET /api/users` - List all users
    - `POST /api/users` - Create new user
    - `GET /api/users/{id}` - Get user details
    - `PUT /api/users/{id}` - Update user information
    - `DELETE /api/users/{id}` - Delete user
    - `PUT /api/users/{id}/config` - Update user configuration
    - `GET /api/users/{id}/status` - Get sync status
    - `POST /api/users/{id}/sync` - Start sync operation
    - `DELETE /api/users/{id}/sync` - Cancel sync operation
    - `GET /api/status` - Get all user statuses
  - **Security Features**:
    - **Token Encryption**: All API tokens encrypted at rest with AES-256-GCM
    - **User Isolation**: Complete separation of user data and sync states
    - **Secure Key Management**: Auto-generated encryption keys with optional override via `ENCRYPTION_KEY`
    - **Token Masking**: API responses mask sensitive tokens for security
  - **Automatic Migration**: Seamless upgrade from single-user to multi-user configuration
    - **Config Detection**: Automatically detects existing `config.yaml` files
    - **Default User Creation**: Creates "Default User" from existing configuration
    - **Backup Creation**: Original config backed up with timestamp
    - **Zero Downtime**: Migration happens automatically on first startup
  - **Concurrent Sync Support**: Multiple users can sync simultaneously with isolated progress tracking
  - **Enhanced Environment Variables**:
    - `ENCRYPTION_KEY` - Optional base64-encoded 32-byte encryption key
    - `DATA_DIR` - Directory for database and encryption key files (default: `./data`)
  - **Backwards Compatibility**: All existing single-user functionality preserved
    - Legacy `/sync` endpoint maintained for existing integrations
    - Environment variable configuration still supported
    - Existing workflows continue to work unchanged
- **🔐 MAJOR: Authentication & Authorization System**: Comprehensive security framework with multi-provider support
  - **Authentication Providers**:
    - **Local Authentication**: Username/password with bcrypt hashing and secure session management
    - **Keycloak/OIDC Integration**: Full OpenID Connect support with automatic user provisioning
    - **Multi-Provider Support**: Mix local and external authentication seamlessly
  - **Role-Based Access Control**: Three-tier permission system
    - **Admin**: Full access, user management, system configuration
    - **User**: Sync functionality, personal configurations, status monitoring
    - **Viewer**: Read-only access to sync status and logs
  - **Security Infrastructure**:
    - **Session Management**: HTTP-only secure cookies with CSRF protection
    - **Token Encryption**: AES-256-GCM encryption for sensitive data at rest
    - **Password Security**: Bcrypt hashing with proper salt rounds
    - **Session Validation**: Client IP and User-Agent tracking with expiration
  - **Authentication Endpoints**:
    - `GET /auth/login` - Login page with provider selection
    - `POST /auth/login` - Local username/password authentication
    - `GET /auth/oauth/oidc` - Initiate OIDC authentication flow
    - `GET /auth/callback/oidc` - OIDC callback handler with role mapping
    - `POST /auth/logout` - Secure logout with session cleanup
    - `GET /api/auth/me` - Current authenticated user information
  - **Web UI Integration**:
    - **Authentication-Aware Interface**: Dynamic user info display and login/logout flows
    - **Session Management**: Automatic authentication checks and login redirects
    - **Modern Login Page**: Responsive design with provider selection and error handling
    - **User Context**: Header displays current user with avatar and logout button
  - **Configuration**:
    - **Environment Variables**: Complete auth configuration via environment variables
    - **Default Admin User**: Automatic admin user creation when no users exist
    - **Keycloak Setup**: Full integration guide with client configuration and role mapping
    - **Optional Authentication**: Disabled by default, enable with `AUTH_ENABLED=true`
    - **Authentication Configuration**: Support for authentication configuration via `config.yaml`
  - **Database Schema**: Extended multi-user database with authentication models
    - **Users Table**: User accounts with roles, providers, and activity tracking
    - **Sessions Table**: Secure session storage with expiration and cleanup
    - **Auth Providers Table**: External authentication provider configurations
  - **Production Ready**: Enterprise-grade security with comprehensive documentation
    - **Security Checklist**: Production deployment guidelines and best practices
    - **Troubleshooting Guide**: Common issues and debugging instructions
    - **API Documentation**: Complete authentication API reference

 - **Process Unread Books Configuration**: New `process_unread_books` configuration option
   - **Configurable Behavior**: Control whether books with 0% progress are processed for mismatches and "want to read" status
   - **Backward Compatible**: Default value `false` maintains existing behavior (skip unread books)
   - **Environment Variable**: Support via `PROCESS_UNREAD_BOOKS=true/false`
   - **Configuration File**: Support via `sync.process_unread_books: true/false` in YAML config
   - **Enhanced Debugging**: Added debug logging to indicate when unread books are being processed

### Changed
- **📚 Enhanced Documentation**: Updated environment variables and endpoints documentation
  - Legacy environment variables now clearly marked for single-user mode
  - Complete API endpoint reference added to application help
  - Multi-user setup and migration instructions
- **🔧 Server Architecture**: Extended HTTP server to support both legacy and multi-user endpoints
  - Static file serving for web UI with security protections
  - Directory traversal protection for static assets
  - Integrated API routing with proper HTTP method handling

### Dependencies
- **Added**: `gorm.io/gorm` v1.25.12 - ORM for database operations
- **Added**: `gorm.io/driver/sqlite` v1.5.6 - SQLite driver for GORM
- **Added**: `golang.org/x/crypto/bcrypt` - Secure password hashing for local authentication
- **Added**: `github.com/golang-jwt/jwt/v5` - JWT parsing and validation for OIDC integration

### Migration Notes
- **Automatic Migration**: Existing single-user setups will be automatically migrated to multi-user database on first startup
- **Backup Safety**: Original `config.yaml` files are backed up before migration
- **No Action Required**: Migration is completely automatic and maintains all existing functionality
- **Web Interface**: After migration, access the new web interface at `http://localhost:8080`

## [v2.1.0] - 2025-08-01

### Added
- **🚀 Comprehensive Sync Performance Optimizations**: Massive performance improvements through multi-tier caching system
  - **ASIN-Level Caching**: Eliminates duplicate BookByASIN API calls with in-memory and persistent caching (24h TTL)
  - **Edition-Level Caching**: Caches edition metadata by edition_id with 7-day TTL (editions change rarely)
  - **User Book Relationship Caching**: Caches GetUserBook/GetUserBookByBook queries with 6-hour TTL
  - **Incremental Sync**: Early filtering to skip unchanged books before expensive API calls
  - **Batch Processing**: Smart optimization with ASIN deduplication and API-respectful delays
  - **Performance Results**: 67% reduction in API calls (1,732 → 570), sync time improved from 25min → 8-10min
  - **Three-Tier Architecture**: ASIN (24h), Edition (7d), UserBook (6h) caches with different TTLs based on data change frequency
  - **Comprehensive Statistics**: Detailed cache performance logging and monitoring
  - **Persistent Storage**: Cache survives application restarts with automatic expired entry cleanup
- **📚 Library Filtering**: Selective sync support for AudioBookShelf libraries (Issue #17)
  - **Include/Exclude Lists**: Configure which libraries to sync via `sync.libraries.include` and `sync.libraries.exclude`
  - **Flexible Matching**: Support for both library names (case-insensitive) and library IDs
  - **Environment Variables**: `SYNC_LIBRARIES_INCLUDE` and `SYNC_LIBRARIES_EXCLUDE` for comma-separated lists
  - **Smart Precedence**: Include list takes precedence over exclude list when both are specified
  - **Comprehensive Logging**: Detailed filtering results and skipped libraries in sync logs
  - **Default Behavior**: All libraries synced when no filtering is configured
  - **Thread-Safe**: Proper mutex protection for all cache operations
  - **Backward Compatible**: All optimizations maintain existing functionality and respect Hardcover's 60 requests/minute rate limit
- **☸️ Official Helm Chart**: Production-ready Kubernetes deployment with automated publishing
  - **GitHub Pages Repository**: Public Helm chart repository at `https://drallgood.github.io/audiobookshelf-hardcover-sync`
  - **Standard Installation**: `helm repo add audiobookshelf-hardcover-sync https://drallgood.github.io/audiobookshelf-hardcover-sync`
  - **Complete Kubernetes Manifests**: Deployment, Service, Secret, ConfigMap, Ingress, PVC, HPA, ServiceAccount
  - **Security Hardened**: Non-root user, read-only filesystem, dropped capabilities, proper RBAC
  - **Production & Development**: Separate value files optimized for different environments
  - **Full Configuration Support**: All application settings mapped to Helm values with environment variable support
  - **Persistent Storage**: Optional PVC for sync state and cache persistence across pod restarts
  - **Health Monitoring**: Kubernetes-native health checks, resource limits, and auto-scaling support
  - **Automated Publishing**: GitHub Actions workflow for automatic chart packaging and deployment
  - **Comprehensive Documentation**: Installation guides, configuration examples, and troubleshooting
  - **🔄 Version Synchronization**: Helm chart versions automatically sync with Git release tags
    - **Tag-Based Versioning**: Release tags like `v1.2.3` automatically update chart and app versions
    - **Integrated Workflow**: Single release process updates Docker images, GitHub releases, and Helm charts
    - **Smart Version Handling**: Automatic version extraction from Git tags with fallback to existing versions
    - **Consistent Versioning**: All release artifacts (Docker, Helm, GitHub) use identical version numbers

### Fixed
- **📚 Reread Tracking Bug**: Fixed critical issue where book rereads were not properly tracked (#21)
  - Previously, when users reread books, the system would overwrite existing finished read records instead of creating new ones
  - This caused loss of reading history - only the most recent read was preserved
  - Root cause: `handleInProgressBook` incorrectly updated finished read records when detecting new progress on previously finished books
  - Solution: Modified logic to create new unfinished read records for rereads instead of updating finished ones
  - Now preserves complete reading history while preventing duplicate unfinished reads
  - Each reread is tracked as a separate reading event in Hardcover
- **🧹 Edition Cache Architecture Cleanup**: Removed unused persistent edition cache infrastructure
  - Eliminated duplicate caching systems (in-memory vs persistent edition cache)
  - Removed unused `PersistentEditionCache` class and `EditionCacheEntry` struct
  - Cleaned up edition cache fields, helper methods, and integration code from sync service
  - Maintained working in-memory edition cache in Hardcover client (delivering 30% performance improvement)
  - Simplified architecture with clear separation of concerns between components
  - Preserved all performance benefits while removing dead code
- **🔧 User Book Lookup & Creation**: Improved user book management and test structure (#19)
  - Fixed user book lookup to properly handle book ID and edition ID lookups
  - Improved error handling and logging in user book creation
  - Removed unused `editionInfo` variable in `service.go`
  - Fixed test structure in `user_book_management_test.go`
  - Standardized status values from "TO_READ" to "WANT_TO_READ"
  - Enhanced title similarity calculation for better book matching
  - Added detailed logging for user book operations
  - Improved error messages and validation in GraphQL operations
  - Fixed variable scoping issues in test files
  - Ensured all tests pass with the updated implementation

### Removed
- **🔧 Removed Debug Configuration**: Removed the `debug` configuration option and `DEBUG` environment variable
  - Logging verbosity should now be controlled using the `LOG_LEVEL` environment variable
  - This change simplifies the configuration and aligns with standard logging practices
  - All test configurations and documentation have been updated accordingly

## [v2.0.1] - 2025-07-15

### Configuration & Logging
- **⚙️ Configuration System**: Added support for CONFIG_PATH environment variable for Docker deployments
  - Fixed issue where application wasn't reading CONFIG_PATH for Docker configuration
  - Improved documentation in help output

## [v2.0.0] - Major Rewrite

> **MAJOR UPDATE**: This release represents a comprehensive rewrite of the application with significant architectural changes, performance improvements, and new features. Users should review the migration guide for important upgrade information.

### Architecture Overhaul
- **🌐 Complete GraphQL Client Rewrite**: Entirely rebuilt the Hardcover client using GraphQL for more efficient and precise API interactions
- **💾 State Management**: Introduced a robust state management system for tracking sync progress and book status across sessions
- **🔒 Rate Limiting**: Implemented token bucket rate limiting with improved concurrency control and deadlock prevention
- **📈 Memory Optimization**: Drastically reduced memory usage with optimized data structures and garbage collection
- **🔄 Concurrency Model**: Redesigned concurrency approach with proper context handling and cancellation support

### Major New Features
- **🔄 Incremental Sync Engine**: Built a sophisticated incremental synchronization system that only processes changed books
  - New state persistence layer between syncs for tracking changes
  - Smart detection of relevant changes to minimize unnecessary API calls
  - Configurable change thresholds for fine-tuning sync behavior
- **🏷️ Edition Management**: Complete rewrite of edition handling with new tools and improved matching
  - Better book matching algorithms that prioritize relevant results and filter out summaries
  - Enhanced edition information extraction and normalization
  - Improved mismatch detection and resolution
- **📦 Docker Support**: Comprehensive Docker implementation with optimized container builds
  - Multi-database support (SQLite, PostgreSQL, MySQL/MariaDB) with automatic fallback to SQLite
  - Database configuration via config.yaml with environment variable overrides
  - Authentication configuration via config.yaml with environment variable overrides
  - Comprehensive database and authentication documentation and examples
  - **📊 Advanced Progress Tracking**: Completely rebuilt progress tracking system
  - More accurate progress calculation and persistence
  - Support for both percentage and seconds-based progress tracking
  - Better handling of finished status and completion events
- **🔍 Ownership Sync**: Added support for syncing ownership status between platforms

### Configuration & Logging
- **⚙️ Configuration System**: Completely redesigned configuration with improved validation
  - More sensible defaults and clearer documentation
  - Better environment variable support and overriding
  - Hierarchical configuration with proper merging of options
- **📝 Logging Framework**: Rebuilt logging system with structured logging
  - Request ID tracking across operations
  - Configurable log levels and formats (JSON/text)
  - Improved context in log messages
  - Better error reporting and debug information

### Tools & Utilities
- **📱 New CLI Tools**: Added suite of command-line tools for advanced operations
  - Edition management tools for creating and updating editions
  - Image tools for managing cover images
  - Hardcover lookup utilities for data verification
  - Mismatch handling and resolution tools
- **🐛 Error Handling**: Introduced BookError type and comprehensive error handling
  - Detailed error classification and reporting
  - Better recovery from transient failures
  - More informative user feedback

### Critical Fixes & Improvements
- **🚨 Data Integrity**:
  - Prevented data loss in user_book_read updates
  - Fixed race conditions in concurrent operations
  - Improved transaction handling and atomicity
  - Better handling of API response failures
- **🔧 Progress & Status**:
  - Fixed progress_seconds handling for accurate time tracking
  - Enhanced finished status detection and updates
  - Prevented duplicate read statuses in Hardcover
  - Improved progress update logic with better error handling
- **🔍 Mismatch Handling**:
  - Improved edition information and format handling
  - Removed "Audiobookshelf." prefix from edition information
  - Set more meaningful edition formats based on publisher (e.g., "Audible Audio", "libro.fm")
  - Better detection and recording of book mismatches
- **🚀 Performance**:
  - Optimized API request batching and caching
  - Reduced memory allocations in hot paths
  - Improved concurrency control and resource usage
  - Enhanced rate limiting with token bucket algorithm

### Removed & Deprecated
- **⚡ Legacy Components**: Removed several deprecated components and systems:
  - Removed old rate limiting system using delays (`sync_delay` and `HARDCOVER_SYNC_DELAY_MS`)
  - Eliminated legacy matching modes and configuration options
  - Removed outdated mismatch handling code and fixed-path configurations
  - Deprecated non-GraphQL API endpoints usage

### Developer Experience
- **🛠️ CI/CD**: Enhanced CI/CD pipeline with improved workflows
  - Better Docker image building and tagging
  - Comprehensive test coverage with race detection
  - Automated release processes
  - Improved build artifacts and versioning
- **📝 Documentation**: Completely rewrote documentation
  - Better API documentation and examples
  - More comprehensive configuration guides
  - Improved troubleshooting information
  - Migration guides for upgrading from previous versions

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
