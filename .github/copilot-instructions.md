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

### Testing
- All new features should include comprehensive test coverage
- Test files follow pattern `*_test.go`
- Use `go test -v ./...` to run all tests
- Current test files: `main_test.go`, `format_test.go`, `incremental_test.go`, `owned_test.go`, `want_to_read_test.go`, `reading_history_fix_test.go`

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