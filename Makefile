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
	@echo "╔════════════════════════════════════════════════════════════════╗"
	@echo "║         PipeOps Agent - Available Commands                    ║"
	@echo "╚════════════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "📦 LOCAL DEVELOPMENT:"
	@echo "  make build              - Build the binary"
	@echo "  make run                - Run the agent locally"
	@echo "  make run-prod           - Run with production config"
	@echo "  make test               - Run tests"
	@echo "  make generate-token     - Generate mock ServiceAccount token"
	@echo ""
	@echo "🐳 DOCKER:"
	@echo "  make docker             - Build Docker image"
	@echo "  make k8s-build          - Build Docker image for Kubernetes"
	@echo ""
	@echo "☸️  KUBERNETES (Any Cluster):"
	@echo "  make k8s-check          - Check cluster connectivity"
	@echo "  make k8s-deploy-all     - Complete deployment (namespace + RBAC + agent)"
	@echo "  make k8s-deploy         - Deploy agent only"
	@echo "  make k8s-status         - Check agent status"
	@echo "  make k8s-logs           - View live logs"
	@echo "  make k8s-logs-tail      - View last 50 log lines"
	@echo "  make k8s-describe       - Describe agent pod"
	@echo "  make k8s-shell          - Open shell in agent pod"
	@echo "  make k8s-restart        - Restart agent deployment"
	@echo "  make k8s-undeploy       - Remove agent deployment"
	@echo "  make k8s-clean          - Clean all resources"
	@echo ""
	@echo "🎯 MINIKUBE (Quick Start):"
	@echo "  make minikube-deploy-all - Complete Minikube setup + deploy"
	@echo "  make minikube-start      - Start Minikube cluster"
	@echo "  make minikube-build      - Build image in Minikube"
	@echo "  make minikube-logs       - View live logs"
	@echo "  make minikube-status     - Check agent status"
	@echo "  make minikube-stop       - Stop Minikube cluster"
	@echo "  make minikube-delete     - Delete Minikube cluster"
	@echo "  make minikube-clean      - Remove all agent resources"
	@echo ""
	@echo "🧹 CLEANUP:"
	@echo "  make clean              - Clean build artifacts"
	@echo "  make clean-state        - Remove agent state (fresh start)"
	@echo "  make clean-all          - Clean everything"
	@echo ""
	@echo "🚀 QUICK START EXAMPLES:"
	@echo ""
	@echo "  Local Development:"
	@echo "    1. make generate-token"
	@echo "    2. make run"
	@echo ""
	@echo "  Minikube Testing:"
	@echo "    1. make minikube-deploy-all"
	@echo "    2. make minikube-logs"
	@echo ""
	@echo "  Any Kubernetes Cluster:"
	@echo "    1. kubectl config use-context <your-cluster>"
	@echo "    2. make k8s-build && docker push <registry>/pipeops-vm-agent:latest"
	@echo "    3. make k8s-deploy-all"
	@echo ""
	@echo "💡 TIP: Use KUBECTL=<path> to specify custom kubectl binary"
	@echo "    Example: make k8s-status KUBECTL=/usr/local/bin/kubectl"
	@echo ""

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
# Kubernetes Deployment (Cluster-Agnostic)
# ====================================================

# Kubernetes Configuration
KUBE_NAMESPACE ?= pipeops-system
IMAGE_NAME ?= pipeops-vm-agent
IMAGE_TAG ?= dev-$(shell git rev-parse --short HEAD 2>/dev/null || echo "latest")
KUBECTL ?= kubectl

# Minikube-specific settings (only used for minikube-* targets)
MINIKUBE_PROFILE ?= pipeops-agent
MINIKUBE_KUBECTL = minikube -p $(MINIKUBE_PROFILE) kubectl --

# ====================================================
# Generic Kubernetes Commands (Work with any cluster)
# ====================================================

# Check if kubectl is available and cluster is accessible
.PHONY: k8s-check
k8s-check:
	@echo "🔍 Checking Kubernetes cluster connectivity..."
	@if ! command -v $(KUBECTL) &> /dev/null; then \
		echo "❌ ERROR: kubectl not found. Please install kubectl."; \
		exit 1; \
	fi
	@if ! $(KUBECTL) cluster-info &> /dev/null; then \
		echo "❌ ERROR: Cannot connect to Kubernetes cluster"; \
		echo "   Make sure your kubeconfig is configured correctly"; \
		exit 1; \
	fi
	@echo "✅ Connected to cluster: $$($(KUBECTL) config current-context)"
	@$(KUBECTL) version --short 2>/dev/null || $(KUBECTL) version

