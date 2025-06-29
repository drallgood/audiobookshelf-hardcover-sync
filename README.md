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

3. Copy the example environment file and update with your settings:
   ```sh
   cp .env.example .env
   # Edit .env with your configuration
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

### Quick Start

1. **Pull the latest image**:
   ```bash
   docker pull ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
   ```

2. **Create a configuration directory**:
   ```bash
   mkdir -p ~/abs-hardcover-sync/config
   cp .env.example ~/abs-hardcover-sync/config/.env
   ```

3. **Edit the configuration**:
   ```bash
   # Edit the environment file with your settings
   nano ~/abs-hardcover-sync/config/.env
   ```

4. **Run the container**:
   ```bash
   docker run -d \
     --name abs-hardcover-sync \
     --restart unless-stopped \
     -v ~/abs-hardcover-sync/config:/app/config \
     -v ~/abs-hardcover-sync/data:/app/data \
     -e CONFIG_FILE=/app/config/.env \
     ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
   ```

5. **View logs**:
   ```bash
   docker logs -f abs-hardcover-sync
   ```

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
       env_file: .env
       volumes:
         - ./data:/app/data
         - ./logs:/app/logs
       environment:
         - TZ=America/New_York  # Set your timezone
         - LOG_LEVEL=info       # debug, info, warn, error
         - LOG_FORMAT=json      # json or text
       healthcheck:
         test: ["CMD", "wget", "--spider", "http://localhost:8080/health"]
         interval: 30s
         timeout: 10s
         retries: 3
         start_period: 10s
   ```

3. **Create a .env file** with your configuration:
   ```bash
   # Required
   HARDCOVER_TOKEN=your_hardcover_token
   AUDIOBOOKSHELF_URL=https://your-audiobookshelf-server.com
   AUDIOBOOKSHELF_TOKEN=your_audiobookshelf_token
   
   # Optional - adjust as needed
   SYNC_INTERVAL=1h
   LOG_LEVEL=info
   LOG_FORMAT=json
   DRY_RUN=false
   ```

4. **Start the service**:
   ```bash
   docker compose up -d
   ```

5. **View logs**:
   ```bash
   # Follow logs
   docker compose logs -f
   
   # View recent logs (last 100 lines)
   docker compose logs --tail=100
   
   # View logs for a specific time period
   docker compose logs --since 1h
   ```

6. **Check container status**:
   ```bash
   # List all containers
   docker compose ps
   
   # View resource usage
   docker stats
   
   # View container details
   docker inspect abs-hardcover-sync
   ```

7. **Common management commands**:
   ```bash
   # Stop the service
   docker compose down
   
   # Restart the service
   docker compose restart
   
   # Update to the latest version
   docker compose pull
   docker compose up -d --force-recreate
   ```

### Configuration Reference

#### Core Environment Variables

| Variable | Description | Required | Default | Example |
|----------|-------------|:--------:|:-------:|---------|
| `HARDCOVER_TOKEN` | Your Hardcover API token | Yes | - | `abc123...` |
| `AUDIOBOOKSHELF_URL` | Your Audiobookshelf server URL | Yes* | - | `https://abs.example.com` |
| `AUDIOBOOKSHELF_TOKEN` | Your Audiobookshelf API token | Yes* | - | `abc123...` |
| `CONFIG_FILE` | Path to config file (if not using env vars) | No | - | `/app/config/.env` |

#### Sync Configuration

| Variable | Description | Default | Example |
|----------|-------------|:-------:|---------|
| `SYNC_INTERVAL` | Sync interval (0 to disable) | `1h` | `30m`, `2h`, `6h` |
| `DRY_RUN` | Enable dry-run mode | `false` | `true`/`false` |
| `AUDIOBOOK_MATCH_MODE` | Book matching behavior | `continue` | `skip`, `fail` |
| `MAX_RETRIES` | Max retry attempts for failed operations | `3` | `5` |
| `REQUEST_TIMEOUT` | HTTP request timeout | `30s` | `1m` |

#### Logging Configuration

| Variable | Description | Default | Example |
|----------|-------------|:-------:|---------|
| `LOG_LEVEL` | Logging level | `info` | `debug`, `warn`, `error` |
| `LOG_FORMAT` | Log format | `json` | `text` |
| `LOG_FILE` | Log file path (empty for stdout) | `` | `/app/logs/sync.log` |

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

#### Example .env File

