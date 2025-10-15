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
	@echo "  make build         - Build the binary"
	@echo "  make run           - Run the agent locally (tunnel disabled)"
	@echo "  make run-local     - Same as 'make run'"
	@echo "  make run-prod      - Run with production config (tunnel enabled)"
	@echo "  make run-test      - Run with test config"
	@echo "  make test          - Run tests"
	@echo "  make clean         - Clean build artifacts"
	@echo "  make clean-state   - Remove agent/cluster ID and tokens (fresh start)"
	@echo "  make clean-all     - Clean everything (build + state)"
	@echo ""
	@echo "Development:"
	@echo "  make generate-token - Generate mock ServiceAccount token for local dev"
	@echo ""
	@echo "Docker:"
	@echo "  make docker        - Build Docker image"
	@echo ""
	@echo "Advanced:"
	@echo "  make release       - Build for all platforms"
	@echo "  make lint          - Run linter"
	@echo ""
	@echo "Quick Start (Local Development):"
	@echo "  1. make generate-token   # Generate mock token"
	@echo "  2. make run              # Run the agent"

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

# Generate mock ServiceAccount token for local development
.PHONY: generate-token
generate-token:
	@echo "🔧 Generating mock ServiceAccount token for local development..."
	@bash scripts/generate-mock-token.sh
	@echo ""
	@echo "📝 Token saved to tmp/agent-state.yaml"
	@echo "🚀 You can now run: make run"

# Clean everything (build artifacts + state)
.PHONY: clean-all
clean-all: clean clean-state
	@echo "✅ Complete cleanup done"

# ====================================================
# Minikube Testing Targets
# ====================================================

MINIKUBE_PROFILE ?= pipeops-agent
KUBE_NAMESPACE ?= pipeops-system
IMAGE_NAME ?= pipeops-vm-agent
IMAGE_TAG ?= dev-$(shell git rev-parse --short HEAD 2>/dev/null || echo "latest")
MINIKUBE_KUBECTL = minikube -p $(MINIKUBE_PROFILE) kubectl --

# Check if Minikube is running
.PHONY: minikube-check
minikube-check:
	@echo "Checking Minikube status..."
	@if ! command -v minikube &> /dev/null; then \
		echo "ERROR: Minikube not found. Install from: https://minikube.sigs.k8s.io/docs/start/"; \
		exit 1; \
	fi
	@if ! minikube status -p $(MINIKUBE_PROFILE) 2>/dev/null | grep -q "Running"; then \
		echo "WARNING: Minikube cluster '$(MINIKUBE_PROFILE)' not running"; \
		echo "Run 'make minikube-start' to start it"; \
		exit 1; \
	fi
	@echo "Minikube cluster '$(MINIKUBE_PROFILE)' is running"

# Start Minikube cluster
.PHONY: minikube-start
minikube-start:
	@echo "Starting Minikube cluster..."
	@if ! command -v minikube &> /dev/null; then \
		echo "ERROR: Minikube not found. Install from: https://minikube.sigs.k8s.io/docs/start/"; \
		exit 1; \
	fi
	@if minikube status -p $(MINIKUBE_PROFILE) 2>/dev/null | grep -q "Running"; then \
		echo "Minikube cluster '$(MINIKUBE_PROFILE)' already running"; \
	else \
		minikube start -p $(MINIKUBE_PROFILE) --cpus=2 --memory=1800 --driver=docker; \
		echo "Minikube cluster '$(MINIKUBE_PROFILE)' started"; \
	fi
	@echo ""
	@echo "Cluster Info:"
	@$(MINIKUBE_KUBECTL) version --client
	@$(MINIKUBE_KUBECTL) get nodes

# Stop Minikube cluster
.PHONY: minikube-stop
minikube-stop:
	@echo "Stopping Minikube cluster..."
	@minikube stop -p $(MINIKUBE_PROFILE)
	@echo "Minikube cluster stopped"

# Delete Minikube cluster
.PHONY: minikube-delete
minikube-delete:
	@echo "Deleting Minikube cluster..."
	@minikube delete -p $(MINIKUBE_PROFILE)
	@echo "Minikube cluster deleted"

