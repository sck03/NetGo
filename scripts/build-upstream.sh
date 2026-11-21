#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
python3 scripts/prepare-upstream.py
mkdir -p dist/upstream-linux-amd64
root=$PWD
cd .cache/upstream-build/netbird
[[ $(uname -s) == Linux ]] || { echo 'Build SQLite IdP natively on Linux with CGO'; exit 1; }
CGO_ENABLED=1 go test ./client/grpc ./client/server ./management/internals/shared/grpc ./management/server/http/handlers/peers -run 'TestNetGo|TestLogin' -count=1
CGO_ENABLED=1 go build -trimpath -o "$root/dist/upstream-linux-amd64/netbird-server" ./combined
CGO_ENABLED=1 go build -trimpath -o "$root/dist/upstream-linux-amd64/netbird" ./client
CGO_ENABLED=1 go build -trimpath -o "$root/dist/upstream-linux-amd64/netbird-relay" ./relay
cd ../dex
CGO_ENABLED=1 go test ./server -run TestNetGo -count=1
cd "$root/dist/upstream-linux-amd64"
sha256sum netbird-server netbird netbird-relay > SHA256SUMS