```ini
# Required
HARDCOVER_TOKEN=your_hardcover_token_here
AUDIOBOOKSHELF_URL=https://your-audiobookshelf-server.com
AUDIOBOOKSHELF_TOKEN=your_audiobookshelf_token_here

# Optional - Sync Settings
SYNC_INTERVAL=1h
DRY_RUN=false
AUDIOBOOK_MATCH_MODE=continue
MAX_RETRIES=3

# Optional - Logging
LOG_LEVEL=info
LOG_FORMAT=json
LOG_FILE=/app/logs/sync.log

# Optional - Advanced
REQUEST_TIMEOUT=30s
TZ=UTC
```

## Command Line Tools

All command-line tools are available as subcommands of the main Docker image. These tools are designed to help manage your audiobook collection and troubleshoot issues.

### Running Tools with Docker

All tools follow this basic pattern:
```bash
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest [TOOL] [COMMAND] [ARGS]
```

### 1. Edition Tool

Create and manage audiobook editions in Hardcover.

#### Commands

##### `create` - Create a new audiobook edition
```bash
# Interactive mode (guided prompts)
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest edition-tool create --interactive

# From JSON file
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest edition-tool create --file /app/edition.json

# With debug logging
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  -e LOG_LEVEL=debug \
  -e LOG_FORMAT=text \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest edition-tool create --file /app/edition.json
```

##### `prepopulate` - Generate a template from an existing book
```bash
# Basic usage
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest edition-tool prepopulate \
    --book-id 12345 \
    --output /app/edition.json

# With additional metadata
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest edition-tool prepopulate \
    --book-id 12345 \
    --asin B0ABCD1234 \
    --isbn13 9781234567890 \
    --output /app/edition.json
```

### 2. Hardcover Lookup Tool

Look up authors, narrators, and publishers in the Hardcover database.

#### Commands

##### `author` - Look up authors
```bash
# Basic search
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest hardcover-lookup author "Brandon Sanderson"

# Search with filters
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest hardcover-lookup author \
    --limit 5 \
    --sort name \
    "Brandon"
```

##### `narrator` - Look up narrators
```bash
# Basic search
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest hardcover-lookup narrator "Michael Kramer"

# Verify narrator ID
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest hardcover-lookup narrator --id 12345

# Bulk lookup
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest hardcover-lookup narrator \
    --bulk "Michael Kramer, Kate Reading, Tim Gerard Reynolds"
```

##### `publisher` - Look up publishers
```bash
# Basic search
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest hardcover-lookup publisher "Macmillan"

# Search with filters
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest hardcover-lookup publisher \
    --limit 3 \
    --sort name \
    "Audio"
```

### Tips for Using the Tools

1. **Persistent Configuration**: Create a shell alias or script to avoid typing the full command:
   ```bash
   # Add to your ~/.bashrc or ~/.zshrc
   alias hc-tool="docker run --rm -v $(pwd):/app -e HARDCOVER_TOKEN=your_token ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest"
   
   # Then use like:
   hc-tool edition-tool create --interactive
   hc-tool hardcover-lookup author "Brandon Sanderson"
   ```

2. **Debugging**: Add these environment variables for troubleshooting:
   ```bash
   -e LOG_LEVEL=debug \
   -e LOG_FORMAT=text \
   ```

3. **Rate Limiting**: Be mindful of API rate limits when running bulk operations. Consider adding delays between requests if needed.

4. **Data Persistence**: Use volumes to persist data between container runs:
   ```bash
   -v ~/abs-tools/data:/app/data
   ```

5. **Configuration Files**: For complex configurations, use environment files:
   ```bash
   --env-file .env
   ```

### 3. Image Tool

Upload, download, and manage cover images for audiobook editions in Hardcover.

#### Commands

##### `upload` - Upload a cover image
```bash
# Basic upload
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest image-tool upload \
    --edition-id 12345 \
    --image /app/cover.jpg \
    --type COVER_ART

# Upload with additional options
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  -e LOG_LEVEL=debug \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest image-tool upload \
    --edition-id 12345 \
    --image /app/cover.jpg \
    --type COVER_ART \
    --primary \
    --position 1 \
    --credit "Cover Art by John Doe" \
    --credit-url "https://example.com/artist/johndoe"
```

##### `download` - Download a cover image
```bash
# Basic download
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest image-tool download \
    --edition-id 12345 \
    --output /app/cover.jpg

# Download specific image by ID
docker run --rm \
  -v $(pwd):/app \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest image-tool download \
    --edition-id 12345 \
    --image-id 67890 \
    --output /app/cover.jpg

# Download all images for an edition
docker run --rm \
  -v $(pwd)/covers:/app/covers \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest image-tool download \
    --edition-id 12345 \
    --all \
    --output-dir /app/covers
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest image-tool upload \
  --book 12345 /app/covers/my-cover.jpg "Custom cover art"
```

