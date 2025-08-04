# Database Configuration

The audiobookshelf-hardcover-sync application supports multiple database management systems (DBMS) with automatic fallback to SQLite for maximum compatibility and ease of deployment.

## Supported Database Systems

### SQLite (Default)
- **Type**: `sqlite`
- **Use Case**: Single-node deployments, development, small-scale usage
- **Benefits**: Zero configuration, embedded database, no external dependencies
- **Limitations**: Single writer, not suitable for high-concurrency scenarios

### PostgreSQL
- **Type**: `postgresql` or `postgres`
- **Use Case**: Production deployments, high availability, concurrent access
- **Benefits**: ACID compliance, excellent performance, advanced features
- **Requirements**: External PostgreSQL server

### MySQL/MariaDB
- **Type**: `mysql` or `mariadb`
- **Use Case**: Production deployments, existing MySQL infrastructure
- **Benefits**: Wide adoption, good performance, familiar to many teams
- **Requirements**: External MySQL/MariaDB server

## Configuration Methods

The application supports three configuration methods with clear precedence:

1. **Environment Variables** (Highest Priority)
2. **Config.yaml File** (Medium Priority)
3. **Default Values** (Lowest Priority)

### Config.yaml Configuration (Recommended for Development)

The most user-friendly way to configure database settings is through the `config.yaml` file:

```yaml
# Database configuration in config.yaml
database:
  # Database type: sqlite, postgresql, mysql, mariadb
  type: postgresql
  
  # Connection parameters (for non-SQLite databases)
  host: postgres.example.com
  port: 5432
  name: audiobookshelf_sync
  user: sync_user
  # password: ""  # Use environment variable for security
  
  # SQLite-specific configuration
  path: ./data/custom-database.db
  
  # PostgreSQL SSL configuration
  ssl_mode: require  # disable, allow, prefer, require, verify-ca, verify-full
  
  # Connection pool settings (for PostgreSQL/MySQL)
  connection_pool:
    max_open_conns: 25
    max_idle_conns: 5
    conn_max_lifetime: 60  # minutes
```

**Benefits of config.yaml:**
- Easy to read and maintain
- Self-documenting with comments
- Version control friendly (without secrets)
- Great for development environments
- Mix with environment variables for production

### Environment Variables (Recommended for Production)

The application automatically detects database configuration from environment variables with intelligent fallback to SQLite:

```bash
# Database Type (defaults to sqlite)
DATABASE_TYPE=postgresql

# Connection Parameters (required for non-SQLite)
DATABASE_HOST=localhost
DATABASE_PORT=5432
DATABASE_NAME=audiobookshelf_sync
DATABASE_USER=sync_user
DATABASE_PASSWORD=secure_password

# SSL Configuration (PostgreSQL)
DATABASE_SSL_MODE=prefer  # disable, allow, prefer, require, verify-ca, verify-full

# Connection Pool Settings (optional)
DATABASE_MAX_OPEN_CONNS=25
DATABASE_MAX_IDLE_CONNS=5
DATABASE_CONN_MAX_LIFETIME=60  # minutes

# SQLite Path Override (optional)
DATABASE_PATH=/custom/path/to/database.db
```

### Mixed Configuration (Config.yaml + Environment)

You can combine config.yaml with environment variables for maximum flexibility:

```yaml
# config.yaml - Base configuration
database:
  type: postgresql
  host: postgres.example.com
  port: 5432
  name: audiobookshelf_sync
  user: sync_user
  ssl_mode: require
  connection_pool:
    max_open_conns: 25
    max_idle_conns: 5
    conn_max_lifetime: 60
```

```bash
# Environment variables override config.yaml (recommended for secrets)
export DATABASE_PASSWORD=secure_production_password

# Override database type for testing
export DATABASE_TYPE=sqlite
```

### Programmatic Configuration

