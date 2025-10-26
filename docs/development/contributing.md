# Contributing

Welcome to the PipeOps Kubernetes Agent project! We're excited that you want to contribute. This guide will help you get started with contributing to the project.

## Quick Start

1. **Fork the repository** on GitHub
2. **Clone your fork** locally
3. **Set up the development environment**
4. **Make your changes**
5. **Test your changes**
6. **Submit a pull request**

## Prerequisites

Before you begin, ensure you have the following installed:

- **Go 1.21+** - [Install Go](https://golang.org/dl/)
- **Docker 20.0+** - [Install Docker](https://docs.docker.com/get-docker/)
- **kubectl** - [Install kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/)
- **Kind or Minikube** - For local Kubernetes clusters
- **Make** - For build automation
- **Git** - For version control

## Development Environment Setup

### 1. Fork and Clone

```bash
# Fork the repository on GitHub, then clone your fork
git clone https://github.com/YOUR-USERNAME/pipeops-k8-agent.git
cd pipeops-k8-agent

# Add upstream remote
git remote add upstream https://github.com/PipeOpsHQ/pipeops-k8-agent.git
```

### 2. Install Dependencies

```bash
# Install Go dependencies
go mod download

# Install development tools
make install-tools

# Verify installation
make verify
```

### 3. Set Up Local Kubernetes Cluster

=== "Kind (Recommended)"

    ```bash
    # Create a local cluster with Kind
    kind create cluster --config=deployments/kind-config.yaml
    
    # Verify cluster is running
    kubectl cluster-info
    ```

=== "Minikube"

    ```bash
    # Start Minikube cluster
    minikube start --driver=docker
    
    # Enable required addons
    minikube addons enable ingress
    minikube addons enable metrics-server
    ```

### 4. Run Local Development

```bash
# Build the agent
make build

# Run in development mode
make dev

# Or run with custom configuration
./bin/pipeops-agent start --config=config-local.yaml --dev-mode
```

## Project Structure

Understanding the project layout will help you navigate and contribute effectively:

```text
pipeops-k8-agent/
├── cmd/                    # Application entry points
│   └── agent/
│       └── main.go        # Main agent binary
├── internal/              # Private application code
│   ├── agent/             # Core agent logic
│   ├── controlplane/      # Control plane communication
│   ├── monitoring/        # Monitoring stack management
│   ├── server/            # HTTP/WebSocket server
│   └── tunnel/            # Secure tunnel implementation
├── pkg/                   # Public libraries
│   ├── k8s/              # Kubernetes utilities
│   ├── state/            # State management
│   └── types/            # Common types
├── deployments/           # Kubernetes manifests
├── docs/                  # Documentation
├── helm/                  # Helm charts
├── scripts/              # Build and utility scripts
├── Dockerfile            # Container build file
├── Makefile             # Build automation
└── go.mod               # Go module definition
```

## Building and Testing

### Building

```bash
# Build for current platform
make build

# Build for all platforms
make build-all

# Build Docker image
make docker-build

# Build with custom version
make build VERSION=v2.1.0-dev
```

### Testing

```bash
# Run all tests
make test

# Run unit tests only
make test-unit

# Run integration tests
make test-integration

# Run tests with coverage
make test-coverage

# Run specific test
go test -v ./internal/agent/...
```

### Code Quality

```bash
# Run linters
make lint

# Format code
make fmt

# Check for security issues
make security-check

# Generate mocks (if needed)
make generate
```

## Development Workflow

### 1. Creating a Feature Branch

```bash
# Update your fork
git fetch upstream
git checkout main
git merge upstream/main

# Create feature branch
git checkout -b feature/your-feature-name

# Or for bug fixes
git checkout -b fix/issue-description
```

### 2. Making Changes

Follow these guidelines when making changes:

#### Code Style

- Follow [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Use `gofmt` for formatting
- Add comments for public functions and complex logic
- Keep functions small and focused

#### Commit Messages

Use conventional commit format:

```text
<type>(<scope>): <description>

<body>

<footer>
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes (formatting, etc.)
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `chore`: Maintenance tasks

Examples:
```bash
git commit -m "feat(monitoring): add custom metrics support"
git commit -m "fix(agent): resolve connection timeout issue"
git commit -m "docs: update installation guide"
```

### 3. Testing Your Changes

Before submitting, ensure your changes work correctly:

```bash
# Test locally
make test
make lint

# Test with local cluster
make deploy-local
kubectl get pods -n pipeops-system

# Manual testing
./bin/pipeops-agent start --dev-mode --log-level=debug
```

### 4. Submitting Pull Request

```bash
# Push your changes
git push origin feature/your-feature-name

# Create pull request on GitHub
# Fill out the PR template completely
```

## Testing Guidelines

### Unit Tests

Write unit tests for all new functionality:

```go
// Example unit test
func TestAgent_Start(t *testing.T) {
    tests := []struct {
        name    string
        config  *Config
        wantErr bool
    }{
        {
            name: "valid configuration",
            config: &Config{
                Token: "test-token",
                Endpoint: "https://api.pipeops.io",
            },
            wantErr: false,
        },
        {
            name: "missing token",
            config: &Config{
                Endpoint: "https://api.pipeops.io",
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            agent := NewAgent(tt.config)
            err := agent.Start()
            if (err != nil) != tt.wantErr {
                t.Errorf("Agent.Start() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Integration Tests

Integration tests verify component interactions:

```go
func TestAgent_Integration(t *testing.T) {
    // Set up test cluster
    cluster := setupTestCluster(t)
    defer cluster.Teardown()

    // Create agent with test configuration
    config := &Config{
        Token: "test-token",
        Endpoint: cluster.ControlPlaneURL(),
    }
    agent := NewAgent(config)

    // Test agent lifecycle
    err := agent.Start()
    require.NoError(t, err)

    // Verify agent is running
    status := agent.Status()
    assert.Equal(t, StatusRunning, status.State)

    // Clean up
    err = agent.Stop()
    require.NoError(t, err)
}
```

### End-to-End Tests

E2E tests verify complete user workflows:

```bash
# Run E2E tests
make test-e2e

# Run specific E2E test
go test -v ./test/e2e -run TestInstallAndDeploy
```

## Documentation

### Code Documentation

- Document all public functions and types
- Use godoc format for documentation comments
- Include examples in documentation

```go
// Agent represents the PipeOps Kubernetes Agent.
// It manages the connection to the control plane and orchestrates
// monitoring and deployment operations within the cluster.
//
// Example:
//   config := &Config{Token: "token", Endpoint: "https://api.pipeops.io"}
//   agent := NewAgent(config)
//   if err := agent.Start(); err != nil {
//       log.Fatal(err)
//   }
type Agent struct {
    config *Config
    // ... other fields
}
```

### User Documentation

When adding new features, update the relevant documentation:

- Update command documentation in `docs/commands/`
- Add configuration examples to `docs/getting-started/configuration.md`
- Update the main README if needed

## Security Considerations

### Secure Coding Practices

- Validate all user inputs
- Use secure defaults
- Handle secrets properly (never log them)
- Follow principle of least privilege

### Security Testing

```bash
# Run security scans
make security-check

# Check for vulnerabilities in dependencies
go list -json -m all | nancy sleuth

# Static analysis
gosec ./...
```

### Handling Sensitive Data

```go
// Good: Use types that prevent accidental logging
type Token struct {
    value string
}

func (t Token) String() string {
    return "[REDACTED]"
}

// Good: Clear sensitive data from memory
func clearToken(token []byte) {
    for i := range token {
        token[i] = 0
    }
}
```

## Debugging

### Debug Mode

Run the agent in debug mode for detailed logging:

```bash
# Enable debug logging
./bin/pipeops-agent start --log-level=debug --dev-mode

# With additional tracing
PIPEOPS_TRACE=true ./bin/pipeops-agent start --dev-mode
```

### Using Delve Debugger

```bash
# Install Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Debug the agent
dlv debug ./cmd/agent -- start --dev-mode

# Set breakpoints and inspect variables
(dlv) break main.main
(dlv) continue
```

### Profiling

```bash
# Enable profiling
./bin/pipeops-agent start --pprof-addr=localhost:6060

# View CPU profile
go tool pprof http://localhost:6060/debug/pprof/profile

# View memory profile
go tool pprof http://localhost:6060/debug/pprof/heap
```

## Release Process

### Versioning

We follow [Semantic Versioning](https://semver.org/):

- **MAJOR** version for incompatible API changes
- **MINOR** version for backwards-compatible functionality
- **PATCH** version for backwards-compatible bug fixes

### Creating a Release

1. **Update version** in relevant files
2. **Update CHANGELOG.md** with release notes  
3. **Create release PR** with version bump
4. **Tag release** after PR merge
5. **GitHub Actions** will build and publish automatically

```bash
# Create release tag
git tag -a v2.1.0 -m "Release v2.1.0"
git push upstream v2.1.0
```

## Community Guidelines

### Code of Conduct

We follow the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). Please read and follow it in all interactions.

### Getting Help

- **GitHub Issues**: For bugs and feature requests
- **GitHub Discussions**: For questions and general discussion
- **Discord**: Join our [community Discord](https://discord.gg/pipeops)

### Communication

- Be respectful and inclusive
- Provide constructive feedback
- Ask questions when unclear
- Share knowledge and help others

## Pull Request Checklist

Before submitting your PR, ensure:

- [ ] Code follows project style guidelines
- [ ] All tests pass (`make test`)
- [ ] Linter passes (`make lint`)
- [ ] Documentation is updated if needed
- [ ] Commit messages follow conventional format
- [ ] PR description explains the changes
- [ ] Related issues are linked
- [ ] Breaking changes are documented

## Recognition

We appreciate all contributions! Contributors will be:

- Listed in the project's AUTHORS file
- Mentioned in release notes for significant contributions
- Eligible for PipeOps swag and recognition
- Invited to join our contributor Discord channel

## Getting Started with Your First Contribution

### Good First Issues

Look for issues labeled with:
- `good first issue` - Beginner-friendly issues
- `help wanted` - Issues where we need community help
- `documentation` - Documentation improvements

### Areas for Contribution

- **Bug fixes** - Help resolve reported issues
- **Feature development** - Implement new capabilities
- **Documentation** - Improve guides and references
- **Testing** - Add test coverage and E2E tests
- **Performance** - Optimize resource usage
- **Security** - Enhance security measures

## Contact

For questions about contributing:

- **Maintainers**: Listed in [MAINTAINERS.md](MAINTAINERS.md)
- **Email**: [contributors@pipeops.io](mailto:contributors@pipeops.io)
- **Discord**: [PipeOps Community](https://discord.gg/pipeops)

---

Thank you for contributing to PipeOps Kubernetes Agent!