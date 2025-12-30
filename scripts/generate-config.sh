#!/usr/bin/env bash
#
# PipeOps Agent Configuration Generator
# Generates configuration files for PipeOps agent deployment
#
# Usage: ./generate-config.sh [OPTIONS]
#

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration defaults
PIPEOPS_API_URL="${PIPEOPS_API_URL:-https://api.pipeops.io}"
PIPEOPS_TOKEN="${PIPEOPS_TOKEN:-}"
PIPEOPS_CLUSTER_NAME="${PIPEOPS_CLUSTER_NAME:-}"
NAMESPACE="${NAMESPACE:-pipeops-system}"
OUTPUT_FORMAT="yaml"
OUTPUT_FILE=""
INTERACTIVE=true

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --api-url)
            PIPEOPS_API_URL="$2"
            shift 2
            ;;
        --token)
            PIPEOPS_TOKEN="$2"
            shift 2
            ;;
        --cluster-name)
            PIPEOPS_CLUSTER_NAME="$2"
            shift 2
            ;;
        --namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        --format)
            OUTPUT_FORMAT="$2"
            shift 2
            ;;
        --output|-o)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        --non-interactive)
            INTERACTIVE=false
            shift
            ;;
        --help|-h)
            cat << EOF
PipeOps Agent Configuration Generator

Usage: $0 [OPTIONS]

