# Repository Instructions

## Scope and authoritative references

This Go service synchronizes Audiobookshelf reading progress and book metadata
with Hardcover. It has a CLI, a web UI, persistent sync state, and Docker and
Helm deployment artifacts.

Read [CONTRIBUTING.md](CONTRIBUTING.md) before changing contribution or pull
request workflow, and [RELEASE.md](RELEASE.md) before changing release
behavior. Those documents are authoritative for their procedures. The module
and CI use Go 1.26 (`go.mod` and `.github/workflows/go.yml`).

## Contribution and pull requests

- Create feature branches from `develop` and target `develop` in pull
  requests. Create urgent `hotfix/*` branches from `main`, then merge them
  into both `main` and `develop`. Keep `main` stable; releases are cut from it.
- Use `.github/pull_request_template.md` for **every** pull request. Complete
  its summary and testing sections, answer every checklist item accurately,
  and explain any item that does not apply. Do not leave checklist items
  unanswered. For a `hotfix/*` pull request to `main`, keep the template's
  sections and checklist but explicitly identify the hotfix exception to its
  `develop`-target warning and describe the required back-merge to `develop`.
- Run the affected tests and relevant lint/build checks before opening a pull
  request. Keep commits and the pull-request description clear.

## Code map and boundaries

- `cmd/audiobookshelf-hardcover-sync/` is the primary application. Supporting
  commands are in `cmd/edition/`, `cmd/edition-tool/`,
  `cmd/hardcover-lookup/`, and `cmd/image-tool/`.
- `internal/api/audiobookshelf/` contains the REST client and its OpenAPI
  schema; `internal/api/hardcover/` contains the GraphQL client and schema.
  Inspect the relevant schema and existing client before changing an API
  request or response shape.
- `internal/sync/` owns synchronization and persisted state. Configuration is
  defined in `internal/config/`; its YAML tags and environment handling are
  the source of truth for configuration names and precedence.
- `internal/auth/`, `internal/database/`, `internal/multiuser/`, and
  `internal/server/` own the web service and profiles. `internal/edition/` and
  `internal/mismatch/` handle matching and edition work. `web/` contains UI
  assets; `helm/audiobookshelf-hardcover-sync/` contains the chart.
- `internal/testutils/` is deliberately excluded from the core Make test
  target. Include it only when its broader coverage is relevant.

## Behavioral safeguards

- Use GraphQL for Hardcover and REST for Audiobookshelf. Keep Hardcover
  ownership checks on the user's `Owned` list, using the existing ownership
  client methods.
- Preserve the existing Audiobookshelf finished-state handling when changing
  progress or reread logic.
- Dry run is a no-external-mutation mode: route Hardcover writes through the
  concrete client safety boundary, keep read operations available, and do not
  persist state that would make a later real incremental sync skip unapplied
  work. Cover the real mutation boundary and the relevant state behavior.
- Preserve the Hardcover client's established rate limiting, retry, timeout,
  and query-shape behavior. The checked-in schema exposes operators that the
  hosted API may disable: `_ilike` is known to be unsupported, and pattern,
  regex, or similarity operators must be confirmed against existing client or
  runtime evidence before use. Do not introduce other undocumented external
  API limits or assumptions.

## Implementation and configuration

- Follow idiomatic Go, run `gofmt` on changed Go files, keep imports clean,
  and wrap errors with useful context. Favor small, composable functions and
  interfaces at real external boundaries.
- Add only proportionate, behavior-focused tests needed to protect observable
  product contracts across relevant success, no-op, and failure paths. Prefer
  table-driven tests where they make cases clearer.
- Do not add brittle tests for implementation details that are not required
  for the code to work. For example, do not test that a source line remains
  absent to "prevent regression" or that a log statement uses a particular
  level. Avoid assertions tied only to source text, internal call order,
  logging wording or level, or code structure unless that detail is itself an
  explicit external contract.
- Preserve YAML and environment-variable compatibility. Environment values
  are applied after defaults and an optional config file by
  `internal/config.Load`; document user-facing configuration in `README.md`.
- Update `README.md` for user-visible configuration or behavior, focused
  documents (and `MIGRATION.md` for upgrade requirements), and the
  `[Unreleased]` section of `CHANGELOG.md` for release-facing changes. Keep
  Docker Compose and Helm examples consistent with configuration changes.

## Validation, containers, and releases

- `make test` runs the core suite with the race detector and atomic coverage;
  it excludes `internal/testutils`. `make test-all` includes all discovered Go
  packages. Use focused `go test` commands while iterating, then the relevant
  Make target before handoff.
- `make lint` runs `golangci-lint` with a five-minute timeout and installs the
  tool into `GOPATH/bin` if absent. `make build` builds the main binary plus
  `edition`, `image-tool`, and `hardcover-lookup`; run
  `go build ./cmd/edition-tool` when that command changes. `make all` runs the
  standard test, lint, and build targets.
- The Dockerfile is a multi-stage build with an Alpine runtime image. Keep its
  runtime dependencies, entrypoint, volumes, and non-root application setup
  aligned with the application; keep Docker Compose and Helm deployment
  examples aligned as well.
- Follow [RELEASE.md](RELEASE.md) rather than inventing a release path. The
  documented release flow updates the changelog, merges `develop` to `main`,
  tags `vX.Y.Z`, and relies on the tag-triggered GitHub Actions workflow to
  publish images and create the GitHub Release.
