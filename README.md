# audiobookshelf-hardcover-sync

[![Trivy Scan](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml/badge.svg)](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml)

Syncs Audiobookshelf to Hardcover.

## Features
- Syncs your Audiobookshelf library with Hardcover
- Optional "Want to Read" sync for unstarted books (set `SYNC_WANT_TO_READ=true`)
- Periodic sync (set `SYNC_INTERVAL`, e.g. `10m`, `1h`)
- Manual sync via HTTP POST/GET to `/sync`
- Health check at `/healthz`
- Multi-arch container images (amd64, arm64)
- Secure, minimal, and production-ready
- Robust debug logging (`-v` flag or `DEBUG_MODE=1`)

## Recent Updates
- 🎯 **"Want to Read" Default Enabled v1.3.2** (June 2025): "Want to Read" sync is now enabled by default! Unstarted books (0% progress) are automatically synced to Hardcover as "Want to Read" status. Set `SYNC_WANT_TO_READ=false` to disable.
- 🚨 **CRITICAL HOTFIX v1.3.1** (June 2025): Fixed critical regression where reading history was being wiped out during sync operations. The `update_user_book_read` mutation now properly preserves `started_at` dates. **IMMEDIATE UPGRADE REQUIRED** for all v1.3.0 users.
- 🚀 **Incremental/Delta Sync** (June 2025): Added timestamp-based incremental syncing to reduce API calls and improve performance. Only processes books with changes since last sync.
- ✅ **Expectation #4 Logic Fix** (June 2025): Fixed re-read scenario handling where books with 100% progress in Hardcover but <99% in AudiobookShelf now correctly create new reading sessions instead of being skipped
- ✅ **Duplicate Reads Prevention**: Implemented logic to prevent duplicate `user_book_reads` entries on the same day

## Environment Variables
| Variable                 | Description                                                                                 |
|--------------------------|-------------------------------------------------------------------------------------------|
| AUDIOBOOKSHELF_URL       | URL to your AudiobookShelf server                                                            |
| AUDIOBOOKSHELF_TOKEN     | API token for AudiobookShelf (see instructions below; required for all API requests)         |
| HARDCOVER_TOKEN          | API token for Hardcover                                                                     |
| SYNC_INTERVAL            | (optional) Go duration string for periodic sync                                             |
| HARDCOVER_SYNC_DELAY_MS  | (optional) Delay between Hardcover syncs in milliseconds (default: 1500)                    |
| MINIMUM_PROGRESS_THRESHOLD | (optional) Minimum progress threshold to sync books (0.0-1.0, default: 0.01 = 1%)           |
| AUDIOBOOK_MATCH_MODE     | (optional) Behavior when ASIN lookup fails: `continue` (default), `skip`, `fail`            |
| SYNC_WANT_TO_READ        | (optional) Sync books with 0% progress as "Want to Read": `true` (default), `false`         |
| INCREMENTAL_SYNC_MODE    | (optional) Incremental sync mode: `enabled` (default), `disabled`, `auto`                   |
| SYNC_STATE_FILE          | (optional) Path to sync state file for incremental sync (default: `sync_state.json`)        |
| FORCE_FULL_SYNC          | (optional) Force full sync on next run: `true`, `false` (default)                           |
| DEBUG_MODE               | (optional) Set to `1` to enable verbose debug logging                                       |

You can copy `.env.example` to `.env` and fill in your values.

## Setup Instructions

### AudiobookShelf Setup
1. Log in to your AudiobookShelf web UI as an admin/user.
2. Go to **config → users**, then click on your account.
3. Copy your API token from the user details page.
   - Alternatively, you can obtain the API token programmatically using the Login endpoint; the token is in `response.user.token`. For example:
     ```sh
     curl -X POST "https://your-abs-server/api/login" -H 'Content-Type: application/json' -d '{"username":"YOUR_USER","password":"YOUR_PASS"}'
     # The token is in response.user.token
     ```
