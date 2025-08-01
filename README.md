# audiobookshelf-hardcover-sync

[![Trivy Scan](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml/badge.svg)](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/drallgood/audiobookshelf-hardcover-sync)](https://goreportcard.com/report/github.com/drallgood/audiobookshelf-hardcover-sync)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Automatically syncs your Audiobookshelf library with Hardcover, including reading progress, book status, and ownership information.

## Project Structure

The project follows standard Go project layout:

```
.
├── cmd/                          # Main application entry points
│   └── audiobookshelf-hardcover-sync/  # Main application
├── internal/                     # Private application code
│   ├── api/                      # API clients
│   │   ├── audiobookshelf/       # Audiobookshelf API client
│   │   └── hardcover/            # Hardcover API client
│   ├── config/                   # Configuration loading and validation
│   ├── logger/                   # Structured logging
│   ├── models/                   # Data structures
│   ├── services/                 # Business logic
│   ├── sync/                     # Sync logic
│   └── utils/                    # Utility functions
├── pkg/                          # Public libraries
│   ├── cache/                    # Caching implementation
│   ├── edition/                  # Edition creation logic
│   └── mismatch/                 # Mismatch detection
└── test/                         # Test files
    ├── testdata/                 # Test data
    ├── unit/                     # Unit tests
    ├── integration/              # Integration tests
    └── e2e/                      # End-to-end tests
```

## Development

## Features
- 📚 **Full Library Sync**: Syncs your entire Audiobookshelf library with Hardcover
- 🎯 **Smart Status Management**: Automatically sets "Want to Read", "Currently Reading", and "Read" status based on progress
- 🏠 **Ownership Tracking**: Marks synced books as "owned" to distinguish from wishlist items
- 🔄 **Incremental Sync**: Efficient state-based syncing to only process changed books
  - Tracks sync state between runs
  - Configurable minimum change threshold
  - Persistent state storage
- 🚀 **Smart Caching**: Intelligent caching of author/narrator lookups with cross-role discovery
- 📊 **Enhanced Progress Detection**: Uses `/api/me` endpoint for accurate finished book detection, preventing false re-read scenarios
- 🔄 **Periodic Sync**: Configurable automatic syncing (e.g., every 10 minutes or 1 hour)
- 🎛️ **Manual Sync**: HTTP endpoints for on-demand synchronization
- 🏥 **Health Monitoring**: Built-in health check endpoint
- 🐳 **Container Ready**: Multi-arch Docker images (amd64, arm64)
- 🔍 **Configurable Logging**: JSON or console (human-readable) output with configurable log levels
- 🔧 **Edition Creation Tools**: Interactive tools for creating missing audiobook editions
- 🔍 **ID Lookup**: Search and verify author, narrator, and publisher IDs from Hardcover database
- 🛡️ **Production Ready**: Secure, minimal, and battle-tested

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
         test: ["CMD", "wget", "--spider", "http://localhost:8080/health"]
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

### Configuration Reference

#### Configuration File (Recommended)

Version 2.0.0 introduces a YAML configuration file as the primary way to configure the application:

```yaml
server:
  port: 8080
  timeout: 30s

logging:
  level: info        # debug, info, warn, error
  format: json       # json or text for improved readability during development
  pretty: false      # human-readable formatting for logs
  file: /app/logs/sync.log  # optional log file path

audiobookshelf:
  url: https://your-abs-server.com
  token: your_abs_token

hardcover:
  token: your_hardcover_token
  rate_limit: 10     # requests per second
  rate_burst: 15     # burst capacity

sync:
  interval: 1h       # sync frequency (10m, 30m, 1h, etc)
  want_to_read: true # sync "Want to Read" status
  owned: true        # mark books as owned
  match_mode: continue # book matching behavior (continue, skip, fail)
  incremental: true # incremental sync strategy enabled (true, false)
  min_progress: 0.01   # minimum progress change to sync (0.01 = 1%)
  state_file: /app/data/sync_state.json # state storage location
  dry_run: false     # simulation mode without making changes
  force_full_sync: false # force full sync regardless of state
  libraries:         # library filtering (optional)
    include: ["Audiobooks", "Fiction"] # only sync these libraries (by name or ID)
    exclude: ["Magazines", "Podcasts"] # exclude these libraries (include takes precedence)
```

#### Environment Variables

Primary configuration variables:

| Variable | Description | Default | Example |
|----------|-------------|:-------:|---------|
| `CONFIG_PATH` | Path to config file | `./config.yaml` | `/app/config/config.yaml` |
| `LOG_LEVEL` | Logging level | `info` | `debug`, `warn`, `error` |
| `LOG_FORMAT` | Log output format | `json` | `json`, `text` |

Legacy variables (config file takes precedence):

| Variable | Description | Maps to config |
|----------|-------------|----------------|
| `AUDIOBOOKSHELF_URL` | URL of your AudiobookShelf instance | `audiobookshelf.url` |
| `AUDIOBOOKSHELF_TOKEN` | AudiobookShelf API token | `audiobookshelf.token` |
| `HARDCOVER_TOKEN` | Hardcover API token | `hardcover.token` |
| `SYNC_INTERVAL` | Time between automatic syncs | `sync.interval` |
| `SYNC_LIBRARIES_INCLUDE` | Comma-separated list of libraries to include | `sync.libraries.include` |
| `SYNC_LIBRARIES_EXCLUDE` | Comma-separated list of libraries to exclude | `sync.libraries.exclude` |

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
| `/health` | GET | Basic health status |
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
