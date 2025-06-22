# Scripts Directory

This directory contains utility scripts for building, testing, and running the Audiobookshelf-Hardcover Sync application.

## Available Scripts

### `build.sh`
A build script that automatically determines the version from Git and builds the application with the appropriate version information.

**Usage:**
```bash
./scripts/build.sh
```

### `demo_incremental_sync.sh`
A demonstration script that shows how to use the incremental sync functionality with the application.

**Usage:**
```bash
./scripts/demo_incremental_sync.sh
```

### `entrypoint.sh`
The entrypoint script used in the Docker container. It handles custom CA certificates and starts the application.

**Usage:**
```bash
./scripts/entrypoint.sh [command]
```

## Migration Scripts

The `migration` directory contains scripts for data migration tasks. See the [migration README](./migration/README.md) for more information.

## Notes

- All scripts should be made executable with `chmod +x scriptname.sh` before use.
- Some scripts may require additional environment variables to be set.
- The `demo_incremental_sync.sh` script is for demonstration purposes and uses demo credentials that won't work for actual synchronization.
