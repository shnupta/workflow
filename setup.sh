#!/usr/bin/env bash
set -e

echo "==> workflow setup"

# Check Go
if ! command -v go &>/dev/null; then
  echo "Error: Go is not installed. Install from https://go.dev/dl/"
  exit 1
fi

# Check sqlite3 (for CGO)
if ! pkg-config --libs sqlite3 &>/dev/null 2>&1; then
  echo "Note: libsqlite3 not found via pkg-config — trying anyway (may need: brew install sqlite or apt install libsqlite3-dev)"
fi

# Build
echo "==> Building..."
go build -o workflow ./cmd/workflow
echo "    Built ./workflow"

# Config
if [ ! -f .env ]; then
  cp .env.example .env
  echo "    Created .env — add your GITHUB_TOKEN and ANTHROPIC_API_KEY"
else
  echo "    .env already exists"
fi

echo ""
echo "Done. To start:"
echo "  source .env && ./workflow"
echo ""
echo "Then open http://localhost:7070"
