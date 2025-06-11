# audiobookshelf-hardcover-sync

[![Trivy Scan](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml/badge.svg)](https://github.com/drallgood/audiobookshelf-hardcover-sync/actions/workflows/trivy.yml)

Automatically syncs your Audiobookshelf library with Hardcover, including reading progress, book status, and ownership information.

## Features
- 📚 **Full Library Sync**: Syncs your entire Audiobookshelf library with Hardcover
- 🎯 **Smart Status Management**: Automatically sets "Want to Read", "Currently Reading", and "Read" status based on progress
- 🏠 **Ownership Tracking**: Marks synced books as "owned" to distinguish from wishlist items
- ⚡ **Incremental Sync**: Efficient timestamp-based syncing to reduce API calls
- 🚀 **Smart Caching**: Intelligent caching of author/narrator lookups with cross-role discovery
- 📊 **Enhanced Progress Detection**: Uses `/api/me` endpoint for accurate finished book detection, preventing false re-read scenarios
- 🔄 **Periodic Sync**: Configurable automatic syncing (e.g., every 10 minutes or 1 hour)
- 🎛️ **Manual Sync**: HTTP endpoints for on-demand synchronization
- 🏥 **Health Monitoring**: Built-in health check endpoint
- 🐳 **Container Ready**: Multi-arch Docker images (amd64, arm64)
- 🔍 **Debug Logging**: Comprehensive logging for troubleshooting
- 🔧 **Edition Creation Tools**: Interactive tools for creating missing audiobook editions
- 🔍 **ID Lookup**: Search and verify author, narrator, and publisher IDs from Hardcover database
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
| `AUDIBLE_API_ENABLED` | `false` | Enable Audible API integration for enhanced metadata |
| `AUDIBLE_API_TOKEN` | None | API token for Audible service (if required) |
| `AUDIBLE_API_TIMEOUT` | `10s` | Timeout for Audible API requests (e.g., `5s`, `30s`) |
| `DRY_RUN` | `false` | Enable dry run mode for testing (`true`, `1`, or `yes`) |
| `MISMATCH_JSON_FILE` | None | Directory path for saving individual mismatch JSON files |
| `TZ` | System default | Timezone for container logs (e.g., `Europe/Vienna`, `UTC`) |

### Development & Testing Variables
| Variable | Default | Description |
|----------|---------|-------------|
| `TEST_BOOK_FILTER` | None | Filter books by title for development testing (case-insensitive) |
| `TEST_BOOK_LIMIT` | None | Limit number of books to process for development testing |

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

# Edition creation commands
./main --create-edition                           # Interactive edition creation
./main --create-edition-prepopulated            # Interactive with prepopulation
./main --generate-example filename.json         # Generate blank template
./main --generate-prepopulated bookid:file.json # Generate prepopulated template
./main --enhance-template file.json:bookid      # Enhance existing template
./main --create-edition-json filename.json      # Create from JSON file

# ID lookup commands for edition creation
./main --lookup-author                           # Search for author IDs by name
./main --lookup-narrator                         # Search for narrator IDs by name  
./main --lookup-publisher                        # Search for publisher IDs by name
./main --verify-author-id 12345                  # Verify author ID
./main --verify-narrator-id 67890                # Verify narrator ID
./main --verify-publisher-id 54321               # Verify publisher ID
```

#### ID Lookup Tools
When creating editions, you need to find the correct Hardcover IDs for authors, narrators, and publishers. These lookup tools help you search the Hardcover database interactively:

```sh
# Search for authors by name (fuzzy matching)
./main --lookup-author
# Interactive prompt: Enter author name to search: Stephen King
# Shows results with IDs, book counts, and canonical vs alias status

# Search for narrators by name
./main --lookup-narrator  
# Similar to author search but filters for people with narrator roles

# Search for publishers by name
./main --lookup-publisher
# Shows publisher results with edition counts

# Bulk lookup commands (comma-separated names)
./main --bulk-lookup-authors "Stephen King,Brandon Sanderson"
./main --bulk-lookup-narrators "Jim Dale,Kate Reading" 
./main --bulk-lookup-publishers "Penguin,Macmillan"

# Verify specific IDs
./main --verify-author-id 123456      # Check author details
./main --verify-narrator-id 789012    # Check narrator details  
./main --verify-publisher-id 345678   # Check publisher details

# Image upload utility
./main --upload-image "https://example.com/image.jpg:Cover art description"
```

**Key Features:**
- **Fuzzy search**: Partial name matching (e.g., "King" finds "Stephen King")
- **Canonical detection**: Shows which entries are primary vs aliases
- **Relevance sorting**: Results ordered by popularity (book/edition counts)
- **Role filtering**: Narrator search only shows people who have narrated books
- **ID verification**: Confirm IDs and see full details before using in JSON files
- **Bulk lookup**: Search multiple names at once with comma-separated lists
- **Image upload**: Upload cover images from URLs for use in editions

### Edition Creation Tools
This tool includes comprehensive functionality to create new audiobook editions in Hardcover when books are missing ASINs:

```sh
# Interactive edition creation (manual entry)
./main --create-edition

# Interactive edition creation with prepopulation
./main --create-edition-prepopulated

# Generate example JSON template
./main --generate-example my_book.json

# Generate prepopulated template from existing book
./main --generate-prepopulated 123456:my_book.json

# Enhance existing template with book data
./main --enhance-template my_book.json:123456

# Create edition from JSON file
./main --create-edition-json my_book.json
```

#### Interactive Edition Creation
The `--create-edition` flag launches an interactive CLI that guides you through:
- Entering book ID, title, ASIN, and image URL
- Looking up and validating authors and narrators
- Setting publisher, release date, and audio length
- Confirming all details before creation

#### Prepopulated Edition Creation
The `--create-edition-prepopulated` flag provides **automated data population** from existing Hardcover books:

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
./main --generate-prepopulated 123456:book1.json
```

**Option 2: Enhance existing template**
```sh
# Add book data to existing template
./main --enhance-template book1.json:123456
```

**Option 3: Manual template creation**
```sh
# Generate blank template
./main --generate-example book1.json
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
./main --create-edition-json book1.json
```

#### Prepopulation Benefits
- **Reduced errors**: Automatically validates author/narrator/publisher IDs
- **Time savings**: No need to manually look up existing book metadata
- **Consistency**: Uses standardized data from Hardcover's database
- **Future-ready**: Designed to integrate with external APIs (Audible, Goodreads, etc.)

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

### Dry Run Mode
Test image upload and edition creation without making actual API calls:

```sh
# Enable dry run mode
export DRY_RUN=true   # or DRY_RUN=1, DRY_RUN=yes

# Test image upload
./main --upload-image "https://example.com/image.jpg" "Test Description"
# Returns fake ID: 999999

# Test edition creation from JSON
./main --create-edition-json my_book.json
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
      - DRY_RUN=false                    # Enable dry run mode (true/1/yes)
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

### v1.5.0 (Latest)
- 🔧 **Fixed**: RE-READ detection for manually finished books - eliminates false positive re-read scenarios
- 📊 **Enhanced**: Progress detection using `/api/me` endpoint for accurate finished book status
- 🛡️ **Conservative**: Skip logic for edge cases to prevent duplicate entries
- 🔗 **URL Support**: Better reverse proxy handling with path prefix support
- 🔧 **Fixed**: 1000x progress multiplication error with smart unit conversion

### v1.4.0
- ✨ **New**: Owned books marking (`SYNC_OWNED=true` by default)
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
