# Advanced Features and Configuration

This document covers advanced configuration options, features, and troubleshooting for the Audiobookshelf-Hardcover sync tool.

## Table of Contents
- [Incremental Sync](#incremental-sync)
- [Mismatch Handling](#mismatch-handling)
- [Rate Limiting and Performance](#rate-limiting-and-performance)
- [Debugging and Logging](#debugging-and-logging)
- [Advanced Configuration](#advanced-configuration)
- [Troubleshooting](#troubleshooting)

## Incremental Sync

The sync tool uses an incremental sync mechanism to improve performance:

- **How it works**: Only syncs books that have changed since the last sync
- **State file**: Tracks sync state in `sync_state.json` by default
- **Force full sync**: Set `FORCE_FULL_SYNC=true` to perform a complete resync
- **Custom state file**: Change the state file location with `SYNC_STATE_FILE`

## Mismatch Handling

When books can't be automatically matched, the tool provides detailed mismatch reports:

### Mismatch Reports
- **Location**: Specified by `MISMATCH_JSON_FILE` (default: `mismatches/` directory)
- **Format**: JSON files containing book metadata and matching details
- **Using reports**:
  ```bash
  # View all mismatches
  find mismatches/ -type f -name "*.json" | xargs cat | jq .
  
  # Search for a specific book
  grep -r "Book Title" mismatches/
  ```

### Handling Mismatches
1. **Check metadata**: Ensure books have complete metadata (ASIN, ISBN, title, author)
2. **Manual lookup**: Use the `/tools/search` endpoint to find the correct book
3. **Create editions**: For audiobooks without matches, use the edition creation tools

## Rate Limiting and Performance

### Rate Limiting
- **Default delay**: 1500ms between requests (`HARDCOVER_SYNC_DELAY_MS`)
- **Automatic retries**: 3 retries with exponential backoff
- **Adjusting delays**: Increase delay if you hit rate limits

### Performance Tuning
- **Batch size**: Process books in parallel with `--batch-size`
- **Memory usage**: Monitor with `docker stats` or `top`
- **Log levels**: Reduce verbosity with `LOG_LEVEL=warn` in production

## Debugging and Logging

### Log Levels
- `debug`: Verbose logging for development
- `info`: Regular operational messages
- `warn`: Non-critical issues
- `error`: Critical errors

### Enabling Debug Mode
```bash
# Environment variable
DEBUG_MODE=true

# Command line flag
./abs-hardcover-sync --debug
```

### Common Log Patterns
```
# Successful sync
synced book title=... status=... progress=...

# Warning
warning: could not find match for book=...

# Error
error: failed to sync book=... error=...
```

## Advanced Configuration

### Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| `SYNC_INTERVAL` | `1h` | Time between syncs (e.g., `10m`, `1h`) |
| `MINIMUM_PROGRESS_THRESHOLD` | `0.01` | Minimum progress to trigger sync (0.0-1.0) |
| `AUDIOBOOK_MATCH_MODE` | `continue` | Behavior on match failure: `continue`, `skip`, `fail` |
| `HTTP_TIMEOUT` | `30s` | HTTP request timeout |
| `MAX_RETRIES` | `3` | Maximum retry attempts for failed requests |

### Customizing HTTP Client
```yaml
http:
  timeout: 30s
  max_retries: 3
  retry_wait: 1s
  max_retry_wait: 30s
```

## Troubleshooting

### Common Issues

#### Books Not Syncing
1. Check API tokens are valid
2. Verify network connectivity to both services
3. Check logs for errors
4. Try a manual sync with debug mode

#### High CPU/Memory Usage
1. Reduce batch size
2. Increase delays between requests
3. Check for memory leaks with `pprof`

#### Authentication Failures
1. Verify tokens are correct
2. Check token expiration
3. Ensure proper URL encoding for special characters

### Getting Help
1. Check the [GitHub Issues](https://github.com/drallgood/audiobookshelf-hardcover-sync/issues)
2. Enable debug logging and include logs when reporting issues
3. Provide steps to reproduce the problem

## Monitoring and Metrics

The service exposes Prometheus metrics at `/metrics`:

### Key Metrics
- `sync_books_total`: Total books processed
- `sync_duration_seconds`: Time taken for sync
- `http_requests_total`: API request count
- `http_request_duration_seconds`: API request duration

### Example PromQL Queries
```promql
# Error rate
rate(http_requests_total{status=~"5.."}[5m])

# Sync duration histogram
histogram_quantile(0.95, sum(rate(sync_duration_seconds_bucket[5m])) by (le))
```

## Security Considerations

- **Token Security**: Never commit API tokens to version control
- **Network Security**: Use HTTPS for all API endpoints
- **File Permissions**: Secure the sync state file as it contains timestamps
- **Logging**: Be cautious with debug logs in production as they may contain sensitive data

---

For additional help, please [open an issue](https://github.com/drallgood/audiobookshelf-hardcover-sync/issues/new) with detailed information about your problem.
