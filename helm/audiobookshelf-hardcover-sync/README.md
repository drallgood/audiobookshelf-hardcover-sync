# Audiobookshelf-Hardcover Sync Helm Chart

This Helm chart deploys the Audiobookshelf-Hardcover Sync application on a Kubernetes cluster using the Helm package manager.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.2.0+
- PV provisioner support in the underlying infrastructure (if persistence is enabled)

## Installing the Chart

To install the chart with the release name `my-sync`:

```bash
helm install my-sync ./helm/audiobookshelf-hardcover-sync
```

The command deploys the sync application on the Kubernetes cluster in the default configuration. The [Parameters](#parameters) section lists the parameters that can be configured during installation.

> **Tip**: List all releases using `helm list`

## Uninstalling the Chart

To uninstall/delete the `my-sync` deployment:

```bash
helm delete my-sync
```

The command removes all the Kubernetes components associated with the chart and deletes the release.

## Web UI Access

The application includes a modern web interface for multi-user management and monitoring. Once deployed, you can access the web UI at:

```
http://<service-url>:<port>/
```

### Web UI Features

- **Multi-User Management**: Create, edit, and delete users with individual sync configurations
- **Authentication Support**: Local username/password or Keycloak/OIDC integration
- **Real-Time Monitoring**: Live sync status updates and progress tracking
- **Individual Sync Control**: Start/stop sync operations per user
- **Configuration Management**: Edit sync settings, API tokens, and preferences per user

### Accessing the Web UI

#### Without Ingress (Port Forward)
```bash
kubectl port-forward service/my-sync-audiobookshelf-hardcover-sync 8080:8080
# Access at: http://localhost:8080
```

#### With Ingress Enabled
```yaml
ingress:
  enabled: true
  className: "nginx"
  hosts:
    - host: sync.example.com
      paths:
        - path: /
          pathType: Prefix
  # For authentication-enabled deployments:
  annotations:
    nginx.ingress.kubernetes.io/session-cookie-name: "abs-hc-sync-session"
    nginx.ingress.kubernetes.io/session-cookie-max-age: "86400"
```

### Authentication Configuration

To enable the authentication system in the web UI:

```yaml
authentication:
  enabled: true
  sessionSecret: "your-secure-session-secret"
  defaultAdmin:
    username: "admin"
    email: "admin@example.com"
    password: "secure-password"
```

See the [Authentication Guide](../../docs/AUTHENTICATION.md) for detailed setup instructions.

## Parameters

### Global parameters

| Name               | Description                                     | Value |
| ------------------ | ----------------------------------------------- | ----- |
| `nameOverride`     | String to partially override common.names.name | `""`  |
| `fullnameOverride` | String to fully override common.names.fullname | `""`  |

### Common parameters

| Name                     | Description                                                                             | Value           |
| ------------------------ | --------------------------------------------------------------------------------------- | --------------- |
| `replicaCount`           | Number of replicas to deploy                                                           | `1`             |
| `image.repository`       | Image repository                                                                        | `ghcr.io/drallgood/audiobookshelf-hardcover-sync` |
| `image.tag`              | Image tag (immutable tags are recommended)                                             | `""`            |
| `image.pullPolicy`       | Image pull policy                                                                       | `IfNotPresent`  |
| `imagePullSecrets`       | Global Docker registry secret names as an array                                        | `[]`            |

### Security Context parameters

| Name                 | Description                                                | Value   |
| -------------------- | ---------------------------------------------------------- | ------- |
| `podSecurityContext` | Set pod security context                                   | `{}`    |
| `securityContext`    | Set container security context                             | `{}`    |

### Service parameters

| Name                 | Description                                                | Value       |
| -------------------- | ---------------------------------------------------------- | ----------- |
| `service.type`       | Service type                                               | `ClusterIP` |
| `service.port`       | Service HTTP port                                          | `8080`      |

### Ingress parameters

| Name                  | Description                                                | Value                    |
| --------------------- | ---------------------------------------------------------- | ------------------------ |
| `ingress.enabled`     | Enable ingress record generation                           | `false`                  |
| `ingress.className`   | IngressClass that will be be used to implement the Ingress | `""`                     |
| `ingress.annotations` | Additional annotations for the Ingress resource            | `{}`                     |
| `ingress.hosts`       | An array with the list of hosts for the ingress           | `[{"host": "audiobookshelf-hardcover-sync.local", "paths": [{"path": "/", "pathType": "Prefix"}]}]` |
| `ingress.tls`         | TLS configuration for the ingress                          | `[]`                     |

### Persistence parameters

| Name                        | Description                                                | Value               |
| --------------------------- | ---------------------------------------------------------- | ------------------- |
| `persistence.enabled`       | Enable persistence using Persistent Volume Claims         | `false`             |
| `persistence.storageClass`  | Persistent Volume storage class                            | `""`                |
| `persistence.accessMode`    | Persistent Volume access mode                              | `ReadWriteOnce`     |
| `persistence.size`          | Persistent Volume size                                     | `1Gi`               |
| `persistence.annotations`   | Additional custom annotations for the PVC                 | `{}`                |

### Application Configuration

| Name                                    | Description                                                | Value              |
| --------------------------------------- | ---------------------------------------------------------- | ------------------ |
| `config.server.port`                    | Server port                                                | `"8080"`           |
| `config.server.shutdownTimeout`         | Graceful shutdown timeout                                  | `"10s"`            |
| `config.rateLimit.rate`                 | Minimum time between requests                              | `"1500ms"`         |
| `config.rateLimit.burst`                | Maximum number of requests in a burst                      | `2`                |
| `config.rateLimit.maxConcurrent`        | Maximum number of concurrent requests                      | `3`                |
| `config.logging.level`                  | Log level                                                  | `"info"`           |
| `config.logging.format`                 | Log format                                                 | `"json"`           |
| `config.app.syncInterval`               | Sync interval                                              | `"1h"`             |
| `config.app.minimumProgress`            | Minimum progress threshold                                 | `0.01`             |
| `config.app.syncWantToRead`             | Sync books with 0% progress as "Want to Read"             | `true`             |
| `config.app.syncOwned`                  | Mark synced books as owned in Hardcover                   | `true`             |
| `config.app.dryRun`                     | Enable dry run mode                                        | `false`            |
| `config.sync.incremental`               | Enable incremental sync                                    | `true`             |
| `config.sync.stateFile`                 | Sync state file path                                       | `"/app/data/sync_state.json"` |
| `config.sync.minChangeThreshold`        | Minimum change threshold for incremental sync             | `60`               |
| `config.sync.libraries.include`         | List of library names/IDs to include in sync              | `[]`               |
| `config.sync.libraries.exclude`         | List of library names/IDs to exclude from sync            | `[]`               |

### Secrets Configuration

| Name                              | Description                                                | Value |
| --------------------------------- | ---------------------------------------------------------- | ----- |
| `secrets.audiobookshelf.url`      | Audiobookshelf instance URL                                | `""`  |
| `secrets.audiobookshelf.token`    | Audiobookshelf API token                                   | `""`  |
| `secrets.hardcover.token`         | Hardcover API token                                        | `""`  |

### Other Parameters

| Name                 | Description                                                | Value   |
| -------------------- | ---------------------------------------------------------- | ------- |
| `nodeSelector`       | Node labels for pod assignment                             | `{}`    |
| `tolerations`        | List of node taints to tolerate                           | `[]`    |
| `affinity`           | Affinity for pod assignment                                | `{}`    |
| `resources`          | The resources to allocate for the container               | `{}`    |

## Configuration and installation details

### Setting up secrets

Before installing the chart, you need to configure the required secrets for Audiobookshelf and Hardcover API access:

```bash
# Create a values file with your secrets
cat > my-values.yaml << EOF
secrets:
  audiobookshelf:
    url: "https://your-audiobookshelf-instance.com"
    token: "your-audiobookshelf-api-token"
  hardcover:
    token: "your-hardcover-api-token"
EOF

# Install with custom values
helm install my-sync ./helm/audiobookshelf-hardcover-sync -f my-values.yaml
```

### Enabling persistence

To enable data persistence across pod restarts:

```yaml
persistence:
  enabled: true
  size: 2Gi
  storageClass: "fast-ssd"  # Optional: specify storage class
```

### Library filtering

You can configure which libraries to sync:

```yaml
config:
  sync:
    libraries:
      include: ["Audiobooks", "Fiction"]  # Only sync these libraries
      # OR
      exclude: ["Magazines", "Podcasts"]  # Exclude these libraries
```

### Ingress configuration

To expose the application via ingress:

```yaml
ingress:
  enabled: true
  className: "nginx"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
  hosts:
    - host: sync.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: sync-tls
      hosts:
        - sync.example.com
```

## Troubleshooting

### Check pod logs

```bash
kubectl logs -l app.kubernetes.io/name=audiobookshelf-hardcover-sync -f
```

### Check configuration

```bash
kubectl get configmap my-sync-audiobookshelf-hardcover-sync -o yaml
kubectl get secret my-sync-audiobookshelf-hardcover-sync -o yaml
```

### Health check

The application provides a health endpoint at `/health` that you can use to verify the service is running correctly.
