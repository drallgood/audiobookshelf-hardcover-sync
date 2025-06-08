# Individual JSON Mismatch Files Feature

## Overview
The system now saves each book mismatch as an individual JSON file when the `MISMATCH_JSON_FILE` environment variable is set to a directory path. This is particularly useful when dealing with many mismatches (e.g., 200+) for easier individual processing.

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

## Usage Example

```bash
# Set output directory
export MISMATCH_JSON_FILE="./book_mismatches"

# Run sync (mismatches will be saved to individual files)
./audiobookshelf-hardcover-sync

# Process individual files
for file in ./book_mismatches/*.json; do
    echo "Processing: $file"
    # Your custom processing logic here
    jq '.Title, .DurationSeconds, .Reason' "$file"
done
```

## Benefits for Large Mismatch Sets

1. **Easier Processing**: Handle one book at a time
2. **Parallel Processing**: Process multiple files simultaneously
3. **Selective Processing**: Skip or prioritize specific books
4. **Custom Workflows**: Build tools around individual files
5. **Version Control**: Track changes to individual mismatches over time

## Backward Compatibility

- Console output remains unchanged (human-readable format)
- If `MISMATCH_JSON_FILE` is not set, no files are saved
- Existing functionality is unaffected
