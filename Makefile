# PipeOps Agent Makefile

# Variables
BINARY_NAME := pipeops-vm-agent
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR := ./bin

# Default target
.PHONY: all
all: build

# Help target
.PHONY: help
help:
	@echo "PipeOps Agent - Available Commands:"
	@echo ""
	@echo "  make build       - Build the binary"
	@echo "  make run         - Run the agent locally (tunnel disabled)"
	@echo "  make run-local   - Same as 'make run'"
	@echo "  make run-prod    - Run with production config (tunnel enabled)"
	@echo "  make run-test    - Run with test config"
	@echo "  make test        - Run tests"
	@echo "  make clean       - Clean build artifacts"
	@echo "  make clean-state - Remove agent/cluster ID and tokens (fresh start)"
	@echo "  make clean-all   - Clean everything (build + state)"
	@echo ""
	@echo "Docker:"
	@echo "  make docker      - Build Docker image"
	@echo ""
	@echo "Advanced:"
	@echo "  make release     - Build for all platforms"
	@echo "  make lint        - Run linter"
	@echo ""
	@echo "Note: Edit config-local.yaml to add your cluster token before running"

# Build target
.PHONY: build
build:
	@echo "🔨 Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) cmd/agent/main.go
	@echo "✅ Built: $(BUILD_DIR)/$(BINARY_NAME)"

# Run targets
.PHONY: run
run: build
	@echo "🚀 Running agent locally..."
	@./$(BUILD_DIR)/$(BINARY_NAME) --config config-local.yaml

.PHONY: run-local
run-local: build
	@echo "🚀 Running agent with local development config..."
	@./$(BUILD_DIR)/$(BINARY_NAME) --config config-local.yaml

.PHONY: run-prod
run-prod: build
	@echo "🚀 Running agent with production config..."
	@./$(BUILD_DIR)/$(BINARY_NAME) --config config-production.yaml

.PHONY: run-test
run-test: build
	@echo "🚀 Running agent with test config..."
	@./$(BUILD_DIR)/$(BINARY_NAME) --config config-test.yaml

.PHONY: run-direct
run-direct:
	@echo "🚀 Running agent directly (no build)..."
	@go run cmd/agent/main.go --config config-local.yaml

# Test target
.PHONY: test
test:
	@echo "🧪 Running tests..."
	@go test -v ./...

.PHONY: test-coverage
test-coverage:
	@echo "🧪 Running tests with coverage..."
	@go test -v -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report: coverage.html"

# Docker target
.PHONY: docker
docker:
	@echo "🐳 Building Docker image..."
	@docker build -t pipeops-vm-agent:$(VERSION) .
	@docker tag pipeops-vm-agent:$(VERSION) pipeops-vm-agent:latest
	@echo "✅ Built: pipeops-vm-agent:$(VERSION)"

# Release target - build for multiple platforms
.PHONY: release
release: clean
	@echo "📦 Creating release artifacts..."
	@mkdir -p dist
	@echo "Building linux/amd64..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/$(BINARY_NAME)-linux-amd64 cmd/agent/main.go
	@echo "Building linux/arm64..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/$(BINARY_NAME)-linux-arm64 cmd/agent/main.go
	@echo "Building darwin/amd64..."
	@CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o dist/$(BINARY_NAME)-darwin-amd64 cmd/agent/main.go
	@echo "Building darwin/arm64..."
	@CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/$(BINARY_NAME)-darwin-arm64 cmd/agent/main.go
	@echo "✅ Release artifacts created in dist/"

# Lint target
.PHONY: lint
lint:
	@echo "🔍 Running linter..."
	@go fmt ./...
	@go vet ./...
	@echo "✅ Linting complete"

# Clean target
.PHONY: clean
clean:
	@echo "🧹 Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR) dist coverage.out coverage.html
	@echo "✅ Clean complete"

# Clean all state files (agent ID, cluster ID, tokens)
.PHONY: clean-state
clean-state:
	@echo "🧹 Cleaning agent state files..."
	@rm -rf tmp/agent-state.yaml 2>/dev/null || true
	@rm -f .pipeops-agent-state.yaml 2>/dev/null || true
	@rm -f .pipeops-agent-id 2>/dev/null || true
	@rm -f .pipeops-cluster-id 2>/dev/null || true
	@rm -f .pipeops-cluster-token 2>/dev/null || true
	@rm -f /var/lib/pipeops/agent-state.yaml 2>/dev/null || true
	@rm -f /var/lib/pipeops/agent-id 2>/dev/null || true
	@rm -f /var/lib/pipeops/cluster-id 2>/dev/null || true
	@rm -f /var/lib/pipeops/cluster-token 2>/dev/null || true
	@rm -f /etc/pipeops/agent-state.yaml 2>/dev/null || true
	@rm -f /etc/pipeops/agent-id 2>/dev/null || true
	@rm -f /etc/pipeops/cluster-id 2>/dev/null || true
	@rm -f /etc/pipeops/cluster-token 2>/dev/null || true
	@echo "✅ All state files removed - agent will register fresh on next run"

# Clean everything (build artifacts + state)
.PHONY: clean-all
clean-all: clean clean-state
	@echo "✅ Complete cleanup done"
