#!/bin/bash
set -e

echo "Building wifichat..."
mkdir -p bin
go build -o bin/wifichat ./cmd/wifichat
echo "Build successful! Binary located at bin/wifichat"

if [ "$1" == "test" ]; then
    echo "Running tests..."
    go test -v -race ./...
fi
