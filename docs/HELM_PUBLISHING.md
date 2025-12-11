# Helm Chart Publishing

The PipeOps Agent Helm chart is automatically published to multiple registries via GitHub Actions.

## Supported Registries

### 1. GitHub Container Registry (GHCR) - OCI Format ✅ Enabled by Default

Charts are automatically published to GHCR on every push to `main` or version tag.

**Installation:**
```bash
helm install pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent/pipeops-agent \
  --version 0.1.0 \
  --set agent.pipeops.token="your-token" \
  --set agent.cluster.name="your-cluster"
```

**List available versions:**
```bash
# Using GitHub API
curl -H "Accept: application/vnd.github.v3+json" \
  https://api.github.com/orgs/pipeopshq/packages/container/pipeops-agent%2Fpipeops-agent/versions
```

### 2. ChartMuseum - Traditional Helm Repository 🔧 Optional

ChartMuseum support is optional and can be enabled by configuring repository variables.

**Installation:**
```bash
# Add repository
helm repo add pipeops https://charts.pipeops.io
helm repo update

# Install chart
helm install pipeops-agent pipeops/pipeops-agent \
  --version 0.1.0 \
  --set agent.pipeops.token="your-token" \
  --set agent.cluster.name="your-cluster"
```

**Search for versions:**
```bash
helm search repo pipeops-agent --versions
```

## Quick Setup Guide

### Prerequisites

- GitHub repository with admin access
- GitHub CLI (`gh`) installed and authenticated

### Option 1: Automated Setup (Recommended)

Use the provided setup script:

```bash
# Clone the repository
git clone https://github.com/pipeopshq/pipeops-k8-agent.git
cd pipeops-k8-agent

# Run the setup script
./scripts/setup-chartmuseum.sh
```

The script will:
1. Detect your repository automatically
2. Prompt for ChartMuseum URL
3. Optionally configure authentication
4. Test the connection
5. Set up all required GitHub secrets and variables

### Option 2: Manual Setup

#### Step 1: Set Repository Variable

Go to: **Settings → Secrets and variables → Actions → Variables**

Add:
- **Name:** `CHARTMUSEUM_URL`
- **Value:** `https://charts.pipeops.io` (your ChartMuseum URL)

#### Step 2: Set Repository Secrets (if authentication required)

Go to: **Settings → Secrets and variables → Actions → Secrets**

Add:
- **Name:** `CHARTMUSEUM_USER`
  - **Value:** Your ChartMuseum username
- **Name:** `CHARTMUSEUM_PASSWORD`
  - **Value:** Your ChartMuseum password or API token

#### Step 3: Verify Configuration

Push a commit to `main` or create a version tag to trigger the workflow.

## GitHub CLI Commands

### Set Variables and Secrets via CLI

```bash
# Set ChartMuseum URL (variable)
gh variable set CHARTMUSEUM_URL --body "https://charts.pipeops.io"

# Set ChartMuseum credentials (secrets)
gh secret set CHARTMUSEUM_USER --body "your-username"
gh secret set CHARTMUSEUM_PASSWORD --body "your-password"
```

### List Current Configuration

```bash
# List variables
gh variable list

# List secrets (names only)
gh secret list
```

### Delete Configuration

```bash
# Delete variable
gh variable delete CHARTMUSEUM_URL

# Delete secrets
gh secret delete CHARTMUSEUM_USER
gh secret delete CHARTMUSEUM_PASSWORD
```

## Workflow Overview

### Trigger Conditions

The Helm chart publishing workflow runs when:
- ✅ Code is pushed to `main` branch
- ✅ A version tag (e.g., `v1.0.0`) is created
- ❌ Pull requests (charts are not published on PRs)

### Publishing Pipeline

```
┌─────────────────┐
│   Code Push     │
│  (main / tag)   │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Run Tests &    │
│  Build Binary   │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Package Helm   │
│     Chart       │
└────────┬────────┘
         │
         ├────────────────┬─────────────────┐
         ▼                ▼                 ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│ Publish to   │  │ Publish to   │  │   Create     │
│     GHCR     │  │ ChartMuseum  │  │   Release    │
│  (Always)    │  │ (Optional)   │  │  (Tags only) │
└──────────────┘  └──────────────┘  └──────────────┘
```

### Job: `helm-package`

**Purpose:** Package the Helm chart with the correct version

**Steps:**
1. Extract version from git tag or branch
2. Update `Chart.yaml` with version
3. Run `helm lint` to validate chart
4. Package chart into `.tgz` file
5. Upload artifact for downstream jobs

### Job: `helm-publish`

**Purpose:** Publish chart to GitHub Container Registry

**Steps:**
1. Download packaged chart
2. Login to GHCR using GitHub token
3. Push chart using `helm push` to OCI registry

**Always runs:** Yes (when code is pushed to `main` or tags)

### Job: `helm-publish-chartmuseum`

**Purpose:** Publish chart to ChartMuseum

**Steps:**
1. Download packaged chart
2. Install `helm-push` plugin
3. Push chart using ChartMuseum API (`curl`)

**Conditional:** Only runs if `CHARTMUSEUM_URL` is set

## Chart Versioning Strategy

### Semantic Versioning

