# audiobookshelf-hardcover-sync

[![Trivy Scan](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml/badge.svg)](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml)

Automatically syncs your Audiobookshelf library with Hardcover, including reading progress, book status, and ownership information.

## Features
- 📚 **Full Library Sync**: Syncs your entire Audiobookshelf library with Hardcover
- 🎯 **Smart Status Management**: Automatically sets "Want to Read", "Currently Reading", and "Read" status based on progress
- 🏠 **Ownership Tracking**: Marks synced books as "owned" to distinguish from wishlist items
- ⚡ **Incremental Sync**: Efficient timestamp-based syncing to reduce API calls
- 🔄 **Periodic Sync**: Configurable automatic syncing (e.g., every 10 minutes or 1 hour)
- 🎛️ **Manual Sync**: HTTP endpoints for on-demand synchronization
- 🏥 **Health Monitoring**: Built-in health check endpoint
- 🐳 **Container Ready**: Multi-arch Docker images (amd64, arm64)
- 🔍 **Debug Logging**: Comprehensive logging for troubleshooting
- 🛡️ **Production Ready**: Secure, minimal, and battle-tested

## Quick Start

### Using Docker Compose (Recommended)
1. Create a `.env` file with your API tokens:
   ```sh
   cp .env.example .env
   # Edit .env with your AUDIOBOOKSHELF_URL, AUDIOBOOKSHELF_TOKEN, and HARDCOVER_TOKEN
   ```

2. Start the service:
   ```sh
   docker compose up -d
   ```

### Using Docker
```sh
docker run -d \
  -e AUDIOBOOKSHELF_URL=https://your-abs-server.com \
  -e AUDIOBOOKSHELF_TOKEN=your_abs_token \
  -e HARDCOVER_TOKEN=your_hardcover_token \
  -e SYNC_INTERVAL=1h \
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
| Variable | Default | Description |
|----------|---------|-------------|
| `SYNC_INTERVAL` | None | Periodic sync interval (e.g., `10m`, `1h`, `30s`) |
| `SYNC_WANT_TO_READ` | `true` | Sync unstarted books (0% progress) as "Want to Read" |
| `SYNC_OWNED` | `true` | Mark synced books as "owned" in Hardcover |
| `INCREMENTAL_SYNC_MODE` | `enabled` | Incremental sync mode: `enabled`, `disabled`, `auto` |
| `MINIMUM_PROGRESS_THRESHOLD` | `0.01` | Minimum progress to sync (0.0-1.0, 0.01 = 1%) |
| `HARDCOVER_SYNC_DELAY_MS` | `1500` | Delay between API calls to prevent rate limiting |
| `AUDIOBOOK_MATCH_MODE` | `continue` | ASIN lookup failure behavior: `continue`, `skip`, `fail` |
| `SYNC_STATE_FILE` | `sync_state.json` | Path to incremental sync state file |
| `FORCE_FULL_SYNC` | `false` | Force full sync on next run |
| `DEBUG_MODE` | `false` | Enable verbose debug logging (`1` or `true`) |

## Setup Instructions

### Getting Your API Tokens

#### AudiobookShelf Token
1. Log in to your AudiobookShelf web interface
2. Navigate to **Settings → Users** and click on your account
3. Copy the API token from your user profile
4. Set `AUDIOBOOKSHELF_URL` to your server URL and `AUDIOBOOKSHELF_TOKEN` to your token

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

### Command Line
```sh
# Run once
./main

# Enable debug logging
./main -v
# or
DEBUG_MODE=1 ./main

# Show version
./main --version

# Health check
./main --health-check
```

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
If you encounter `429 Too Many Requests` errors:

```sh
# Increase delay between API calls (default: 1500ms)
export HARDCOVER_SYNC_DELAY_MS=3000

# For very large libraries
export HARDCOVER_SYNC_DELAY_MS=5000
```

### Troubleshooting
Enable debug logging to diagnose issues:

```sh
# Via environment variable
export DEBUG_MODE=1

# Via command line flag
./main -v
```

Debug logs include:
- API request/response details
- Book matching logic
- Progress calculations
- Error context

## Development

### Building from Source
```sh
git clone https://github.com/drallgood/audiobookshelf-hardcover-sync.git
cd audiobookshelf-hardcover-sync
cp .env.example .env  # Edit with your tokens
make build
./main
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

```yaml
# docker-compose.yml
services:
  abs-hardcover-sync:
    image: ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
    restart: unless-stopped
    environment:
      - AUDIOBOOKSHELF_URL=${AUDIOBOOKSHELF_URL}
      - AUDIOBOOKSHELF_TOKEN=${AUDIOBOOKSHELF_TOKEN}
      - HARDCOVER_TOKEN=${HARDCOVER_TOKEN}
      - SYNC_INTERVAL=1h
    deploy:
      resources:
        limits:
          memory: 128M
          cpus: '0.5'
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--spider", "http://localhost:8080/healthz"]
      interval: 30s
      timeout: 10s
      retries: 3
```

## Recent Updates & Migration

### v1.4.0 (Current)
- ✨ **New**: Owned books marking (`SYNC_OWNED=true` by default)
- 🎯 **Enhanced**: "Want to Read" sync now enabled by default
- ⚡ **Improved**: Better incremental sync performance
- 🔧 **Fixed**: Re-read scenario handling and duplicate prevention

### Migration Notes
- **From v1.3.x**: No breaking changes, new features enabled by default
- **From v1.2.x**: Set `SYNC_WANT_TO_READ=false` if you prefer old behavior
- **From v1.1.x**: Review incremental sync settings (`INCREMENTAL_SYNC_MODE`)

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
