# Helm Chart Publishing Guide

This document explains how the Helm chart for audiobookshelf-hardcover-sync is published and how users can install it.

## 📦 Chart Repository

The Helm chart is automatically published to GitHub Pages and available at:
- **Repository URL**: `https://drallgood.github.io/audiobookshelf-hardcover-sync`
- **Chart Name**: `audiobookshelf-hardcover-sync`

## 🚀 Installation

### Add the Helm Repository

```bash
helm repo add audiobookshelf-hardcover-sync https://drallgood.github.io/audiobookshelf-hardcover-sync
helm repo update
```

### Search for Available Charts

```bash
helm search repo audiobookshelf-hardcover-sync
```

### Install the Chart

1. **Create a values file with your configuration:**

```yaml
# my-values.yaml
secrets:
  audiobookshelf:
    url: "https://your-audiobookshelf-instance.com"
    token: "your-audiobookshelf-api-token"
  hardcover:
    token: "your-hardcover-api-token"

# Optional: Enable persistence
persistence:
  enabled: true
  size: 2Gi

# Optional: Configure resource limits
resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

2. **Install the chart:**

```bash
helm install my-sync audiobookshelf-hardcover-sync/audiobookshelf-hardcover-sync -f my-values.yaml
```

### Upgrade an Existing Installation

```bash
helm upgrade my-sync audiobookshelf-hardcover-sync/audiobookshelf-hardcover-sync -f my-values.yaml
```

### Uninstall the Chart

```bash
helm uninstall my-sync
```

## 🔄 Publishing Process

The Helm chart is automatically published when:

1. **Changes to Helm chart files** (`helm/**`) are pushed to the `main` branch
2. **New releases** are created on GitHub
3. **Manual trigger** via GitHub Actions workflow dispatch

### Publishing Workflow

The publishing process includes:

1. **Linting** - Validates chart syntax and best practices
2. **Packaging** - Creates `.tgz` chart packages
3. **Indexing** - Updates the Helm repository index
4. **Deployment** - Publishes to GitHub Pages
5. **Testing** - Validates the published chart can be installed

### Chart Versioning

- Chart versions follow semantic versioning (semver)
- App version corresponds to the application release version
- Each chart package is immutable once published

## 📋 Available Configuration

For detailed configuration options, see:
- [Chart Values Documentation](../helm/audiobookshelf-hardcover-sync/README.md)
- [Production Values Example](../helm/audiobookshelf-hardcover-sync/values-production.yaml)
- [Development Values Example](../helm/audiobookshelf-hardcover-sync/values-development.yaml)

## 🛠️ Development

### Local Testing

Test the chart locally before publishing:

```bash
# Lint the chart
helm lint helm/audiobookshelf-hardcover-sync

# Template the chart
helm template test-release helm/audiobookshelf-hardcover-sync -f my-values.yaml

# Install locally
helm install test-release helm/audiobookshelf-hardcover-sync -f my-values.yaml
```

### Chart Structure

```
helm/audiobookshelf-hardcover-sync/
├── Chart.yaml              # Chart metadata
├── values.yaml             # Default values
├── values-production.yaml  # Production example
├── values-development.yaml # Development example
├── README.md              # Chart documentation
└── templates/             # Kubernetes manifests
    ├── deployment.yaml
    ├── service.yaml
    ├── secret.yaml
    ├── configmap.yaml
    ├── ingress.yaml
    ├── persistentvolumeclaim.yaml
    ├── serviceaccount.yaml
    ├── hpa.yaml
    ├── _helpers.tpl
    └── NOTES.txt
```

## 🔒 Security

- API tokens are stored as Kubernetes secrets
- Charts follow security best practices
- Non-root user execution
- Read-only root filesystem
- Dropped capabilities

## 📞 Support

For issues with the Helm chart:
1. Check the [troubleshooting guide](../helm/audiobookshelf-hardcover-sync/README.md#troubleshooting)
2. Review [GitHub Issues](https://github.com/drallgood/audiobookshelf-hardcover-sync/issues)
3. Create a new issue with chart-related problems

## 🤝 Contributing

To contribute to the Helm chart:
1. Make changes to files in `helm/audiobookshelf-hardcover-sync/`
2. Test locally using the commands above
3. Submit a pull request
4. Chart will be automatically published after merge
