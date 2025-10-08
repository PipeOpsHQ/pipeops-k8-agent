# Build stage
FROM golang:1.23-alpine AS builder

# Install git, ca-certificates, curl, and tar for fetching dependencies and FRP
RUN apk add --no-cache git ca-certificates tzdata curl tar

# Create non-root user for building
RUN adduser -D -g '' appuser

# Set working directory
WORKDIR /app

# Copy go mod files first (better caching)
COPY go.mod go.sum ./

# Download dependencies (this layer will be cached if go.mod/go.sum don't change)
RUN go mod download && go mod verify

# Copy only necessary source files (exclude unnecessary files for better caching)
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY pkg/ ./pkg/
COPY *.go ./

# Build arguments for metadata
ARG VERSION=dev
ARG BUILD_TIME=unknown
ARG COMMIT=unknown

# FRP version to download
ARG FRP_VERSION=0.58.1

# Download and install FRP binary
RUN ARCH=$(case $(uname -m) in x86_64) echo "amd64" ;; aarch64) echo "arm64" ;; armv7l) echo "arm" ;; *) echo "$(uname -m)" ;; esac) && \
    curl -L "https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/frp_${FRP_VERSION}_linux_${ARCH}.tar.gz" -o frp.tar.gz && \
    tar -xzf frp.tar.gz && \
    mv frp_${FRP_VERSION}_linux_${ARCH}/frpc /usr/local/bin/frpc && \
    chmod +x /usr/local/bin/frpc && \
    rm -rf frp.tar.gz frp_${FRP_VERSION}_linux_${ARCH}

# Build the binary with optimizations
RUN CGO_ENABLED=0 go build \
    -ldflags="-w -s -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME} -X main.commit=${COMMIT}" \
    -a -installsuffix cgo \
    -trimpath \
    -o pipeops-agent \
    cmd/agent/main.go

# Final stage
FROM scratch

# Import timezone data and ca-certificates
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd

# Copy the binaries
COPY --from=builder /app/pipeops-agent /usr/local/bin/pipeops-agent
COPY --from=builder /usr/local/bin/frpc /usr/local/bin/frpc

# Use non-root user
USER appuser

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/pipeops-agent"]

# Default command
CMD ["--help"]

# Labels
LABEL org.opencontainers.image.title="PipeOps Agent"
LABEL org.opencontainers.image.description="Kubernetes agent for PipeOps platform"
LABEL org.opencontainers.image.vendor="PipeOps"
LABEL org.opencontainers.image.source="https://github.com/PipeOpsHQ/pipeops-k8-agent"
LABEL org.opencontainers.image.licenses="MIT"
