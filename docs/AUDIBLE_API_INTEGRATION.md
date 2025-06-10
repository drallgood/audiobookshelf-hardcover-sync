# Audible API Integration

This document describes the Audible API integration feature that enhances book metadata with external data sources to provide more accurate and complete information, particularly for release dates.

## Overview

The Audible API integration is designed to enhance the existing book metadata from Hardcover with additional data from Audible's catalog. This is particularly useful for:

- **Complete Release Dates**: Converting year-only dates (e.g., "2023") to full dates (e.g., "2023-12-25")
- **Duration Information**: Adding accurate audiobook duration when missing
- **Publisher Information**: Enhancing publisher details
- **Image Quality**: Upgrading to higher resolution cover images
- **Additional Metadata**: Subtitles, narrator information, and more

## Configuration

The Audible API integration is controlled by environment variables:

### Required Configuration

```bash
# Enable/disable Audible API integration
AUDIBLE_API_ENABLED=true|false  # Default: false

# API authentication token (if required by the Audible service)
AUDIBLE_API_TOKEN=your_token_here  # Default: empty

# API request timeout
AUDIBLE_API_TIMEOUT=10s  # Default: 10s
```

### Configuration Examples

```bash
# Enable with API token
export AUDIBLE_API_ENABLED=true
export AUDIBLE_API_TOKEN=your_api_token
export AUDIBLE_API_TIMEOUT=15s

# Enable without token (for public endpoints)
export AUDIBLE_API_ENABLED=true
export AUDIBLE_API_TIMEOUT=5s

# Disable completely (default)
export AUDIBLE_API_ENABLED=false
```

## How It Works

### Enhancement Process

1. **Initial Data Gathering**: The system first gathers basic metadata from Hardcover
2. **ASIN Identification**: Uses the AudiobookShelf ASIN to identify the book in Audible's catalog
3. **API Call**: Makes a request to Audible's API for detailed metadata
4. **Data Enhancement**: Intelligently merges Audible data with existing data
5. **Quality Assessment**: Determines if the enhancement was successful

### Data Enhancement Logic

The enhancement process follows these principles:

- **Preserve Existing Data**: Never overwrite existing high-quality data
- **Improve Specificity**: Replace less specific data with more specific data
- **Quality Over Quantity**: Only use external data that improves the overall quality

#### Release Date Enhancement

```go
// Example: Year -> Full Date
Existing: "2023"
Audible:  "2023-12-25"
Result:   "2023-12-25"  // More specific

// Example: Keep existing full date
Existing: "2023-11-15"
Audible:  "2023-12-25"
Result:   "2023-11-15"  // Keep existing
```

#### Image URL Enhancement

- Prefers high-resolution Audible images over low-resolution alternatives
- Considers images with "500", "1000", "large", or "._SL" as high-resolution
- Keeps existing Audible images to avoid unnecessary changes

## API Implementation

### Core Functions

#### `getAudibleMetadata(asin string) (*AudibleMetadata, error)`
Fetches metadata from Audible API for a given ASIN.

#### `enhanceWithExternalData(prepopulated *PrepopulatedEditionInput, asin string) error`
Main enhancement function that integrates Audible data with existing metadata.

#### Helper Functions
- `parseAudibleDate(dateStr string) (time.Time, error)`: Parses various date formats
- `formatAudibleDate(t time.Time) string`: Formats dates for Hardcover compatibility
- `isDateMoreSpecific(newDate, existingDate string) bool`: Compares date specificity
- `isAudibleImageBetter(audibleURL, existingURL string) bool`: Evaluates image quality

### Fallback Behavior

When the Audible API is unavailable or returns errors:

1. **Graceful Degradation**: The system continues to work without external enhancement
2. **Minimal Enhancement**: Still adds ASIN information when available
3. **Source Tracking**: Marks data source as "hardcover+external" vs "hardcover+audible"

## Data Sources and Prepopulation Sources

The system tracks data enhancement sources:

