# Development Guide

This document provides information for developers working on the Audiobookshelf-Hardcover Sync project.

## Prerequisites

- Go 1.24 or later
- Docker and Docker Compose (for testing with containers)
- Make (optional, for build automation)

## Getting Started

1. **Clone the repository**
   ```bash
   git clone https://github.com/drallgood/audiobookshelf-hardcover-sync.git
   cd audiobookshelf-hardcover-sync
   ```

2. **Install dependencies**
   ```bash
   go mod download
   ```

3. **Set up environment variables**
   ```bash
   cp .env.example .env
   # Edit .env with your configuration
   ```

## Building

### Build the binary
```bash
make build
```

### Build with Docker
```bash
make docker-build
```

### Run tests
```bash
# Run unit tests
make test

# Run integration tests
make test-integration

# Run all tests with coverage
make test-coverage
```

## Code Structure

```
.
├── cmd/                  # Main application entry points
├── config/              # Configuration management
├── docs/                # Documentation
├── errors/              # Custom error types and handling
├── graphql/             # GraphQL client implementation
├── hardcover/           # Hardcover API client and models
├── http/                # HTTP client implementation
├── logging/             # Logging utilities
├── metrics/             # Metrics collection and reporting
├── sync/                # Core synchronization logic
└── worker_pool.go       # Worker pool implementation for concurrent processing
```

## Batch Processing

The application includes a high-performance batch processing system for efficient API usage:

### Key Components

1. **HTTP Batch Client**
   - Handles batching of HTTP requests
   - Implements retry logic and circuit breaking
   - Configurable batch size and concurrency

2. **GraphQL Batch Client**
   - Extends HTTP batch client for GraphQL operations
   - Handles GraphQL-specific error cases
   - Supports batch queries and mutations

3. **Hardcover Batch Client**
   - High-level client for Hardcover API operations
   - Implements book lookup, status updates, and sync operations
   - Handles rate limiting and backoff

### Configuration

Batch processing can be configured using environment variables:

```bash
# Batch processing configuration
BATCH_SIZE=10                # Number of items per batch
MAX_CONCURRENT_BATCHES=3     # Maximum concurrent batches
BATCH_DELAY_MS=100           # Delay between batch operations (ms)
MAX_RETRIES=3                # Maximum retry attempts for failed operations
RETRY_DELAY=1s               # Initial retry delay (exponential backoff)
```

## Testing

### Running Tests

```bash
# Run all tests
make test

# Run only unit tests
go test -short ./...

# Run integration tests
go test -run Integration ./...

# Or use the make target
make test-integration

# Run tests with coverage
make test-coverage
```

### Writing Tests

- Unit tests should be placed in the same directory as the code they test
- Integration tests should be marked with `// +build integration`
- Use table-driven tests where appropriate
- Mock external dependencies using interfaces

## Debugging

### Enable Debug Logging

```bash
DEBUG_MODE=true go run main.go
```

Or set the environment variable:

```bash
export DEBUG_MODE=true
```

### Profiling

CPU and memory profiling can be enabled with:

```bash
# CPU profiling
go test -cpuprofile cpu.prof -bench .


# Memory profiling
go test -memprofile mem.prof -bench .


# View profiles
go tool pprof -http=:8080 cpu.prof
```

## Versioning

The project follows [Semantic Versioning](https://semver.org/).

### Updating Version

1. Update the version in `version.go`
2. Update `CHANGELOG.md` with the new version and changes
3. Create a git tag: `git tag vX.Y.Z`
4. Push the tag: `git push origin vX.Y.Z`

## CI/CD

The project uses GitHub Actions for CI/CD. Workflows are defined in `.github/workflows/`:

- `docker-publish.yml`: Builds and publishes Docker images on push to main and tags
- `trivy.yml`: Runs security scanning on container images

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Troubleshooting

### Common Issues

1. **Dependency Issues**
   - Run `go mod tidy` to clean up dependencies
   - Ensure you're using the correct Go version

2. **Test Failures**
   - Check that all required environment variables are set
   - Run tests with `-v` flag for verbose output

3. **Build Issues**
   - Clean the build cache: `go clean -cache`
   - Remove vendor directory and re-download: `rm -rf vendor && go mod vendor`

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
