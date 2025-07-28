# Individual JSON Mismatch Files Feature

## Overview
The system now saves each book mismatch as an individual JSON file when the `MISMATCH_JSON_FILE` environment variable is set to a directory path. This is particularly useful when dealing with many mismatches (e.g., 200+) for easier individual processing.

## Usage

### Basic Usage
```bash
# Run the sync process
./audiobookshelf-hardcover-sync sync

# Check for mismatch files
ls -l mismatches/*.json
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MISMATCH_DIR` | Directory to store mismatch JSON files | `./mismatches` |
| `LOG_FORMAT` | Log output format (`json` or `text`) | `json` |
| `LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |

### Example Workflow

1. **Run the Sync**
   ```bash
   # Enable debug logging for detailed output
   LOG_LEVEL=debug LOG_FORMAT=text ./audiobookshelf-hardcover-sync sync
   ```

2. **Check for Mismatches**
   ```bash
   # List all mismatch files
   find mismatches -name '*_mismatch.json' | xargs ls -l
   
   # View a specific mismatch
   cat mismatches/1234567890_mismatch.json | jq .
   ```

3. **Process Mismatches**
   - Review the JSON files for details
   - Update book metadata in AudiobookShelf if needed
   - Retry the sync for specific books

4. **Automated Processing**
   ```bash
   # Example: Generate a report of all mismatches
   echo "Mismatch Report - $(date)" > mismatch_report.txt
   echo "================================" >> mismatch_report.txt
   for f in mismatches/*_mismatch.json; do
     echo "\n$(basename $f)" >> mismatch_report.txt
     jq -r '"Title: \(.book.title)\nAuthor: \(.book.author)\nIssue: \(.issue)\n"' $f >> mismatch_report.txt
   done
   ```

## Configuration

Set the environment variable to specify where individual JSON files should be saved:

```bash
export MISMATCH_JSON_FILE="./mismatches"
```

## File Structure

Each mismatch is saved as a separate JSON file with the following naming pattern:
```
001_Book_Title.json
002_Another_Book_Title.json
003_Special_Characters_Cleaned.json
...
```

### Filename Sanitization
- Special characters are replaced with underscores for filesystem compatibility
- Titles longer than 50 characters are truncated
- Numbers and hyphens are preserved
- Spaces become underscores

## JSON File Content

Each file contains a complete `BookMismatch` object with all metadata:

```json
{
  "Title": "The Great Audiobook",
  "Subtitle": "A Tale of Audio",
  "Author": "John Author",
  "Narrator": "Jane Narrator",
  "Publisher": "Audio Books Inc",
  "PublishedYear": "2023",
  "ReleaseDate": "2023-05-15",
  "Duration": 18.758333,
  "DurationSeconds": 67530,
  "ISBN": "1234567890123",
  "ASIN": "B0ABCDEFGH",
  "BookID": "12345",
  "EditionID": "67890",
  "Reason": "ASIN lookup failed for ASIN B0ABCDEFGH, using fallback book matching",
  "Timestamp": "2025-06-08T17:45:30+02:00"
}
```

## Key Features

- **Duration in Seconds**: Each file includes both human-readable duration (hours) and machine-readable duration (seconds)
- **Complete Metadata**: All available book information is preserved
- **Safe Filenames**: Automatic sanitization prevents filesystem issues
- **Individual Processing**: Each mismatch can be processed independently
- **Numbered Sequence**: Files are numbered for easy sorting and reference

## Implementation Details

### Mismatch Detection
Mismatches are detected during the sync process when:
1. A book is not found in the Hardcover database
2. The found book doesn't match expected criteria (e.g., wrong format, missing metadata)
3. The sync process encounters an error processing the book

### Logging Configuration

Mismatch JSON files are generated regardless of the log format setting, but the application logs can be configured for different formats:

```bash
# JSON format (default, recommended for production)
export LOG_FORMAT=json

1. **Easier Processing**: Handle one book at a time
2. **Parallel Processing**: Process multiple files simultaneously
3. **Selective Processing**: Skip or prioritize specific books
4. **Custom Workflows**: Build tools around individual files
5. **Version Control**: Track changes to individual mismatches over time

## Backward Compatibility

- Console output remains unchanged (human-readable format)
- If `MISMATCH_JSON_FILE` is not set, no files are saved
- Existing functionality is unaffected
