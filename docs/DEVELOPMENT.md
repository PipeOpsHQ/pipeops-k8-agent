# Development Guide

This guide covers local development setup and best practices for the PipeOps Agent.

## Quick Start

### Prerequisites

- Go 1.23+
- Docker
- Docker Compose (optional)
- Make

### Local Development

1. **Clone and setup**:

   ```bash
   git clone <repo-url>
   cd pipeops-vm-agent
   make deps
   ```

2. **Run with live reload**:

   ```bash
   make dev-watch
   ```

   This will automatically restart the agent when you make changes.

3. **Run normally**:

   ```bash
   make dev
   ```

### Docker Development

1. **Using development Dockerfile**:

   ```bash
   make dev-docker
   ```

2. **Using docker-compose**:

   ```bash
   make dev-compose
   ```

## Development Tools

### Air (Live Reload)

Air provides automatic rebuilding and restarting when files change:

- Configuration: `.air.toml`
- Excludes test files and unnecessary directories
- Builds to `./tmp/main` for fast rebuilds

### Make Targets

| Target | Description |
|--------|-------------|
| `make dev` | Run development server with debug logging |
| `make dev-watch` | Run with live reload (installs air if needed) |
| `make dev-docker` | Run in development Docker container |
| `make dev-compose` | Run with docker-compose |
| `make build` | Build production binary |
| `make test` | Run all tests |
| `make lint` | Run linter |
| `make clean` | Clean build artifacts |

## Docker Setup

### Development Dockerfile

`Dockerfile.dev` is optimized for development:

- Based on golang:1.23-alpine
- Includes air for live reload
- Mounts source code as volume
- Faster rebuilds

### Production Dockerfile

`Dockerfile` is optimized for production:

- Multi-stage build
- Minimal final image
- Security hardening
- Build cache optimization

### Build Context Optimization

`.dockerignore` excludes unnecessary files:

- Git files and directories
- Documentation
- Test files
- Build artifacts
- CI/CD files

This reduces build context size and improves build speed.

## Testing

### Unit Tests

```bash
make test
```

### Integration Tests

```bash
make test-integration
```

### Coverage

Tests generate `coverage.out` which is uploaded to Codecov in CI.

## Code Quality

### Linting

```bash
make lint
```

Uses golangci-lint with standard Go linting rules.

### Formatting

```bash
make fmt
```

### Security Scanning

```bash
make security      # gosec
make vuln-check    # govulncheck
```

## Build Optimization

### Cache Layers

The Dockerfile is optimized for Docker layer caching:

1. Go modules downloaded first (changes less frequently)
2. Source code copied and built (changes more frequently)
3. Final runtime image is minimal

### Parallel Builds

CI uses Docker Buildx with:

- Build cache from registry
- Multi-platform builds (linux/amd64, linux/arm64)
- Parallel build jobs

## CI/CD Pipeline

### Triggers

- **Push to main**: Full build, test, security scan, release
- **Pull requests**: Build, test, security scan only
- **Tags**: Full release with multi-platform builds

### Artifacts

- Docker images (multi-platform)
- Helm chart package
- GitHub release with binaries
- Documentation deployment

### Caching

- Go module cache
- Docker layer cache
- Test cache

## Performance Tips

### Local Dev

1. Use `make dev-watch` for fastest feedback
2. Use `make dev-docker` for containerized testing
3. Use `make dev-compose` for full environment

### Docker Builds

1. `.dockerignore` reduces build context
2. Multi-stage build optimizes final image size
3. Build cache speeds up rebuilds

### CI/CD

1. Parallel jobs reduce total time
2. Build cache prevents redundant work
3. Conditional steps skip unnecessary work

## Troubleshooting

### Common Issues

1. **Slow Docker builds**:
   - Check `.dockerignore` is present
   - Verify build cache is working
   - Use development Dockerfile for iteration

2. **Air not working**:
   - Check `.air.toml` configuration
   - Ensure Go files are in included directories
   - Verify excluded patterns

3. **Module issues**:
   - Run `make deps` to refresh
   - Check Go version compatibility
   - Clear module cache: `go clean -modcache`

### Debug Mode

Enable debug logging:

```bash
make dev  # Already includes --log-level=debug
```

Or set environment variable:

```bash
LOG_LEVEL=debug go run cmd/agent/main.go
```

## Contributing

1. Create feature branch
2. Make changes with tests
3. Run `make lint test` locally
4. Create pull request
5. CI will validate all changes

### Code Style

- Follow standard Go conventions
- Use meaningful variable names
- Add comments for complex logic
- Keep functions focused and small

### Commit Messages

Use conventional commits:

- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation
- `refactor:` for code refactoring
- `test:` for test changes

## Resources

- [Go Documentation](https://golang.org/doc/)
- [Docker Best Practices](https://docs.docker.com/develop/dev-best-practices/)
- [Kubernetes Documentation](https://kubernetes.io/docs/)
- [Helm Documentation](https://helm.sh/docs/)
