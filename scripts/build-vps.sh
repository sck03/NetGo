#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Build on Linux amd64'; exit 1; }
root=$PWD
export GOCACHE="$root/.cache/toolchain/go-build" GOPATH="$root/.cache/toolchain/go"
export GOMODCACHE="$GOPATH/pkg/mod" GOTMPDIR="$root/.cache/toolchain/tmp"
export GOMAXPROCS=${GOMAXPROCS:-2} GOFLAGS=${GOFLAGS:--p=1} GOMEMLIMIT=${GOMEMLIMIT:-2GiB}
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR" "$root/dist/vps/bin"
CGO_ENABLED=0 go test ./internal/node ./internal/deployment
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$root/dist/vps/bin/netgo-vpsctl" ./cmd/netgo-vpsctl
source=$(python3 -B scripts/prepare-upstream.py --path-only)
cd "$source"
CGO_ENABLED=0 go test ./relay/cmd ./shared/relay/tls ./shared/relay/client -run 'TestNetGo|Test.*Relay' -count=1
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$root/dist/vps/bin/netbird-relay" ./relay
cd "$root"
frp=$(python3 -c 'import json;print(json.load(open("upstream/versions.lock.json"))["frp"]["version"])')
GOBIN="$root/dist/vps/bin" CGO_ENABLED=0 go install -trimpath -tags frps,noweb -ldflags '-s -w' "github.com/fatedier/frp/cmd/frps@v$frp"
"$root/dist/vps/bin/frps" --version
(cd dist/vps && sha256sum bin/frps bin/netbird-relay bin/netgo-vpsctl > SHA256SUMS)
python3 -B scripts/package-vps.py
echo 'VPS auxiliary: dist/vps-package'
