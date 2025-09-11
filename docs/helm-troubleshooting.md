# Helm Chart CI/CD Troubleshooting

This document helps troubleshoot common issues with the Helm chart CI/CD pipeline.

## Common Issues

### 1. "Chart.yaml not found" Error

**Symptoms:**
```
sed: can't read helm/pipeops-agent/Chart.yaml: No such file or directory
Error: Process completed with exit code 2.
```

**Causes:**
- Files not checked out properly in CI
- Working directory is different than expected
- Files not committed to git
- `.gitignore` excluding Helm files

**Debugging Steps:**
1. Check if files are committed: `git ls-files helm/`
2. Verify local structure: `find helm/ -type f`
3. Check CI logs for directory listing
4. Verify checkout step includes all files

### 2. Helm Lint Failures

**Symptoms:**
```
[ERROR] Chart.yaml: ... error message
```

**Solutions:**
- Run `helm lint helm/pipeops-agent/` locally
- Check template syntax
- Verify values.yaml structure
- Ensure all required fields in Chart.yaml

### 3. Permission Errors

**Symptoms:**
```
permission denied: packages.write
```

**Solutions:**
- Check repository permissions
- Verify GITHUB_TOKEN has packages:write scope
- Ensure job has correct permissions block

### 4. Version Update Failures

**Symptoms:**
```
sed: command not found or syntax error
```

**Solutions:**
- Verify sed command syntax for Linux vs macOS
- Check version extraction logic
- Ensure git tags exist for version calculation

## Local Testing

Always test locally before pushing:

```bash
# Test the chart
./test-helm.sh

# Manual testing
helm lint helm/pipeops-agent/
helm template test helm/pipeops-agent/ --set agent.pipeops.token="test"
helm package helm/pipeops-agent/ --destination /tmp/
```

## CI/CD Debugging

The workflow includes extensive debugging output. Check logs for:
- Directory listings
- File existence checks
- Current working directory
- Git repository state

## Quick Fixes

1. **Ensure files are committed:**
   ```bash
   git add helm/
   git commit -m "Add Helm chart files"
   git push
   ```

2. **Reset Chart.yaml if corrupted:**
   ```bash
   git checkout helm/pipeops-agent/Chart.yaml
   ```

3. **Test packaging locally:**
   ```bash
   helm package helm/pipeops-agent/ --destination /tmp/test/
   ```
