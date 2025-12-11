#!/bin/bash
#
# Setup script for configuring ChartMuseum integration with GitHub Actions
# This script helps configure the required GitHub repository variables and secrets
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Script configuration
SCRIPT_NAME="ChartMuseum Setup"
REPO_OWNER=""
REPO_NAME=""

# Function to print colored messages
print_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_header() {
    echo ""
    echo -e "${BLUE}═══════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  $1${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════${NC}"
    echo ""
}

# Function to check if gh CLI is installed
check_gh_cli() {
    if ! command -v gh &> /dev/null; then
        print_error "GitHub CLI (gh) is not installed"
        echo ""
        echo "Install GitHub CLI:"
        echo "  macOS:   brew install gh"
        echo "  Linux:   https://github.com/cli/cli/blob/trunk/docs/install_linux.md"
        echo "  Windows: https://github.com/cli/cli#installation"
        echo ""
        exit 1
    fi
}

# Function to check if user is authenticated
check_auth() {
    if ! gh auth status &> /dev/null; then
        print_error "Not authenticated with GitHub CLI"
        echo ""
        print_info "Run: gh auth login"
        echo ""
        exit 1
    fi
}

# Function to get repository info
get_repo_info() {
    # Try to get repo info from current directory
    if git rev-parse --is-inside-work-tree &> /dev/null; then
        REMOTE_URL=$(git config --get remote.origin.url)
        if [[ $REMOTE_URL =~ github.com[:/]([^/]+)/([^/]+)(\.git)?$ ]]; then
            REPO_OWNER="${BASH_REMATCH[1]}"
            REPO_NAME="${BASH_REMATCH[2]}"
            REPO_NAME="${REPO_NAME%.git}"
            return 0
        fi
    fi
    return 1
}

# Function to prompt for repository
prompt_repo() {
    echo ""
    print_info "Enter your GitHub repository details:"
    read -p "Repository owner (e.g., PipeOpsHQ): " REPO_OWNER
    read -p "Repository name (e.g., pipeops-k8-agent): " REPO_NAME
}

# Function to set a repository variable
set_repo_variable() {
    local var_name=$1
    local var_value=$2

    print_info "Setting repository variable: $var_name"

    if gh variable set "$var_name" --body "$var_value" --repo "$REPO_OWNER/$REPO_NAME" 2>/dev/null; then
        print_success "Variable $var_name set successfully"
        return 0
    else
        print_error "Failed to set variable $var_name"
        return 1
    fi
}

# Function to set a repository secret
set_repo_secret() {
    local secret_name=$1
    local secret_value=$2

    print_info "Setting repository secret: $secret_name"

    if echo "$secret_value" | gh secret set "$secret_name" --repo "$REPO_OWNER/$REPO_NAME" 2>/dev/null; then
        print_success "Secret $secret_name set successfully"
        return 0
    else
        print_error "Failed to set secret $secret_name"
        return 1
    fi
}

