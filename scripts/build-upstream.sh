#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
python3 -B scripts/linux_preflight.py --mode build
source=$(python3 -B scripts/prepare-upstream.py --path-only)
mkdir -p dist/upstream-linux-amd64
root=$PWD
cd "$source"
[[ $(uname -s) == Linux ]] || { echo 'Build SQLite IdP natively on Linux with CGO'; exit 1; }
CGO_ENABLED=1 go test ./client/cmd ./client/grpc ./client/server ./management/internals/shared/grpc ./management/server/http/handlers/peers -run 'TestNetGo|TestLogin' -count=1
CGO_ENABLED=1 go build -trimpath -o "$root/dist/upstream-linux-amd64/netbird-server" ./combined
CGO_ENABLED=1 go build -trimpath -o "$root/dist/upstream-linux-amd64/netbird" ./client
CGO_ENABLED=1 go build -trimpath -o "$root/dist/upstream-linux-amd64/netbird-relay" ./relay
cd ../dex
CGO_ENABLED=1 go test ./server -run TestNetGo -count=1
cd "$root"
caddy_version=$(python3 -c 'import json; print(json.load(open("upstream/versions.lock.json"))["caddy"]["version"])')
frp_version=$(python3 -c 'import json; print(json.load(open("upstream/versions.lock.json"))["frp"]["version"])')
GOBIN="$root/dist/upstream-linux-amd64" CGO_ENABLED=0 go install "github.com/caddyserver/caddy/v2/cmd/caddy@v$caddy_version"
GOBIN="$root/dist/upstream-linux-amd64" CGO_ENABLED=0 go install "github.com/fatedier/frp/cmd/frpc@v$frp_version" "github.com/fatedier/frp/cmd/frps@v$frp_version"
cd "$root/dist/upstream-linux-amd64"
./caddy version
./frpc --version
./frps --version
sha256sum netbird-server netbird netbird-relay caddy frpc frps > SHA256SUMS