Options:
    --api-url URL           PipeOps API URL (default: https://api.pipeops.io)
    --token TOKEN           PipeOps authentication token
    --cluster-name NAME     Cluster identifier name
    --namespace NAME        Kubernetes namespace (default: pipeops-system)
    --format FORMAT         Output format: yaml, env, secret (default: yaml)
    --output, -o FILE       Output file path (default: stdout)
    --non-interactive       Skip interactive prompts (use env vars/flags only)
    --help, -h             Show this help message

Output Formats:
    yaml      - Full Kubernetes secret YAML manifest
    env       - Environment variable file (.env format)
    secret    - Kubectl secret command

Examples:
    $0                                          # Interactive mode
    $0 --token xxx --cluster-name prod          # Using flags
    $0 --format env --output .env               # Generate .env file
    PIPEOPS_TOKEN=xxx $0 --non-interactive      # Using environment variables

EOF
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Print functions
print_status() {
    echo -e "${BLUE}==>${NC} $1" >&2
}

print_success() {
    echo -e "${GREEN}✓${NC} $1" >&2
}

print_error() {
    echo -e "${RED}✗${NC} $1" >&2
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1" >&2
}

# Validation functions
validate_url() {
    local url="$1"
    
    # Basic URL validation
    if [[ ! "$url" =~ ^https?:// ]]; then
        return 1
    fi
    
    return 0
}

validate_token() {
    local token="$1"
    
    # Token should not be empty
    if [ -z "$token" ]; then
        return 1
    fi
    
    # Token should be at least 20 characters
    if [ ${#token} -lt 20 ]; then
        return 1
    fi
    
    return 0
}

validate_cluster_name() {
    local name="$1"
    
    # Name should not be empty
    if [ -z "$name" ]; then
        return 1
    fi
    
    # Name should be a valid Kubernetes name
    if [[ ! "$name" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
        return 1
    fi
    
    return 0
}

# Interactive prompt functions
prompt_api_url() {
    if [ -n "$PIPEOPS_API_URL" ]; then
        print_success "Using API URL: $PIPEOPS_API_URL"
        return 0
    fi
    
    echo "" >&2
    echo "PipeOps API URL" >&2
    echo "Default: https://api.pipeops.io" >&2
    read -p "Enter API URL (or press Enter for default): " -r
    
    if [ -z "$REPLY" ]; then
        PIPEOPS_API_URL="https://api.pipeops.io"
    else
        PIPEOPS_API_URL="$REPLY"
    fi
    
    if ! validate_url "$PIPEOPS_API_URL"; then
        print_error "Invalid URL format"
        return 1
    fi
    
    return 0
}

prompt_token() {
    if [ -n "$PIPEOPS_TOKEN" ]; then
        print_success "Using provided token"
        return 0
    fi
    
    echo "" >&2
    echo "PipeOps Authentication Token" >&2
    echo "Get your token from: https://console.pipeops.io/settings/tokens" >&2
    read -sp "Enter token: " -r
    echo "" >&2
    
    PIPEOPS_TOKEN="$REPLY"
    
    if ! validate_token "$PIPEOPS_TOKEN"; then
        print_error "Invalid token (must be at least 20 characters)"
        return 1
    fi
    
    return 0
}

prompt_cluster_name() {
    if [ -n "$PIPEOPS_CLUSTER_NAME" ]; then
        print_success "Using cluster name: $PIPEOPS_CLUSTER_NAME"
        return 0
    fi
    
    echo "" >&2
    echo "Cluster Name" >&2
    echo "Choose a unique name for this cluster (lowercase, alphanumeric, hyphens)" >&2
    read -p "Enter cluster name: " -r
    
    PIPEOPS_CLUSTER_NAME="$REPLY"
    
    if ! validate_cluster_name "$PIPEOPS_CLUSTER_NAME"; then
        print_error "Invalid cluster name (must be lowercase alphanumeric with hyphens)"
        return 1
    fi
    
    return 0
}

# Interactive mode
run_interactive() {
    echo "" >&2
    echo "╔═══════════════════════════════════════════════════════╗" >&2
    echo "║     PipeOps Agent Configuration Generator            ║" >&2
    echo "╚═══════════════════════════════════════════════════════╝" >&2
    echo "" >&2
    echo "This wizard will help you generate the configuration" >&2
    echo "for your PipeOps agent deployment." >&2
    
    # Prompt for each value
    if ! prompt_api_url; then
        print_error "Failed to get API URL"
        exit 1
    fi
    
    if ! prompt_token; then
        print_error "Failed to get token"
        exit 1
    fi
    
    if ! prompt_cluster_name; then
        print_error "Failed to get cluster name"
        exit 1
    fi
    
    echo "" >&2
    print_success "Configuration collected successfully!"
}

# Generate YAML secret
generate_yaml() {
    cat << EOF
apiVersion: v1
kind: Secret
metadata:
  name: pipeops-agent-config
  namespace: ${NAMESPACE}
type: Opaque
stringData:
  PIPEOPS_API_URL: "${PIPEOPS_API_URL}"
  PIPEOPS_TOKEN: "${PIPEOPS_TOKEN}"
  PIPEOPS_CLUSTER_NAME: "${PIPEOPS_CLUSTER_NAME}"
EOF
}

# Generate environment file
generate_env() {
    cat << EOF
# PipeOps Agent Configuration
# Generated on $(date)

# PipeOps API URL
PIPEOPS_API_URL=${PIPEOPS_API_URL}

# PipeOps Authentication Token
PIPEOPS_TOKEN=${PIPEOPS_TOKEN}

# Cluster Name
PIPEOPS_CLUSTER_NAME=${PIPEOPS_CLUSTER_NAME}

# Optional: Enable debug logging
# PIPEOPS_LOG_LEVEL=debug
EOF
}

# Generate kubectl secret command
generate_secret_command() {
    cat << EOF
# Create Kubernetes secret for PipeOps agent
kubectl create secret generic pipeops-agent-config \\
  --namespace=${NAMESPACE} \\
  --from-literal=PIPEOPS_API_URL="${PIPEOPS_API_URL}" \\
  --from-literal=PIPEOPS_TOKEN="${PIPEOPS_TOKEN}" \\
  --from-literal=PIPEOPS_CLUSTER_NAME="${PIPEOPS_CLUSTER_NAME}" \\
  --dry-run=client -o yaml | kubectl apply -f -
EOF
}

# Display summary
display_summary() {
    echo "" >&2
    echo "╔═══════════════════════════════════════════════════════╗" >&2
    echo "║              Configuration Summary                    ║" >&2
    echo "╚═══════════════════════════════════════════════════════╝" >&2
    echo "" >&2
    echo "  API URL:      ${PIPEOPS_API_URL}" >&2
    echo "  Token:        ${PIPEOPS_TOKEN:0:8}...${PIPEOPS_TOKEN: -4}" >&2
    echo "  Cluster Name: ${PIPEOPS_CLUSTER_NAME}" >&2
    echo "  Namespace:    ${NAMESPACE}" >&2
    echo "  Format:       ${OUTPUT_FORMAT}" >&2
    echo "" >&2
}

# Validate all required values
validate_config() {
    local valid=true
    
    if ! validate_url "$PIPEOPS_API_URL"; then
        print_error "Invalid API URL: $PIPEOPS_API_URL"
        valid=false
    fi
    
    if ! validate_token "$PIPEOPS_TOKEN"; then
        print_error "Invalid token (must be at least 20 characters)"
        valid=false
    fi
    
    if ! validate_cluster_name "$PIPEOPS_CLUSTER_NAME"; then
        print_error "Invalid cluster name: $PIPEOPS_CLUSTER_NAME"
        valid=false
    fi
    
    if [ "$valid" = false ]; then
        return 1
    fi
    
    return 0
}

# Main function
main() {
    # Run interactive mode if needed
    if [ "$INTERACTIVE" = true ]; then
        run_interactive
    else
        # Validate required values in non-interactive mode
        if [ -z "$PIPEOPS_TOKEN" ] || [ -z "$PIPEOPS_CLUSTER_NAME" ]; then
            print_error "In non-interactive mode, you must provide:"
            echo "  - PIPEOPS_TOKEN (via --token or env var)" >&2
            echo "  - PIPEOPS_CLUSTER_NAME (via --cluster-name or env var)" >&2
            exit 1
        fi
    fi
    
    # Validate configuration
    if ! validate_config; then
        print_error "Configuration validation failed"
        exit 1
    fi
    
    # Display summary
    display_summary
    
    # Generate output based on format
    local output=""
    case "$OUTPUT_FORMAT" in
        yaml)
            output=$(generate_yaml)
            ;;
        env)
            output=$(generate_env)
            ;;
        secret)
            output=$(generate_secret_command)
            ;;
        *)
            print_error "Unknown format: $OUTPUT_FORMAT"
            exit 1
            ;;
    esac
    
    # Write output
    if [ -n "$OUTPUT_FILE" ]; then
        echo "$output" > "$OUTPUT_FILE"
        print_success "Configuration written to: $OUTPUT_FILE"
        
        # Show next steps
        echo "" >&2
        print_status "Next steps:" >&2
        case "$OUTPUT_FORMAT" in
            yaml)
                echo "  kubectl apply -f $OUTPUT_FILE" >&2
                ;;
            env)
                echo "  source $OUTPUT_FILE" >&2
                echo "  Or use with Docker: docker run --env-file $OUTPUT_FILE ..." >&2
                ;;
            secret)
                echo "  bash $OUTPUT_FILE" >&2
                ;;
        esac
    else
        # Output to stdout
        echo "$output"
    fi
    
    echo "" >&2
    print_success "Configuration generated successfully!"
}

# Run main function
main
