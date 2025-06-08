# AudiobookShelf-Hardcover Sync Project Instructions

## Project Overview
This is a Go application that syncs reading progress and book data between AudiobookShelf and Hardcover. It uses GraphQL for Hardcover API interactions and REST for AudiobookShelf.

## Development Guidelines

### Code Structure
- `main.go` - Entry point and version info
- `config.go` - Environment variable configuration with getter functions
- `sync.go` - Core synchronization logic
- `hardcover.go` - Hardcover GraphQL API interactions
- `audiobookshelf.go` - AudiobookShelf REST API interactions
- `types.go` - Data structures and type definitions
- `utils.go` - Utility functions
- `incremental.go` - Incremental sync functionality
- `mismatch.go` - Mismatch detection and collection features
- `docs/` - Documentation for specific features and fixes

### API definitions

- Use GraphQL for Hardcover API, see `hardcover-schema.graphql` for schema definitions
- Use REST for AudiobookShelf API, documented in `audiobookshelf-openapi.json`
- **Important**: Hardcover ownership is stored in the `lists` table (via "Owned" list), NOT in `user_books.owned` field
- The `user_books.owned` field is unreliable and always returns `false` - use `getOwnedBooks()` and `isBookOwnedDirect()` functions instead

#### Hardcover API Limitations
- API tokens automatically expire after 1 year, and reset on January 1st.
- API is rate-limited to 60 requests per minute.
- The following queries are disabled:
    - _like
    - _nlike
    - _ilike
    - _niregex
    - _nregex
    - _iregex
    - _regex
    - _nsimilar
    - _similar
    - Queries have a max timeout of 30 seconds.
    - Queries have a maximum depth of 3.

### Testing
- All new features should include comprehensive test coverage
- Use `go test -v ./...` to run all tests
- Test files follow Go conventions with pattern `*_test.go` in the root directory (same package as source)
- Current test files: `main_test.go`, `format_test.go`, `incremental_test.go`, `owned_test.go`, `owned_flag_test.go`, `want_to_read_test.go`, `reading_history_fix_test.go`
- Tests include unit tests, integration tests, and configuration validation
- Use table-driven tests and subtests for comprehensive coverage

### Environment Configuration
- All configuration uses environment variables
- Add getter functions in `config.go` for new env vars (e.g., `getSyncOwned()`)
- Use sensible defaults and document in README.md
- Environment variables should follow pattern: `SYNC_*`, `HARDCOVER_*`, `AUDIOBOOKSHELF_*`

### Release Process
- We use git tags for releases (semantic versioning: v1.2.3)
- Pipeline builds and publishes releases based on tags
- Also publishes beta versions from main branch
- Use `gh` tool to create GitHub releases with detailed release notes
- Update version in `main.go` and add changelog entry in `CHANGELOG.md`
- Release workflow: commit → push → create tag → push tag → create GitHub release

### Makefile Tasks
- `make build` - Build the binary with version info
- `make run` - Build and run locally  
- `make test` - Run all tests
- `make lint` - Run linting tools
- `make docker-build` - Build Docker image
- `make docker-run` - Run in Docker container

### Code Patterns
- Configuration functions in `config.go` should handle env vars with defaults
- GraphQL operations in `hardcover.go` use structured queries
- Error handling should be comprehensive with proper logging
- New sync features should be configurable via environment variables
- Follow existing patterns for API interactions and data mapping
- Follow idiomatic Go conventions (https://go.dev/doc/effective_go).
- Use named functions over long anonymous ones.
- Organize logic into small, composable functions.
- Prefer interfaces for dependencies to enable mocking and testing.
- Use gofmt or goimports to enforce formatting.
- Avoid unnecessary abstraction; keep things simple and readable.

### Iteration & Review

- Review Copilot output before committing.
- Refactor generated code to ensure readability and testability.
- Use comments to give Copilot context for better suggestions.
- Regenerate parts that are unidiomatic or too complex.

### Documentation
- Keep README.md updated with new features and environment variables
- Update CHANGELOG.md for all releases
- Document configuration options thoroughly
- Include usage examples and troubleshooting guidance

### Docker & Deployment
- Multi-stage Docker build with scratch base image
- Support for environment file configuration
- GitHub Container Registry for image publishing
- Docker Compose for easy local development