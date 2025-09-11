# Helm Chart Publishing Fix

## Issue Identified

The Helm chart publishing was failing with:
```
Error: invalid_reference: invalid repository
```

## Root Cause

The OCI registry path was incorrectly formatted. GitHub Container Registry (GHCR) expects a specific format for Helm charts.

## Fix Applied

### 1. Corrected OCI Registry Path

**Before (incorrect):**
```bash
helm push "$chart" oci://${{ env.REGISTRY }}/${{ github.repository_owner }}/charts
```

**After (correct):**
```bash
helm push "$chart" oci://${{ env.REGISTRY }}/${{ github.repository_owner }}/pipeops-agent
```

### 2. Updated Installation Instructions

**Before:**
```bash
helm install pipeops-agent oci://ghcr.io/pipeopsqh/charts/pipeops-agent
```

**After:**
```bash
helm install pipeops-agent oci://ghcr.io/pipeopsqh/pipeops-agent
```

### 3. Added Better Error Handling

Added debugging and validation to the Helm publishing step:
- List available packages before pushing
- Show target registry path
- Validate files exist before attempting push
- Improved error messages

## GitHub Container Registry (GHCR) Format

For Helm charts in GHCR, the correct format is:
- **Registry**: `ghcr.io`
- **Repository**: `{owner}/{chart-name}`
- **Full OCI Path**: `oci://ghcr.io/{owner}/{chart-name}`

**NOT**: `oci://ghcr.io/{owner}/charts/{chart-name}` ❌
**NOT**: `oci://ghcr.io/{owner}` ❌

## Testing the Fix

### Local Testing
```bash
# Test chart packaging
./test-helm.sh

# Manual package test
helm package helm/pipeops-agent/ --destination ./test-packages/
helm push ./test-packages/pipeops-agent-*.tgz oci://ghcr.io/pipeopsqh
```

### CI/CD Testing
1. Push changes to main branch
2. Monitor the `helm-publish` job
3. Check GitHub Packages for the published chart

## Verification

After the fix, you should see:
1. Successful Helm chart packaging in the `helm-package` job
2. Successful Helm chart publishing in the `helm-publish` job
3. Chart available at: `oci://ghcr.io/pipeopsqh/pipeops-agent`

## Installation After Fix

```bash
# Install from GHCR
helm install pipeops-agent oci://ghcr.io/pipeopsqh/pipeops-agent \
  --version latest \
  --set agent.pipeops.token="your-pipeops-token" \
  --set agent.cluster.name="your-cluster-name"
```

## Additional Improvements Made

1. **Better Logging**: Added debug output to show what's being pushed
2. **Validation**: Check if chart files exist before attempting push
3. **Error Handling**: Clearer error messages for troubleshooting
4. **Documentation**: Updated all references to use correct OCI path

## GitHub Container Registry Permissions

Ensure the GitHub token has the following permissions:
- `packages: write` (already configured in workflow)
- `contents: read` (already configured in workflow)

## Related Files Changed

- `.github/workflows/ci.yml`: Fixed OCI registry path and added debugging
- `test-helm.sh`: Updated with correct registry format
- Documentation: Updated installation instructions

## Next Steps

1. Monitor the next CI/CD run to confirm the fix works
2. Test chart installation from GHCR
3. Update any external documentation with the correct installation commands