```go
import "github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"

// Create custom database configuration
config := &database.DatabaseConfig{
    Type:     database.DatabaseTypePostgreSQL,
    Host:     "postgres.example.com",
    Port:     5432,
    Database: "audiobookshelf_sync",
    Username: "sync_user",
    Password: "secure_password",
    SSLMode:  "require",
}

// Connect with fallback to SQLite
db, err := database.NewDatabase(config, logger)
```

## Database-Specific Setup

### PostgreSQL Setup

1. **Create Database and User**:
```sql
CREATE DATABASE audiobookshelf_sync;
CREATE USER sync_user WITH PASSWORD 'secure_password';
GRANT ALL PRIVILEGES ON DATABASE audiobookshelf_sync TO sync_user;
```

2. **Environment Configuration**:
```bash
DATABASE_TYPE=postgresql
DATABASE_HOST=postgres.example.com
DATABASE_PORT=5432
DATABASE_NAME=audiobookshelf_sync
DATABASE_USER=sync_user
DATABASE_PASSWORD=secure_password
DATABASE_SSL_MODE=require
```

### MySQL/MariaDB Setup

1. **Create Database and User**:
```sql
CREATE DATABASE audiobookshelf_sync CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'sync_user'@'%' IDENTIFIED BY 'secure_password';
GRANT ALL PRIVILEGES ON audiobookshelf_sync.* TO 'sync_user'@'%';
FLUSH PRIVILEGES;
```

2. **Environment Configuration**:
```bash
DATABASE_TYPE=mysql
DATABASE_HOST=mysql.example.com
DATABASE_PORT=3306
DATABASE_NAME=audiobookshelf_sync
DATABASE_USER=sync_user
DATABASE_PASSWORD=secure_password
```

### SQLite Setup (Default)

SQLite requires no external setup. The application will automatically:
- Create the database file at the default location (`data/database.db`)
- Create necessary directories
- Handle all migrations

**Custom SQLite Path**:
```bash
DATABASE_TYPE=sqlite
DATABASE_PATH=/custom/path/to/database.db
```

## Fallback Behavior

The application implements intelligent fallback to ensure maximum reliability:

1. **Configuration Validation**: Invalid configurations automatically fall back to SQLite
2. **Connection Failure**: Failed connections to external databases fall back to SQLite
3. **Unsupported Types**: Unknown database types fall back to SQLite
4. **Logging**: All fallback events are logged with appropriate warnings

Example fallback scenarios:
- PostgreSQL server unreachable → SQLite fallback
- Invalid MySQL credentials → SQLite fallback
- Unsupported database type → SQLite fallback

## Migration and Compatibility

### Automatic Migration
- The application automatically migrates existing SQLite databases
- Schema changes are applied automatically on startup
- No manual intervention required for database updates

### Cross-Database Migration
Currently, the application doesn't support automatic migration between different database types. To migrate:

1. **Export Data**: Use database-specific tools to export data
2. **Update Configuration**: Change environment variables
3. **Import Data**: Import data into the new database system
4. **Restart Application**: The application will detect and use the new database

## Performance Considerations

### SQLite
- **Pros**: Fast for read-heavy workloads, zero maintenance
- **Cons**: Single writer limitation, not suitable for high concurrency
- **Best For**: Single-user deployments, development, small-scale usage

### PostgreSQL
- **Pros**: Excellent concurrent performance, ACID compliance, advanced features
- **Cons**: Requires external server management
- **Best For**: Production deployments, multi-user environments, high availability

### MySQL/MariaDB
- **Pros**: Good performance, wide adoption, familiar tooling
- **Cons**: Requires external server management
- **Best For**: Existing MySQL infrastructure, production deployments

## Connection Pool Settings

For PostgreSQL and MySQL/MariaDB, connection pool settings can be tuned:

```bash
# Maximum number of open connections
DATABASE_MAX_OPEN_CONNS=25

# Maximum number of idle connections
DATABASE_MAX_IDLE_CONNS=5

# Connection lifetime in minutes
DATABASE_CONN_MAX_LIFETIME=60
```

