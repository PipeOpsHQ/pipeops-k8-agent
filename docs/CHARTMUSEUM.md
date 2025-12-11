# ChartMuseum Integration

This document explains how to configure GitHub Actions to publish Helm charts to ChartMuseum.

## Overview

The CI/CD pipeline automatically publishes the `pipeops-agent` Helm chart to:
1. **GitHub Container Registry (GHCR)** - OCI format (always enabled)
2. **ChartMuseum** - Traditional Helm repository (optional)

## Prerequisites

- A running ChartMuseum instance
- ChartMuseum URL (e.g., `https://charts.example.com`)
- Optional: Authentication credentials if your ChartMuseum requires authentication

## Configuration

### Step 1: Set up GitHub Repository Variables

Go to your repository settings: **Settings → Secrets and variables → Actions → Variables**

Create a new repository variable:
- **Name:** `CHARTMUSEUM_URL`
- **Value:** Your ChartMuseum URL (e.g., `https://charts.pipeops.io`)

### Step 2: Set up GitHub Repository Secrets (Optional)

If your ChartMuseum requires authentication, go to: **Settings → Secrets and variables → Actions → Secrets**

Create two new secrets:
- **Name:** `CHARTMUSEUM_USER`
  - **Value:** Your ChartMuseum username
- **Name:** `CHARTMUSEUM_PASSWORD`
  - **Value:** Your ChartMuseum password or API token

### Step 3: Trigger the Workflow

The ChartMuseum publishing workflow runs automatically when:
- Code is pushed to the `main` branch
- A version tag (e.g., `v1.0.0`) is created

**Note:** If `CHARTMUSEUM_URL` is not set, the ChartMuseum job will be skipped.

## How It Works

### Workflow Job: `helm-publish-chartmuseum`

Located in `.github/workflows/ci.yml` (lines 439-510), this job:

1. **Downloads** the packaged Helm chart from the previous `helm-package` job
2. **Installs** the `helm-push` plugin (for compatibility)
3. **Publishes** the chart to ChartMuseum using the ChartMuseum API

### Publishing Logic

The workflow uses `curl` to push charts via the ChartMuseum REST API:

```bash
# With authentication
curl -X POST \
  --data-binary "@pipeops-agent-<version>.tgz" \
  -u "username:password" \
  "https://charts.example.com/api/charts"

# Without authentication
curl -X POST \
  --data-binary "@pipeops-agent-<version>.tgz" \
  "https://charts.example.com/api/charts"
```

### Success Criteria

- **HTTP 200 OK** or **HTTP 201 Created** = Success ✅
- Any other status code = Failure ❌ (workflow fails)

## Using Charts from ChartMuseum

Once published, users can install the chart from your ChartMuseum:

### 1. Add the Helm Repository

```bash
helm repo add pipeops https://charts.pipeops.io
helm repo update
```

### 2. Install the Chart

```bash
helm install pipeops-agent pipeops/pipeops-agent \
  --set agent.pipeops.token="your-token" \
  --set agent.cluster.name="your-cluster"
```

### 3. Search for Available Versions

```bash
helm search repo pipeops-agent --versions
```

## Troubleshooting

### ChartMuseum Job is Skipped

**Problem:** The `helm-publish-chartmuseum` job doesn't run.

**Solution:** Ensure `CHARTMUSEUM_URL` is set in repository variables:
```bash
# Check in GitHub UI: Settings → Secrets and variables → Actions → Variables
```

### Authentication Failures (HTTP 401)

**Problem:** `❌ Failed to publish chart (HTTP 401)`

**Solutions:**
1. Verify `CHARTMUSEUM_USER` and `CHARTMUSEUM_PASSWORD` secrets are correct
2. Check if your ChartMuseum requires Basic Auth:
   ```bash
   # Test locally
   curl -v -u "user:pass" https://charts.example.com/api/charts
   ```
3. Ensure the user has write permissions in ChartMuseum

