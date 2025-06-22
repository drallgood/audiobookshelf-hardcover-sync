# Test Utilities

This package contains test utilities and mocks for the Audiobookshelf-Hardcover Sync project.

## Overview

The `testutils` package provides:

1. **Test Data Structures**: Common data structures used across tests
2. **Mock Implementations**: Stubs and mocks for external dependencies
3. **Helper Functions**: Utility functions to simplify test setup and assertions
4. **Test Fixtures**: Predefined test data for consistent testing

## Key Components

### Data Structures

- `Audiobook`: Represents an audiobook with metadata and progress information
- `PersonSearchResult`: Represents a person (author/narrator) search result
- `EditionCreatorInput`: Input structure for creating a new edition
- `EditionCreationResponse`: Response from creating a new edition
- `PrepopulatedEditionInput`: Input structure with prepopulated data for edition creation

### Cache Implementation

- `PersonCache`: Thread-safe cache for person search results with TTL support
- `CacheEntry`: Represents a cached search result with metadata

### Utility Functions

- `ParseAudibleDuration`: Converts Audible duration strings to seconds
- `convertTimeUnits`: Converts between different time units
- `calculateProgressWithConversion`: Calculates progress percentage
- `generateExampleJSON`: Generates example JSON for testing

## Usage

Import the package in your test files:

```go
import "github.com/yourusername/audiobookshelf-hardcover-sync/internal/testutils"
```

## Testing

Run the tests with:

```bash
go test -v ./internal/testutils/...
```

## Notes

- This package is intended for testing purposes only and should not be used in production code.
- The implementations here are simplified versions of the actual production code.
- Some functions may have hardcoded values or simplified logic for testing purposes.
