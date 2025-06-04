#!/bin/bash

# Incremental Sync Demo Script
# This script demonstrates the incremental sync functionality

echo "🚀 Incremental Sync Demo for AudiobookShelf-Hardcover Sync"
echo "=========================================================="
echo

# Set up demo environment
export AUDIOBOOKSHELF_URL="https://demo.audiobookshelf.org"
export AUDIOBOOKSHELF_TOKEN="demo-token"
export HARDCOVER_TOKEN="demo-token"
export DEBUG_MODE="1"
export INCREMENTAL_SYNC_MODE="enabled"
export SYNC_STATE_FILE="demo_sync_state.json"

echo "📋 Demo Configuration:"
echo "   INCREMENTAL_SYNC_MODE: $INCREMENTAL_SYNC_MODE"
echo "   SYNC_STATE_FILE: $SYNC_STATE_FILE"
echo "   DEBUG_MODE: $DEBUG_MODE"
echo

echo "🔍 Checking if sync state file exists..."
if [ -f "$SYNC_STATE_FILE" ]; then
    echo "   ✅ Found existing sync state:"
    cat "$SYNC_STATE_FILE" | jq .
    echo
    echo "   📅 This will be an INCREMENTAL sync (only recent changes)"
else
    echo "   ❌ No sync state file found"
    echo "   📅 This will be a FULL sync (first run)"
fi
echo

echo "🏃 Running sync with incremental mode..."
echo "Note: This will fail with demo credentials, but shows the incremental sync logic"
echo

# Run the sync (will fail due to demo credentials, but will show the logic)
./abs-hardcover-sync -v 2>&1 | head -20

echo
echo "📊 After sync, check the sync state file:"
if [ -f "$SYNC_STATE_FILE" ]; then
    echo "   ✅ Sync state updated:"
    cat "$SYNC_STATE_FILE" | jq .
else
    echo "   ❌ Sync state file not created (sync failed due to demo credentials)"
fi
echo

echo "🔄 To demonstrate different sync modes:"
echo "   export INCREMENTAL_SYNC_MODE=disabled   # Always full sync"
echo "   export INCREMENTAL_SYNC_MODE=auto       # Auto-detect best mode"
echo "   export FORCE_FULL_SYNC=true             # Force full sync once"
echo

echo "📁 Sync state file location:"
echo "   Default: sync_state.json"
echo "   Custom:  export SYNC_STATE_FILE=/path/to/custom/state.json"
echo

echo "✨ Demo complete! The incremental sync feature is now ready to use."

# Cleanup demo state file
if [ -f "$SYNC_STATE_FILE" ]; then
    echo "🧹 Cleaning up demo state file..."
    rm "$SYNC_STATE_FILE"
fi
