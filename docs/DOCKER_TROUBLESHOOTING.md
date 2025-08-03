# Docker Troubleshooting

This document covers common Docker deployment issues and their solutions.

## SQLite CGO Error

### Problem
```
Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work. This is a stub
Failed to connect to configured database, falling back to SQLite
Failed to initialize database: failed to connect to fallback SQLite database
```

### Root Cause
The application was compiled with `CGO_ENABLED=0`, which disables CGO (C bindings). However, the SQLite driver (`go-sqlite3`) requires CGO to work because it uses C libraries under the hood.

### Solution
The Dockerfile has been updated to:

1. **Enable CGO**: Set `CGO_ENABLED=1` in the build environment
2. **Add Build Dependencies**: Install `gcc`, `musl-dev`, and `sqlite-dev` for compilation
3. **Add Runtime Dependencies**: Install `sqlite` in the final image

### Updated Dockerfile Changes
```dockerfile
# Build stage - Enable CGO and add SQLite dependencies
ENV CGO_ENABLED=1
RUN apk add --no-cache \
    git \
    gcc \
    musl-dev \
    sqlite-dev

# Runtime stage - Add SQLite runtime
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    sqlite
```

### Verification
After rebuilding the Docker image, the application should start successfully with SQLite support:

```bash
docker build -t audiobookshelf-hardcover-sync .
docker run -d --name abs-sync audiobookshelf-hardcover-sync
docker logs abs-sync
```

You should see successful database initialization instead of CGO errors.

## Alternative Database Solutions

If you encounter persistent SQLite issues, consider using external databases:

### PostgreSQL (Recommended for Production)
```yaml
# config.yaml
database:
  type: postgresql
  host: postgres.example.com
  port: 5432
  name: audiobookshelf_sync
  user: sync_user
  password: secure_password
  ssl_mode: require
```

### MySQL/MariaDB
```yaml
# config.yaml
database:
  type: mysql
  host: mysql.example.com
  port: 3306
  name: audiobookshelf_sync
  user: sync_user
  password: secure_password
```

### Environment Variables
```bash
export DATABASE_TYPE=postgresql
export DATABASE_HOST=postgres.example.com
export DATABASE_USER=sync_user
export DATABASE_PASSWORD=secure_password
export DATABASE_NAME=audiobookshelf_sync
```

## Docker Compose Example

For development with external database:

```yaml
version: '3.8'
services:
  app:
    build: .
    environment:
      - DATABASE_TYPE=postgresql
      - DATABASE_HOST=postgres
      - DATABASE_USER=sync_user
      - DATABASE_PASSWORD=secure_password
      - DATABASE_NAME=audiobookshelf_sync
    depends_on:
      - postgres
  
  postgres:
    image: postgres:15-alpine
    environment:
      - POSTGRES_DB=audiobookshelf_sync
      - POSTGRES_USER=sync_user
      - POSTGRES_PASSWORD=secure_password
    volumes:
      - postgres_data:/var/lib/postgresql/data

volumes:
  postgres_data:
```

## Build Optimization

The CGO-enabled build is slightly larger and takes longer to compile, but it's necessary for SQLite support. If you need a smaller image and don't use SQLite, you can:

1. Use external databases (PostgreSQL/MySQL)
2. Set `DATABASE_TYPE` to your preferred external database
3. The application will skip SQLite entirely

## Kubernetes Deployment

For Kubernetes deployments, ensure your Helm values specify the database type:

```yaml
# values.yaml
database:
  type: postgresql  # or mysql
  host: postgres-service
  # ... other config
```

This avoids SQLite altogether in containerized environments where external databases are preferred.