- `"hardcover"`: Data from Hardcover only
- `"hardcover+external"`: Hardcover data with minimal external enhancement
- `"hardcover+audible"`: Hardcover data successfully enhanced with Audible API data

## Error Handling

### Common Scenarios

1. **API Disabled**: No external calls made, uses existing data only
2. **Network Errors**: Falls back gracefully, doesn't fail the entire process
3. **Invalid ASIN**: Validates ASIN format before making API calls
4. **API Rate Limits**: Respects timeout settings and fails gracefully
5. **Missing Token**: Attempts API call without authentication, falls back if needed

### Validation

- **ASIN Format**: Must be 10 uppercase alphanumeric characters
- **Date Parsing**: Supports multiple date formats with robust error handling
- **Data Quality**: Checks for meaningful data before enhancement

## Testing

### Test Coverage

The Audible API integration includes comprehensive tests:

- **Unit Tests**: Individual function testing (`audible_test.go`)
- **Integration Tests**: End-to-end workflow testing (`audible_integration_test.go`)
- **Configuration Tests**: Environment variable handling
- **Error Handling Tests**: Fallback and error scenarios

### Running Tests

```bash
# Run all Audible-related tests
go test -v -run "TestParseAudibleDate|TestFormatAudibleDate|TestIsDateMoreSpecific|TestEnhanceWithExternalData"

# Run integration tests specifically
go test -v -run "TestEnhanceWithExternalDataIntegration"

# Run ASIN validation tests
go test -v -run "TestIsValidASIN"
```

## Performance Considerations

### Optimization Features

- **Timeout Management**: Configurable request timeouts prevent hanging
- **Graceful Fallback**: Failures don't block the main synchronization process
- **Minimal API Calls**: Only makes requests when ASIN is available and API is enabled
- **Data Quality Checks**: Avoids unnecessary API calls for books that already have complete data

### Best Practices

1. **Set Reasonable Timeouts**: Balance between data quality and performance
2. **Monitor API Usage**: Track API calls if using a rate-limited service
3. **Test Fallback Scenarios**: Ensure the system works even when APIs are unavailable
4. **Quality Over Speed**: Prefer accurate data over fast processing

## Future Enhancements

### Potential Improvements

1. **Web Scraping Fallback**: When API is unavailable, fall back to web scraping
2. **Caching**: Cache Audible metadata to reduce API calls
3. **Multiple Data Sources**: Support additional external metadata sources
4. **Batch Processing**: Process multiple ASINs in a single API call
5. **Data Validation**: Cross-validate data between multiple sources

### Integration Points

The Audible API integration is designed to work seamlessly with:

- **Edition Creator**: Automatic enhancement during edition creation
- **Mismatch Collection**: Enhanced metadata for better mismatch detection
- **Sync Process**: Real-time enhancement during synchronization
- **CLI Tools**: Manual enhancement capabilities

## Troubleshooting

### Common Issues

**API calls are slow or timing out:**
- Reduce `AUDIBLE_API_TIMEOUT` value
- Check network connectivity
- Verify API endpoint availability

**No enhancements are happening:**
- Ensure `AUDIBLE_API_ENABLED=true`
- Check that ASINs are available in your audiobook metadata
- Verify API token if required

**Tests are failing:**
- Run tests with `-v` flag for detailed output
- Check environment variable configuration
- Ensure all dependencies are properly installed

**Data quality issues:**
- Review the enhancement logic in `enhanceWithExternalData()`
- Check date parsing with various input formats
- Verify ASIN validation is working correctly

### Debug Mode

Enable debug logging to see detailed enhancement information:

```bash
export DEBUG_MODE=true
```

This will show:
- API call attempts and results
- Data enhancement decisions
- Fallback scenarios
- Source attribution changes

## Security Considerations

- **API Tokens**: Store API tokens securely, never commit them to version control
- **Rate Limiting**: Respect API rate limits to avoid being blocked
- **Error Handling**: Don't expose sensitive information in error messages
- **Timeout Settings**: Prevent resource exhaustion with reasonable timeouts
