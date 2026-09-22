# Edition Creation Tool

This tool helps create and manage audiobook editions in Hardcover. It provides two main commands:

1. `prepopulate`: Generate a prepopulated JSON template from an existing book
2. `create`: Create a new edition using a JSON input file

## Running with Docker

### Prerequisites
- Docker installed on your system
- A `config.yaml` file with the Hardcover API token under `hardcover.token`:

  ```yaml
  hardcover:
    token: your_token
  ```

  The token needs these scopes:
  - `read:library`
  - `write:library`
  - `read:catalog`
  - `write:catalog:append`

### Basic Usage

#### Build the Docker Image
```bash
docker build -t audiobookshelf-hardcover-sync .
```

#### Prepopulate a Template
Generate a JSON template from an existing book:

```bash
docker run --rm \
  -v "$(pwd)/config.yaml:/app/config.yaml:ro" \
  -v "$(pwd):/work" \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest \
  edition-tool --config /app/config.yaml prepopulate --book-id 12345 --output /work/edition.json
```

#### Create a New Edition
Create a new edition using a JSON input file:

```bash
docker run --rm \
  -v "$(pwd)/config.yaml:/app/config.yaml:ro" \
  -v "$(pwd):/work" \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest \
  edition-tool --config /app/config.yaml create --input /work/edition.json
```

### Advanced Options

#### Dry Run Mode
Test without making any changes:

```bash
docker run --rm \
  -v "$(pwd)/config.yaml:/app/config.yaml:ro" \
  -v "$(pwd):/work" \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest \
  edition-tool --config /app/config.yaml --dry-run create --input /work/edition.json
```

#### Interactive Mode
Run in interactive mode to be prompted for input:

```bash
docker run -it --rm \
  -v "$(pwd)/config.yaml:/app/config.yaml:ro" \
  ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest \
  edition-tool --config /app/config.yaml create --interactive
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
./edition --config ./config.yaml prepopulate --book-id 12345 --output edition.json
```

#### Create a New Edition

```bash
./edition --config ./config.yaml create --input edition.json
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
  "reading_format": "audiobook",
  "edition_information": "Special edition with bonus content",
  "language_id": 1,
  "country_id": 1
}
```

`reading_format` is optional: `audiobook` (the default) or `ebook`. An `ebook`
edition is created with Hardcover's ebook reading format, an `Ebook` default
`edition_format`, and without narrators or `audio_seconds`, and duplicate
detection by ASIN and ISBN only considers ebook editions. Mismatch files
exported for ebook items already carry `"reading_format": "ebook"`.

## Configuration

The tool reads `config.yaml` by default, or another file supplied with
`--config`. Configure the Hardcover API token in that file; the edition
commands do not use `HARDCOVER_TOKEN` environment overrides.

```yaml
hardcover:
  token: your_token
```

## Examples

### Basic Usage

1. First, generate a template:
   ```bash
   ./edition --config ./config.yaml prepopulate --book-id 12345 --output my-audiobook.json
   ```

2. Edit the generated JSON file as needed

3. Create the edition:
   ```bash
   ./edition --config ./config.yaml create --input my-audiobook.json
   ```

   Before creating, the tool looks for an existing audiobook edition with the
   same ASIN, ISBN-13 or ISBN-10 (an ISBN also under its converted form). One of
   the same book is reused untouched (no cover or metadata is sent) and the
   printed result includes `"existing": true`. An existing edition of a
   different book, or one whose book cannot be confirmed, is refused with an
   error.

   No cover is uploaded for now: Hardcover's cover upload endpoint is not part of
   its documented API and rejected a new scoped API token. An `image_url` in the
   input is not fetched; the edition is still created and the printed result
   carries an `image_error` saying so. The cover code is kept, switched off.

### Dry Run

```bash
./edition --config ./config.yaml --dry-run create --input my-audiobook.json
```

## Error Handling

- The tool will validate the input JSON before making any API calls
- If an error occurs, it will be displayed with a helpful message
- Use the `--dry-run` flag to test without making changes
