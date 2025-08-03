# Authentication Setup Guide

This guide covers the authentication system for audiobookshelf-hardcover-sync, including local username/password authentication and Keycloak/OIDC integration.

## Overview

The authentication system provides:
- **Local Authentication**: Username/password with bcrypt hashing
- **OIDC Integration**: Support for Keycloak and other OpenID Connect providers
- **Role-Based Access Control**: Admin, User, and Viewer roles
- **Session Management**: Secure HTTP-only cookies with CSRF protection
- **Multi-Provider Support**: Mix local and external authentication

## Quick Start

### Configuration Methods

Authentication can be configured using multiple methods with the following priority:

1. **Environment Variables** (highest priority)
2. **config.yaml** (medium priority)
3. **Default values** (lowest priority)

### Method 1: config.yaml Configuration

The easiest way to configure authentication is through the `config.yaml` file:

```yaml
authentication:
  # Enable authentication system
  enabled: true
  
  # Session configuration
  session:
    # Secret for signing cookies (auto-generated if empty)
    secret: "your-secret-key-here"
    # Cookie name for sessions
    cookie_name: "audiobookshelf-sync-session"
    # Session max age in seconds (24 hours)
    max_age: 86400
    # Secure cookie (set to true for HTTPS)
    secure: false
    # HTTP only cookie (prevents JavaScript access)
    http_only: true
    # SameSite cookie policy (Strict, Lax, None)
    same_site: "Lax"
  
  # Default admin user (created automatically if auth is enabled)
  default_admin:
    # Default admin username
    username: "admin"
    # Default admin email
    email: "admin@localhost"
    # Default admin password (REQUIRED if auth is enabled)
    password: "your-secure-password"
  
  # Keycloak/OIDC authentication (optional)
  keycloak:
    # Enable Keycloak/OIDC provider
    enabled: false
    # OIDC issuer URL
    issuer: "https://keycloak.example.com/realms/myrealm"
    # OIDC client ID
    client_id: "audiobookshelf-sync"
    # OIDC client secret
    client_secret: "your-client-secret"
    # Redirect URI (must match Keycloak client config)
    redirect_uri: "http://localhost:8080/auth/callback"
    # OIDC scopes to request
    scopes: "openid profile email"
    # JWT claim containing user roles
    role_claim: "realm_access.roles"
```

### Method 2: Environment Variables

Environment variables override config.yaml settings and are recommended for production deployments: to enable authentication:

```bash
export AUTH_ENABLED=true
```

### Default Admin User

When authentication is enabled and no users exist, a default admin user is created automatically:

```bash
export AUTH_DEFAULT_ADMIN_USERNAME="admin"
export AUTH_DEFAULT_ADMIN_EMAIL="admin@localhost"
export AUTH_DEFAULT_ADMIN_PASSWORD="changeme"
```

**⚠️ Important**: Change the default password immediately after first login!

## Configuration

### Basic Authentication Settings

```bash
# Enable/disable authentication
AUTH_ENABLED=true

# Session configuration
AUTH_SESSION_SECRET="your-secure-random-secret-key-here"
AUTH_COOKIE_NAME="abs-hc-sync-session"

# Default admin user (created if no users exist)
AUTH_DEFAULT_ADMIN_USERNAME="admin"
AUTH_DEFAULT_ADMIN_EMAIL="admin@localhost"
AUTH_DEFAULT_ADMIN_PASSWORD="changeme"
```

### Keycloak/OIDC Configuration

For Keycloak or other OIDC providers:

```bash
# OIDC Provider Configuration
KEYCLOAK_ISSUER="https://your-keycloak.example.com/realms/your-realm"
KEYCLOAK_CLIENT_ID="audiobookshelf-hardcover-sync"
KEYCLOAK_CLIENT_SECRET="your-client-secret"
KEYCLOAK_REDIRECT_URI="https://your-app.example.com/auth/callback/oidc"
KEYCLOAK_SCOPES="openid profile email roles"
KEYCLOAK_ROLE_CLAIM="realm_access.roles"
```

## User Roles

### Admin
- Full access to all features
- User management capabilities
- System configuration access
- Can create, edit, and delete users

### User
- Access to sync functionality
- Can manage their own sync configurations
- View sync status and logs

### Viewer
- Read-only access
- Can view sync status
- Cannot modify configurations or start syncs

## Authentication Providers

### Local Provider

The local provider uses username/password authentication with bcrypt password hashing.

**Features:**
- Secure password hashing with bcrypt
- Password strength validation
- Account lockout protection (planned)

### OIDC Provider (Keycloak)

The OIDC provider supports OpenID Connect authentication with Keycloak and other compatible providers.

**Features:**
- Standard OIDC flow
- Role mapping from JWT claims
- Automatic user provisioning
- Token refresh handling

## Keycloak Setup

### 1. Create a Client

In your Keycloak admin console:

1. Navigate to **Clients** → **Create Client**
2. Set **Client ID**: `audiobookshelf-hardcover-sync`
3. Set **Client Type**: `OpenID Connect`
4. Enable **Client authentication**
5. Set **Valid redirect URIs**: `https://your-app.example.com/auth/callback/oidc`
6. Set **Web origins**: `https://your-app.example.com`

### 2. Configure Client Settings

In the client settings:

