# Enhanced Progress Detection

## Overview
The Enhanced Progress Detection system uses AudiobookShelf's `/api/me` endpoint to provide accurate finished book detection, solving issues with manually finished books that show 0% progress.

## Problem Solved
Previously, books that were manually marked as "Finished" in AudiobookShelf but showed 0% progress due to API detection issues were incorrectly treated as "re-read" scenarios. This resulted in:
- False positive re-read detections
- Duplicate read entries in Hardcover
- Incorrect progress synchronization

## Technical Implementation

### API Endpoint
- **Endpoint**: `/api/me` (replaces previous `/api/authorize` usage)
- **Response**: Comprehensive user data including `mediaProgress` array with `isFinished` flags
- **Authentication**: Uses existing AudiobookShelf token

### Key Components

#### 1. Enhanced Progress Detection (`enhanced_progress_detection.go`)
- `fetchAudiobookShelfStatsEnhanced()`: Main function to get enhanced progress data
- `fetchAuthorizeData()`: Fetches user data from `/api/me` endpoint
- `getMediaProgressByLibraryItemID()`: Looks up progress by library item ID

#### 2. Updated Type Definitions (`types.go`)
- `AuthorizeResponse`: Fixed to match actual API response structure
- `MediaProgress`: Comprehensive progress data including `IsFinished`, `Progress`, `CurrentTime`
- Removed nested `user` object that was causing parsing errors

#### 3. Smart RE-READ Detection (`sync.go`)
- Modified condition: `hasExistingFinishedRead && a.Progress < 0.99 && !isBookFinished`
- Prevents treating manually finished books as re-read scenarios
- Conservative skip logic for edge cases

## Usage

### Automatic Detection
The enhanced progress detection is automatically used when:
- AudiobookShelf URL is properly configured
- Valid AudiobookShelf token is provided
- `/api/me` endpoint is accessible

### Debugging Enhanced Progress Detection

### Environment Variables

```bash
# Enable debug logging (default: false)
DEBUG=true

# Set log format (default: json, options: json, text)
LOG_FORMAT=text
```

### Example Debug Output

#### JSON Format (LOG_FORMAT=json)
```json
{
  "level": "debug",
  "time": "2025-06-28T19:40:00+02:00",
  "message": "Enhanced progress detection",
  "book_id": "book123",
  "title": "Example Book",
  "is_finished": true,
  "progress": 1.0,
  "source": "audiobookshelf",
  "enhanced_detection": true
}
```

#### Text Format (LOG_FORMAT=text)
```
[DEBUG] 2025-06-28T19:40:00+02:00 Enhanced progress detection
  book_id=book123
  title="Example Book"
  is_finished=true
  progress=1.0
  source=audiobookshelf
  enhanced_detection=true
```

Debug output includes:
- Found authorize data with `isFinished` status
- Progress detection statistics
- Book matching with enhanced detection

## Configuration

### Required Environment Variables
- `AUDIOBOOKSHELF_URL`: Must include full path (e.g., `https://abs.example.com/audiobookshelf` for reverse proxy setups)
- `AUDIOBOOKSHELF_TOKEN`: Valid API token with access to `/api/me` endpoint

### Optional Settings
- `DEBUG_MODE`: Enable to see detailed progress detection logs
- `MINIMUM_PROGRESS_THRESHOLD`: Still applies to non-finished books

## Benefits

1. **Accurate Finished Detection**: Correctly identifies manually finished books
2. **No False Positives**: Eliminates incorrect re-read scenarios
3. **Robust API Integration**: Uses comprehensive user data from `/api/me`
4. **Backward Compatible**: Works with existing configurations
5. **Conservative Logic**: Skips problematic cases to prevent errors

## Troubleshooting

### Troubleshooting Common Issues

#### 1. No Enhanced Progress Data
```
[DEBUG] No authorize progress data found for 'Book Title'
```

**Solutions:**
1. **Check URL Configuration**:
   - Verify `AUDIOBOOKSHELF_URL` includes the correct path prefix
   - Example: `https://your-server.com/audiobookshelf`

2. **Verify Token Permissions**:
   - Ensure the token has access to the `/api/me` endpoint
   - Regenerate the token if necessary

3. **Check API Accessibility**:
   - Test the endpoint manually: `curl -H "Authorization: Bearer YOUR_TOKEN" $AUDIOBOOKSHELF_URL/api/me`
   - Verify network connectivity and CORS settings

4. **Enable Debug Logging**:
   ```bash
   export DEBUG=true
   export LOG_FORMAT=text  # or json for structured output
   ```
   - Look for detailed error messages in the logs

#### 2. Type Parsing Errors
```
Error parsing authorize response
```
**Solutions:**
- Update to latest version with fixed type definitions
- Check AudiobookShelf API version compatibility

#### 3. False RE-READ Detection (Legacy)
If you're still seeing false re-read detections:
- Ensure you're using the latest version with the fix
- Enable debug mode to verify enhanced detection is working
- Check that `isBookFinished` is being properly detected

## Migration Notes

### From Previous Versions
- **Automatic Upgrade**: Enhanced detection is automatically enabled
- **Configuration**: No changes required, but new options available
- **Backward Compatibility**: Existing sync state remains compatible
- **Data Accuracy**: Progress detection is more accurate

### New Configuration Options

| Variable | Description | Default |
|----------|-------------|---------|
| `LOG_FORMAT` | Output format: `json` or `text` | `json` |
| `DEBUG` | Enable debug logging | `false` |
| `AUDIOBOOKSHELF_URL` | Base URL for AudiobookShelf | Required |
| `AUDIOBOOKSHELF_TOKEN` | API token with proper permissions | Required |

### Example Configuration

```bash
# Required
AUDIOBOOKSHELF_URL=https://your-audiobookshelf-server.com
AUDIOBOOKSHELF_TOKEN=your_api_token_here

# Optional - enable debug logging
DEBUG=true

# Optional - set log format (json or text)
LOG_FORMAT=json
```

### API Changes
- Now uses `/api/me` instead of `/api/authorize` for some operations
- Type definitions updated to match actual API responses
- Enhanced error handling for API parsing

## Implementation Example

```go
// Get enhanced progress data
stats, err := fetchAudiobookShelfStatsEnhanced()
if err != nil {
    return fmt.Errorf("failed to get enhanced stats: %w", err)
}

// Check if book is finished using enhanced detection
isFinished := stats.isBookFinished(libraryItemID)

// Use in RE-READ detection logic
if hasExistingFinishedRead && progress < 0.99 && !isFinished {
    // This is likely a re-read scenario
    createNewReadEntry()
} else {
    // Book is properly finished, update existing record
    updateExistingRecord()
}
```

## Related Documentation
- [CHANGELOG.md](../CHANGELOG.md) - Version history and changes
- [README.md](../README.md) - Main project documentation
- [CONTRIBUTING.md](../CONTRIBUTING.md) - Development guidelines
