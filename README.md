# audiobookshelf-hardcover-sync

[![Trivy Scan](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml/badge.svg)](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/drallgood/audiobookshelf-hardcover-sync)](https://goreportcard.com/report/github.com/drallgood/audiobookshelf-hardcover-sync)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/drallgood/audiobookshelf-hardcover-sync?label=latest%20release)](https://github.com/drallgood/audiobookshelf-hardcover-sync/releases/latest)

> **Note:** This README reflects the latest development version. For documentation specific to the latest stable release, please check the [latest release](https://github.com/drallgood/audiobookshelf-hardcover-sync/releases/latest) on GitHub.

Automatically syncs your Audiobookshelf library with Hardcover, including reading progress, book status, and ownership information.

## 🎉 Multi-Profile Sync Support (v3.0.0+)

**audiobookshelf-hardcover-sync** now supports multiple sync profiles with a modern web interface and secure token management!

### Key Features

- **🌐 Web Management Interface**: Modern, responsive web UI at `http://localhost:8080`
- **👥 Multiple Sync Profiles**: Each profile can have individual Audiobookshelf and Hardcover tokens
- **🔒 Secure Storage**: All API tokens encrypted at rest with AES-256-GCM
- **🔄 Concurrent Syncing**: Multiple profiles can sync simultaneously
- **📊 Real-Time Monitoring**: Live sync status with quiet auto-refresh and automatic retry after a temporary profile-load failure
- **🔧 REST API**: Complete programmatic control via RESTful endpoints
- **⬆️ Automatic Migration**: Seamless upgrade from single-profile setups
- **🚀 Cache Busting**: Automatic cache invalidation ensures profiles always get the latest UI updates

### Browser support

The web interface supports desktop Chrome 84+, Edge 84+, Firefox 74+, and Safari 15+, plus Safari on iOS 15.5+. These minimums account for optional chaining in the untranspiled JavaScript, flexbox `gap` in the layout, and `focus({ preventScroll: true })` when live status cards update. The loading overlay's background blur is decorative and may differ between supported browsers. Other mobile browsers may scroll when focus is restored during a status update.

### Quick Start (Multi-User)

1. **Start the application with web UI enabled**:
   ```bash
   # Using environment variable
   ENABLE_WEB_UI=true ./audiobookshelf-hardcover-sync --server-only
   
   # Using config file (recommended)
   # Set enable_web_ui: true in your config.yaml
   ./audiobookshelf-hardcover-sync --server-only
   ```

2. **Access the web interface**: Open `http://localhost:8080` in your browser

3. **Add Sync Profiles**: Use the "Add Profile" tab to create profiles with individual tokens

4. **Monitor syncs**: View real-time sync status and control operations

### Migration from Single-Profile

Existing single-profile setups are **automatically migrated** on first startup:

- Your existing `config.yaml` is detected and backed up
- A "Default Profile" is created with your current configuration
- All functionality continues to work as before
- Access the new web interface at `http://localhost:8080`

### REST API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/` | Web management interface |
| `GET` | `/api/profiles` | List all sync profiles |
| `POST` | `/api/profiles` | Create new sync profile |
| `GET` | `/api/profiles/{id}` | Get profile details |
| `PUT` | `/api/profiles/{id}` | Update profile |
| `DELETE` | `/api/profiles/{id}` | Delete profile |
| `PUT` | `/api/profiles/{id}/config` | Update profile configuration |
| `GET` | `/api/profiles/{id}/edition-capability` | Report separate ebook and audiobook edition-write capability evidence |
| `GET` | `/api/profiles/{id}/runs/{runId}/details` | Get book-level details for a retained sync run |
| `GET` | `/api/profiles/{id}/edition-drafts/source/{itemID}` | Prepare a read-only edition draft from an Audiobookshelf item |
| `POST` | `/api/profiles/{id}/edition-drafts/create` | Create an edition from a verified needs-review sync record |
| `POST` | `/api/profiles/{id}/edition-drafts/check-import` | Check an Audible import and save its verified match |
| `DELETE` | `/api/profiles/{id}/edition-associations/{itemID}` | Forget the saved Hardcover match for one Audiobookshelf item |
| `POST` | `/api/profiles/{id}/sync` | Start sync |
| `DELETE` | `/api/profiles/{id}/sync` | Cancel sync |
| `GET` | `/api/status` | All profile statuses |

### Current-run sync status

The Sync Status page shows the current run for each profile, including its
progress and outcome counts. Select **View Details** to see the books in each
category. Each result includes its Audiobookshelf cover, format, and series
position when available, and its title links to the Audiobookshelf library
item. Known Hardcover books link to Hardcover; a needs-review result instead
shows the Hardcover candidate's series when available and links the candidate
title. The Audiobookshelf ASIN links to Audible, and its ISBN links to a Goodreads
search. Hardcover candidate identifiers are omitted because no matching edition
has been confirmed.

#### Add an edition or forget a match from View Details

After a sync finishes or is canceled, open **View Details** and select **Add
edition** on an eligible needs-review book. You need permission to change the
profile and a Hardcover token with `write:catalog:append` access.

Review the preview, then confirm to add or reuse a Hardcover edition. Audiobooks
need an Audible ASIN in Audiobookshelf; their metadata is read-only, and the app
finds the Audible region automatically. Ebooks need an ASIN or ISBN, and you can
correct their details before confirming. After success, **Hardcover Edition
Added** appears and the app resyncs that book's reading progress. If resync
fails, the edition remains added; run another sync to retry progress updates.

You can preview in dry run, but cannot add editions or forget matches. Wait for
an active sync to finish before making changes. If the book's Audiobookshelf
metadata has changed since the displayed run, run a new sync first.

If an import cannot be confirmed, follow the dialog's recovery guidance:

- Use **Check import status** when available to confirm an audiobook import and
  save its match without submitting it again. The recovery link is valid for 48
  hours; after it expires, inspect Hardcover before running a new sync. Retry a
  check if it temporarily fails; run a sync afterward to update reading progress.
- If no status check is available, including for an uncertain ebook import,
  open Hardcover to inspect the book, then run a new sync. Avoid submitting
  another import while its outcome is unknown, including after a page reload.
- Use **Retry add edition** when the dialog confirms nothing was submitted.
  Restore access or wait for the displayed service/quota limit before retrying.

Matched books offer **Forget match** to clear this app's saved match. Nothing is
deleted from Hardcover. The next sync searches again and may find the same
edition if the catalogue has not changed.

Each processed book is counted once as `synced`, `already_current`, `skipped`,
`needs_review`, `not_found`, `failed`, or dry-run `would_sync`. A total of zero
means the number of books is not known yet, so the processed count may still
increase. A sync start durably reserves an accepted `queued` run before worker
launch; the HTTP response may race processing. The status card then follows that run through `running`,
`finalizing`, and its terminal phase. Canceled and failed runs retain their
partial counts, including unattempted candidates. The card distinguishes the
last attempted run from the last successful non-dry-run run, and labels active
dry runs without implying that Hardcover was changed.

The service retains the newest 10 terminal run reports per profile by default. Set
`database.sync_run_report_retention` or `DATABASE_SYNC_RUN_REPORT_RETENTION` to change
the retention window. View Details can open
the report for an exact run ID, including a completed, canceled, or failed run,
so the latest report remains available after a restart. While a run is active,
an empty missing-books category means `No missing books reported in this run
so far`; after a terminal run it means `No missing books reported in this run`.

For API clients, `GET /api/status` provides a lightweight snapshot with the run
ID, start time, state, totals, and outcome counts, but no book-level records.
Book-level outcomes and unredacted run errors are available from the authenticated
`GET /api/profiles/{id}/runs/{runId}/details` route; run IDs outside the retained
history return `404`. Clients can filter `book_outcomes` for `needs_review`,
`not_found`, and `failed` records. `last_attempted_at` includes dry-run,
failed, and canceled attempts; `last_successful_at` is updated only by a
successful non-dry-run completion.

### Read-only edition draft

`GET /api/profiles/{id}/edition-drafts/source/{itemID}` previews Audiobookshelf
metadata without changing Hardcover. Audiobook previews may also show confirmed
Audnexus details. Audiobooks need an Audible ASIN for the Sync Status creation
flow; ebooks may use an ASIN or ISBN.

Use a trusted Audiobookshelf URL and enable authentication when exposing the
API beyond localhost. See [OpenAPI](docs/openapi.yaml) for fields, warnings,
request limits, and retry guidance.

### Create an edition

`POST /api/profiles/{id}/edition-drafts/create` adds or reuses a Hardcover edition
only when requested; normal sync never creates editions. It requires a
Hardcover token with `write:catalog:append` access.

Send the `run_id` and `abs_item_id` of a needs-review item from a completed or
canceled run. The app uses the Hardcover book identified by that run.

- **Audiobooks** use their Audible ASIN and region. Omit `audible_identifier`
  for automatic region discovery, or provide `ASIN:region`. Audiobook metadata
  cannot be edited.
- **Ebooks** need an ASIN or ISBN and allow corrections to their edition details.

Creation is unavailable during a sync or in dry run. If the source metadata
has changed, run a new sync first. A successful request saves the verified match
for future syncs. Set `"resync": true` to update that book's reading progress
immediately; otherwise, progress updates on the next sync. Resync failures are
reported separately and do not undo edition creation.

For an unconfirmed audiobook import, use
`POST /api/profiles/{id}/edition-drafts/check-import` with the original run/item
IDs and returned recovery details. This checks the existing import and saves
its confirmed match without creating another edition. When recovery details
are unavailable, inspect Hardcover and run a new sync before attempting
another import. See [OpenAPI](docs/openapi.yaml) for request fields, outcomes,
and retry guidance.

### Edition capability

`GET /api/profiles/{id}/edition-capability` checks whether the profile's Hardcover
token permits adding audiobook or ebook editions. It requires profile write
access and does not change the Hardcover catalogue.

A confirmed permission denial disables **Add edition**. An inconclusive check
allows an attempt, but does not guarantee success. Dry run skips the permission
check and never creates an edition. See [OpenAPI](docs/openapi.yaml) for response
fields and [capability evidence](docs/implementations/edition-capability-evidence.md)
for implementation details.

### Remembered edition matches

Sync checks a saved local match before searching Hardcover. It stores an
audiobook match only when an exact, region-qualified Audible mapping confirms
the edition; other audiobook ASIN, ISBN, and title/author results continue to
be checked through read-only lookups but are not saved. For ebooks, it stores
a match whenever an exact `editions.asin` match or an exact ISBN match
confirms the edition. These associations live with the CLI sync state or the
individual web profile's state and survive restarts when that file is kept.
Audiobook ASIN lookup uses regional Audible mappings only; an edition's own
`asin` field is no longer used to match an audiobook. An audiobook whose only
Hardcover link was that field becomes `needs_review` when title/author search
finds the book (resolve it with the add-edition action) or `not_found` when it
does not (fix it in Hardcover or with the `edition` CLI and a book ID). Ebook
ASIN lookup continues to use Kindle editions, ahead of ISBN. An unchanged,
already-synced book keeps its Hardcover edition until its progress or status
changes; use forget match to rematch one now. Sync matching only reads the
Hardcover catalogue; it does not add or change books or editions there. Dry
run can reuse an existing association but does not save or forget one.

A saved match is reused only while the item's source identifiers and reading
format still match. ISBN punctuation and surrounding ASIN whitespace alone do
not invalidate it. Incremental sync may skip an otherwise unchanged item
before checking identifiers, so an identifier-only change is handled the next
time that item is processed. If sync gets an error that could mean the saved
edition is gone, it checks the edition with a fresh Hardcover read and removes
the association only when the edition is confirmed absent.

Use `DELETE /api/profiles/{id}/edition-associations/{itemID}` to remove one
profile's saved match and incremental checkpoint. The next sync follows its
normal matching order and may find the same edition again if the catalogue has
not changed. The action never deletes Hardcover data. During dry run it leaves
the saved match and checkpoint in place. It returns `409` while that profile
is syncing or its state file is busy. See [OpenAPI](docs/openapi.yaml) for the
response and authorization details. Removing a match from a legacy profile
state file migrates it to the current schema; see [MIGRATION.md](MIGRATION.md)
before downgrading.

### Environment Variables (Multi-Profile)

| Variable | Description | Default |
|----------|-------------|:-------:|
| `ENCRYPTION_KEY` | Base64-encoded 32-byte encryption key (auto-generated if not set) | Auto-generated |
| `DATA_DIR` | Directory for database and encryption files | `./data` |
| `DATABASE_SYNC_RUN_REPORT_RETENTION` | Terminal run reports retained per profile | `10` |

### Security Features

- **Token Encryption**: All API tokens encrypted at rest
- **Profile Management**: Full CRUD operations for sync profiles data
- **Secure Key Management**: Auto-generated encryption keys
- **Token Redaction**: Profile API responses never return Audiobookshelf or Hardcover tokens
- **Directory Protection**: Static file serving with traversal protection

---

## Project Structure

The project follows standard Go project layout:

```
.
├── cmd/                          # Main application entry points
│   └── audiobookshelf-hardcover-sync/  # Main application
├── internal/                     # Private application code
│   ├── api/                      # Multi-user API handlers
│   │   ├── audiobookshelf/       # Audiobookshelf API client
│   │   ├── hardcover/            # Hardcover API client
│   │   └── handlers.go           # REST API endpoints
│   ├── auth/                     # Authentication system
│   │   ├── handlers.go           # Auth HTTP handlers (login/logout)
│   │   ├── local.go              # Local username/password provider
│   │   ├── middleware.go         # Auth middleware and RBAC
│   │   ├── models.go             # User, session, provider models
│   │   ├── oidc.go               # Keycloak/OIDC provider
│   │   ├── provider.go           # Auth provider interface
│   │   ├── repository.go         # Database operations
│   │   ├── service.go            # Auth service orchestration
│   │   └── session.go            # Session management
│   ├── config/                   # Configuration loading and validation
│   ├── crypto/                   # Encryption utilities
│   │   └── encryption.go         # AES-256-GCM token encryption
│   ├── database/                 # Database layer
│   │   ├── database.go           # Connection management
│   │   ├── migration.go          # Single-user to multi-user migration
│   │   ├── models.go             # User, config, sync state models
│   │   └── repository.go         # CRUD operations
│   ├── logger/                   # Structured logging
│   ├── models/                   # Data structures
│   ├── multiuser/                # Multi-user service
│   │   └── service.go            # User management and sync orchestration
│   ├── server/                   # HTTP server
│   │   └── server.go             # Routing and middleware setup
│   ├── services/                 # Business logic
│   ├── sync/                     # Sync logic
│   └── utils/                    # Utility functions
├── pkg/                          # Public libraries
│   ├── cache/                    # Caching implementation
│   ├── edition/                  # Edition creation logic
│   └── mismatch/                 # Mismatch detection
├── web/                          # Web UI assets
│   └── static/                   # Static files
│       ├── app.js                # Multi-user management JavaScript
│       ├── index.html            # Web dashboard
│       ├── login.html            # Authentication login page
│       └── styles.css            # Modern responsive styling
├── docs/                         # Documentation
│   ├── AUTHENTICATION.md        # Authentication setup guide
│   └── helm-chart-publishing.md  # Helm deployment guide
├── helm/                         # Kubernetes Helm chart
│   └── audiobookshelf-hardcover-sync/
└── test/                         # Test files
    ├── testdata/                 # Test data
    ├── unit/                     # Unit tests
    ├── integration/              # Integration tests
    └── e2e/                      # End-to-end tests
```

## Development

## Features

### 🎉 Multi-Profile Sync Support (v3.0.0+)
- **👥 Multiple Sync Profiles**: Individual Audiobookshelf and Hardcover tokens per profile
- **🌐 Web Interface**: Modern, responsive management dashboard at `http://localhost:8080`
- **🔒 Secure Storage**: AES-256-GCM encrypted token storage
- **🔄 Concurrent Syncing**: Multiple users can sync simultaneously
- **📊 Real-Time Monitoring**: Live sync status with quiet auto-refresh and automatic retry after a temporary profile-load failure
- **🔧 REST API**: Complete programmatic control via RESTful endpoints
- **⬆️ Automatic Migration**: Seamless upgrade from single-user setups

### 📚 Core Sync Features
- **Full Library Sync**: Syncs your entire Audiobookshelf library with Hardcover
- **Smart Status Management**: Automatically sets "Want to Read", "Currently Reading", and "Read" status based on progress
- **Ownership Tracking**: Marks synced books as "owned" to distinguish from wishlist items
- **Incremental Sync**: Efficient state-based syncing to only process changed books
  - Tracks sync state between runs
  - Configurable minimum change threshold
  - Checkpoints changed state after each processed book (except dry runs); each run still scans the library from the beginning
  - For books that would be newly matched, an item explicitly marked finished or computed at 100% progress without `finished_at` is excluded from Hardcover matching until a finished date is available. This fix applies to new matches and does not repair existing reads created with the old sync-time synthetic date.

- **Smart Caching**: Intelligent caching of author/narrator lookups with cross-role discovery
- **Enhanced Progress Detection**: Uses `/api/me` endpoint for accurate finished book detection, preventing false re-read scenarios

### 🔧 Operations & Management
- **Periodic Sync**: Configurable automatic syncing (e.g., every 10 minutes or 1 hour)
- **Manual Sync**: HTTP endpoints for on-demand synchronization
- **Health Monitoring**: Built-in health check endpoint
- **Configurable Logging**: JSON or console (human-readable) output with configurable log levels

### 🛠️ Developer Tools
- **Edition Creation Tools**: Interactive tools for creating missing audiobook editions
- **ID Lookup**: Search and verify author, narrator, and publisher IDs from Hardcover database
- **Container Ready**: Multi-arch Docker images (amd64, arm64)
- **Production Ready**: Secure, minimal, and battle-tested

## Quick Start

### Prerequisites

- Go 1.26 or later
- Docker (optional, for containerized deployment)
- Audiobookshelf instance with API access
- Hardcover API token with `read:library`, `read:catalog`, `read:lists`,
  `read:me`, `write:library`, and `write:catalog:append`
  ([create a token with these scopes](https://hardcover.app/account/api/keys/new?scope=read%3Alibrary+read%3Acatalog+write%3Alibrary+read%3Alists+read%3Ame+write%3Acatalog%3Aappend)).

### Local Development

1. Clone the repository:
   ```sh
   git clone https://github.com/drallgood/audiobookshelf-hardcover-sync.git
   cd audiobookshelf-hardcover-sync
   ```

2. Install dependencies:
   ```sh
   make deps
   ```

3.### Configuration

Create a `config.yaml` file based on the example:

```bash
cp config.example.yaml config.yaml
```

Edit the configuration to match your environment. See [Configuration Reference](#configuration-reference) for all available options.

#### Important Configuration Changes

> **Deprecation Notice**: The `app` section in the configuration is now deprecated and will be removed in a future version. Please migrate to the new `sync` section.

**Migrating from old configuration (app.*) to new configuration (sync.*):**

```yaml
# Old deprecated format (app.*):
# app:
#   sync_interval: "1h"
#   minimum_progress: 0.01
#   sync_want_to_read: true
#   sync_owned: false
#   dry_run: false

# New format (sync.*):
sync:
  sync_interval: "1h"
  minimum_progress: 0.01
  sync_want_to_read: true
  sync_owned: false
  dry_run: false
  # Other sync settings...
```

The application will automatically migrate settings from the old `app` section to the new `sync` section and log a warning if any deprecated settings are found.

4. Build and run the application:
   ```sh
   make build
   ./bin/audiobookshelf-hardcover-sync
   ```

## Running with Docker

### Prerequisites

- [Docker](https://docs.docker.com/engine/install/) installed on your system
- [Docker Compose](https://docs.docker.com/compose/install/) (recommended for the main sync service)
- [Hardcover API token](#prerequisites) with all required scopes — [create one with this link](https://hardcover.app/account/api/keys/new?scope=read%3Alibrary+read%3Acatalog+write%3Alibrary+read%3Alists+read%3Ame+write%3Acatalog%3Aappend).
- (Optional) [Audiobookshelf](https://www.audiobookshelf.org/) URL and token if using the sync service


### Main Sync Service

#### Using Docker Compose (Recommended)

1. **Create a project directory** and navigate to it:
   ```bash
   mkdir -p ~/abs-hardcover-sync && cd ~/abs-hardcover-sync
   ```

2. **Create a docker-compose.yml** file:
   ```yaml
   version: '3.8'

   services:
     audiobookshelf-hardcover-sync:
       image: ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
       container_name: abs-hardcover-sync
       restart: unless-stopped
       volumes:
         - ./config:/app/config # For configuration files
         - ./data:/app/data # For persistent storage and state
         - ./logs:/app/logs # For log files (optional)
       # Optional environment variables (config file takes precedence)
       environment:
         - CONFIG_PATH=/app/config/config.yaml
         - LOG_LEVEL=info
       healthcheck:
         test: ["CMD", "wget", "--spider", "http://localhost:8080/healthz"]
         interval: 30s
         timeout: 10s
         retries: 3
         start_period: 10s
   ```

3. **Create a config directory and config.yaml file**:
   ```bash
   mkdir -p config && touch config/config.yaml
   ```
   
4. **Add your configuration to config.yaml**:
   ```yaml
   audiobookshelf:
     url: https://your-abs-server.com
     token: your_abs_token

   hardcover:
     token: your_hardcover_token

   sync:
     interval: 10m
     state_file: /app/data/sync_state.json

   logging:
     level: info
     format: json # or text for improved readability during development
   ```

5. **Create data directories**:
   ```bash
   mkdir -p data logs
   ```

6. **Start the service**:
   ```bash
   docker compose up -d
   ```

7. **View logs**:
   ```bash
   # Follow logs
   docker compose logs -f
   
   # View recent logs (last 100 lines)
   docker compose logs --tail=100
   
   # View logs for a specific time period
   docker compose logs --since 1h
   ```

8. **Common management commands**:
   ```bash
   # Stop the service
   docker compose down
   
   # Restart the service
   docker compose restart
   
   # Update to the latest version
   docker compose pull
   docker compose up -d --force-recreate
   ```

#### Using Helm (Kubernetes)

For Kubernetes deployments, use the official Helm chart:

1. **Add the Helm repository**:
   ```bash
# Stable releases (from main)
helm repo add audiobookshelf-hardcover-sync \
  https://drallgood.github.io/audiobookshelf-hardcover-sync/stable

# Dev channel (from develop)
helm repo add audiobookshelf-hardcover-sync-dev \
  https://drallgood.github.io/audiobookshelf-hardcover-sync/dev
helm repo update
```

2. **Create a values file** with your configuration:
   ```yaml
   # my-values.yaml
   secrets:
     audiobookshelf:
       url: "https://your-audiobookshelf-instance.com"
       token: "your-audiobookshelf-token"
     hardcover:
       token: "your-hardcover-token"
   
   # Optional: Enable persistence
   persistence:
     enabled: true
     size: 2Gi
   
   # Optional: Configure resources
   resources:
     limits:
       cpu: 500m
       memory: 512Mi
     requests:
       cpu: 100m
       memory: 128Mi
   ```

3. **Install the chart (stable)**:
   ```bash
helm install my-sync audiobookshelf-hardcover-sync/audiobookshelf-hardcover-sync -f my-values.yaml
```

3b. **Install the chart (dev)**:

```bash
helm install my-sync-dev audiobookshelf-hardcover-sync-dev/audiobookshelf-hardcover-sync -f my-values.yaml
```

4. **Check the deployment**:
   ```bash
   kubectl get pods -l app.kubernetes.io/name=audiobookshelf-hardcover-sync
   kubectl logs -l app.kubernetes.io/name=audiobookshelf-hardcover-sync -f
   ```

5. **Upgrade the deployment**:
   ```bash
   helm upgrade my-sync audiobookshelf-hardcover-sync/audiobookshelf-hardcover-sync -f my-values.yaml
   ```

For detailed Helm chart configuration options, see the [Helm Chart Documentation](docs/helm-chart-publishing.md).

## Authentication

Version 3.0.0 introduces optional authentication support for securing the web UI and API endpoints.

### Quick Start

To enable authentication with a default admin user:

```bash
# Enable authentication
export AUTH_ENABLED=true

# Set session secret (required)
export AUTH_SESSION_SECRET="your-secure-random-secret-key-here"

# Optional: Configure default admin user
export AUTH_DEFAULT_ADMIN_USERNAME="admin"
export AUTH_DEFAULT_ADMIN_EMAIL="admin@localhost"
export AUTH_DEFAULT_ADMIN_PASSWORD="changeme"
```

After enabling authentication:
1. Access the web UI at `http://localhost:8080`
2. Login with the default admin credentials
3. **Important**: Change the default password immediately!

### Authentication Providers

#### Local Authentication
- Username/password with bcrypt hashing
- Secure session management
- Role-based access control

#### Keycloak/OIDC Integration
- OpenID Connect support
- Automatic user provisioning
- Role mapping from JWT claims

```bash
# Keycloak configuration
export KEYCLOAK_ISSUER="https://your-keycloak.example.com/realms/your-realm"
export KEYCLOAK_CLIENT_ID="audiobookshelf-hardcover-sync"
export KEYCLOAK_CLIENT_SECRET="your-client-secret"
export KEYCLOAK_REDIRECT_URI="https://your-app.example.com/auth/callback/oidc"
```

### User Roles

- **Admin**: Full access, user management, system configuration
- **User**: Sync functionality and read/write access to owned profiles
- **Viewer**: Read-only access to owned profiles

When authentication is enabled, profiles created before ownership was recorded
remain active and continue to run scheduled syncs. Administrators can still
manage them, but regular users and viewers cannot see or use them. To give a
regular user control of one, create a new profile while signed in as that user,
then have an administrator remove the old profile to prevent duplicate syncs.

### Security Features

- HTTP-only secure cookies
- CSRF protection
- Session expiration and cleanup
- Password strength validation
- Token encryption at rest

For detailed authentication setup and configuration, see the [Authentication Guide](docs/AUTHENTICATION.md).

### Configuration Reference

#### Configuration File (Recommended)

Version 2.0.0 introduces a YAML configuration file as the primary way to configure the application:

```yaml
# Server configuration
server:
  port: "8080"
  shutdown_timeout: "10s"  # Graceful shutdown timeout

# Rate limiting configuration
rate_limit:
  rate: "2s"            # Minimum time between requests (30 requests per minute)
  max_concurrent: 1     # Maximum number of concurrent requests

# Logging configuration
logging:
  level: "info"   # debug, info, warn, error, fatal, panic
  format: "json"  # json or console

# Audiobookshelf configuration
audiobookshelf:
  url: "https://your-audiobookshelf-instance.com"
  token: "your-audiobookshelf-token"
  network_trust: "allow_private"

# Hardcover configuration
hardcover:
  # Base URL for the Hardcover GraphQL API. Defaults to the official endpoint
  # https://api.hardcover.app/v1/graphql. Override if/when self-hosting is supported.
  # Can also be set via HARDCOVER_BASE_URL environment variable.
  base_url: ""
  token: "your-hardcover-token"

# Sync settings
sync:
  sync_interval: "1h"
  minimum_progress: 0.01  # Minimum progress threshold (0.0 to 1.0)
  sync_want_to_read: true  # Sync books with 0% progress as "Want to Read"
  sync_owned: true        # Mark synced books as owned in Hardcover
  include_ebooks: false    # Include ebook-only Audiobookshelf items in sync
  process_unread_books: false  # Process books with 0% progress for mismatches and want-to-read status
  preserve_dnf: true      # Preserve books marked as "Did Not Finish" in Hardcover
  dry_run: false           # Enable dry run mode (no changes will be made)
  test_book_filter: ""    # Filter books by title for testing
  test_book_limit: 0       # Limit number of books to process for testing (0 = no limit)

# Application settings (deprecated - use sync section above)
app:
  # Deprecated: These settings are moved to the 'sync' section and will be removed in a future version
  sync_want_to_read: true  # Deprecated: Use sync.sync_want_to_read
  sync_owned: true        # Deprecated: Use sync.sync_owned
  dry_run: false          # Deprecated: Use sync.dry_run

# Database configuration
database:
  # Database type: sqlite, postgresql, mysql, mariadb
  type: "sqlite"
  
  # SQLite configuration (default)
  path: ""  # Uses default path if empty: ./data/database.db
  
  # Connection pool settings
  connection_pool:
    max_open_conns: 25      # Maximum number of open connections
    max_idle_conns: 5       # Maximum number of idle connections
    conn_max_lifetime: 60   # Connection lifetime in minutes

# Authentication Configuration
authentication:
  enabled: false
  
  # Session configuration
  session:
    secret: ""  # Auto-generated if empty
    cookie_name: "audiobookshelf-sync-session"
    max_age: 86400  # Session max age in seconds (24 hours)
    secure: false   # Set to true for HTTPS
    http_only: true
    same_site: "Lax"
  
  # Default admin user (created if auth is enabled)
  default_admin:
    username: "admin"
    email: "admin@localhost"
    password: ""  # Set via AUTH_DEFAULT_ADMIN_PASSWORD env var
  
  # Keycloak/OIDC authentication (optional)
  keycloak:
    enabled: false
    issuer: ""
    client_id: ""
    client_secret: ""  # Set via environment variable
    redirect_uri: "http://localhost:8080/auth/callback"
    scopes: "openid profile email"
    role_claim: "realm_access.roles"

# Sync configuration
sync:
  incremental: true
  state_file: "./data/sync_state.json"
  min_change_threshold: 60  # seconds
  libraries:  # Optional library filtering
    include: ["Audiobooks"]  # Only sync these libraries
    exclude: ["Podcasts"]    # Exclude these libraries (include takes precedence)

# Paths configuration
paths:
  data_dir: "./data"      # Base directory for application data
  cache_dir: "./cache"    # Directory for cache files
  mismatch_output_dir: "./mismatches"  # Directory for mismatch reports
```

`audiobookshelf.network_trust` is a deployment-wide setting; profile owners
cannot change it. `allow_private` is the default and permits self-hosted
Audiobookshelf addresses, including LAN, loopback, shared overlay, and IPv6
unique-local addresses, over HTTP or HTTPS.
`public_only` permits public addresses over HTTPS and is intended for
deployments where profile owners are less trusted. Set it with
`AUDIOBOOKSHELF_NETWORK_TRUST` or the YAML value above. Unsupported values are
configuration errors. The base URL must be absolute HTTP or HTTPS; connection
and redirect destinations are checked against the selected mode. Hosts with
both allowed and disallowed DNS addresses are rejected entirely. Requests
connect directly and do not use `HTTP_PROXY`, `HTTPS_PROXY`, or
`NO_PROXY`, since proxy-side DNS resolution would bypass these checks.

#### Environment Variables

**Multi-User Mode (v3.0.0+)** - Recommended:

| Variable | Description | Default | Example |
|----------|-------------|:-------:|---------|
| `ENCRYPTION_KEY` | Base64-encoded 32-byte encryption key | Auto-generated | `base64-encoded-key` |
| `DATA_DIR` | Directory for database and encryption files | `./data` | `/app/data` |
| `LOG_LEVEL` | Logging level | `info` | `debug`, `warn`, `error` |
| `LOG_FORMAT` | Log output format | `json` | `json`, `text` |
| `HARDCOVER_BASE_URL` | Hardcover GraphQL API base URL | `https://api.hardcover.app/v1/graphql` | `https://api.hardcover.app/v1/graphql` |
| `AUDIOBOOKSHELF_NETWORK_TRUST` | Audiobookshelf destination policy (`allow_private` or `public_only`) | `allow_private` | `public_only` |
| `RATE_LIMIT_RATE` | Minimum time between Hardcover API requests | unset | `2s` (30 rpm) |
| `RATE_LIMIT_MAX_CONCURRENT` | Max concurrent requests | unset | `1` |

**Headless Mode** - Existing configuration-file and environment-variable setup (web UI disabled):

### Configuration Modes

The application supports two distinct operating modes controlled by the `enable_web_ui` configuration option:

#### Web UI Mode (Multi-User) - `enable_web_ui: true`
- **Modern web interface** at `http://localhost:8080`
- **Multi-user support** with individual token management
- **REST API** for programmatic access
- **Real-time monitoring** and control
- **No token requirements** at startup (tokens configured via web UI)

#### Headless Mode - `enable_web_ui: false` (default)
- **Backward compatible** with existing setups
- **Environment variable/configuration file** based token management
- **No web interface** - runs as a service only
- **Requires tokens** at startup via environment variables or config file

> Note: In both single-user and multi-user modes, the Hardcover client is created with a unified configuration (NewClientWithConfig), honoring `hardcover.base_url` and `rate_limit.*` settings.

### Configuration Options

#### Environment Variables
- `ENABLE_WEB_UI`: Enable/disable web UI (`true`/`false`, default: `false`)
- `AUDIOBOOKSHELF_URL`: Audiobookshelf server URL (required)
- `AUDIOBOOKSHELF_TOKEN`: Audiobookshelf API token (required for single-user mode)
- `AUDIOBOOKSHELF_NETWORK_TRUST`: Deployment-wide ABS destination policy
  (`allow_private` by default or `public_only`)
- `AUDIOBOOKSHELF_AUDNEXUS_REGION`: Legacy Audnexus setting; used as the default region for sync, edition drafts, and edition creation when a profile has no `sync_config.audnexus_region`.
- `HARDCOVER_TOKEN`: Hardcover API token (required for single-user mode)

#### Config File
```yaml
server:
  port: 8080
  enable_web_ui: true  # Enable web UI for multi-user mode
  shutdown_timeout: 30s

# Single-user mode (when enable_web_ui: false)
audiobookshelf:
  url: "https://audiobookshelf.example.com"
  token: "your-audiobookshelf-token"

hardcover:
  token: "your-hardcover-token"
```

| Variable | Description | Maps to config | Notes |
|----------|-------------|----------------|-------|
| `CONFIG_PATH` | Path to config file | - | `./config.yaml` |
| `AUDIOBOOKSHELF_URL` | URL of your AudiobookShelf instance | `audiobookshelf.url` | Legacy mode only |
| `AUDIOBOOKSHELF_TOKEN` | AudiobookShelf API token | `audiobookshelf.token` | Legacy mode only |
| `AUDIOBOOKSHELF_AUDNEXUS_REGION` | Legacy Audnexus setting | `audiobookshelf.audnexus_region` | Fallback region for sync, edition drafts, and edition creation when a profile has no `sync_config.audnexus_region`; carried into the default profile during single-user config migration. |
| `HARDCOVER_TOKEN` | Hardcover API token | `hardcover.token` | Legacy mode only |
| `HARDCOVER_BASE_URL` | Hardcover API base URL | `hardcover.base_url` | Override default endpoint |
| `RATE_LIMIT_RATE` | Min time between requests | `rate_limit.rate` | e.g. `2s` (30 rpm) |
| `RATE_LIMIT_MAX_CONCURRENT` | Max concurrent requests | `rate_limit.max_concurrent` | e.g. `1` |
| `SYNC_INTERVAL` | Time between automatic syncs | `sync.sync_interval` | Legacy mode only |
| `SYNC_INCLUDE_EBOOKS` | Include ebook-only Audiobookshelf items | `sync.include_ebooks` | Legacy mode only |
| `SYNC_LIBRARIES_INCLUDE` | Comma-separated list of libraries to include | `sync.libraries.include` | Legacy mode only |
| `SYNC_LIBRARIES_EXCLUDE` | Comma-separated list of libraries to exclude | `sync.libraries.exclude` | Legacy mode only |

> **💡 Tip**: Set `sync_config.audnexus_region` on each profile for sync, draft, and create lookups. When a profile has no region preference, these fall back to the legacy `audiobookshelf.audnexus_region` setting, which is also carried into the default profile when a single-user config is migrated.

The profile region preference chooses the first marketplace to check, not the ASIN's
assumed origin. Profile configuration updates preserve omitted settings;
explicit `false` updates a boolean, and an empty `audnexus_region` clears the
preference.

#### Volume Mounts

| Container Path | Recommended Host Path | Description |
|----------------|----------------------|-------------|
| `/app/config` | `./config` | Configuration files |
| `/app/data` | `./data` | Persistent data (cache, database, sync state) |
| `/app/logs` | `./logs` | Application logs |
| `/tmp` | - | Temporary file storage |

#### Health Check Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/healthz` | GET | Basic health status |
| `/ready` | GET | Service readiness |
| `/metrics` | GET | Prometheus metrics |

## Library Filtering

The sync service supports filtering which AudioBookShelf libraries to sync. This is useful when you have multiple libraries (e.g., Audiobooks, Podcasts, Magazines) but only want to sync specific ones to Hardcover.

### Configuration Examples

#### Include Only Specific Libraries
Sync only "Audiobooks" and "Fiction" libraries:

```yaml
sync:
  libraries:
    include: ["Audiobooks", "Fiction"]
```

#### Exclude Specific Libraries
Sync all libraries except "Magazines" and "Podcasts":

```yaml
sync:
  libraries:
    exclude: ["Magazines", "Podcasts"]
```

#### Using Library IDs
You can also use library IDs instead of names:

```yaml
sync:
  libraries:
    include: ["lib_abc123", "lib_def456"]
```

### Environment Variable Examples

#### Include Only Audiobooks
```bash
SYNC_LIBRARIES_INCLUDE="Audiobooks"
```

#### Exclude Multiple Libraries
```bash
SYNC_LIBRARIES_EXCLUDE="Magazines,Podcasts,Children's Books"
```

### Important Notes

- **Include takes precedence**: If both `include` and `exclude` are specified, only the `include` list is used
- **Case-insensitive matching**: Library names are matched case-insensitively
- **ID matching**: You can use either library names or library IDs
- **Comma-separated**: When using environment variables, separate multiple libraries with commas
- **Default behavior**: If no filtering is configured, all libraries are synced

### Finding Your Library Names

To find your library names, check your AudioBookShelf web interface or look at the sync logs when filtering is disabled. The service will log all discovered libraries at the start of each sync.

## Recent Updates & Migration

### v2.0.0 (Latest)
- **Major Rewrite**: Complete architectural overhaul for improved stability and maintainability
- **New**: YAML configuration file support (replaces environment variables)
- **Enhanced**: GraphQL client for Hardcover API interactions
- **Improved**: Incremental sync with better state management
- **New**: Comprehensive logging system with multiple format options
- **Enhanced**: Edition information now properly handles "Unabridged" and source-specific formats
- **Added**: Health monitoring and metrics
- **Fixed**: Multiple issues with book matching and progress tracking

See the [CHANGELOG.md](CHANGELOG.md) for complete details and the [MIGRATION.md](MIGRATION.md) guide for upgrading from previous versions.

### Migration Notes
- **From v1.x to v2.0.0**: Major breaking changes - refer to [MIGRATION.md](MIGRATION.md)
  - Configuration now uses YAML files (environment variables still supported but limited)
  - Several environment variables have been renamed or removed
  - Docker setup requires volume mapping for persistent configuration
  - New logging system with configurable formats
- **Reverse Proxy Users**: Review the new config.yaml path settings

## Command Line Tools

The project includes several utility tools to help with specific tasks:

### Edition Tool

The `edition` command adds a missing edition to an existing Hardcover book.
Audiobooks are imported through Hardcover's regional Audible import; ebooks
are inserted as ebook editions. It can also save the match for an
Audiobookshelf item so the next sync uses the new edition.

```bash
# Build the tool
make build-tools

# Create an edition from a mismatch export or JSON file
./bin/edition --config ./config.yaml create --input edition.json

# Also save the match for this Audiobookshelf item in sync.state_file
./bin/edition --config ./config.yaml create --input edition.json --abs-item-id li_123
```

See [cmd/edition/README.md](cmd/edition/README.md) for input fields, regions,
state files, and results.

### Image Tool

Cover upload is currently unsupported and disabled. `image-tool` reports this
explicitly and does not upload or attach cover images.

### Hardcover Lookup

The `hardcover-lookup` tool helps you search and verify author, narrator, and publisher information in Hardcover.

```bash
# Look up an author
./bin/hardcover-lookup author "Stephen King"

# Look up a narrator with JSON output
./bin/hardcover-lookup narrator "Neil Gaiman" --json

# Look up a publisher with custom results limit
./bin/hardcover-lookup publisher "Penguin Random House" --limit 10

# Get help for a specific command
./bin/hardcover-lookup help author
```

## Troubleshooting

### Common Issues

#### Configuration Issues
If you're experiencing issues with configuration:

1. **Check Configuration File Path**
   ```sh
   # Make sure CONFIG_PATH is correctly set
   CONFIG_PATH=/app/config/config.yaml
   ```

2. **Enable Debug Mode**
   ```yaml
   logging:
     level: debug
     format: text  # For more human-readable output
   ```

3. **API Endpoint Access**
   Ensure your AudiobookShelf token has the necessary permissions.

#### Progress Not Syncing
- Check `sync.min_progress` setting (default: 0.01 = 1%)
- Verify incremental sync is working properly
- Enable debug logging to see detailed progress calculations

### Getting Help
For additional support:
- 📋 Check [existing issues](https://github.com/drallgood/audiobookshelf-hardcover-sync/issues)
- 📖 Review the [MIGRATION.md](MIGRATION.md) documentation
- 🐛 Create a new issue with debug logs if problems persist

### "Manual verification required" / "Edition not matched"

If sync reports:
- `Edition not matched, create or link Hardcover edition`
- `Manual verification required`

it means the app could not confidently match your AudiobookShelf item to a specific Hardcover audiobook edition.

For ISBN matching, sync checks the ISBN recorded in Audiobookshelf and, when
its checksum permits conversion, the corresponding ISBN-10 or ISBN-13 form.
It matches only Hardcover editions of the item's reading format, so an ebook
ISBN cannot select an audiobook edition.

The web UI does not include a one-click "link edition" action. API clients can
request a read-only draft for an Audiobookshelf item using the profile-scoped
edition-draft endpoint listed above.

Use this workflow:

1. **Find the mismatch details**
  - Open the sync summary in the web UI and inspect the mismatch entries.
  - Optionally review JSON mismatch files in your configured `paths.mismatch_output_dir` (default: `./mismatches`). Multi-profile runs use an encoded profile-specific subdirectory beneath it.

2. **Identify why matching failed**
  - Missing or incorrect identifiers in AudiobookShelf (ASIN/ISBN)
  - Book exists in Hardcover, but the audiobook edition does not
  - Book itself does not exist in Hardcover
  - Invalid/expired Hardcover token (least common)

3. **Resolve based on cause**
  - **Missing ASIN/ISBN in AudiobookShelf**: Add/correct identifiers, then re-run sync.
  - **Book exists, edition missing in Hardcover**:
    - Create the edition manually on Hardcover, or
    - Use the `edition` command with the generated mismatch JSON.
  - **Book missing in Hardcover**: Create the book and audiobook edition in Hardcover, then re-run sync.
  - **Token issue**: Generate a new Hardcover token and update configuration.

4. **Re-run sync and verify**
  - Re-run sync after your metadata or Hardcover updates.
  - The mismatch warning should disappear once the correct edition can be matched.

#### Using `edition` for faster fixes

```bash
# Build tools
make build-tools

# Preview without changing Hardcover
./bin/edition --config ./config.yaml --dry-run create --input path/to/mismatch.json

# Create the edition and save the match for the next sync
./bin/edition --config ./config.yaml create --input path/to/mismatch.json
```

Mismatch exports include `abs_item_id`, so the second command saves the match
in `sync.state_file`. For a web-service profile, pass that profile's state file
with `--state-file`. Without an item ID, no match is saved, and an audiobook
import that reused an existing edition may still need review after the next
sync.
If a submitted ASIN or ebook ISBN differs from the ABS item, the command
requires `--confirm-identifier-correction` before creating and saving the
match. Check the ABS item and Hardcover book before confirming.

Tip: Always review JSON data before creating editions to avoid linking to the wrong book/edition.

## Contributing

We welcome contributions! Please see our [contributing guidelines](CONTRIBUTING.md) for details.

**Areas for contribution:**
- 🧪 Test coverage improvements
- 📖 Documentation enhancements  
- 🐛 Bug fixes and performance improvements
- ✨ New features and integrations

## Support & Community

- 📋 **Issues**: [GitHub Issues](https://github.com/drallgood/audiobookshelf-hardcover-sync/issues)
- 🔒 **Security**: See [SECURITY.md](SECURITY.md) for vulnerability reporting
- 📜 **License**: [Apache 2.0](LICENSE)

---

**⭐ If this project helps you, please consider giving it a star on GitHub!**
