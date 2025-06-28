# Edition Creation Tool

This tool helps create and manage audiobook editions in Hardcover. It provides two main commands:

1. `prepopulate`: Generate a prepopulated JSON template from an existing book
2. `create`: Create a new edition using a JSON input file

## Running with Docker

### Prerequisites
- Docker installed on your system
- Hardcover API token (set as `HARDCOVER_TOKEN` environment variable)

### Basic Usage

#### Build the Docker Image
```bash
docker build -t audiobookshelf-hardcover-sync .
```

#### Prepopulate a Template
Generate a JSON template from an existing book:

```bash
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  -v $(pwd):/app \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest \
  edition-tool prepopulate --book-id 12345 --output /app/edition.json
```

#### Create a New Edition
Create a new edition using a JSON input file:

```bash
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  -v $(pwd):/app \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest \
  edition-tool create --input /app/edition.json
```

### Advanced Options

#### Dry Run Mode
Test without making any changes:

```bash
docker run --rm \
  -e HARDCOVER_TOKEN=your_token \
  -v $(pwd):/app \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest \
  edition-tool create --input /app/edition.json --dry-run
```

#### Interactive Mode
Run in interactive mode to be prompted for input:

```bash
docker run -it --rm \
  -e HARDCOVER_TOKEN=your_token \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest \
  edition-tool create --interactive
```

## Local Development

### Installation

Build the tool using Go:

```bash
go build -o edition cmd/edition/main.go
```

### Usage

#### Prepopulate a Template

```bash
HARDCOVER_TOKEN=your_token ./edition prepopulate --book-id 12345 --output edition.json
```

#### Create a New Edition

```bash
HARDCOVER_TOKEN=your_token ./edition create --input edition.json
```

## JSON Schema

The input JSON should follow this structure:

```json
{
  "book_id": 12345,
  "title": "Book Title",
  "subtitle": "Unabridged Edition",
  "image_url": "https://example.com/cover.jpg",
  "asin": "B00XXXYYZZ",
  "isbn_10": "1234567890",
  "isbn_13": "9781234567890",
  "author_ids": [1, 2, 3],
  "narrator_ids": [4, 5],
  "publisher_id": 10,
  "release_date": "2023-01-01",
  "audio_seconds": 3600,
  "edition_format": "Audible Audio",
  "edition_information": "Special edition with bonus content",
  "language_id": 1,
  "country_id": 1
}
```

## Configuration

The tool reads from the same `config.yaml` file as the main application. Make sure to configure your Hardcover API key and other settings there.

## Examples

### Basic Usage

1. First, generate a template:
   ```bash
   ./edition prepopulate --book-id 12345 --output my-audiobook.json
   ```

2. Edit the generated JSON file as needed

3. Create the edition:
   ```bash
   ./edition create --input my-audiobook.json
   ```

### Dry Run

```bash
./edition create --input my-audiobook.json --dry-run
```

## Error Handling

- The tool will validate the input JSON before making any API calls
- If an error occurs, it will be displayed with a helpful message
- Use the `--dry-run` flag to test without making changes