## Advanced Configuration

### Using Docker Compose for Tools

For frequently used tools, you can create a `docker-compose.tools.yml`:

```yaml
version: '3.8'

services:
  edition-tool:
    image: ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
    entrypoint: ["edition-tool"]
    environment:
      - HARDCOVER_TOKEN=${HARDCOVER_TOKEN}
    volumes:
      - ./data:/app/data
      - ./config:/app/config

  hardcover-lookup:
    image: ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
    entrypoint: ["hardcover-lookup"]
    environment:
      - HARDCOVER_TOKEN=${HARDCOVER_TOKEN}

  image-tool:
    image: ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
    entrypoint: ["image-tool"]
    environment:
      - HARDCOVER_TOKEN=${HARDCOVER_TOKEN}
    volumes:
      - ./covers:/app/covers
```

Then use it like this:
```bash
# Run edition tool
docker compose -f docker-compose.tools.yml run --rm edition-tool create --interactive

# Lookup an author
docker compose -f docker-compose.tools.yml run --rm hardcover-lookup author "Brandon Sanderson"
```

### Custom Configuration File

1. Create a `config/config.yaml` file:
   ```yaml
   hardcover:
     token: your_hardcover_token
     
   logging:
     level: debug
     format: console
     
   server:
     port: 8080
     shutdown_timeout: 30s
   ```

2. Mount it when running the container:
   ```bash
   docker run --rm \
     -v $(pwd)/config:/app/config \
     ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
   ```

### Persistent Cache

To persist the cache between container restarts:

```bash
docker run --rm \
  -v abs_hardcover_data:/app/data \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
```

### Using Docker Secrets

For enhanced security, use Docker secrets:

1. Create secrets:
   ```bash
   echo "your_hardcover_token" | docker secret create hardcover_token -
   echo "your_audiobookshelf_token" | docker secret create audiobookshelf_token -
   ```

2. Update your `docker-compose.yml`:
   ```yaml
   services:
     audiobookshelf-hardcover-sync:
       image: ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
       environment:
         - HARDCOVER_TOKEN_FILE=/run/secrets/hardcover_token
         - AUDIOBOOKSHELF_TOKEN_FILE=/run/secrets/audiobookshelf_token
       secrets:
         - hardcover_token
         - audiobookshelf_token
   
   secrets:
     hardcover_token:
       external: true
     audiobookshelf_token:
       external: true
   ```

## Troubleshooting

### Viewing Logs

```bash
# Follow container logs
docker compose logs -f

# View logs from the last 5 minutes
docker compose logs --since 5m

# View logs with timestamps
docker compose logs -t
```

### Debugging

Run the container with a shell for debugging:

```bash
docker run -it --rm --entrypoint /bin/sh ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
```

### Common Issues

- **Permission Denied**: Ensure the container has write access to mounted volumes
- **Connection Issues**: Verify network connectivity and proxy settings
- **Authentication Errors**: Double-check your API tokens and ensure they're properly escaped in the environment

### Using Docker
```sh
docker run -d \
  -e AUDIOBOOKSHELF_URL=https://your-abs-server.com \
  -e AUDIOBOOKSHELF_TOKEN=your_abs_token \
  -e HARDCOVER_TOKEN=your_hardcover_token \
  -e SYNC_INTERVAL=1h \
  -e TEST_BOOK_LIMIT=10 \
  --name abs-hardcover-sync \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
```

## Configuration

### Required Environment Variables
| Variable | Description |
|----------|-------------|
| `AUDIOBOOKSHELF_URL` | URL to your AudiobookShelf server (e.g., `https://abs.example.com`) |
| `AUDIOBOOKSHELF_TOKEN` | API token for AudiobookShelf (see setup instructions below) |
| `HARDCOVER_TOKEN` | API token for Hardcover (see setup instructions below) |

