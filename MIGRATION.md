# Migration Guide for Audiobookshelf-Hardcover-Sync

This guide will help you migrate from previous versions to the latest version, which represents a complete rewrite of the application with many architectural improvements and breaking changes.

## Major Changes: Environment Variables to Configuration Files

The most significant change is the shift from a purely environment variable-based configuration to a YAML configuration file system. Previous versions of the application relied entirely on environment variables, while this version introduces a comprehensive configuration file system.

### New Configuration File System

A new `config.yaml` file is now used for configuration, with an example provided in `config.example.yaml`. This file contains sections for:

- Server settings
- Logging configuration
- Audiobookshelf connection details
- Hardcover connection details
- Rate limiting parameters
- Sync options and preferences
- Path configurations

### Migration from Environment Variables

If you previously used environment variables for configuration, you will need to migrate to the new configuration file system. Below is a mapping of key environment variables to their configuration file equivalents:

| Previous Environment Variable | New Configuration | Default |
|-------------------------------|-------------------|---------||
| `AUDIOBOOKSHELF_URL` | `audiobookshelf.url` | N/A |
| `AUDIOBOOKSHELF_TOKEN` | `audiobookshelf.token` | N/A |
| `HARDCOVER_TOKEN` | `hardcover.token` | N/A |
| `HARDCOVER_SYNC_DELAY_MS` | Removed (see Rate Limiting) | N/A |
| `LOG_LEVEL` | `logging.level` | "info" |
| `LOG_FORMAT` | `logging.format` | "json" |
| `SYNC_INTERVAL` | `app.sync_interval` | "1h" |
| `MINIMUM_PROGRESS` | `app.minimum_progress` | 0.01 |
| `PROGRESS_CHANGE_THRESHOLD` | `sync.min_change_threshold` | 60 (seconds) |
| `SYNC_WANT_TO_READ` | `app.sync_want_to_read` | true |

## Breaking Changes

### Removed Environment Variables

- **`HARDCOVER_SYNC_DELAY_MS`**: Replaced with token bucket rate limiting
  - **Migration**: Use the rate limiting configuration in `config.yaml` instead
- **`AUDIOBOOK_MATCH_MODE`**: This legacy option has been removed
  - **Migration**: No action needed, improved matching is now the default

### New Environment Variables (Optional)

- **`HARDCOVER_RATE_LIMIT`**: Controls requests per second to Hardcover API (default: 10)
- **`CONFIG_FILE`**: Path to the configuration file (default: "./config.yaml")

### New Configuration Options

- **`sync.incremental`**: Controls incremental sync (default: true)
- **`sync.state_file`**: Path to store sync state (default: "./data/sync_state.json")
- **`sync.min_change_threshold`**: Minimum progress change threshold in seconds (default: 60)
- **`app.sync_owned`**: Enable ownership syncing between platforms (default: true)
- **`paths.cache_dir`**: Directory for cache files (default: "./cache")
- **`app.mismatch_output_dir`**: Directory to store mismatch files (default: "./mismatches")
- **`rate_limit`**: Configuration section for rate limiting

### Data Storage Changes
- **State File**: The application now maintains state between runs in a JSON file
  - **Migration**: Ensure the directory for state files exists (default: "./data")
  - **Configuration**: Set via `sync.state_file` in config.yaml
- **Mismatch Files**: Mismatches are now stored as individual files in a dedicated directory
  - **Migration**: Ensure the mismatch directory exists (default: "./mismatches")
  - **Configuration**: Set via `app.mismatch_output_dir` in config.yaml

## Docker Changes

If you're using Docker, there are several important changes:

- **Config File**: You now need to provide a configuration file instead of just environment variables
- **Volume Mounting**: You should now mount these volumes:
  - `/app/config` - for configuration files (e.g., config.yaml)
  - `/app/data` - for state and other persistent data
  - `/app/mismatches` - for mismatch output files (if needed)

Example docker-compose.yml:

