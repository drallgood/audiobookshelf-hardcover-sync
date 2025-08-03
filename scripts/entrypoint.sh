#!/bin/sh
set -e

# Handle user switching if running as root
if [ "$(id -u)" = "0" ]; then
    # Running as root, ensure proper ownership and switch to app user
    # Support both /data and /app/data volume approaches
    chown -R app:app /data /app/data 2>/dev/null || true
    # Switch to app user for the main process
    exec su-exec app "$0" "$@"
fi

# If we reach here, we're running as the app user

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