Charts follow [SemVer 2.0.0](https://semver.org/):

- **MAJOR** version (e.g., v**1**.0.0) - Incompatible API changes
- **MINOR** version (e.g., v1.**2**.0) - New features, backwards compatible
- **PATCH** version (e.g., v1.2.**3**) - Bug fixes, backwards compatible

### Version Sources

| Branch/Tag | Chart Version | App Version | Example |
|------------|---------------|-------------|---------|
| `main` branch | `0.0.0-main.<short-sha>` | `0.0.0-main.<short-sha>` | `0.0.0-main.abc1234` |
| `v1.2.3` tag | `1.2.3` | `1.2.3` | `1.2.3` |
| `develop` branch | `0.0.0-develop.<short-sha>` | `0.0.0-develop.<short-sha>` | `0.0.0-develop.def5678` |

### Tagging Best Practices

```bash
# Create a new version tag
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0

# Create a pre-release tag
git tag -a v1.0.0-beta.1 -m "Beta release v1.0.0-beta.1"
git push origin v1.0.0-beta.1
```

## Monitoring and Debugging

### View Workflow Runs

```bash
# Open workflow runs in browser
gh workflow view ci.yml --web

# List recent workflow runs
gh run list --workflow=ci.yml --limit 5

# Watch the latest workflow run
gh run watch
```

### View Job Logs

```bash
# View logs for specific job
gh run view --log --job helm-publish-chartmuseum

# Download all logs
gh run download <run-id> --name logs
```

### Common Issues

#### 1. ChartMuseum Job Skipped

**Symptom:** `helm-publish-chartmuseum` job doesn't run

**Cause:** `CHARTMUSEUM_URL` variable is not set

**Solution:**
```bash
gh variable set CHARTMUSEUM_URL --body "https://charts.pipeops.io"
```

#### 2. Authentication Failure (HTTP 401)

**Symptom:** `❌ Failed to publish chart (HTTP 401)`

**Cause:** Incorrect credentials

**Solution:**
```bash
# Update credentials
gh secret set CHARTMUSEUM_USER --body "correct-username"
gh secret set CHARTMUSEUM_PASSWORD --body "correct-password"

# Test manually
curl -u "user:pass" https://charts.pipeops.io/api/charts
```

#### 3. Chart Already Exists (HTTP 409)

**Symptom:** `❌ Failed to publish chart (HTTP 409)`

**Cause:** ChartMuseum doesn't allow overwriting existing versions

**Solution:**
```bash
# Option 1: Enable overwrite in ChartMuseum
chartmuseum --allow-overwrite ...

# Option 2: Delete existing version
curl -X DELETE https://charts.pipeops.io/api/charts/pipeops-agent/0.1.0

# Option 3: Increment version
# Edit helm/pipeops-agent/Chart.yaml and bump version
```

## Security Considerations

### Best Practices

1. **Use HTTPS:** Always use `https://` for ChartMuseum URL
2. **Rotate Secrets:** Regularly rotate `CHARTMUSEUM_PASSWORD`
3. **Least Privilege:** Use a dedicated CI/CD user with minimal permissions
4. **Network Security:** Restrict ChartMuseum access via IP allowlists
5. **Audit Logs:** Enable logging in ChartMuseum to track uploads

### Secret Management

```bash
# Secrets are encrypted by GitHub
# Never commit secrets to the repository
# Rotate secrets regularly

# Rotate ChartMuseum password
gh secret set CHARTMUSEUM_PASSWORD --body "new-secure-password"
```

## Testing Locally

### Test Chart Packaging

```bash
# Lint the chart
helm lint helm/pipeops-agent/

# Package the chart
helm package helm/pipeops-agent/ --destination ./dist/

# Verify package
tar -tzf dist/pipeops-agent-*.tgz
```

### Test Chart Publishing

```bash
# Push to ChartMuseum manually
curl -X POST \
  --data-binary "@dist/pipeops-agent-0.1.0.tgz" \
  -u "user:pass" \
  "https://charts.pipeops.io/api/charts"

# Verify chart is available
curl https://charts.pipeops.io/api/charts | jq
```

## Advanced Configuration

### Custom ChartMuseum Configuration

If you need advanced ChartMuseum features:

1. **Multi-tenancy:** Use different chart repositories per environment
   ```bash
   gh variable set CHARTMUSEUM_URL --body "https://charts.pipeops.io/dev"
   ```

2. **Context Path:** ChartMuseum behind reverse proxy
   ```bash
   gh variable set CHARTMUSEUM_URL --body "https://example.com/chartmuseum"
   ```

3. **Custom Headers:** Modify `.github/workflows/ci.yml` to add headers
   ```bash
   curl -X POST \
     -H "X-Custom-Header: value" \
     --data-binary "@$chart" \
     ...
   ```

## References

- [Helm Documentation](https://helm.sh/docs/)
- [ChartMuseum Documentation](https://chartmuseum.com)
- [GitHub Actions Documentation](https://docs.github.com/en/actions)
- [OCI Registry Support](https://helm.sh/docs/topics/registries/)

## Support

For help with Helm chart publishing:
- 📖 [ChartMuseum Guide](./CHARTMUSEUM.md)
- 🐛 [Report Issues](https://github.com/pipeopshq/pipeops-k8-agent/issues)
- 💬 [Discussions](https://github.com/pipeopshq/pipeops-k8-agent/discussions)
