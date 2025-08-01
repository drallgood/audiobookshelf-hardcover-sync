#!/bin/bash

# Test script for audiobookshelf-hardcover-sync Helm chart
# This script validates the Helm chart templates and performs basic tests

set -e

CHART_DIR="./audiobookshelf-hardcover-sync"
RELEASE_NAME="test-sync"
NAMESPACE="test-sync"

echo "🧪 Testing Audiobookshelf-Hardcover Sync Helm Chart"
echo "=================================================="

# Check if helm is installed
if ! command -v helm &> /dev/null; then
    echo "❌ Helm is not installed. Please install Helm first."
    exit 1
fi

echo "✅ Helm is installed: $(helm version --short)"

# Validate chart structure
echo "📋 Validating chart structure..."
if [ ! -f "$CHART_DIR/Chart.yaml" ]; then
    echo "❌ Chart.yaml not found"
    exit 1
fi

if [ ! -f "$CHART_DIR/values.yaml" ]; then
    echo "❌ values.yaml not found"
    exit 1
fi

echo "✅ Chart structure is valid"

# Lint the chart
echo "🔍 Linting Helm chart..."
helm lint "$CHART_DIR"
echo "✅ Chart linting passed"

# Template the chart with default values
echo "📝 Templating chart with default values..."
helm template "$RELEASE_NAME" "$CHART_DIR" > /tmp/default-templates.yaml
echo "✅ Default templating successful"

# Template the chart with production values
echo "📝 Templating chart with production values..."
helm template "$RELEASE_NAME" "$CHART_DIR" -f "$CHART_DIR/values-production.yaml" > /tmp/production-templates.yaml
echo "✅ Production templating successful"

# Template the chart with development values
echo "📝 Templating chart with development values..."
helm template "$RELEASE_NAME" "$CHART_DIR" -f "$CHART_DIR/values-development.yaml" > /tmp/development-templates.yaml
echo "✅ Development templating successful"

# Test with custom values
echo "📝 Testing with custom values..."
cat > /tmp/test-values.yaml << EOF
secrets:
  audiobookshelf:
    url: "https://test.example.com"
    token: "test-token"
  hardcover:
    token: "test-hardcover-token"

persistence:
  enabled: true
  size: "500Mi"

ingress:
  enabled: true
  hosts:
    - host: test.local
      paths:
        - path: /
          pathType: Prefix
EOF

helm template "$RELEASE_NAME" "$CHART_DIR" -f /tmp/test-values.yaml > /tmp/custom-templates.yaml
echo "✅ Custom values templating successful"

# Validate Kubernetes manifests (if kubectl is available)
if command -v kubectl &> /dev/null; then
    echo "🔍 Validating Kubernetes manifests..."
    kubectl apply --dry-run=client -f /tmp/default-templates.yaml > /dev/null
    echo "✅ Kubernetes manifest validation passed"
else
    echo "⚠️  kubectl not found, skipping Kubernetes validation"
fi

# Check for required secrets
echo "🔐 Checking secret configuration..."
if grep -q 'audiobookshelf-url: ""' /tmp/default-templates.yaml; then
    echo "⚠️  Warning: Default values contain empty secrets"
    echo "   Make sure to configure secrets before deployment"
fi

echo ""
echo "🎉 All tests passed!"
echo ""
echo "📚 Next steps:"
echo "1. Configure your secrets in values.yaml or a custom values file"
echo "2. Install the chart: helm install my-sync $CHART_DIR -f my-values.yaml"
echo "3. Check the deployment: kubectl get pods -l app.kubernetes.io/name=audiobookshelf-hardcover-sync"
echo ""
echo "📁 Generated template files:"
echo "   - /tmp/default-templates.yaml"
echo "   - /tmp/production-templates.yaml"
echo "   - /tmp/development-templates.yaml"
echo "   - /tmp/custom-templates.yaml"
