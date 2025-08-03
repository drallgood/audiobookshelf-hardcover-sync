#!/bin/sh
set -e

# Ensure necessary directories exist with proper permissions
# Note: These should already exist from Dockerfile, but ensure they're accessible
if [ "$(id -u)" = "0" ]; then
    # Running as root, ensure directories exist and have proper ownership
    mkdir -p /app/data /app/cache /app/mismatches
    chown -R app:app /app/data /app/cache /app/mismatches
    # Switch to app user for the main process
    exec su-exec app "$0" "$@"
else
    # Already running as app user, just ensure directories exist
    mkdir -p /app/data /app/cache /app/mismatches
fi

# Default CA bundle location
CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
CUSTOM_CA=/ca.crt
COMBINED_CA=/tmp/ca-certificates-combined.crt

if [ -f "$CUSTOM_CA" ]; then
  cat "$CA_BUNDLE" "$CUSTOM_CA" > "$COMBINED_CA"
  export SSL_CERT_FILE="$COMBINED_CA"
else
  export SSL_CERT_FILE="$CA_BUNDLE"
fi

# Execute the binary with any provided arguments
exec /app/audiobookshelf-hardcover-sync "$@"