### Chart Already Exists (HTTP 409)

**Problem:** `❌ Failed to publish chart (HTTP 409)`

**Cause:** ChartMuseum already has this chart version.

**Solutions:**
1. Enable chart overwrite in ChartMuseum:
   ```bash
   # Start ChartMuseum with overwrite flag
   chartmuseum --storage local --storage-local-rootdir ./charts --allow-overwrite
   ```
2. Delete the existing chart version:
   ```bash
   curl -X DELETE https://charts.example.com/api/charts/pipeops-agent/0.1.0
   ```
3. Update the chart version in `helm/pipeops-agent/Chart.yaml`

### Connection Errors (HTTP 000 or timeouts)

**Problem:** `curl: (6) Could not resolve host: charts.example.com`

**Solutions:**
1. Verify `CHARTMUSEUM_URL` is correct (include `https://`)
2. Ensure ChartMuseum is publicly accessible (or accessible from GitHub Actions)
3. Check firewall rules if ChartMuseum is behind a firewall

## ChartMuseum API Reference

The workflow uses the ChartMuseum API v1:

- **POST** `/api/charts` - Upload a chart
- **GET** `/api/charts` - List all charts
- **GET** `/api/charts/<name>` - List versions for a chart
- **DELETE** `/api/charts/<name>/<version>` - Delete a chart version

Full API documentation: https://github.com/helm/chartmuseum#api

## Security Best Practices

1. **Use HTTPS:** Always use `https://` for `CHARTMUSEUM_URL`
2. **Rotate Credentials:** Regularly rotate `CHARTMUSEUM_PASSWORD`
3. **Least Privilege:** Create a dedicated ChartMuseum user for CI/CD with write-only permissions
4. **Network Security:** Restrict ChartMuseum access using IP allowlists or VPN
5. **Audit Logs:** Enable ChartMuseum logging to track chart uploads

## Example: Self-Hosted ChartMuseum

### Docker Compose Setup

```yaml
version: '3'
services:
  chartmuseum:
    image: ghcr.io/helm/chartmuseum:latest
    container_name: chartmuseum
    ports:
      - "8080:8080"
    environment:
      - STORAGE=local
      - STORAGE_LOCAL_ROOTDIR=/charts
      - BASIC_AUTH_USER=admin
      - BASIC_AUTH_PASS=yourpassword
      - ALLOW_OVERWRITE=true
      - DISABLE_API=false
    volumes:
      - ./charts:/charts
    restart: unless-stopped
```

### Start ChartMuseum

```bash
docker-compose up -d
```

### Configure GitHub Actions

```bash
# Repository Variable
CHARTMUSEUM_URL=https://charts.example.com

# Repository Secrets
CHARTMUSEUM_USER=admin
CHARTMUSEUM_PASSWORD=yourpassword
```

## Alternative: Using helm-push Plugin

If you prefer using the `helm-push` plugin instead of `curl`:

```bash
# Add ChartMuseum as a Helm repo
helm repo add chartmuseum $CHARTMUSEUM_URL \
  --username $CHARTMUSEUM_USER \
  --password $CHARTMUSEUM_PASSWORD

# Push the chart
helm cm-push pipeops-agent-0.1.0.tgz chartmuseum
```

The current workflow installs the plugin but uses `curl` for better error handling and visibility.

## Support

For issues related to:
- **Workflow configuration:** Open an issue in this repository
- **ChartMuseum setup:** See [ChartMuseum documentation](https://chartmuseum.com)
- **Helm charts:** See [Helm documentation](https://helm.sh/docs/)

## References

- [ChartMuseum GitHub](https://github.com/helm/chartmuseum)
- [ChartMuseum API Documentation](https://github.com/helm/chartmuseum#api)
- [Helm Push Plugin](https://github.com/chartmuseum/helm-push)
- [GitHub Actions Secrets](https://docs.github.com/en/actions/security-guides/encrypted-secrets)