4. Set `AUDIOBOOKSHELF_URL` to your server URL (e.g., https://abs.example.com).
5. Set `AUDIOBOOKSHELF_TOKEN` to the token you copied.

> **Note:** AudiobookShelf does not support username/password or session/JWT tokens for API access. You must use an API token as described above. For GET requests, the API token is sent as a Bearer token (or optionally as a query string).

### Hardcover Setup
1. Log in to https://hardcover.app.
2. Go to your account settings or API section.
3. Generate a new API token and copy it.
4. Set `HARDCOVER_TOKEN` to the token you generated.

## Features Configuration

### Want to Read Sync

By default, the sync tool processes both books with reading progress and unstarted books (0% progress) as "Want to Read" status in Hardcover. You can disable this behavior if you only want to sync books with actual progress.

To disable this feature:
```sh
export SYNC_WANT_TO_READ=false
```

**How it works:**
- Books with 0% progress are synced to Hardcover with status "Want to Read" (status_id=1)
- Books with any progress (>0% but <99%) are synced as "Currently Reading" (status_id=2)  
- Books with ≥99% progress are synced as "Read" (status_id=3)

**Use cases:**
- You have books in your AudiobookShelf library that you plan to read but haven't started
- You want to maintain a comprehensive "Want to Read" list in Hardcover
- You're migrating from another platform and want to preserve your reading list

**Example usage:**
```sh
# Feature is enabled by default, no configuration needed

# To disable syncing of unstarted books:
SYNC_WANT_TO_READ=false

# Or run with the environment variable
SYNC_WANT_TO_READ=false ./main
```

This feature is enabled by default. If you prefer the old behavior (only syncing books with progress), set `SYNC_WANT_TO_READ=false`.

## How it works

This service fetches all libraries from your AudiobookShelf server using the `/api/libraries` endpoint, then fetches all items for each library using `/api/libraries/{libraryId}/items`. It filters for items with `mediaType == "book"` and extracts title/author from `media.metadata`, then syncs their progress to Hardcover.

### Smart Progress Filtering
The service only syncs books that have meaningful progress (configurable via `MINIMUM_PROGRESS_THRESHOLD`, default 1%). This prevents syncing books that have been barely started or accidentally opened.

### Accurate Progress Calculation
Progress is calculated using the most accurate method available:
1. **User Progress API**: Fetches progress from `/api/me` endpoint which includes manually finished books with `isFinished=true` flags
2. **Current Time**: Uses actual `currentTime` from AudiobookShelf when available
3. **Duration Calculation**: Calculates progress from `totalDuration * progressPercentage` when duration is known
4. **Fallback**: Uses a reasonable 10-hour duration estimate (much better than the previous 1-hour assumption)

### Manual Finish Detection
The service now properly detects books that have been manually marked as finished in AudiobookShelf, even if their progress percentage is less than 100%. This is achieved by checking the `mediaProgress` data from the `/api/me` endpoint for `isFinished=true` flags.

### Status Mapping
- Books with progress >= 99% are marked as "read" (status_id=3) on Hardcover
- Books with less progress are marked as "currently reading" (status_id=2)
- The service looks up books by ISBN-13, ISBN-10, or ASIN first, then falls back to title/author matching for better accuracy

## Getting Started

### Prerequisites
- Go 1.22+
- Docker (for container usage)

### Building Locally
```sh
git clone https://github.com/drallgood/audiobookshelf-hardcover-sync.git
cd audiobookshelf-hardcover-sync
cp .env.example .env # Edit as needed
make build
./main
```

### Running with Docker
Build the image:
```sh
make docker-build VERSION=dev
```
Run the container:
```sh
make docker-run VERSION=dev
```

### Running with Docker Compose
1. Copy `.env.example` to `.env` and edit your secrets:
   ```sh
   cp .env.example .env
   # Edit .env as needed
   ```
2. Start the service:
   ```sh
   docker compose up -d
   ```

### Using a Custom CA Certificate (for Internal TLS)

If your AudiobookShelf server uses a certificate signed by a custom or internal CA, you can provide your CA certificate at runtime without rebuilding the image.

1. Save your CA certificate as `ca.crt`.
2. When running the container, mount your CA cert to `/ca.crt`:
   
   **Docker Compose example:**
   ```yaml
   services:
     abs-hardcover-sync:
       # ...existing config...
       volumes:
         - ./ca.crt:/ca.crt:ro
   ```
   **Docker CLI example:**
   ```sh
   docker run -v $(pwd)/ca.crt:/ca.crt:ro ... ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
   ```

At startup, the container will automatically combine your custom CA with the system CA bundle and set `SSL_CERT_FILE` so your internal certificates are trusted. This is handled by the `entrypoint.sh` script.

> **Note:** No image rebuild is required. The CA is picked up at runtime via the entrypoint script.

### Endpoints
- `GET /healthz` — Health check
- `POST/GET /sync` — Trigger a sync manually (both methods supported)

### Rate Limiting / Throttling

If you see errors like `429 Too Many Requests` or `{ "error": "Throttled" }` from Hardcover, the sync tool will automatically delay between requests (default 1.5s, configurable via `HARDCOVER_SYNC_DELAY_MS`) and retry up to 3 times on 429 errors, respecting the `Retry-After` header if present.

You can increase the delay if you have a large library or continue to see throttling:

```sh
export HARDCOVER_SYNC_DELAY_MS=3000 # 3 seconds between syncs
```

### Command Line Options

```sh
./main --help
```

Available flags:
- `-v` — Enable verbose debug logging
- `--health-check` — Run health check and exit
- `--version` — Show version and exit

### Debug Logging

To enable verbose debug logging for troubleshooting, either run with the `-v` flag or set the environment variable:

```sh
export DEBUG_MODE=1
```

This will print detailed logs for API requests, responses, errors, and sync progress.

### Pulling from GitHub Container Registry (GHCR)
To pull the latest image:
```sh
docker pull ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
```

### Publishing to GHCR
Images are published automatically via GitHub Actions on every push to main and on release.

## Multi-Architecture Images
Images are published for both `linux/amd64` and `linux/arm64` platforms. This means you can run the container on most modern servers, desktops, and ARM-based devices (like Raspberry Pi and Apple Silicon Macs).

## Makefile Usage
Common tasks are available via the Makefile:
- `make build` – Build the Go binary
- `make run` – Build and run locally
- `make lint` – Run Go vet and lint
- `make test` – Run tests
- `make docker-build` – Build the Docker image
- `make docker-run` – Run the Docker image with Compose

## Testing
To run tests:
```sh
make test
```

Tests cover the AudiobookShelf and Hardcover API integration, error handling, rate limiting, and progress sync logic. Contributions to test coverage are welcome!

## Contributing
Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Security
See [SECURITY.md](SECURITY.md) for vulnerability reporting.

## License
This project is licensed under the terms of the Apache 2.0 license. See [LICENSE](LICENSE) for details.
