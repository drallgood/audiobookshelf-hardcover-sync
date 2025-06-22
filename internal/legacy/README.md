# Legacy Code

This directory contains legacy code from previous versions of the application that has been refactored or replaced. The code is kept for reference and potential future use, but should not be used in new development.

## Contents

- `audiobookshelf.go`: Legacy Audiobookshelf API client implementation
- `cache.go`: Legacy caching implementation
- `config.go`: Legacy configuration management
- `debug_api.go` & `debug_api_response.go`: Debug API endpoints and response handling
- `edition_cli.go` & `edition_creator.go`: Legacy edition creation and CLI commands
- `enhanced_progress_detection.go`: Progress detection logic
- `hardcover.go`: Legacy Hardcover API client implementation
- `incremental.go`: Incremental sync functionality
- `local_image_handler.go`: Local image handling utilities
- `mismatch.go`: Mismatch detection and handling
- `progress_threshold_config.go`: Progress threshold configuration
- `sync.go`: Legacy sync service implementation
- `types.go`: Legacy type definitions
- `unit_conversion.go`: Unit conversion utilities
- `utils.go`: Utility functions

## Status

This code is no longer actively maintained. New features and bug fixes should be implemented in the appropriate packages under the `internal/` directory.

## Migration Status

| File | Status | Migration Target | Notes |
|------|--------|-------------------|-------|
| audiobookshelf.go | Replaced | internal/api/audiobookshelf | New implementation with better error handling |
| hardcover.go | Replaced | internal/api/hardcover | New implementation with better error handling |
| sync.go | Replaced | internal/sync | New implementation with improved architecture |
| config.go | Replaced | internal/config | New configuration management |
| logger.go | Replaced | internal/logger | New logging implementation |

## Cleanup Plan

1. Verify that all functionality has been migrated to new implementations
2. Update any remaining references to legacy code
3. Consider moving this directory to an archive location
4. Document any important design decisions or lessons learned from the legacy code