**Recommended Settings**:
- **Small deployments**: 10 open, 2 idle, 30 min lifetime
- **Medium deployments**: 25 open, 5 idle, 60 min lifetime
- **Large deployments**: 50 open, 10 idle, 120 min lifetime

## Security Best Practices

### Connection Security
- **Use SSL/TLS**: Always enable SSL for production databases
- **Network Security**: Use private networks or VPNs for database connections
- **Firewall Rules**: Restrict database access to application servers only

### Credential Management
- **Environment Variables**: Store credentials in environment variables
- **Secrets Management**: Use Kubernetes secrets or similar for production
- **Rotation**: Regularly rotate database passwords
- **Least Privilege**: Grant minimal required permissions to database users

### SQLite Security
- **File Permissions**: Ensure database file has appropriate permissions (600)
- **Directory Security**: Secure the data directory
- **Backup Security**: Encrypt database backups

## Troubleshooting

### Common Issues

**Connection Refused**:
```
failed to connect to database: connection refused
```
- Check database server is running
- Verify host and port configuration
- Check firewall rules

**Authentication Failed**:
```
failed to connect to database: authentication failed
```
- Verify username and password
- Check user permissions
- Ensure user can connect from application host

**Database Not Found**:
```
failed to connect to database: database does not exist
```
- Create the database manually
- Check database name spelling
- Verify user has access to the database

**SSL/TLS Issues**:
```
failed to connect to database: SSL connection error
```
- Check SSL mode configuration
- Verify server SSL certificate
- Update SSL mode if needed (disable for testing)

### Debugging

Enable debug logging to troubleshoot database issues:

```bash
LOG_LEVEL=debug
```

The application will log detailed information about:
- Database connection attempts
- Fallback events
- Migration progress
- Query performance

### Health Checks

The application provides database health check endpoints:
- `/healthz`: Overall application health including database
- Database connection status is included in health responses

## Examples

### Docker Compose with PostgreSQL

```yaml
version: '3.8'
services:
  postgres:
    image: postgres:15
    environment:
      POSTGRES_DB: audiobookshelf_sync
      POSTGRES_USER: sync_user
      POSTGRES_PASSWORD: secure_password
    volumes:
      - postgres_data:/var/lib/postgresql/data
    ports:
      - "5432:5432"

  audiobookshelf-sync:
    image: audiobookshelf-hardcover-sync:latest
    environment:
      DATABASE_TYPE: postgresql
      DATABASE_HOST: postgres
      DATABASE_PORT: 5432
      DATABASE_NAME: audiobookshelf_sync
      DATABASE_USER: sync_user
      DATABASE_PASSWORD: secure_password
      DATABASE_SSL_MODE: disable
    depends_on:
      - postgres
    ports:
      - "8080:8080"

volumes:
  postgres_data:
```

### Kubernetes with External Database

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: database-credentials
type: Opaque
stringData:
  DATABASE_PASSWORD: secure_password

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: audiobookshelf-sync
spec:
  template:
    spec:
      containers:
      - name: audiobookshelf-sync
        image: audiobookshelf-hardcover-sync:latest
        env:
        - name: DATABASE_TYPE
          value: "postgresql"
        - name: DATABASE_HOST
          value: "postgres.example.com"
        - name: DATABASE_PORT
          value: "5432"
        - name: DATABASE_NAME
          value: "audiobookshelf_sync"
        - name: DATABASE_USER
          value: "sync_user"
        - name: DATABASE_PASSWORD
          valueFrom:
            secretKeyRef:
              name: database-credentials
              key: DATABASE_PASSWORD
        - name: DATABASE_SSL_MODE
          value: "require"
```

This comprehensive database configuration system provides flexibility while maintaining the simplicity of SQLite for basic deployments.
