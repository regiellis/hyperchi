#!/bin/bash
# HyperChi Release Preparation Script
# Prepares the repository for release to pkg.go.dev

set -e  # Exit on error

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
# The CLI is a separate module (github.com/regiellis/hyperchi/cmd/hyperchi).
CLI_DIR="$ROOT_DIR/cmd/hyperchi"
VERSION="${1:-vX.Y.Z}"
cd "$ROOT_DIR"

echo "🚀 HyperChi Release Preparation"
echo "================================"
echo ""

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Function to print colored output
print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

# Step 1: Run tests
echo "📋 Step 1: Running tests (framework and CLI modules)..."
if go test ./... > /dev/null 2>&1; then
    print_success "Framework tests passed"
else
    print_error "Framework tests failed! Fix tests before releasing."
    exit 1
fi
if go -C "$CLI_DIR" test ./... > /dev/null 2>&1; then
    print_success "CLI tests passed"
else
    print_error "CLI tests failed! Fix tests before releasing."
    exit 1
fi

# Step 2: Check go.mod
echo ""
echo "📋 Step 2: Checking go.mod in both modules..."
go mod tidy
go mod verify > /dev/null
go -C "$CLI_DIR" mod tidy
go -C "$CLI_DIR" mod verify > /dev/null
print_success "go.mod files are tidy and verified"
if go list -deps ./... | grep -q -E 'spf13/cobra|charmbracelet'; then
    print_error "The framework module builds CLI-only packages (cobra/lipgloss)"
    exit 1
fi
print_success "Framework does not build CLI-only dependencies"

# Step 3: Run go vet
echo ""
echo "📋 Step 3: Running go vet..."
if go vet ./... > /dev/null 2>&1 && go -C "$CLI_DIR" vet ./... > /dev/null 2>&1; then
    print_success "No issues found by go vet"
else
    print_error "go vet found issues"
    exit 1
fi

# Step 4: Check for common issues
echo ""
echo "📋 Step 4: Checking for common issues..."

# Check if LICENSE exists and is not empty
if [ -s "$ROOT_DIR/LICENSE" ]; then
    print_success "LICENSE file exists"
else
    print_error "LICENSE file is missing or empty"
    exit 1
fi

# Check if README exists
if [ -f "$ROOT_DIR/README.md" ]; then
    print_success "README.md exists"
else
    print_error "README.md is missing"
    exit 1
fi

# Check if CHANGELOG exists
if [ -f "$ROOT_DIR/CHANGELOG.md" ]; then
    print_success "CHANGELOG.md exists"
else
    print_warning "CHANGELOG.md is missing"
fi

# Check if doc.go exists
if [ -f "$ROOT_DIR/hyperchi/doc.go" ]; then
    print_success "Package documentation (doc.go) exists"
else
    print_warning "Consider adding hyperchi/doc.go for better pkg.go.dev documentation"
fi

# Step 5: Check git status
echo ""
echo "📋 Step 5: Checking git status..."
if [ -d "$ROOT_DIR/.git" ]; then
    if [ -z "$(git status --porcelain)" ]; then
        print_success "Working directory is clean"
    else
        print_warning "Working directory has uncommitted changes"
        echo "  Run 'git status' to see changes"
    fi
else
    print_warning "Not a git repository"
fi

# Step 6: Check if we're on correct branch
echo ""
echo "📋 Step 6: Checking git branch..."
if [ -d "$ROOT_DIR/.git" ]; then
    CURRENT_BRANCH=$(git branch --show-current)
    if [ "$CURRENT_BRANCH" == "master" ] || [ "$CURRENT_BRANCH" == "main" ]; then
        print_success "On $CURRENT_BRANCH branch"
    else
        print_warning "Currently on '$CURRENT_BRANCH'. Consider releasing from master/main."
    fi
fi

# Step 7: Check module path
echo ""
echo "📋 Step 7: Verifying module path..."
MODULE_PATH=$(go list -m)
if [ "$MODULE_PATH" == "github.com/regiellis/hyperchi" ]; then
    print_success "Module path is correct: $MODULE_PATH"
else
    print_warning "Module path is: $MODULE_PATH"
    print_warning "Expected: github.com/regiellis/hyperchi"
fi
CLI_MODULE_PATH=$(go -C "$CLI_DIR" list -m)
if [ "$CLI_MODULE_PATH" == "github.com/regiellis/hyperchi/cmd/hyperchi" ]; then
    print_success "CLI module path is correct: $CLI_MODULE_PATH"
else
    print_error "CLI module path is: $CLI_MODULE_PATH"
    exit 1
fi
if grep -q '^replace' "$CLI_DIR/go.mod"; then
    print_warning "cmd/hyperchi/go.mod has a replace directive: drop it in the CLI release commit"
    print_warning "(go install module@version refuses modules with replace directives)"
fi

# Step 8: Build test
echo ""
echo "📋 Step 8: Testing build..."
if go build ./... > /dev/null 2>&1 && go -C "$CLI_DIR" build ./... > /dev/null 2>&1; then
    print_success "Build successful"
else
    print_error "Build failed"
    exit 1
fi

# Summary
echo ""
echo "================================"
echo "✨ Release Preparation Complete!"
echo "================================"
echo ""
echo "📝 Next steps (full sequence in docs/RELEASING.md):"
echo ""
echo "1. Review CHANGELOG.md and update the version date"
echo "2. Tag and push the framework, then confirm it resolves:"
echo "   git tag -a $VERSION -m \"Release $VERSION\""
echo "   go list -m github.com/regiellis/hyperchi@$VERSION"
echo ""
echo "3. In cmd/hyperchi: drop the replace, require the tag, tidy, test, commit:"
echo "   go mod edit -dropreplace=github.com/regiellis/hyperchi"
echo "   go get github.com/regiellis/hyperchi@$VERSION && go mod tidy"
echo ""
echo "4. Tag and push the CLI module:"
echo "   git tag -a cmd/hyperchi/$VERSION -m \"Release CLI $VERSION\""
echo ""
echo "5. Verify the install path, then restore the replace for development:"
echo "   go install github.com/regiellis/hyperchi/cmd/hyperchi@$VERSION"
echo ""
