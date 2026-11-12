#!/usr/bin/env bash
# Build the web SPA into internal/server/webdist for go:embed.
set -euo pipefail
cd "$(dirname "$0")/../web"
bun install
bun run build
echo "webdist rebuilt"
