# audiobookshelf-hardcover-sync

[![Trivy Scan](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml/badge.svg)](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml)

Syncs Audiobookshelf to Hardcover.

## Features
- Syncs your Audiobookshelf library with Hardcover
- Lightweight, container-ready Go application

## Environment Variables
| Variable                | Description                        |
|-------------------------|------------------------------------|
| AUDIOBOOKSHELF_URL      | URL to your AudiobookShelf server  |
| AUDIOBOOKSHELF_TOKEN    | API token for AudiobookShelf       |
| HARDCOVER_TOKEN         | API token for Hardcover            |

You can copy `.env.example` to `.env` and fill in your values.

## Getting Started

### Prerequisites
- Go 1.21+
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

### Pulling from GitHub Container Registry (GHCR)
To pull the latest image:
```sh
docker pull ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest
```

### Publishing to GHCR
Images are published automatically via GitHub Actions on every push to main and on release.

## Multi-Architecture Images

Images are published for both `linux/amd64` and `linux/arm64` platforms. This means you can run the container on most modern servers, desktops, and ARM-based devices (like Raspberry Pi and Apple Silicon Macs).

## Docker Compose Example

A sample `docker-compose.yml` is provided for easy local or production deployment. It includes health checks, logging, and resource limits.

## Makefile Usage

Common tasks are available via the Makefile:

- `make build` – Build the Go binary
- `make run` – Build and run locally
- `make lint` – Run Go vet and lint
- `make test` – Run tests
- `make docker-build` – Build the Docker image
- `make docker-run` – Run the Docker image with Compose

## Testing
```sh
make test
```

## Contributing
Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Security
See [SECURITY.md](SECURITY.md) for vulnerability reporting.

## License
This project is licensed under the terms of the MIT license. See [LICENSE](LICENSE) for details.
