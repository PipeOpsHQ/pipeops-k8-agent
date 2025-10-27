#!/bin/bash

# Documentation versioning helper script
# Usage: ./scripts/docs-version.sh [version]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}📚 PipeOps Agent Documentation Versioning${NC}"
echo "================================================"

# Check if mike is installed
if ! command -v mike &> /dev/null; then
    echo -e "${RED}❌ Error: mike is not installed${NC}"
    echo "Install it with: pip install mike"
    exit 1
fi

# Check if we're in a git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    echo -e "${RED}❌ Error: Not in a git repository${NC}"
    exit 1
fi

cd "$ROOT_DIR"

# Get current version or use provided version
if [ -n "$1" ]; then
    VERSION="$1"
else
    # Try to get version from git tag
    if git describe --tags --exact-match HEAD > /dev/null 2>&1; then
        VERSION=$(git describe --tags --exact-match HEAD | sed 's/^v//')
        echo -e "${GREEN}✅ Detected git tag version: $VERSION${NC}"
    else
        # Get latest tag or default
        LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
        VERSION=${LATEST_TAG#v}
        echo -e "${YELLOW}⚠️  Using latest tag version: $VERSION${NC}"
    fi
fi

echo "Version: $VERSION"
echo ""

# Update mkdocs.yml with version
echo -e "${BLUE}📝 Updating mkdocs.yml with version $VERSION${NC}"
if grep -q "release_version:" mkdocs.yml; then
    sed -i.bak "s/release_version:.*/release_version: \"$VERSION\"/" mkdocs.yml
else
    echo "  release_version: \"$VERSION\"" >> mkdocs.yml
fi

# Build versioned documentation
echo -e "${BLUE}🔨 Building versioned documentation${NC}"
mike deploy --update-aliases "$VERSION" latest

# Set as default if it's the latest
echo -e "${BLUE}🎯 Setting version $VERSION as default${NC}"
mike set-default latest

# List available versions
echo -e "${GREEN}📋 Available documentation versions:${NC}"
mike list

echo ""
echo -e "${GREEN}✅ Documentation versioning complete!${NC}"
echo -e "${BLUE}🌐 Serve locally with: mike serve${NC}"
echo -e "${BLUE}🚀 Deploy with: mike deploy --push $VERSION latest${NC}"