# Repository Instructions

## Project Overview

This Go application synchronizes reading progress and book metadata between
Audiobookshelf and Hardcover. It uses Audiobookshelf's REST API and Hardcover's
GraphQL API, and includes a web UI, authentication, persistent sync state, and
supporting command-line tools.

Before changing contribution, branching, pull-request, or release behavior,
read [CONTRIBUTING.md](CONTRIBUTING.md) and [RELEASE.md](RELEASE.md). Those files
are authoritative when their procedures are more specific than this summary.

## Contribution Workflow

- Create feature branches from `develop` and target `develop` in pull requests.
- Create urgent hotfix branches from `main`; merge hotfixes back into both
  `main` and `develop`.
- Keep `main` stable. Releases are cut from `main`.
- Use clear commit messages and a clear pull-request description.
- Use `.github/pull_request_template.md` for every pull request. Complete its
  summary and testing sections, and answer every checklist item accurately;
  explain any item that does not apply rather than leaving the checklist
  unanswered.
- Ensure the affected tests, lint checks, and builds pass before opening a pull
  request.
- Follow the issue-reporting and licensing guidance in `CONTRIBUTING.md`.

## Repository Layout

- `cmd/audiobookshelf-hardcover-sync/`: main application entry point and CLI
- `cmd/edition`, `cmd/edition-tool`, `cmd/hardcover-lookup`, `cmd/image-tool`:
  supporting command-line tools
- `internal/api/audiobookshelf/`: Audiobookshelf REST client and API schema
- `internal/api/hardcover/`: Hardcover GraphQL client and schema
- `internal/sync/`: synchronization behavior and persistent sync state
- `internal/config/`: YAML and environment configuration
- `internal/auth/`, `internal/database/`, `internal/server/`, and
  `internal/multiuser/`: web service, authentication, persistence, and profile
  orchestration
- `internal/edition/` and `internal/mismatch/`: edition and mismatch handling
- `internal/testutils/`: legacy and broader integration-style tests excluded
  from the default core test target
- `web/`: web UI assets
- `helm/audiobookshelf-hardcover-sync/`: Helm chart
- `docs/`: feature and operational documentation

## API Contracts

- Use GraphQL for Hardcover. Consult
  `internal/api/hardcover/hardcover-schema.graphql` before changing queries or
  mutations.
- Use REST for Audiobookshelf. Consult
  `internal/api/audiobookshelf/audiobookshelf-openapi.json` and the current
  Audiobookshelf implementation when changing endpoints or payloads.
- Hardcover ownership is represented by the user's `Owned` list, not by a
  `user_books.owned` field. Use the existing ownership client methods.
- Finished-book detection incorporates Audiobookshelf `/api/me` `isFinished`
  data. Preserve the finished-state check that prevents ordinary completed
  books from being mistaken for re-reads.
- Hardcover tokens expire after one year and reset on January 1. Its API is
  rate-limited, queries time out after 30 seconds, query depth is limited to
  three, and regex/similarity operators may be disabled. Preserve the existing
  rate-limiter and query-shape constraints.

## Implementation Guidelines

- Follow idiomatic Go and run `gofmt` on changed Go files.
- Keep functions small and composable; prefer named functions over long
  anonymous functions.
- Use interfaces at external boundaries when they improve testability, but
  avoid unnecessary abstraction.
- Follow the existing client, mapping, configuration, logging, and error
  handling patterns. Wrap errors with useful context.
- Keep imports at the top of each file and remove unused code.
- Configuration belongs in `internal/config`. Preserve existing YAML and
  environment-variable compatibility, choose sensible defaults, and document
  user-facing options in `README.md`.
- Preserve dry-run as a no-external-mutation contract. Route new Hardcover
  mutations through the existing client safety boundary.

## Testing and Validation

- Add behavior-focused tests for new or changed behavior, including relevant
  success, no-op, and failure paths.
- Keep tests next to their package using Go's `*_test.go` convention. Prefer
  table-driven tests and subtests where they make cases easier to understand.
- `make test` runs the core suite with the race detector and coverage.
- `make test-all` includes the legacy `internal/testutils` package.
- `make lint` runs `golangci-lint`; `make build` builds the application and
  command-line tools; `make all` runs the standard test, lint, and build gates.
- Run the narrowest relevant tests while iterating, then the applicable full
  project checks before handing off a completed change.

## Documentation, Docker, and Releases

- Update `README.md` for user-visible features and configuration changes.
- Add release-facing changes to the `[Unreleased]` section of `CHANGELOG.md`.
- Update focused documentation and `MIGRATION.md` when behavior or upgrade
  requirements change.
- Keep the multi-stage container build and scratch runtime image minimal and
  secure. Keep Docker Compose and Helm examples aligned with configuration
  changes.
- Follow `RELEASE.md` rather than improvising a release. Tags use semantic
  versions (`vX.Y.Z`), and GitHub Actions publishes the release artifacts.
