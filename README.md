# audiobookshelf-hardcover-sync

[![Trivy Scan](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml/badge.svg)](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml)

Syncs Audiobookshelf to Hardcover.

## Features
- Syncs your Audiobookshelf library with Hardcover
- Periodic sync (set `SYNC_INTERVAL`, e.g. `10m`, `1h`)
- Manual sync via HTTP POST/GET to `/sync`
- Health check at `/healthz`
- Multi-arch container images (amd64, arm64)
- Secure, minimal, and production-ready

## Environment Variables
| Variable                | Description                                                                                 |
|-------------------------|---------------------------------------------------------------------------------------------|
| AUDIOBOOKSHELF_URL      | URL to your AudiobookShelf server                                                            |
| AUDIOBOOKSHELF_TOKEN    | API token for AudiobookShelf (see instructions below; required for all API requests)         |
| HARDCOVER_TOKEN         | API token for Hardcover                                                                     |
| SYNC_INTERVAL           | (optional) Go duration string for periodic sync                                             |

You can copy `.env.example` to `.env` and fill in your values.

## Setup Instructions

### AudiobookShelf Setup
1. Log in to your AudiobookShelf web UI as an admin/user.
2. Go to **config → users**, then click on your account.
3. Copy your API token from the user details page.
   - Alternatively, you can obtain the API token programmatically using the Login endpoint; the token is in `response.user.token`.
4. Set `AUDIOBOOKSHELF_URL` to your server URL (e.g., https://abs.example.com).
5. Set `AUDIOBOOKSHELF_TOKEN` to the token you copied.

> **Note:** AudiobookShelf does not support username/password or session/JWT tokens for API access. You must use an API token as described above. For GET requests, the API token is sent as a Bearer token (or optionally as a query string).

### Hardcover Setup
1. Log in to https://hardcover.app.
2. Go to your account settings or API section.
3. Generate a new API token and copy it.
4. Set `HARDCOVER_TOKEN` to the token you generated.

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

### Endpoints
- `GET /healthz` — Health check
- `POST/GET /sync` — Trigger a sync manually

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

## Contributing
Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Security
See [SECURITY.md](SECURITY.md) for vulnerability reporting.

## License
This project is licensed under the terms of the Apache 2.0 license. See [LICENSE](LICENSE) for details.