# Build Docker image
.PHONY: k8s-build
k8s-build:
	@echo "🐳 Building Docker image..."
	@docker build -t $(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile .
	@docker tag $(IMAGE_NAME):$(IMAGE_TAG) $(IMAGE_NAME):latest
	@echo "✅ Built: $(IMAGE_NAME):$(IMAGE_TAG)"

# Create namespace
.PHONY: k8s-create-namespace
k8s-create-namespace: k8s-check
	@echo "📦 Creating namespace $(KUBE_NAMESPACE)..."
	@$(KUBECTL) create namespace $(KUBE_NAMESPACE) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	@echo "✅ Namespace created/verified"

# Create RBAC resources
.PHONY: k8s-rbac
k8s-rbac: k8s-check
	@echo "🔐 Creating RBAC resources..."
	@$(KUBECTL) apply -f deployments/minikube-rbac.yaml
	@echo "✅ RBAC resources created"

# Create ConfigMap with test configuration
.PHONY: k8s-config
k8s-config: k8s-check
	@echo "⚙️  Creating configuration..."
	@$(KUBECTL) apply -f deployments/minikube-config.yaml
	@echo "✅ ConfigMap created"

# Setup monitoring (if needed)
.PHONY: k8s-monitoring-setup
k8s-monitoring-setup: k8s-check
	@echo "📊 Setting up monitoring ConfigMaps..."
	@$(KUBECTL) create namespace pipeops-monitoring --dry-run=client -o yaml | $(KUBECTL) apply -f - 2>/dev/null || true
	@$(KUBECTL) apply -f deployments/minikube-monitoring-values.yaml
	@echo "✅ Monitoring ConfigMaps created"

# Deploy agent
.PHONY: k8s-deploy
k8s-deploy: k8s-check
	@echo "🚀 Deploying agent to cluster..."
	@sed 's|image: pipeops-vm-agent:latest|image: $(IMAGE_NAME):$(IMAGE_TAG)|g' deployments/minikube-deployment.yaml | $(KUBECTL) apply -f -
	@echo "✅ Agent deployed"
	@echo ""
	@echo "⏳ Waiting for agent to be ready..."
	@$(KUBECTL) wait --for=condition=available --timeout=60s deployment/pipeops-agent -n $(KUBE_NAMESPACE) || true
	@echo ""
	@$(MAKE) k8s-status

# View agent logs (live)
.PHONY: k8s-logs
k8s-logs: k8s-check
	@echo "📋 Viewing agent logs (live)..."
	@$(KUBECTL) logs -n $(KUBE_NAMESPACE) -l app=pipeops-agent --tail=100 -f

# View agent logs (last 50 lines)
.PHONY: k8s-logs-tail
k8s-logs-tail: k8s-check
	@echo "📋 Last 50 log lines:"
	@$(KUBECTL) logs -n $(KUBE_NAMESPACE) -l app=pipeops-agent --tail=50

# Check agent status
.PHONY: k8s-status
k8s-status: k8s-check
	@echo "📊 Agent Status:"
	@echo ""
	@echo "Deployment:"
	@$(KUBECTL) get deployment -n $(KUBE_NAMESPACE) pipeops-agent 2>/dev/null || echo "  No deployment found"
	@echo ""
	@echo "Pods:"
	@$(KUBECTL) get pods -n $(KUBE_NAMESPACE) -l app=pipeops-agent 2>/dev/null || echo "  No pods found"
	@echo ""
	@echo "Recent Events:"
	@$(KUBECTL) get events -n $(KUBE_NAMESPACE) --sort-by='.lastTimestamp' 2>/dev/null | tail -10 || echo "  No events"

# Describe agent pod
.PHONY: k8s-describe
k8s-describe: k8s-check
	@echo "🔍 Describing agent pod..."
	@$(KUBECTL) describe pod -n $(KUBE_NAMESPACE) -l app=pipeops-agent

# Get shell in agent pod
.PHONY: k8s-shell
k8s-shell: k8s-check
	@echo "💻 Opening shell in agent pod..."
	@$(KUBECTL) exec -it -n $(KUBE_NAMESPACE) $$($(KUBECTL) get pod -n $(KUBE_NAMESPACE) -l app=pipeops-agent -o jsonpath='{.items[0].metadata.name}') -- /bin/sh

# Remove agent deployment
.PHONY: k8s-undeploy
k8s-undeploy: k8s-check
	@echo "🗑️  Removing agent deployment..."
	@$(KUBECTL) delete deployment pipeops-agent -n $(KUBE_NAMESPACE) --ignore-not-found=true
	@echo "✅ Agent deployment removed"

# Clean all resources
.PHONY: k8s-clean
k8s-clean: k8s-check
	@echo "🧹 Cleaning all resources..."
	@$(KUBECTL) delete namespace $(KUBE_NAMESPACE) --ignore-not-found=true
	@$(KUBECTL) delete clusterrole pipeops-agent --ignore-not-found=true
	@$(KUBECTL) delete clusterrolebinding pipeops-agent --ignore-not-found=true
	@echo "✅ All resources cleaned"

# Restart agent deployment
.PHONY: k8s-restart
k8s-restart: k8s-check
	@echo "🔄 Restarting agent..."
	@$(KUBECTL) rollout restart deployment/pipeops-agent -n $(KUBE_NAMESPACE)
	@$(KUBECTL) rollout status deployment/pipeops-agent -n $(KUBE_NAMESPACE)
	@echo "✅ Agent restarted"

# Complete deployment workflow
.PHONY: k8s-deploy-all
k8s-deploy-all: k8s-check k8s-create-namespace k8s-rbac k8s-config k8s-deploy
	@echo ""
	@echo "✅ Agent deployed successfully!"
	@echo ""
	@echo "📝 Quick commands:"
	@echo "  make k8s-logs        - View live logs"
	@echo "  make k8s-status      - Check deployment status"
	@echo "  make k8s-describe    - Describe pod details"
	@echo "  make k8s-shell       - Open shell in pod"
	@echo "  make k8s-restart     - Restart deployment"
	@echo "  make k8s-clean       - Remove all resources"

# ====================================================
# Minikube-Specific Commands
# ====================================================

# Check if Minikube is running
.PHONY: minikube-check
minikube-check:
	@echo "🔍 Checking Minikube status..."
	@if ! command -v minikube &> /dev/null; then \
		echo "❌ ERROR: Minikube not found. Install from: https://minikube.sigs.k8s.io/docs/start/"; \
		exit 1; \
	fi
	@if ! minikube status -p $(MINIKUBE_PROFILE) 2>/dev/null | grep -q "Running"; then \
		echo "⚠️  WARNING: Minikube cluster '$(MINIKUBE_PROFILE)' not running"; \
		echo "   Run 'make minikube-start' to start it"; \
		exit 1; \
	fi
	@echo "✅ Minikube cluster '$(MINIKUBE_PROFILE)' is running"

# Start Minikube cluster
.PHONY: minikube-start
minikube-start:
	@echo "🚀 Starting Minikube cluster..."
	@if ! command -v minikube &> /dev/null; then \
		echo "❌ ERROR: Minikube not found. Install from: https://minikube.sigs.k8s.io/docs/start/"; \
		exit 1; \
	fi
	@if minikube status -p $(MINIKUBE_PROFILE) 2>/dev/null | grep -q "Running"; then \
		echo "✅ Minikube cluster '$(MINIKUBE_PROFILE)' already running"; \
	else \
		minikube start -p $(MINIKUBE_PROFILE) --cpus=2 --memory=4096 --driver=docker; \
		echo "✅ Minikube cluster '$(MINIKUBE_PROFILE)' started"; \
	fi
	@echo ""
	@echo "📊 Cluster Info:"
	@$(MINIKUBE_KUBECTL) version --short 2>/dev/null || $(MINIKUBE_KUBECTL) version --client
	@$(MINIKUBE_KUBECTL) get nodes

# Stop Minikube cluster
.PHONY: minikube-stop
minikube-stop:
	@echo "⏸️  Stopping Minikube cluster..."
	@minikube stop -p $(MINIKUBE_PROFILE)
	@echo "✅ Minikube cluster stopped"

# Delete Minikube cluster
.PHONY: minikube-delete
minikube-delete:
	@echo "🗑️  Deleting Minikube cluster..."
	@minikube delete -p $(MINIKUBE_PROFILE)
	@echo "✅ Minikube cluster deleted"

# Build Docker image in Minikube's Docker daemon
.PHONY: minikube-build
minikube-build: minikube-check
	@echo "🐳 Building Docker image in Minikube..."
	@eval $$(minikube -p $(MINIKUBE_PROFILE) docker-env) && \
		docker build -t $(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile . && \
		docker tag $(IMAGE_NAME):$(IMAGE_TAG) $(IMAGE_NAME):latest
	@echo "✅ Image $(IMAGE_NAME):$(IMAGE_TAG) built in Minikube"

# Complete Minikube setup and deployment
.PHONY: minikube-deploy-all
minikube-deploy-all: minikube-start minikube-build
	@echo ""
	@echo "📦 Setting up agent in Minikube..."
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-create-namespace
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-rbac
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-config
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-deploy
	@echo ""
	@echo "✅ Agent deployed successfully to Minikube!"
	@echo ""
	@echo "📝 Quick commands:"
	@echo "  make minikube-logs       - View live logs"
	@echo "  make minikube-status     - Check deployment status"
	@echo "  make minikube-describe   - Describe pod details"
	@echo "  make minikube-shell      - Open shell in pod"
	@echo "  make minikube-restart    - Restart deployment"
	@echo "  make minikube-clean      - Remove all resources"
	@echo ""
	@echo "📋 Showing recent logs:"
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-logs-tail

# Convenience aliases for Minikube using generic commands
.PHONY: minikube-logs
minikube-logs: minikube-check
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-logs

.PHONY: minikube-logs-tail
minikube-logs-tail: minikube-check
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-logs-tail

.PHONY: minikube-status
minikube-status: minikube-check
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-status

.PHONY: minikube-describe
minikube-describe: minikube-check
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-describe

.PHONY: minikube-shell
minikube-shell: minikube-check
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-shell

.PHONY: minikube-restart
minikube-restart: minikube-check
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-restart

.PHONY: minikube-undeploy
minikube-undeploy: minikube-check
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-undeploy

.PHONY: minikube-clean
minikube-clean: minikube-check
	@KUBECTL="$(MINIKUBE_KUBECTL)" $(MAKE) k8s-clean