1. **Access Type**: `confidential`
2. **Standard Flow Enabled**: `ON`
3. **Direct Access Grants Enabled**: `OFF`
4. **Service Accounts Enabled**: `OFF`

### 3. Role Mapping

Create roles in Keycloak and map them to application roles:

**Keycloak Role** → **Application Role**
- `abs-hc-admin` → `admin`
- `abs-hc-user` → `user`
- `abs-hc-viewer` → `viewer`

Configure the role claim path in `KEYCLOAK_ROLE_CLAIM` (default: `realm_access.roles`).

### 4. User Assignment

Assign appropriate roles to users in Keycloak:

1. Navigate to **Users** → Select user → **Role mapping**
2. Assign client roles: `abs-hc-admin`, `abs-hc-user`, or `abs-hc-viewer`

## Security Considerations

### Session Security

- Sessions use HTTP-only, secure cookies
- CSRF protection enabled
- Session expiration and cleanup
- Client IP and User-Agent tracking

### Password Security

- Bcrypt hashing with configurable cost
- Password strength requirements (recommended)
- Secure password reset flow (planned)

### OIDC Security

- State parameter validation
- Nonce validation for ID tokens
- Token signature verification
- Secure token storage

## API Authentication

### Protected Endpoints

All API endpoints under `/api/` require authentication when `AUTH_ENABLED=true`:

- `/api/users` - User management
- `/api/status` - Sync status
- `/api/auth/me` - Current user info

### Authentication Headers

For API access, include the session cookie:

```bash
curl -H "Cookie: abs-hc-sync-session=..." \
     https://your-app.example.com/api/status
```

## Web UI Integration

### Login Flow

1. User accesses protected page
2. Redirected to `/auth/login` if not authenticated
3. Choose authentication provider (local or OIDC)
4. Complete authentication flow
5. Redirected back to original page

### User Interface

The web UI shows:
- Current user information in header
- Logout button
- Role-based feature access
- Authentication status indicators

## Troubleshooting

### Common Issues

**1. Default admin user not created**
- Check `AUTH_ENABLED=true` is set
- Verify database permissions
- Check application logs for errors

**2. OIDC authentication fails**
- Verify `KEYCLOAK_ISSUER` URL is accessible
- Check client ID and secret configuration
- Validate redirect URI matches exactly
- Review Keycloak logs for errors

**3. Session issues**
- Ensure `AUTH_SESSION_SECRET` is set and consistent
- Check cookie domain and path settings
- Verify HTTPS configuration for secure cookies

**4. Role mapping problems**
- Verify `KEYCLOAK_ROLE_CLAIM` path is correct
- Check user has assigned roles in Keycloak
- Review JWT token claims in browser developer tools

### Debug Mode

Enable debug logging for authentication:

```bash
export LOG_LEVEL=debug
```

This will log:
- Authentication attempts
- Session creation/validation
- OIDC token exchange
- Role mapping results

## Migration from Single-User

When enabling authentication on an existing installation:

1. Existing multi-user data is preserved
2. No authentication required initially
3. Default admin user created automatically
4. Existing API tokens continue to work
5. Web UI requires authentication after enabling

## Environment Variables Reference

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `AUTH_ENABLED` | No | `false` | Enable authentication system |
| `AUTH_SESSION_SECRET` | Yes* | - | Secret for session signing |
| `AUTH_COOKIE_NAME` | No | `abs-hc-sync-session` | Session cookie name |
| `AUTH_DEFAULT_ADMIN_USERNAME` | No | `admin` | Default admin username |
| `AUTH_DEFAULT_ADMIN_EMAIL` | No | `admin@localhost` | Default admin email |
| `AUTH_DEFAULT_ADMIN_PASSWORD` | No | `changeme` | Default admin password |
| `KEYCLOAK_ISSUER` | No | - | OIDC issuer URL |
| `KEYCLOAK_CLIENT_ID` | No | - | OIDC client ID |
| `KEYCLOAK_CLIENT_SECRET` | No | - | OIDC client secret |
| `KEYCLOAK_REDIRECT_URI` | No | - | OIDC redirect URI |
| `KEYCLOAK_SCOPES` | No | `openid profile email` | OIDC scopes |
| `KEYCLOAK_ROLE_CLAIM` | No | `realm_access.roles` | JWT role claim path |

*Required when `AUTH_ENABLED=true`

## Production Deployment

### Security Checklist

- [ ] Change default admin password
- [ ] Use strong `AUTH_SESSION_SECRET` (32+ characters)
- [ ] Enable HTTPS for all authentication flows
- [ ] Configure proper CORS settings
- [ ] Set secure cookie attributes
- [ ] Review and audit user roles
- [ ] Monitor authentication logs
- [ ] Regular security updates

### High Availability

- Session data is stored in database (SQLite)
- Multiple instances can share the same database
- Consider external session store for scaling (planned)

## API Reference

### Authentication Endpoints

- `GET /auth/login` - Login page
- `POST /auth/login` - Local authentication
- `GET /auth/oauth/oidc` - Initiate OIDC flow
- `GET /auth/callback/oidc` - OIDC callback
- `POST /auth/logout` - Logout
- `GET /api/auth/me` - Current user info

### Response Format

```json
{
  "success": true,
  "data": {
    "id": "user-id",
    "username": "admin",
    "email": "admin@localhost",
    "role": "admin",
    "provider": "local",
    "active": true
  }
}
```
