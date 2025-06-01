#!/bin/sh
set -e

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

exec /main "$@"
