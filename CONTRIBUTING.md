# Contributing to audiobookshelf-hardcover-sync

Thank you for your interest in contributing! This document provides guidelines for contributing to the project.

## Table of Contents
- [Development Setup](#development-setup)
- [Code Structure](#code-structure)
- [Making Changes](#making-changes)
- [Testing](#testing)
- [Code Style](#code-style)
- [Docker Development](#docker-development)
- [Submitting Changes](#submitting-changes)
- [Reporting Issues](#reporting-issues)
- [Questions](#questions)
- [License](#license)

## Development Setup

### Prerequisites
- Go 1.20+
- Docker (optional, for containerized development)
- Make

### Getting Started
1. Fork and clone the repository
2. Install dependencies: `go mod download`
3. Copy `.env.example` to `.env` and update with your configuration
4. Build the project: `make build`

## Code Structure

- `/cmd` - Main application entry points
- `/internal` - Core application code
  - `/batch` - Batch processing logic
  - `/config` - Configuration management
  - `/hardcover` - Hardcover API client
  - `/http` - HTTP client with retries
  - `/models` - Data models
  - `/sync` - Synchronization logic
  - `/worker` - Worker pool implementation
- `/pkg` - Reusable packages
- `/scripts` - Build and deployment scripts
- `/test` - Test utilities and fixtures

## Making Changes

1. Create a new branch for your changes:
   ```
   git checkout -b feature/your-feature-name
   ```

2. Make your changes following the code style guidelines below

3. Add tests for new functionality

4. Run tests and verify everything passes

5. Commit your changes with a clear, descriptive message:
   ```
   git commit -m "Add feature: brief description"
   ```

## Testing

### Running Tests
- Run all tests: `make test`
- Run tests with coverage: `make test-cover`
- Run integration tests: `make test-integration`

### Writing Tests
- Place test files next to the code they test with `_test.go` suffix
- Use table-driven tests for testing multiple scenarios
- Mock external dependencies in tests

## Code Style

### General
- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Keep functions small and focused
- Use meaningful variable and function names
- Document public APIs with GoDoc comments

### Error Handling
- Always check and handle errors
- Provide meaningful error messages
- Use `fmt.Errorf()` or `errors.New()` for simple errors
- Consider using custom error types for more complex error handling

### Concurrency
- Use the worker pool pattern for concurrent operations
- Always use `context.Context` for cancellation and timeouts
- Document any non-obvious concurrency patterns

## Docker Development

### Building the Image
```bash
docker build -t audiobookshelf-hardcover-sync .
```

### Running in Docker
```bash
docker run --env-file .env audiobookshelf-hardcover-sync
```

## Submitting Changes

1. Push your changes to your fork:
   ```
   git push origin feature/your-feature-name
   ```

2. Open a pull request against the `main` branch

3. Ensure all CI checks pass

4. Request review from maintainers

## Reporting Issues

When reporting issues, please include:
- Description of the issue
- Steps to reproduce
- Expected vs actual behavior
- Environment details (OS, Go version, etc.)
- Any relevant logs or error messages

## Questions

For questions, please:
1. Check the [documentation](README.md)
2. Search existing issues
3. If your question hasn't been answered, open an issue

For direct inquiries, contact the maintainer at patrice@brendamour.net.

## License

By contributing, you agree that your contributions will be licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