```yaml
version: '3'
services:
  audiobookshelf-hardcover-sync:
    image: drallgood/audiobookshelf-hardcover-sync:latest
    volumes:
      - ./config:/app/config
      - ./data:/app/data
      - ./mismatches:/app/mismatches
    environment:
      # You can still override some config values with environment variables
      # but a config.yaml file is now required in the config directory
      - LOG_LEVEL=info
    restart: unless-stopped
```

Example minimal config.yaml to place in your ./config directory:

```yaml
# Audiobookshelf configuration
audiobookshelf:
  url: "https://your-audiobookshelf-instance.com"
  token: "your-audiobookshelf-token"

# Hardcover configuration
hardcover:
  token: "your-hardcover-token"

# Sync configuration
sync:
  incremental: true
  state_file: "/app/data/sync_state.json"
```

## API Changes

### GraphQL Client
- The application now uses GraphQL exclusively for Hardcover API interactions
  - **Migration**: No action needed, but any custom scripts interacting with the application may need updates

### Progress Tracking
- Progress is now tracked using `progress_seconds` instead of percentage-based progress
  - **Migration**: No action needed, conversion happens automatically

## Upgrade Steps

1. **Create a Configuration File**
   ```bash
   # Create config directory if not exists
   mkdir -p config
   
   # Copy the example configuration
   cp config.example.yaml config/config.yaml
   
   # Edit the configuration file
   nano config/config.yaml
   ```

2. **Transfer Environment Variables to Configuration**
   - Move values from your environment variables to the appropriate sections in config.yaml
   - See the mapping table above for guidance on where each setting belongs
   - Example:
     ```yaml
     audiobookshelf:
       url: "https://your-audiobookshelf-instance.com"  # was AUDIOBOOKSHELF_URL
       token: "your-token-here"  # was AUDIOBOOKSHELF_TOKEN
     ```

3. **Prepare Directories**
   ```bash
   # Create directories for state and mismatches
   mkdir -p data mismatches
   ```

4. **Remove Deprecated Environment Variables**
   - Remove any deprecated variables from your startup scripts or environment
   - Specifically, remove `HARDCOVER_SYNC_DELAY_MS` and `AUDIOBOOK_MATCH_MODE`

5. **Run Initial Sync**
   - The first sync after upgrading will be a full sync
   - Subsequent syncs will use the new incremental sync system
   - Command: `./audiobookshelf-hardcover-sync`

6. **Check Logs**
   - Review logs to ensure everything is working as expected
   - Adjust log level in config.yaml if needed (`logging.level: "debug"`)

## Troubleshooting Common Issues

### Rate Limiting Errors
- If you see rate limiting errors, adjust the rate limit settings in your config.yaml:
  ```yaml
  rate_limit:
    rate: "3000ms"  # Increase this value to slow down requests
    burst: 1        # Reduce burst capacity
  ```

### Progress Not Syncing
- Ensure your Hardcover instance is properly configured
- Check that progress tracking is correctly configured in your config.yaml
- Make sure `app.minimum_progress` is set appropriately (default: 0.01)

### Missing Mismatches
- Verify that the mismatch output directory is correctly set in config.yaml:
  ```yaml
  app:
    mismatch_output_dir: "./mismatches"
  ```
- Check that the directory exists and has proper write permissions

### State File Issues
- If you encounter problems with incremental sync, try removing the state file to force a full sync:
  ```bash
  rm data/sync_state.json
  ```
- Ensure the path in config.yaml is correct:
  ```yaml
  sync:
    state_file: "./data/sync_state.json"
  ```

### Configuration File Not Found
- Make sure config.yaml is in the expected location (default: ./config.yaml)
- You can specify a different location with the CONFIG_FILE environment variable

## Getting Help

If you encounter issues not covered by this guide:
- Check the [project README](README.md) for updated documentation
- Open an issue on the GitHub repository with details about your problem
- Include logs and configuration (with sensitive information redacted)
