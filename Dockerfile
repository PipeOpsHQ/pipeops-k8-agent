# Build stage
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

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

# Build arguments for metadata and cross-compilation
ARG VERSION=dev
ARG BUILD_TIME=unknown
ARG COMMIT=unknown
ARG TARGETOS
ARG TARGETARCH

# Build the binary with optimizations for target platform
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
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

# Bundle Helm charts needed at runtime (local installs)
COPY helm/ /helm/

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
