# Web UI Configuration Guide

This guide explains how to configure the optional web UI for audiobookshelf-hardcover-sync, allowing you to choose between headless mode and multi-user web UI mode.

## Overview

The application supports two distinct operating modes. The existing headless configuration remains supported and is still the default:

1. **Headless Mode** - `enable_web_ui: false` (default)
2. **Web UI Mode** (multi-user) - `enable_web_ui: true`

## Configuration Methods

### Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `ENABLE_WEB_UI` | Enable/disable web UI | `false` | No |
| `AUDIOBOOKSHELF_URL` | Audiobookshelf server URL | - | Yes |
| `AUDIOBOOKSHELF_TOKEN` | Audiobookshelf API token | - | Only for headless mode |
| `HARDCOVER_TOKEN` | Hardcover API token | - | Only for headless mode |

### Configuration File

Add the following to your `config.yaml`:

```yaml
server:
  port: 8080                    # HTTP server port
  enable_web_ui: true          # Enable web UI (default: false)
  shutdown_timeout: 30s        # Graceful shutdown timeout

# Headless mode configuration (when enable_web_ui: false)
audiobookshelf:
  url: "https://audiobookshelf.example.com"
  token: "your-audiobookshelf-token"

hardcover:
  token: "your-hardcover-token"
```

## Mode Comparison

### Headless Mode (`enable_web_ui: false`)

**Characteristics:**
- Preserves the existing configuration and startup behavior
- No web interface
- Requires tokens at startup
- Uses environment variables or config file for configuration
- Runs as a background service

**Use Cases:**
- Simple deployments
- Docker containers with environment variables
- Automated scripts
- Background service deployments

**Required Configuration:**
```bash
AUDIOBOOKSHELF_URL=https://audiobookshelf.example.com
AUDIOBOOKSHELF_TOKEN=your-audiobookshelf-token
HARDCOVER_TOKEN=your-hardcover-token
```

### Web UI Mode (`enable_web_ui: true`)

**Characteristics:**
- Modern web interface at `http://localhost:8080`
- Multi-user support with individual token management
- REST API endpoints
- Real-time monitoring
- No token requirements at startup (configured via web UI)
- Encrypted token storage

**Use Cases:**
- Multi-user environments
- Manual token management
- Real-time monitoring needs
- REST API integration
- Modern deployments

**Required Configuration:**
```bash
AUDIOBOOKSHELF_URL=https://audiobookshelf.example.com
# No tokens required - configured via web UI
```

## Migration Guide

### Upgrading from Headless to Web UI Mode

1. **Update Configuration:**
   ```yaml
   server:
     enable_web_ui: true
   ```

2. **Restart Application:**
   ```bash
   ./audiobookshelf-hardcover-sync --server-only
   ```

3. **Access Web Interface:**
   - Open `http://localhost:8080` in your browser
   - Your existing configuration is automatically migrated
   - Create users with their individual tokens

### Downgrading from Web UI to Headless Mode

1. **Update Configuration:**
   ```yaml
   server:
     enable_web_ui: false
   ```

2. **Add Required Tokens:**
   ```yaml
   audiobookshelf:
     url: "https://audiobookshelf.example.com"
     token: "your-audiobookshelf-token"

   hardcover:
     token: "your-hardcover-token"
   ```

3. **Restart Application**

## Examples

### Docker with Web UI

```yaml
version: '3.8'
services:
  audiobookshelf-sync:
    image: audiobookshelf-hardcover-sync:latest
    environment:
      - ENABLE_WEB_UI=true
      - AUDIOBOOKSHELF_URL=https://audiobookshelf.example.com
    ports:
      - "8080:8080"
    volumes:
      - ./data:/app/data
```

### Docker with Headless Mode

```yaml
version: '3.8'
services:
  audiobookshelf-sync:
    image: audiobookshelf-hardcover-sync:latest
    environment:
      - AUDIOBOOKSHELF_URL=https://audiobookshelf.example.com
      - AUDIOBOOKSHELF_TOKEN=your-audiobookshelf-token
      - HARDCOVER_TOKEN=your-hardcover-token
    volumes:
      - ./data:/app/data
```

### Command Line Examples

**Web UI Mode:**
```bash
# Using environment variable
ENABLE_WEB_UI=true ./audiobookshelf-hardcover-sync --audiobookshelf-url https://audiobookshelf.example.com --server-only

# Using config file
./audiobookshelf-hardcover-sync --config config.yaml --server-only
```

**Headless Mode:**
```bash
# Using environment variables
./audiobookshelf-hardcover-sync \
  --audiobookshelf-url https://audiobookshelf.example.com \
  --audiobookshelf-token your-token \
  --hardcover-token your-token

# Using config file
./audiobookshelf-hardcover-sync --config config.yaml
```

## Troubleshooting

### Common Issues

1. **"Required configuration values are missing"**
   - Headless mode: Ensure both tokens are provided
   - Web UI mode: Only requires `AUDIOBOOKSHELF_URL`

2. **"Web UI disabled" message**
   - Check that `ENABLE_WEB_UI=true` or `enable_web_ui: true` is set

3. **Port conflicts**
   - Change port with `PORT=8081` or `server.port: 8081`

### Configuration Validation

The application validates configuration based on the selected mode:

- **Headless mode:** Requires `AUDIOBOOKSHELF_URL`, `AUDIOBOOKSHELF_TOKEN`, and `HARDCOVER_TOKEN`
- **Web UI mode:** Requires only `AUDIOBOOKSHELF_URL` (tokens configured via web UI)

### Debug Mode

Enable debug logging to see detailed configuration information:

```bash
LOG_LEVEL=debug ./audiobookshelf-hardcover-sync --config config.yaml
```

## Security Considerations

- **Web UI Mode:** Tokens are encrypted at rest using AES-256-GCM
- **Headless Mode:** Tokens are stored in environment variables or config files
- **HTTPS:** Use HTTPS in production (configure reverse proxy)
- **Authentication:** Consider enabling authentication for web UI mode
