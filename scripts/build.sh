#!/bin/bash
# Build script that automatically determines version from Git

set -e

# Determine version from Git
if git describe --tags --exact-match HEAD 2>/dev/null; then
    # We're on a tag
    VERSION=$(git describe --tags --exact-match HEAD)
elif git rev-parse --verify HEAD 2>/dev/null; then
    # We have commits, use branch + short commit
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    COMMIT=$(git rev-parse --short HEAD)
    if [[ "$BRANCH" == "main" ]]; then
        VERSION="dev-${COMMIT}"
    else
        VERSION="dev-${BRANCH}-${COMMIT}"
    fi
else
    # No Git info available
    VERSION="dev-unknown"
fi

echo "Building with version: $VERSION"

# Build with version injection
go build -ldflags="-X main.version=${VERSION}" -o main .

echo "Build complete. Version:"
./main --version
