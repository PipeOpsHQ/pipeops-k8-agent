# PipeOps Agent Makefile

# Variables
BINARY_NAME := pipeops-agent
PACKAGE := github.com/pipeops/pipeops-vm-agent
VERSION := $(shell git describe --tags --always --dirty)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
COMMIT := $(shell git rev-parse --short HEAD)

# Go build flags
LDFLAGS := -ldflags="-w -s -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.commit=$(COMMIT)"
BUILD_FLAGS := -a -installsuffix cgo

# Docker settings
DOCKER_REGISTRY := pipeops
DOCKER_IMAGE := $(DOCKER_REGISTRY)/agent
DOCKER_TAG := $(VERSION)

# Directories
BUILD_DIR := ./bin
DIST_DIR := ./dist

# Default target
.PHONY: all
all: clean build

# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build       - Build the binary"
	@echo "  build-linux - Build Linux binary"
	@echo "  docker      - Build Docker image"
	@echo "  push        - Push Docker image"
	@echo "  test        - Run tests"
	@echo "  lint        - Run linter"
	@echo "  clean       - Clean build artifacts"
	@echo "  deps        - Download dependencies"
	@echo "  generate    - Generate code (if any)"
	@echo "  install     - Install the binary"
	@echo "  release     - Create release artifacts"

# Build targets
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) cmd/agent/main.go

.PHONY: build-linux
build-linux:
	@echo "Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux cmd/agent/main.go

# Docker targets
.PHONY: docker
docker:
	@echo "Building Docker image $(DOCKER_IMAGE):$(DOCKER_TAG)..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	docker tag $(DOCKER_IMAGE):$(DOCKER_TAG) $(DOCKER_IMAGE):latest

.PHONY: push
push: docker
	@echo "Pushing Docker image $(DOCKER_IMAGE):$(DOCKER_TAG)..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):latest

# Test targets
.PHONY: test
test:
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...

.PHONY: test-integration
test-integration:
	@echo "Running integration tests..."
	go test -v -tags=integration ./...

# Linting
.PHONY: lint
lint:
	@echo "Running linter..."
	golangci-lint run ./...

.PHONY: fmt
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Dependencies
.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

.PHONY: deps-update
deps-update:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

# Code generation
.PHONY: generate
generate:
	@echo "Generating code..."
	go generate ./...

# Installation
.PHONY: install
install: build
	@echo "Installing $(BINARY_NAME)..."
	go install $(LDFLAGS) cmd/agent/main.go

# Release
.PHONY: release
release: clean
	@echo "Creating release artifacts..."
	@mkdir -p $(DIST_DIR)
	
	# Build for multiple platforms
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 cmd/agent/main.go
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 cmd/agent/main.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 cmd/agent/main.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 cmd/agent/main.go
	
	# Create checksums
	cd $(DIST_DIR) && sha256sum * > checksums.txt

# Development targets
.PHONY: dev
dev:
	@echo "Starting development server..."
	go run cmd/agent/main.go --log-level=debug

.PHONY: dev-docker
dev-docker:
	@echo "Building and running in Docker..."
	docker build -t $(DOCKER_IMAGE):dev .
	docker run --rm -it $(DOCKER_IMAGE):dev

# Kubernetes targets
.PHONY: k8s-deploy
k8s-deploy:
	@echo "Deploying to Kubernetes..."
	kubectl apply -f deployments/agent.yaml

.PHONY: k8s-delete
k8s-delete:
	@echo "Removing from Kubernetes..."
	kubectl delete -f deployments/agent.yaml

.PHONY: k8s-logs
k8s-logs:
	@echo "Showing agent logs..."
	kubectl logs -f deployment/pipeops-agent -n pipeops-system

# Utility targets
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	rm -rf $(DIST_DIR)
	rm -f coverage.out

.PHONY: version
version:
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Commit: $(COMMIT)"

# Install development tools
.PHONY: tools
tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/swaggo/swag/cmd/swag@latest

# Security scan
.PHONY: security
security:
	@echo "Running security scan..."
	gosec ./...

# Vulnerability check
.PHONY: vuln-check
vuln-check:
	@echo "Checking for vulnerabilities..."
	govulncheck ./...
