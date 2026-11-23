#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
export GOCACHE="$root/.cache/toolchain/go-build" GOPATH="$root/.cache/toolchain/go"
export GOMODCACHE="$GOPATH/pkg/mod" GOTMPDIR="$root/.cache/toolchain/tmp" npm_config_cache="$root/.cache/toolchain/npm-cache"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR" "$npm_config_cache"
report=".cache/linux-server-$(date -u +%Y%m%d-%H%M%S)-$$"
python3 -B scripts/verify-linux.py --output "$report"
python3 -B scripts/package-server.py --verification "$report/verification.json" --output dist/linux-server
printf 'Linux server: dist/linux-server\n'
