# Enhanced Mismatch Collection Feature

## Overview

The Enhanced Mismatch Collection system provides comprehensive metadata tracking and reporting for books that encounter matching issues during the sync process. This upgrade significantly improves the manual review process by providing detailed book information.

## What's New in v1.2.0

### Enhanced Metadata Collection

The system now collects and displays the following additional metadata when book mismatches occur:

- **Subtitle**: Complete book subtitle for better identification
- **Narrator**: Audiobook narrator information
- **Publisher**: Publishing house details
- **Published Year**: Original publication year
- **Release Date**: Specific release date (preferred over year when available)
- **Duration**: Audio duration in human-readable hours format (e.g., "18.1 hours")

### Improved Display Format

Mismatch summaries now show comprehensive book information:

```
⚠️  MANUAL REVIEW NEEDED: Found 1 book(s) that may need verification
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
1. Title: The Fellowship of the Ring
   Subtitle: The Lord of the Rings, Book 1
   Author: J.R.R. Tolkien
   Narrator: Rob Inglis
   Publisher: Recorded Books
   Release Date: 1954-07-29
   Duration: 18.1 hours
   ISBN: 9780544003415
   ASIN: B007978NPG
   Hardcover Book ID: book123
   Hardcover Edition ID: edition456
   Issue: ASIN lookup failed for ASIN B007978NPG - no audiobook edition found
   Time: 2025-06-03 15:30:22
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

## Technical Implementation

### Enhanced Data Flow

1. **Metadata Preservation**: The `Audiobook` struct includes a `Metadata` field containing the complete `MediaMetadata`
2. **Data Flow**: Full metadata flows from `fetchAudiobookShelfStats()` → `syncToHardcover()` → `addBookMismatchWithMetadata()`
3. **Processing**: Duration conversion (seconds → hours) and release date handling (prefer date over year)
4. **Logging**: Mismatch events are logged in the configured format (JSON or text)

### Logging Configuration

Mismatch collection supports configurable logging formats:

```bash
# JSON format (default, recommended for production)
export LOG_FORMAT=json

text format (more human-readable)
export LOG_FORMAT=text
```

#### Example JSON Output
```json
{
  "level": "warn",
  "time": "2025-06-28T19:35:00+02:00",
  "message": "Book mismatch detected",
  "book": {
    "title": "The Fellowship of the Ring",
    "subtitle": "The Lord of the Rings, Book 1",
    "author": "J.R.R. Tolkien",
    "narrator": "Rob Inglis",
    "isbn": "9780544003415",
    "asin": "B007978NPG",
    "book_id": "book123",
    "edition_id": "edition456",
    "issue": "ASIN lookup failed for ASIN B007978NPG - no audiobook edition found"
  }
}
```

### New Functions

- `addBookMismatchWithMetadata()`: Enhanced mismatch collection with full metadata
- Enhanced `printMismatchSummary()`: Displays all new metadata fields in organized format

### Backward Compatibility

The original `addBookMismatch()` function is preserved to maintain compatibility with any existing integrations or code that might depend on it.

## Benefits for Users

### Better Manual Review Process

- **Complete Identification**: Users can now see subtitle, narrator, and publisher information to better identify books
- **Duration Information**: Easily verify if duration matches expectations (helps identify wrong editions)
- **Release Date Clarity**: Specific dates help distinguish between different editions
- **Professional Display**: Clean, organized format makes review process more efficient

### Improved Troubleshooting

- **Edition Verification**: Rich metadata helps users verify if the correct audiobook edition was matched
- **Publisher Matching**: Publisher information aids in identifying correct editions
- **Narrator Confirmation**: Narrator details help confirm audiobook-specific editions

## Use Cases

This enhanced system is particularly valuable for:

1. **Large Libraries**: Users with extensive audiobook collections who need to review multiple mismatches
2. **Multiple Editions**: Books available in different formats (audiobook vs ebook vs physical)
3. **Series Identification**: Complex series where subtitle and volume information is crucial
4. **Quality Assurance**: Users who want to ensure accurate metadata before syncing

## Migration Notes

- **No Breaking Changes**: Existing configurations and workflows continue to work unchanged
- **Automatic Enhancement**: New metadata collection happens automatically for all new sync operations
- **Gradual Adoption**: The enhanced display only shows additional fields when available
- **Log Format**: Mismatch logging respects the global `LOG_FORMAT` setting (default: JSON)

## Future Enhancements

The enhanced metadata foundation enables future improvements such as:

- **Export Capabilities**: Generate mismatch reports in various formats (CSV, JSON, HTML)
- **Automated Suggestions**: Implement smart matching using additional metadata
- **ASIN Reference Tracking**: Track and resolve ASIN mismatches over time
- **Advanced Filtering**: Filter mismatches by type, date, or metadata fields
- **Web Interface**: Visual dashboard for reviewing and resolving mismatches
- **Integration**: Webhook support for real-time notifications
- **Metrics**: Track mismatch patterns and resolution rates

This feature represents a significant improvement in the user experience for managing and resolving sync issues in the audiobookshelf-hardcover-sync tool.
