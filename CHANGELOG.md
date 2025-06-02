# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v1.1.0] - 2025-06-02

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
- All existing functionality preserved and tested

### Notes
- This is a refactoring release with no breaking changes
- All existing features and API compatibility maintained
- No configuration changes required for existing users

## [v1.0.0] - Previous Release
- Initial stable release with full sync functionality between AudiobookShelf and Hardcover
