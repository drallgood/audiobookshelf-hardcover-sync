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
- **📊 Real-Time Monitoring**: Live sync status with auto-refresh
- **🔧 REST API**: Complete programmatic control via RESTful endpoints
- **⬆️ Automatic Migration**: Seamless upgrade from single-profile setups
- **🔙 Backwards Compatible**: All existing functionality preserved
- **🚀 Cache Busting**: Automatic cache invalidation ensures profiles always get the latest UI updates

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
| `GET` | `/api/profiles/{id}/status` | Get sync status |
| `POST` | `/api/profiles/{id}/sync` | Start sync |
| `DELETE` | `/api/profiles/{id}/sync` | Cancel sync |
| `GET` | `/api/status` | All profile statuses |

### Environment Variables (Multi-Profile)

| Variable | Description | Default |
|----------|-------------|:-------:|
| `ENCRYPTION_KEY` | Base64-encoded 32-byte encryption key (auto-generated if not set) | Auto-generated |
| `DATA_DIR` | Directory for database and encryption files | `./data` |

### Security Features

- **Token Encryption**: All API tokens encrypted at rest
- **Profile Management**: Full CRUD operations for sync profiles data
- **Secure Key Management**: Auto-generated encryption keys
- **Token Masking**: Sensitive data masked in API responses
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
- **📊 Real-Time Monitoring**: Live sync status with auto-refresh
- **🔧 REST API**: Complete programmatic control via RESTful endpoints
- **⬆️ Automatic Migration**: Seamless upgrade from single-user setups
- **🔙 Backwards Compatible**: All existing functionality preserved

### 📚 Core Sync Features
- **Full Library Sync**: Syncs your entire Audiobookshelf library with Hardcover
- **Smart Status Management**: Automatically sets "Want to Read", "Currently Reading", and "Read" status based on progress
- **Ownership Tracking**: Marks synced books as "owned" to distinguish from wishlist items
- **Incremental Sync**: Efficient state-based syncing to only process changed books
  - Tracks sync state between runs
  - Configurable minimum change threshold
  - Persistent state storage
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

- Go 1.21 or later
- Docker (optional, for containerized deployment)
- Audiobookshelf instance with API access
- Hardcover API token

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

3. Create a config file:
   ```sh
   cp config.example.yaml config.yaml
   # Edit config.yaml with your settings
   ```

4. Build and run the application:
   ```sh
   make build
   ./bin/audiobookshelf-hardcover-sync
   ```

## Running with Docker

### Prerequisites

- [Docker](https://docs.docker.com/engine/install/) installed on your system
- [Docker Compose](https://docs.docker.com/compose/install/) (recommended for the main sync service)
- [Hardcover API token](#getting-started)
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
   helm repo add audiobookshelf-hardcover-sync https://drallgood.github.io/audiobookshelf-hardcover-sync
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

3. **Install the chart**:
   ```bash
   helm install my-sync audiobookshelf-hardcover-sync/audiobookshelf-hardcover-sync -f my-values.yaml
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
- **User**: Sync functionality, personal configurations
- **Viewer**: Read-only access to sync status

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
  rate: "1500ms"        # Minimum time between requests (e.g., 1500ms for ~40 requests per minute)
  burst: 2              # Maximum number of requests in a burst
  max_concurrent: 3     # Maximum number of concurrent requests

# Logging configuration
logging:
  level: "info"   # debug, info, warn, error, fatal, panic
  format: "json"  # json or console

# Audiobookshelf configuration
audiobookshelf:
  url: "https://your-audiobookshelf-instance.com"
  token: "your-audiobookshelf-token"

# Hardcover configuration
hardcover:
  token: "your-hardcover-token"

# Sync settings
sync:
  sync_interval: "1h"
  minimum_progress: 0.01  # Minimum progress threshold (0.0 to 1.0)
  sync_want_to_read: true  # Sync books with 0% progress as "Want to Read"
  sync_owned: true        # Mark synced books as owned in Hardcover
  process_unread_books: false  # Process books with 0% progress for mismatches and want-to-read status
  mismatch_output_dir: "./mismatches"  # Directory to store mismatch JSON files
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

#### Environment Variables

**Multi-User Mode (v3.0.0+)** - Recommended:

| Variable | Description | Default | Example |
|----------|-------------|:-------:|---------|
| `ENCRYPTION_KEY` | Base64-encoded 32-byte encryption key | Auto-generated | `base64-encoded-key` |
| `DATA_DIR` | Directory for database and encryption files | `./data` | `/app/data` |
| `LOG_LEVEL` | Logging level | `info` | `debug`, `warn`, `error` |
| `LOG_FORMAT` | Log output format | `json` | `json`, `text` |

**Single-User Mode (Legacy)** - For backwards compatibility (web UI disabled):

### Configuration Modes

The application supports two distinct operating modes controlled by the `enable_web_ui` configuration option:

#### Web UI Mode (Multi-User) - `enable_web_ui: true`
- **Modern web interface** at `http://localhost:8080`
- **Multi-user support** with individual token management
- **REST API** for programmatic access
- **Real-time monitoring** and control
- **No token requirements** at startup (tokens configured via web UI)

#### Single-User Mode (Legacy) - `enable_web_ui: false` (default)
- **Backward compatible** with existing setups
- **Environment variable/configuration file** based token management
- **No web interface** - runs as a service only
- **Requires tokens** at startup via environment variables or config file

### Configuration Options

#### Environment Variables
- `ENABLE_WEB_UI`: Enable/disable web UI (`true`/`false`, default: `false`)
- `AUDIOBOOKSHELF_URL`: Audiobookshelf server URL (required)
- `AUDIOBOOKSHELF_TOKEN`: Audiobookshelf API token (required for single-user mode)
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
| `HARDCOVER_TOKEN` | Hardcover API token | `hardcover.token` | Legacy mode only |
| `SYNC_INTERVAL` | Time between automatic syncs | `sync.sync_interval` | Legacy mode only |
| `SYNC_LIBRARIES_INCLUDE` | Comma-separated list of libraries to include | `sync.libraries.include` | Legacy mode only |
| `SYNC_LIBRARIES_EXCLUDE` | Comma-separated list of libraries to exclude | `sync.libraries.exclude` | Legacy mode only |

> **💡 Tip**: For new installations, use the multi-user web interface instead of environment variables. Legacy environment variables are automatically migrated to the multi-user database on first startup.

#### Volume Mounts

| Container Path | Recommended Host Path | Description |
|----------------|----------------------|-------------|
| `/app/config` | `./config` | Configuration files |
| `/app/data` | `./data` | Persistent data (cache, database) |
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

The `edition-tool` helps create and manage audiobook editions in Hardcover.

```bash
# Build the tool
make build-tools

# Show help
./bin/edition-tool help

# Create an edition interactively
./bin/edition-tool create --interactive

# Create an edition prepopulated with data from Hardcover
./bin/edition-tool create --prepopulated

# Create an edition from a JSON template file
./bin/edition-tool create --file path/to/edition-template.json
```

### Image Tool

The `image-tool` allows you to upload and attach cover images to books and editions in Hardcover.

```bash
# Upload an image to a book
./bin/image-tool upload --url "https://example.com/cover.jpg" --book "hardcover-book-id" 

# Upload an image to an edition
./bin/image-tool upload --url "https://example.com/cover.jpg" --edition "hardcover-edition-id" --description "Audiobook Cover"

# Using a custom config file
./bin/image-tool --config /path/to/config.yaml --url "https://example.com/cover.jpg" --book "hardcover-book-id"
```

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
