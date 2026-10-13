#!/bin/bash

# HyperChi Documentation Generation Script
# Generates and serves documentation using godoc

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

show_help() {
    cat << EOF
HyperChi Documentation Generator

USAGE:
    $0 [COMMAND] [OPTIONS]

COMMANDS:
    serve       Start godoc server (default)
    generate    Generate static documentation
    check       Check documentation coverage
    install     Install godoc tools
    help        Show this help

OPTIONS:
    -p, --port PORT     Server port (default: 6060)
    -o, --output DIR    Output directory for static docs (default: docs/generated)
    -h, --help          Show help

EXAMPLES:
    $0                  # Start godoc server on :6060
    $0 serve -p 8080    # Start godoc server on :8080
    $0 generate         # Generate static HTML documentation
    $0 check            # Check documentation coverage

EOF
}

# Function to check if godoc is installed
check_godoc() {
    if ! command -v godoc &> /dev/null; then
        print_warning "godoc not found, installing..."
        install_godoc
    fi
}

# Function to install godoc
install_godoc() {
    print_status "Installing godoc..."
    go install golang.org/x/tools/cmd/godoc@latest
    
    if command -v godoc &> /dev/null; then
        print_success "godoc installed successfully"
    else
        print_error "Failed to install godoc"
        exit 1
    fi
}

# Function to start godoc server
serve_docs() {
    local port=${1:-6060}
    
    print_status "Starting godoc server on port $port..."
    print_status "Documentation will be available at:"
    print_success "  • Main package: http://localhost:$port/pkg/github.com/regiellis/hyperchi/"
    print_success "  • HyperChi package: http://localhost:$port/pkg/github.com/regiellis/hyperchi/hyperchi/"
    print_success "  • Examples: http://localhost:$port/pkg/github.com/regiellis/hyperchi/examples/"
    echo ""
    print_status "Press Ctrl+C to stop the server"
    
    godoc -http=:$port
}

# Function to generate static documentation
generate_static_docs() {
    local output_dir=${1:-"docs/generated"}
    
    check_godoc
    
    print_status "Generating static documentation to $output_dir..."
    
    # Create output directory
    mkdir -p "$output_dir"
    
    # Start godoc server in background
    print_status "Starting temporary godoc server..."
    godoc -http=:6061 &
    GODOC_PID=$!
    
    # Wait for server to start
    sleep 3
    
    # Function to cleanup on exit
    cleanup() {
        print_status "Stopping godoc server..."
        kill $GODOC_PID 2>/dev/null || true
    }
    trap cleanup EXIT
    
    # Check if server is running
    if ! curl -s http://localhost:6061 > /dev/null; then
        print_error "Could not start godoc server"
        exit 1
    fi
    
    print_status "Downloading documentation pages..."
    
    # Download main package documentation
    curl -s "http://localhost:6061/pkg/github.com/regiellis/hyperchi/" > "$output_dir/index.html" || {
        print_error "Failed to download main documentation"
        exit 1
    }
    
    # Download HyperChi package documentation
    mkdir -p "$output_dir/hyperchi"
    curl -s "http://localhost:6061/pkg/github.com/regiellis/hyperchi/hyperchi/" > "$output_dir/hyperchi/index.html" || {
        print_error "Failed to download hyperchi package documentation"
        exit 1
    }
    
    # Download examples documentation if it exists
    if curl -s "http://localhost:6061/pkg/github.com/regiellis/hyperchi/examples/" > /dev/null 2>&1; then
        mkdir -p "$output_dir/examples"
        curl -s "http://localhost:6061/pkg/github.com/regiellis/hyperchi/examples/" > "$output_dir/examples/index.html"
    fi
    
    # Download CSS and other assets
    print_status "Downloading assets..."
    mkdir -p "$output_dir/lib/godoc"
    curl -s "http://localhost:6061/lib/godoc/style.css" > "$output_dir/lib/godoc/style.css" 2>/dev/null || true
    curl -s "http://localhost:6061/lib/godoc/jquery.js" > "$output_dir/lib/godoc/jquery.js" 2>/dev/null || true
    curl -s "http://localhost:6061/lib/godoc/godocs.js" > "$output_dir/lib/godoc/godocs.js" 2>/dev/null || true
    
    # Stop the temporary server
    kill $GODOC_PID 2>/dev/null || true
    
    print_success "Static documentation generated in $output_dir/"
    print_status "Open $output_dir/index.html in your browser to view"
}

# Function to check documentation coverage
check_docs_coverage() {
    check_godoc
    
    print_status "Checking documentation coverage..."
    
    # Find all exported functions, types, etc. without documentation
    print_status "Scanning for undocumented exports..."
    
    # Use go doc to list all exports and check for documentation
    go doc -all ./hyperchi | grep -E "^(func|type|var|const)" | while read -r line; do
        echo "  $line"
    done
    
    print_status "Checking for missing package documentation..."
    
    # Check each Go file for package-level documentation
    find ./hyperchi -name "*.go" -not -name "*_test.go" | while read -r file; do
        if ! grep -q "^// Package\|^/\*" "$file"; then
            print_warning "Missing package documentation: $file"
        fi
    done
    
    print_status "Documentation coverage check complete"
    print_status "Review the output above for any missing documentation"
}

# Function to open documentation in browser
open_docs() {
    local port=${1:-6060}
    local url="http://localhost:$port/pkg/github.com/regiellis/hyperchi/"
    
    print_status "Opening documentation in browser..."
    
    # Try to open in browser (cross-platform)
    if command -v open &> /dev/null; then
        # macOS
        open "$url"
    elif command -v xdg-open &> /dev/null; then
        # Linux
        xdg-open "$url"
    elif command -v start &> /dev/null; then
        # Windows
        start "$url"
    else
        print_warning "Could not open browser automatically"
        print_status "Please open: $url"
    fi
}

# Parse command line arguments
COMMAND="serve"
PORT="6060"
OUTPUT_DIR="docs/generated"

while [[ $# -gt 0 ]]; do
    case $1 in
        serve|generate|check|install|help)
            COMMAND="$1"
            shift
            ;;
        -p|--port)
            PORT="$2"
            shift 2
            ;;
        -o|--output)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -h|--help)
            show_help
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

# Execute command
case $COMMAND in
    serve)
        check_godoc
        serve_docs "$PORT"
        ;;
    generate)
        generate_static_docs "$OUTPUT_DIR"
        ;;
    check)
        check_docs_coverage
        ;;
    install)
        install_godoc
        ;;
    help)
        show_help
        ;;
    *)
        print_error "Unknown command: $COMMAND"
        show_help
        exit 1
        ;;
esac