# Build Docker image and load into Minikube
.PHONY: minikube-build
minikube-build: minikube-check
	@echo "Building Docker image for Minikube..."
	@eval $$(minikube -p $(MINIKUBE_PROFILE) docker-env) && \
		docker build -t $(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile .
	@echo "Image $(IMAGE_NAME):$(IMAGE_TAG) built in Minikube"

# Create namespace and RBAC resources
.PHONY: minikube-rbac
minikube-rbac: minikube-check
	@echo "Creating namespace and RBAC resources..."
	@$(MINIKUBE_KUBECTL) create namespace $(KUBE_NAMESPACE) --dry-run=client -o yaml | $(MINIKUBE_KUBECTL) apply -f - 2>/dev/null || true
	@$(MINIKUBE_KUBECTL) apply -f deployments/minikube-rbac.yaml
	@echo "RBAC resources created"

# Create ConfigMap with test configuration
.PHONY: minikube-config
minikube-config: minikube-check
	@echo "Creating test configuration..."
	@$(MINIKUBE_KUBECTL) apply -f deployments/minikube-config.yaml
	@echo "ConfigMap created"

# Create monitoring namespace and ConfigMaps
.PHONY: minikube-monitoring-setup
minikube-monitoring-setup: minikube-check
	@echo "Setting up monitoring ConfigMaps..."
	@$(MINIKUBE_KUBECTL) create namespace pipeops-monitoring --dry-run=client -o yaml | $(MINIKUBE_KUBECTL) apply -f - 2>/dev/null || true
	@$(MINIKUBE_KUBECTL) apply -f deployments/minikube-monitoring-values.yaml
	@echo "Monitoring ConfigMaps created"

# Deploy agent to Minikube
.PHONY: minikube-deploy
minikube-deploy: minikube-check minikube-rbac minikube-config minikube-monitoring-setup
	@echo "Deploying agent to Minikube..."
	@sed 's|image: pipeops-vm-agent:latest|image: $(IMAGE_NAME):$(IMAGE_TAG)|g' deployments/minikube-deployment.yaml | $(MINIKUBE_KUBECTL) apply -f -
	@echo "Agent deployed"
	@echo ""
	@echo "Waiting for agent to be ready..."
	@$(MINIKUBE_KUBECTL) wait --for=condition=available --timeout=60s deployment/pipeops-agent -n $(KUBE_NAMESPACE) || true
	@echo ""
	@$(MAKE) minikube-status

# View agent logs
.PHONY: minikube-logs
minikube-logs: minikube-check
	@echo "Viewing agent logs..."
	@$(MINIKUBE_KUBECTL) logs -n $(KUBE_NAMESPACE) -l app=pipeops-agent --tail=100 -f

# View agent logs (last 50 lines, no follow)
.PHONY: minikube-logs-short
minikube-logs-short: minikube-check
	@$(MINIKUBE_KUBECTL) logs -n $(KUBE_NAMESPACE) -l app=pipeops-agent --tail=50

# Check agent status
.PHONY: minikube-status
minikube-status: minikube-check
	@echo "Agent Status:"
	@echo ""
	@echo "Deployment:"
	@$(MINIKUBE_KUBECTL) get deployment -n $(KUBE_NAMESPACE) pipeops-agent
	@echo ""
	@echo "Pods:"
	@$(MINIKUBE_KUBECTL) get pods -n $(KUBE_NAMESPACE) -l app=pipeops-agent
	@echo ""
	@echo "Events:"
	@$(MINIKUBE_KUBECTL) get events -n $(KUBE_NAMESPACE) --sort-by='.lastTimestamp' | tail -10

# Describe agent pod (for debugging)
.PHONY: minikube-describe
minikube-describe: minikube-check
	@echo "Describing agent pod..."
	@$(MINIKUBE_KUBECTL) describe pod -n $(KUBE_NAMESPACE) -l app=pipeops-agent

# Shell into agent pod
.PHONY: minikube-shell
minikube-shell: minikube-check
	@echo "Opening shell in agent pod..."
	@$(MINIKUBE_KUBECTL) exec -it -n $(KUBE_NAMESPACE) $$($(MINIKUBE_KUBECTL) get pod -n $(KUBE_NAMESPACE) -l app=pipeops-agent -o jsonpath='{.items[0].metadata.name}') -- /bin/sh

# Remove agent deployment
.PHONY: minikube-undeploy
minikube-undeploy: minikube-check
	@echo "Removing agent deployment..."
	@$(MINIKUBE_KUBECTL) delete deployment pipeops-agent -n $(KUBE_NAMESPACE) --ignore-not-found=true
	@echo "Agent deployment removed"

# Clean all Minikube resources
.PHONY: minikube-clean
minikube-clean: minikube-check
	@echo "Cleaning all Minikube resources..."
	@$(MINIKUBE_KUBECTL) delete namespace $(KUBE_NAMESPACE) --ignore-not-found=true
	@$(MINIKUBE_KUBECTL) delete clusterrole pipeops-agent --ignore-not-found=true
	@$(MINIKUBE_KUBECTL) delete clusterrolebinding pipeops-agent --ignore-not-found=true
	@echo "All resources cleaned"

# Complete test workflow: build, deploy, and view logs
.PHONY: minikube-test
minikube-test: minikube-start minikube-build minikube-deploy
	@echo ""
	@echo "Agent deployed successfully!"
	@echo ""
	@echo "Quick commands:"
	@echo "  make minikube-logs        - View live logs"
	@echo "  make minikube-status      - Check deployment status"
	@echo "  make minikube-describe    - Describe pod details"
	@echo "  make minikube-shell       - Open shell in pod"
	@echo "  make minikube-clean       - Remove all resources"
	@echo ""
	@echo "Showing recent logs:"
	@$(MAKE) minikube-logs-short

# Restart agent deployment
.PHONY: minikube-restart
minikube-restart: minikube-check
	@echo "Restarting agent..."
	@$(MINIKUBE_KUBECTL) rollout restart deployment/pipeops-agent -n $(KUBE_NAMESPACE)
	@$(MINIKUBE_KUBECTL) rollout status deployment/pipeops-agent -n $(KUBE_NAMESPACE)
	@echo "Agent restarted"
