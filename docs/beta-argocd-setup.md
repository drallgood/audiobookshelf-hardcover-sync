# Beta Releases with ArgoCD ImageUpdater

This guide explains how to set up automatic beta deployments using ArgoCD ImageUpdater with the `latest-dev` tag from the develop branch.

## Overview

The `audiobookshelf-hardcover-sync` chart publishes beta versions to GitHub Container Registry with the `latest-dev` tag when code is pushed to the `develop` branch. This enables automatic updates in your cluster using ArgoCD ImageUpdater.

## Prerequisites

- ArgoCD installed in your cluster
- ArgoCD ImageUpdater installed and configured
- Access to the GitHub Container Registry (if private)

## Setup Instructions

### 1. Deploy with Beta Values

Deploy the chart using the development values file:

```bash
helm install my-beta-sync ./helm/audiobookshelf-hardcover-sync \
  -f ./helm/audiobookshelf-hardcover-sync/values-development.yaml \
  --namespace audiobookshelf-sync-beta \
  --create-namespace
```

### 2. Configure ArgoCD ImageUpdater

Add the following annotations to your ArgoCD Application to enable automatic updates:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: audiobookshelf-sync-beta
  namespace: argocd
  annotations:
    # Enable image updates
    argocd-image-updater.argoproj.io/image-list: sync=ghcr.io/drallgood/audiobookshelf-hardcover-sync
    # Use the beta tag pattern for updates
    argocd-image-updater.argoproj.io/sync.update-strategy: version
    argocd-image-updater.argoproj.io/sync.version-tag: beta
    argocd-image-updater.argoproj.io/sync.allow-tags: regexp:^beta-[0-9]+-[a-f0-9]+$
    # Write back to the values file
    argocd-image-updater.argoproj.io/write-back-method: git
    # Commit message template
    argocd-image-updater.argoproj.io/git-commit-message: "chore: update image to {{.Image}}:{{.Tag}}"
spec:
  # ... your existing ArgoCD Application spec
```

### 3. Alternative: Using ArgoCD ApplicationSet

If you're using ApplicationSet, configure it like this:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: audiobookshelf-sync-beta
spec:
  generators:
  - git:
      repoURL: https://github.com/your-org/your-gitops-repo.git
      revision: HEAD
      directories:
      - path: environments/beta
  template:
    metadata:
      name: 'audiobookshelf-sync-beta'
      annotations:
        argocd-image-updater.argoproj.io/image-list: sync=ghcr.io/drallgood/audiobookshelf-hardcover-sync
        # Use the beta tag pattern for updates
        argocd-image-updater.argoproj.io/sync.update-strategy: version
        argocd-image-updater.argoproj.io/sync.version-tag: beta
        argocd-image-updater.argoproj.io/sync.allow-tags: regexp:^beta-[0-9]+-[a-f0-9]+$
        argocd-image-updater.argoproj.io/write-back-method: git
    spec:
      # ... your application spec
```

### 4. Verify ImageUpdater Configuration

Check that ArgoCD ImageUpdater is monitoring your image:

```bash
# List images being watched
argocd-image-updater list

# Check specific application
argocd-image-updater test audiobookshelf-sync-beta
```

## Available Tags

When code is pushed to the `develop` branch, the following tags are published:

- `beta-{build-number}-{sha}` - Unique beta tag (e.g., `beta-123-abc123def`) - **RECOMMENDED for ArgoCD**
- `latest-dev` - Latest beta version (may not trigger updates due to tag caching)
- `dev` - Same as latest-dev
- `develop` - Same as latest-dev
- `sha-{commit}` - Specific commit SHA

### Why Use Unique Beta Tags?

Kubernetes caches images by tag, so using `latest-dev` may not trigger updates because:
- The tag name doesn't change
- K8s uses image digests, not just tags
- ImagePullPolicy of `IfNotPresent` won't re-pull if tag exists locally

The unique `beta-{build-number}-{sha}` tags solve this by ensuring each build has a unique identifier.

## Alternative Solutions

### Option 1: Unique Tags (Recommended Above)