### Optional Environment Variables
See the [Configuration Options](#configuration-options) section for a complete list of all available configuration options.

### Configuration Options

#### Paths
| Variable | Description | Default |
|----------|-------------|---------|
| `CACHE_DIR` | Directory for storing cache files | `/app/cache` |
| `MISMATCH_OUTPUT_DIR` | Directory where mismatch JSON files will be saved | `/app/mismatches` |
| `SYNC_STATE_FILE` | Path to incremental sync state file | `sync_state.json` |

#### Sync Behavior
| Variable | Default | Description |
|----------|---------|-------------|
| `SYNC_INTERVAL` | None | Periodic sync interval (e.g., `10m`, `1h`, `30s`) |
| `SYNC_WANT_TO_READ` | `true` | Sync unstarted books (0% progress) as "Want to Read" |
| `SYNC_OWNED` | `true` | Mark synced books as "owned" in Hardcover |
| `MINIMUM_PROGRESS` | `0.01` | Minimum progress to sync (0.0-1.0, 0.01 = 1%) |
| `AUDIOBOOK_MATCH_MODE` | `continue` | Book matching behavior: `continue`, `skip`, `fail` |
| `TEST_BOOK_FILTER` | None | Filter books by title/author (case-insensitive) |
| `TEST_BOOK_LIMIT` | `0` (no limit) | Limit number of books to process |
| `DRY_RUN` | `false` | Enable dry-run mode (no changes will be made) |

#### Logging
| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_LEVEL` | `info` | Logging level: `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | Log format: `json` or `console` |
| `TZ` | System default | Timezone for logs (e.g., `UTC`, `America/New_York`) |



## Setup Instructions

### Getting Your API Tokens

#### AudiobookShelf Token
1. Log in to your AudiobookShelf web interface
2. Navigate to **Settings → Users** and click on your account
3. Copy the API token from your user profile
4. Set `AUDIOBOOKSHELF_URL` to your server URL and `AUDIOBOOKSHELF_TOKEN` to your token

> **Important for Reverse Proxy Setups**: If AudiobookShelf is behind a reverse proxy with a path prefix, include the full path in your URL:
> ```sh
> # Example for reverse proxy with /audiobookshelf path
> AUDIOBOOKSHELF_URL=https://your-domain.com/audiobookshelf
> 
> # NOT just: https://your-domain.com
> ```

> **Alternative**: Get token via API:
> ```sh
> curl -X POST "https://your-abs-server/api/login" \
>   -H 'Content-Type: application/json' \
>   -d '{"username":"YOUR_USER","password":"YOUR_PASS"}'
> # Token is in response.user.token
> ```

#### Hardcover Token
1. Log in to [hardcover.app](https://hardcover.app)
2. Go to your account settings → API section
3. Generate a new API token
4. Set `HARDCOVER_TOKEN` to your generated token

### Feature Configuration

#### Want to Read Sync
**Default: Enabled** - Unstarted books (0% progress) are synced as "Want to Read" in Hardcover.

```sh
# Disable if you only want to sync books with progress
export SYNC_WANT_TO_READ=false
```

**Status Mapping:**
- 0% progress → "Want to Read" (status_id=1)
- 1-98% progress → "Currently Reading" (status_id=2)
- ≥99% progress → "Read" (status_id=3)

#### Owned Books Sync
**Default: Enabled** - Synced books are marked as "owned" to distinguish from wishlist items.

```sh
# Disable if you don't want to mark books as owned
export SYNC_OWNED=false
```

This helps you:
- Distinguish owned books from wishlist items
- Maintain accurate ownership records
- Filter your Hardcover library by ownership status

## How It Works

### Sync Process
1. **Fetch Libraries**: Retrieves all libraries from AudiobookShelf (`/api/libraries`)
2. **Get Library Items**: Fetches items from each library (`/api/libraries/{id}/items`)
3. **Filter Books**: Processes only items with `mediaType == "book"`
4. **Progress Detection**: Uses multiple methods for accurate progress tracking
5. **Book Matching**: Matches books using ISBN, ASIN, or title/author
6. **Status Sync**: Creates or updates books in Hardcover with correct status

### Smart Features

#### Incremental Sync
- **Timestamp-based**: Only processes books changed since last sync
- **State Persistence**: Maintains sync state in `sync_state.json`
- **Performance**: Reduces API calls and improves sync speed

#### Accurate Progress Tracking
1. **User Progress API**: Checks `/api/me` for manually finished books
2. **Current Time**: Uses actual listening position when available
3. **Smart Calculation**: Handles edge cases and provides fallbacks
4. **Manual Finish Detection**: Respects AudiobookShelf "mark as finished" status

#### Book Matching Priority
1. ISBN-13, ISBN-10, or ASIN lookup (most accurate)
2. Title and author matching (fallback)
3. Configurable behavior when matches fail

### Rate Limiting & Reliability
- **Auto-retry**: Retries failed requests up to 3 times
- **Throttling**: Respects Hardcover rate limits with configurable delays
- **429 Handling**: Automatically backs off when rate limited
- **Error Recovery**: Continues processing other books on individual failures

## Usage

### Endpoints
- `GET /healthz` — Health check endpoint
- `POST /sync` — Trigger manual sync
- `GET /sync` — Trigger manual sync (alternative)

## Command Line Tools

The project provides several command-line tools for different purposes. Each tool has its own set of commands and options.

### 1. Main Sync Service

The main synchronization service that runs as a daemon to keep your Audiobookshelf and Hardcover libraries in sync.

```sh
# Run the sync service with default settings
./bin/audiobookshelf-hardcover-sync

# Run a one-time sync and exit
./bin/audiobookshelf-hardcover-sync --once

# Run only the HTTP server without starting the sync service
./bin/audiobookshelf-hardcover-sync --server-only

# Show version information
./bin/audiobookshelf-hardcover-sync --version

# Enable debug logging
./bin/audiobookshelf-hardcover-sync -v
# or
DEBUG=1 ./bin/audiobookshelf-hardcover-sync

# Run with a specific config file
./bin/audiobookshelf-hardcover-sync --config /path/to/config.yaml

# Test with a specific book (filter by title/author)
./bin/audiobookshelf-hardcover-sync --test-book-filter "book title"

# Limit the number of books to process (for testing)
./bin/audiobookshelf-hardcover-sync --test-book-limit 5
```

### 2. Edition Tool

Manage audiobook editions in Hardcover.

```sh
# Interactive edition creation
./bin/edition create --interactive

# Interactive edition creation with prepopulated data
./bin/edition create --prepopulated

# Create edition from JSON file
./bin/edition create --file edition.json

# Show help
./bin/edition --help
```

### 3. Lookup Tool

Look up authors, narrators, and publishers in the Hardcover database.

```sh
# Look up an author by name
./bin/lookup-tool author search "Stephen King"

# Verify an author by ID
./bin/lookup-tool author verify 12345

# Bulk lookup multiple authors
./bin/lookup-tool author bulk "Stephen King, Brandon Sanderson"

# Look up a narrator by name
./bin/lookup-tool narrator search "Jim Dale"

# Look up a publisher by name
./bin/lookup-tool publisher search "Penguin"

# Output results in JSON format
./bin/lookup-tool --json author search "Stephen King"

# Limit number of results
./bin/lookup-tool --limit 3 author search "Stephen King"

# Show help
./bin/lookup-tool --help
```

### 4. Image Tool

Upload and manage book and edition cover images.

```sh
# Upload an image to a book
./bin/image-tool upload --url https://example.com/cover.jpg --book 12345 --description "Hardcover edition"

# Upload an image to an edition
./bin/image-tool upload --url https://example.com/edition.jpg --edition 67890 --description "Special edition cover"

# Show help
./bin/image-tool --help
```

## Common Options

### Configuration

All tools support the following configuration methods (in order of precedence):

1. Command-line flags
2. Environment variables
3. Configuration file (`config.yaml` in current directory or `/etc/audiobookshelf-hardcover-sync/`)

### Sync Behavior

- **Incremental Sync**: When enabled (default), the sync will only process books that have changed since the last sync. This significantly reduces API calls and improves performance.
  ```yaml
  sync:
    incremental: true
    state_file: "./data/sync_state.json"
    min_change_threshold: 60  # seconds
  ```

- **Progress Updates**: The sync will only update progress in Hardcover if the change is greater than the minimum threshold (default: 60 seconds). This prevents excessive updates for minor progress changes.

### Environment Variables

- `AUDIOBOOKSHELF_URL`: URL of your Audiobookshelf server
- `AUDIOBOOKSHELF_TOKEN`: API token for Audiobookshelf
- `HARDCOVER_TOKEN`: API token for Hardcover
- `LOG_LEVEL`: Log level (debug, info, warn, error, fatal, panic)
- `LOG_FORMAT`: Log format (json, text)
- `DRY_RUN`: Set to 1 to enable dry-run mode (no changes will be made)

### Debugging

To enable debug output, set the `DEBUG` environment variable:

```sh
DEBUG=1 ./bin/audiobookshelf-hardcover-sync
# or
DEBUG=1 ./bin/edition-tool create --interactive
```

Or use the `-v` flag where supported:

```sh
./bin/audiobookshelf-hardcover-sync -v
```

## Edition Templates

You can create editions using JSON templates. Here's how:

**Features:**
- 🎯 **Auto-fills metadata** from existing Hardcover book data
- 📚 **Populates authors, narrators, and publishers** automatically
- 🔍 **Validates data** before template generation
- ⚡ **Saves time** by reducing manual data entry
- 🛠️ **Extensible** for future external API integrations

**Workflow:**
1. Enter or select the Hardcover book ID
2. Tool fetches comprehensive book metadata from Hardcover
3. Automatically populates all available fields (title, authors, narrators, publishers, etc.)
4. You only need to add missing fields like ASIN and image URL
5. Creates the edition with validated, complete data

#### Batch Edition Creation
For multiple editions, use JSON files with automated prepopulation:

**Option 1: Generate prepopulated template**
```sh
# Generate template with data from Hardcover book ID 123456
./bin/audiobookshelf-hardcover-sync --generate-prepopulated 123456:book1.json
```

**Option 2: Enhance existing template**
```sh
# Add book data to existing template
./bin/audiobookshelf-hardcover-sync --enhance-template book1.json:123456
```

**Option 3: Manual template creation**
```sh
# Generate blank template
./bin/audiobookshelf-hardcover-sync --generate-example book1.json
```

**Example JSON with all configurable fields:**
```json
{
  "book_id": 123456,
  "title": "The Martian",
  "subtitle": "A Novel",
  "image_url": "https://m.media-amazon.com/images/I/...",
  "asin": "B00B5HZGUG",
  "isbn_10": "0553418025",
  "isbn_13": "9780553418026",
  "author_ids": [12345],
  "narrator_ids": [54321],
  "publisher_id": 999,
  "release_date": "2024-01-15",
  "audio_seconds": 53357,
  "edition_format": "Audible Audio",
  "edition_information": "Unabridged",
  "language_id": 1,
  "country_id": 1
}
```

**Field descriptions:**
- `book_id`: Hardcover book ID (required)
- `title`: Edition title (required)
- `subtitle`: Edition subtitle (optional)
- `image_url`: Cover image URL (required)
- `asin`: Audible ASIN (required)
- `isbn_10`: ISBN-10 identifier (optional)
- `isbn_13`: ISBN-13 identifier (optional)
- `author_ids`: Array of Hardcover author IDs (required)
- `narrator_ids`: Array of Hardcover narrator IDs (optional)
- `publisher_id`: Hardcover publisher ID (optional)
- `release_date`: Release date in YYYY-MM-DD format (required)
- `audio_seconds`: Audio duration in seconds (required)
- `edition_format`: Edition format description (optional, defaults to "Audible Audio")
- `edition_information`: Additional edition information (optional)
- `language_id`: Language ID (optional, defaults to 1 for English)
- `country_id`: Country ID (optional, defaults to 1 for USA)

**Then create the edition:**
```sh
./bin/audiobookshelf-hardcover-sync --create-edition-json book1.json
```

#### Prepopulation Benefits
- **Reduced errors**: Automatically validates author/narrator/publisher IDs
- **Time savings**: No need to manually look up existing book metadata
- **Consistency**: Uses standardized data from Hardcover's database
- **Future-ready**: Designed to work with ASIN references and existing metadata sources

**Note**: The prepopulation feature fetches existing book data from Hardcover's database, so you only need to provide the book ID and any missing fields like ASIN or image URL. This significantly reduces the manual work required for edition creation.

## Advanced Configuration

### Custom CA Certificates
For AudiobookShelf servers with custom SSL certificates:

```yaml
# docker-compose.yml
services:
  abs-hardcover-sync:
    image: ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
    volumes:
      - ./ca.crt:/ca.crt:ro  # Mount your CA certificate
    environment:
      # ... your environment variables
```

The container automatically trusts custom CAs at runtime.

### Rate Limiting Configuration
The application includes built-in rate limiting to prevent API rate limits. By default, it allows up to 10 requests per second. If you encounter `429 Too Many Requests` errors, you can adjust the rate limit using the `HARDCOVER_RATE_LIMIT` environment variable:

```sh
# Reduce the rate limit if needed (e.g., 5 requests per second)
export HARDCOVER_RATE_LIMIT=5
```

### Troubleshooting
Enable debug logging to diagnose issues:

```sh
# Via environment variable
export DEBUG_MODE=1

# Via command line flag
./bin/audiobookshelf-hardcover-sync -v
```

Debug logs include:
- API request/response details
- Book matching logic
- Progress calculations
- Error context

### Dry Run Mode
Test image upload and edition creation without making actual API calls:

```sh
# Enable dry run mode
export DRY_RUN=true   # or DRY_RUN=1, DRY_RUN=yes

# Test image upload
./bin/audiobookshelf-hardcover-sync --upload-image "https://example.com/image.jpg" "Test Description"
# Returns fake ID: 999999

# Test edition creation from JSON
./bin/audiobookshelf-hardcover-sync --create-edition-json my_book.json
# Returns fake Image ID: 888888, Edition ID: 777777
```

**Dry run benefits:**
- 🧪 **Test without side effects**: Validate your workflow without creating actual records
- 🐛 **Debug API issues**: Isolate problems in image upload logic vs API connectivity
- 📚 **Verify JSON structure**: Ensure your edition JSON files are properly formatted
- ⚡ **Fast iteration**: Test changes quickly without waiting for API calls

**What gets simulated in dry run:**
- ✅ Image uploads (both CLI and edition creation)
- ✅ Edition creation mutations
- ✅ All GraphQL API calls in edition workflow
- ❌ Regular sync operations (not yet supported)

## Development

### Building from Source
```sh
git clone https://github.com/drallgood/audiobookshelf-hardcover-sync.git
cd audiobookshelf-hardcover-sync
cp .env.example .env  # Edit with your tokens
make build
./bin/audiobookshelf-hardcover-sync
```

### Docker Development
```sh
# Build image
make docker-build VERSION=dev

# Run with compose
make docker-run VERSION=dev
```

### Testing
```sh
# Run all tests
make test

# Run with coverage
go test -v -cover ./...

# Lint code
make lint
```

### Available Make Targets
- `make build` — Build Go binary
- `make run` — Build and run locally  
- `make test` — Run test suite
- `make lint` — Run linters
- `make docker-build` — Build Docker image
- `make docker-run` — Run with Docker Compose

## Deployment

### Docker Hub / GHCR
Pre-built multi-architecture images are available:

```sh
# Pull latest
docker pull ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest

# Pull specific version  
docker pull ghcr.io/drallgood/audiobookshelf-hardcover-sync:v1.4.0
```

**Supported Platforms:**
- `linux/amd64` (Intel/AMD servers, most cloud providers)
- `linux/arm64` (ARM servers, Raspberry Pi, Apple Silicon)

### Production Deployment
For production use, consider:

1. **Resource Limits**: Set appropriate CPU/memory limits
2. **Restart Policy**: Use `restart: unless-stopped` or similar
3. **Health Monitoring**: Monitor the `/healthz` endpoint
4. **Log Management**: Configure log rotation and shipping
5. **Security**: Run as non-root user (images use `nobody:nobody`)

### Docker Compose Example

Here's a comprehensive `docker-compose.yml` example with all available options:

```yaml
version: '3.8'

services:
  audiobookshelf-hardcover-sync:
    image: ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
    container_name: abs-hardcover-sync
    restart: unless-stopped
    
    environment:
      # --- Required Configuration ---
      - AUDIOBOOKSHELF_URL=https://your-abs-server.com  # Your Audiobookshelf server URL
      - AUDIOBOOKSHELF_TOKEN=your_abs_token            # Your Audiobookshelf API token
      - HARDCOVER_TOKEN=your_hardcover_token           # Your Hardcover API token
      
      # --- Sync Behavior ---
      - SYNC_INTERVAL=1h                 # How often to sync (e.g., 10m, 1h, 6h)
      - SYNC_WANT_TO_READ=true           # Sync unread books as "Want to Read"
      - SYNC_OWNED=true                  # Mark synced books as owned
      - AUDIOBOOK_MATCH_MODE=continue    # Action on ASIN lookup failure: continue/skip/fail
      - INCREMENTAL_SYNC_MODE=enabled    # Enable/disable incremental sync
      - MINIMUM_PROGRESS_THRESHOLD=0.01  # Minimum progress to sync (0.0-1.0)
      - SYNC_STATE_FILE=/app/data/sync_state.json  # Path to sync state file
      
      # --- Rate Limiting ---
      - HARDCOVER_RATE_LIMIT=10          # Max requests per second to Hardcover
      - RATE_BURST=15                    # Max burst requests
      
      # --- Logging ---
      - LOG_LEVEL=info                   # debug, info, warn, error, fatal
      - LOG_FORMAT=json                  # json or console
      - LOG_PRETTY=false                 # Pretty-print JSON logs
      - TZ=UTC                           # Timezone for logs
      
      # --- Advanced ---
      # - DRY_RUN=true                   # Enable dry-run mode (no changes made)
      # - FORCE_FULL_SYNC=false          # Force full sync on next run
      # - TEST_BOOK_LIMIT=10             # Limit number of books to process (testing)
      # - TEST_BOOK_FILTER=keyword        # Filter books by title/author (testing)
    
    # Volume mounts
    volumes:
      # Mount config directory (for config.yaml)
      - ./config:/app/config
      
      # Persistent data (cache, state, etc.)
      - abs_hardcover_data:/app/data
      
      # Temporary storage for file uploads
      - /tmp
    
    # Health check
    healthcheck:
      test: ["CMD", "wget", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
    
    # Resource limits (adjust based on your needs)
    deploy:
      resources:
        limits:
          cpus: '1'     # Adjust based on your system
          memory: 512M  # Adjust based on your library size
    
    # Port mapping (only needed if accessing the HTTP API)
    ports:
      - "8080:8080"

# Named volume for persistent data
volumes:
  abs_hardcover_data:
    driver: local
    
# Optional: Network configuration
# networks:
#   default:
#     driver: bridge
#     ipam:
#       config:
#         - subnet: 172.20.0.0/16
```

### Configuration File Alternative

Instead of environment variables, you can use a `config.yaml` file in the mounted `/app/config` directory:

```yaml
# config/config.yaml
server:
  port: 8080
  timeout: 30s

logging:
  level: info
  format: json
  pretty: false

audiobookshelf:
  url: https://your-abs-server.com
  token: your_abs_token

hardcover:
  token: your_hardcover_token
  rate_limit: 10
  rate_burst: 15

sync:
  interval: 1h
  want_to_read: true
  owned: true
  match_mode: continue
  incremental: enabled
  min_progress: 0.01
  state_file: /app/data/sync_state.json
  dry_run: false
  force_full_sync: false

# For testing
test:
  book_limit: 0
  book_filter: ""
```

## Recent Updates & Migration

### v1.5.0 (Latest)
- **Fixed**: RE-READ detection for manually finished books - eliminates false positive re-read scenarios
- **Enhanced**: Progress detection using `/api/me` endpoint for accurate finished book status
- **Conservative**: Skip logic for edge cases to prevent duplicate entries
- **URL Support**: Better reverse proxy handling with path prefix support
- **Fixed**: 1000x progress multiplication error with smart unit conversion

### v1.4.0
- **New**: Owned books marking (`SYNC_OWNED=true` by default)
- **Enhanced**: "Want to Read" sync now enabled by default
- **Improved**: Better incremental sync performance
- **Fixed**: Re-read scenario handling and duplicate prevention
- 🎯 **Enhanced**: "Want to Read" sync now enabled by default
- ⚡ **Improved**: Better incremental sync performance
- 🔧 **Fixed**: Re-read scenario handling and duplicate prevention

### Migration Notes
- **From v1.4.x**: No breaking changes, enhanced progress detection is automatic
- **From v1.3.x**: No breaking changes, new features enabled by default
- **From v1.2.x**: Set `SYNC_WANT_TO_READ=false` if you prefer old behavior
- **From v1.1.x**: Review incremental sync settings (`INCREMENTAL_SYNC_MODE`)
- **Reverse Proxy Users**: Update `AUDIOBOOKSHELF_URL` to include full path if using path prefix

## Troubleshooting

### Common Issues

#### Enhanced Progress Detection Issues
If you're experiencing issues with book progress not syncing correctly:

1. **Check AudiobookShelf URL Configuration**
   ```sh
   # For reverse proxy setups, include the full path
   AUDIOBOOKSHELF_URL=https://your-domain.com/audiobookshelf
   ```

2. **Enable Debug Mode**
   ```sh
   DEBUG_MODE=true
   ```
   Look for messages like:
   - `[DEBUG] Found authorize data for 'Book Title': isFinished=true`
   - `[DEBUG] Enhanced progress detection found X items`

3. **API Endpoint Access**
   Ensure your AudiobookShelf token has access to the `/api/me` endpoint by testing:
   ```sh
   curl -H "Authorization: Bearer YOUR_TOKEN" \
        "https://your-abs-server/audiobookshelf/api/me"
   ```

#### False RE-READ Detection (Legacy Issue)
If books are still being incorrectly treated as re-reads:
- Update to the latest version (v1.5.0+) which includes the RE-READ detection fix
- Check that enhanced progress detection is working (see debug logs)
- Verify your AudiobookShelf URL includes the correct path prefix

#### Progress Not Syncing
- Check `MINIMUM_PROGRESS_THRESHOLD` setting (default: 0.001 = 0.1%)
- Verify books are marked as "Finished" in AudiobookShelf if they should be 100%
- Enable debug mode to see detailed progress calculations

### Getting Help
For additional support:
- 📋 Check [existing issues](https://github.com/drallgood/audiobookshelf-hardcover-sync/issues)
- 📖 Review [Enhanced Progress Detection docs](docs/ENHANCED_PROGRESS_DETECTION.md)
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
