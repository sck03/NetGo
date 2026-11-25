#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
python3 -B scripts/linux_preflight.py --mode build
arch=$(python3 -B scripts/linux_arch.py)
source=$(python3 -B scripts/prepare-upstream.py --path-only)
mkdir -p dist/upstream-linux-$arch
root=$PWD
cd "$source"
[[ $(uname -s) == Linux ]] || { echo 'Build SQLite IdP natively on Linux with CGO'; exit 1; }
CGO_ENABLED=1 go test ./client/firewall/nftables ./client/firewall/uspfilter ./client/cmd ./client/grpc ./client/server ./client/internal/peer ./shared/relay/client ./shared/relay/tls ./shared/relay/client/dialer/ws ./shared/relay/client/dialer/quic ./management/internals/shared/grpc ./management/server/http/handlers/peers ./management/internals/server ./combined/cmd ./relay/cmd -run 'TestNetGo|TestLogin|TestFragment|Test.*Relay' -count=1
CGO_ENABLED=1 go build -trimpath -ldflags '-s -w' -o "$root/dist/upstream-linux-$arch/netbird-server" ./combined
CGO_ENABLED=1 go build -trimpath -ldflags '-s -w' -o "$root/dist/upstream-linux-$arch/netbird" ./client
CGO_ENABLED=1 go build -trimpath -ldflags '-s -w' -o "$root/dist/upstream-linux-$arch/netbird-relay" ./relay
cd ../dex
CGO_ENABLED=1 go test ./server -run TestNetGo -count=1
cd "$root"
caddy_version=$(python3 -c 'import json; print(json.load(open("upstream/versions.lock.json"))["caddy"]["version"])')
frp_version=$(python3 -c 'import json; print(json.load(open("upstream/versions.lock.json"))["frp"]["version"])')
GOBIN="$root/dist/upstream-linux-$arch" CGO_ENABLED=0 go install -trimpath -ldflags "-s -w -X github.com/caddyserver/caddy/v2.CustomVersion=v$caddy_version" "github.com/caddyserver/caddy/v2/cmd/caddy@v$caddy_version"
# NetGo does not expose frp's separate dashboard. The module archive omits its
# generated web assets; use the same documented noweb fallback as frp's Makefile.
GOBIN="$root/dist/upstream-linux-$arch" CGO_ENABLED=0 go install -tags frpc,noweb -ldflags '-s -w' "github.com/fatedier/frp/cmd/frpc@v$frp_version"
GOBIN="$root/dist/upstream-linux-$arch" CGO_ENABLED=0 go install -tags frps,noweb -ldflags '-s -w' "github.com/fatedier/frp/cmd/frps@v$frp_version"
cd "$root/dist/upstream-linux-$arch"
./caddy version
./frpc --version
./frps --version
sha256sum netbird-server netbird netbird-relay caddy frpc frps > SHA256SUMS