Use the `beta-{build-number}-{sha}` tags that are automatically generated.

### Option 2: Use ImagePullPolicy Always

You can force Kubernetes to always check for new images:

```yaml
# In your values.yaml or deployment
image:
  pullPolicy: Always
```

**Drawback:** This will check for updates on every pod restart, which is inefficient.

### Option 3: Use Digest-Based Updates

For maximum reliability, configure ArgoCD ImageUpdater to use image digests:

```yaml
annotations:
  argocd-image-updater.argoproj.io/image-list: sync=ghcr.io/drallgood/audiobookshelf-hardcover-sync
  argocd-image-updater.argoproj.io/sync.update-strategy: digest
  argocd-image-updater.argoproj.io/sync.allow-tags: regexp:^beta-[0-9]+-[a-f0-9]+$
```

This ensures updates are detected even if the tag doesn't change, but requires more complex setup.

### Option 4: Manual Trigger with Annotation

Add an annotation to force deployment refresh:

```yaml
metadata:
  annotations:
    rollme: {{ now | date "20060102150405" }}
```

This changes on each deployment, forcing Kubernetes to recreate the pods.

## Update Workflow

1. **Code pushed to develop branch** → GitHub Actions builds and publishes image
2. **Image tagged as `beta-{build-number}-{sha}`** → Pushed to GitHub Container Registry
3. **ArgoCD ImageUpdater detects new beta tag** → Compares with current version
4. **Automatic update triggered** → Updates values file in Git with new tag
5. **ArgoCD syncs changes** → Deploys new version to cluster (K8s pulls new image due to unique tag)

## Configuration Options

### Update Strategy

You can configure different update strategies:

```yaml
# Version-based (recommended)
argocd-image-updater.argoproj.io/sync.update-strategy: version

# Digest-based (most secure)
argocd-image-updater.argoproj.io/sync.update-strategy: digest
```

### Update Interval

Control how often ImageUpdater checks for updates:

```yaml
# Check every 5 minutes (default)
argocd-image-updater.argoproj.io/update-interval: 5m

# Check every hour for production
argocd-image-updater.argoproj.io/update-interval: 1h
```

### Dry Run Mode

Test the setup without making changes:

```yaml
argocd-image-updater.argoproj.io/sync.dry-run: "true"
```

## Troubleshooting

### Image Not Updating

1. Check ImageUpdater logs:
   ```bash
   kubectl logs -n argocd deployment/argocd-image-updater
   ```

2. Verify registry access:
   ```bash
   docker pull ghcr.io/drallgood/audiobookshelf-hardcover-sync:latest-dev
   ```

3. Check annotations on your Application:
   ```bash
   kubectl get application audiobookshelf-sync-beta -n argocd -o yaml
   ```

### Permissions Issues

Ensure ArgoCD ImageUpdater has permission to write to your Git repository:

1. Check Git credentials in ArgoCD
2. Verify write access to the repository
3. Check SSH key or token permissions

### Rollback

If a beta version causes issues:

1. Manually update the tag in your values file:
   ```yaml
   image:
     tag: "latest-dev"  # Or a specific known-good version
   ```

2. Or disable ImageUpdater temporarily:
   ```bash
   argocd app set audiobookshelf-sync-beta --parameter image.tag=latest-dev
   ```

## Best Practices

1. **Use separate namespace** for beta deployments
2. **Monitor beta deployments** closely with alerts
3. **Keep dry-run enabled** initially to test setup
4. **Use resource limits** appropriate for testing
5. **Enable debug logging** to troubleshoot issues:
   ```yaml
   env:
     - name: DEBUG
       value: "true"
   ```

## Security Considerations

- Beta versions may contain unstable features
- Always test in a non-production environment first
- Review changes before promoting to production
- Consider using image digests for maximum security

## Related Documentation

- [ArgoCD ImageUpdater Documentation](https://argocd-image-updater.readthedocs.io/)
- [Helm Chart Documentation](./README.md)
- [Docker Publishing Workflow](../.github/workflows/docker-publish.yml)
