#!/bin/bash
# HyperChi development script with Air live reload

# Check if Air is installed
if ! command -v air &> /dev/null; then
    echo "Air is not installed. Please install it first:"
    echo ""
    echo "go install github.com/air-verse/air@latest"
    echo ""
    echo "Or visit: https://github.com/air-verse/air"
    exit 1
fi

# Check if .air.toml exists
if [ ! -f .air.toml ]; then
    echo "Air configuration not found. Run 'hyperchi air init' first."
    exit 1
fi

# Load environment variables if .env exists
if [ -f .env ]; then
    echo "Loading environment variables from .env"
    export $(cat .env | grep -v '^#' | xargs)
fi

# Start Air with live reload
echo "Starting HyperChi development server with Air..."
echo "Visit http://localhost:8080 (or your configured port)"
echo "Press Ctrl+C to stop"
echo ""
air