# Function to configure ChartMuseum
configure_chartmuseum() {
    print_header "ChartMuseum Configuration"

    echo "This script will configure GitHub Actions to publish Helm charts to ChartMuseum."
    echo ""

    # Get ChartMuseum URL
    read -p "Enter your ChartMuseum URL (e.g., https://charts.pipeops.io): " CHARTMUSEUM_URL

    if [[ -z "$CHARTMUSEUM_URL" ]]; then
        print_error "ChartMuseum URL is required"
        exit 1
    fi

    # Validate URL format
    if [[ ! "$CHARTMUSEUM_URL" =~ ^https?:// ]]; then
        print_warning "ChartMuseum URL should start with http:// or https://"
        read -p "Continue anyway? (y/n): " confirm
        if [[ "$confirm" != "y" ]]; then
            exit 1
        fi
    fi

    # Set ChartMuseum URL variable
    set_repo_variable "CHARTMUSEUM_URL" "$CHARTMUSEUM_URL"

    echo ""
    print_info "Does your ChartMuseum require authentication?"
    read -p "Require authentication? (y/n): " requires_auth

    if [[ "$requires_auth" == "y" ]]; then
        echo ""
        read -p "Enter ChartMuseum username: " CHARTMUSEUM_USER
        read -sp "Enter ChartMuseum password/token: " CHARTMUSEUM_PASSWORD
        echo ""

        if [[ -z "$CHARTMUSEUM_USER" ]] || [[ -z "$CHARTMUSEUM_PASSWORD" ]]; then
            print_error "Both username and password are required for authentication"
            exit 1
        fi

        # Set ChartMuseum credentials
        set_repo_secret "CHARTMUSEUM_USER" "$CHARTMUSEUM_USER"
        set_repo_secret "CHARTMUSEUM_PASSWORD" "$CHARTMUSEUM_PASSWORD"
    else
        print_info "Skipping authentication configuration"
    fi
}

# Function to test ChartMuseum connection
test_connection() {
    print_header "Testing ChartMuseum Connection"

    print_info "Testing connection to: $CHARTMUSEUM_URL"

    if [[ -n "$CHARTMUSEUM_USER" ]] && [[ -n "$CHARTMUSEUM_PASSWORD" ]]; then
        RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" -u "$CHARTMUSEUM_USER:$CHARTMUSEUM_PASSWORD" "$CHARTMUSEUM_URL/api/charts" 2>/dev/null || echo "000")
    else
        RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" "$CHARTMUSEUM_URL/api/charts" 2>/dev/null || echo "000")
    fi

    case $RESPONSE in
        200)
            print_success "ChartMuseum is accessible (HTTP 200)"
            ;;
        401)
            print_warning "Authentication required (HTTP 401)"
            print_info "Make sure your credentials are correct"
            ;;
        404)
            print_warning "Endpoint not found (HTTP 404)"
            print_info "Check if your ChartMuseum URL is correct"
            ;;
        000)
            print_error "Cannot connect to ChartMuseum"
            print_info "Check if the URL is correct and ChartMuseum is running"
            ;;
        *)
            print_warning "Unexpected response (HTTP $RESPONSE)"
            ;;
    esac
}

# Function to display summary
display_summary() {
    print_header "Configuration Summary"

    echo "✅ ChartMuseum integration configured!"
    echo ""
    echo "📋 Configuration:"
    echo "   Repository: $REPO_OWNER/$REPO_NAME"
    echo "   ChartMuseum URL: $CHARTMUSEUM_URL"
    echo "   Authentication: ${requires_auth:-n}"
    echo ""
    echo "🚀 Next Steps:"
    echo "   1. Commit and push changes to trigger the workflow"
    echo "   2. Monitor the workflow at: https://github.com/$REPO_OWNER/$REPO_NAME/actions"
    echo "   3. Charts will be published on push to 'main' or version tags (v*)"
    echo ""
    echo "📚 Documentation: docs/CHARTMUSEUM.md"
    echo ""
}

# Main execution
main() {
    print_header "$SCRIPT_NAME"

    # Check prerequisites
    check_gh_cli
    check_auth

    # Get repository information
    if get_repo_info; then
        print_success "Detected repository: $REPO_OWNER/$REPO_NAME"
        read -p "Use this repository? (y/n): " use_detected
        if [[ "$use_detected" != "y" ]]; then
            prompt_repo
        fi
    else
        prompt_repo
    fi

    # Validate repository exists
    if ! gh repo view "$REPO_OWNER/$REPO_NAME" &> /dev/null; then
        print_error "Repository $REPO_OWNER/$REPO_NAME not found or not accessible"
        exit 1
    fi

    print_success "Repository validated: $REPO_OWNER/$REPO_NAME"

    # Configure ChartMuseum
    configure_chartmuseum

    # Test connection
    test_connection

    # Display summary
    display_summary
}

# Run main function
main
