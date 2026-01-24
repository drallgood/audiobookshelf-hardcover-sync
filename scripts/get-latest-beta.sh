#!/bin/bash
# Script to find the latest beta tag for ArgoCD ImageUpdater
# Usage: ./get-latest-beta.sh

REPO="ghcr.io/drallgood/audiobookshelf-hardcover-sync"
# Get all beta tags and sort by build number, then get the latest
LATEST_BETA=$(curl -s "https://api.github.com/repos/drallgood/audiobookshelf-hardcover-sync/packages/container/audiobookshelf-hardcover-sync/versions" \
  -H "Authorization: token $GITHUB_TOKEN" \
  | jq -r '.[] | select(.metadata.container.tags[] | startswith("beta-")) | .metadata.container.tags[] | select(startswith("beta-"))' \
  | sort -V -r \
  | head -n 1)

if [ -n "$LATEST_BETA" ]; then
  echo "$LATEST_BETA"
else
  echo "latest-dev"  # fallback
fi
