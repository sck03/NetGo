#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
version=${NETGO_VERSION:-}
arch=${NETGO_ARCH:-amd64}
plan=false
while (($#)); do
 case "$1" in
  --version) version=${2:?Missing version}; shift 2;;
  --arch) arch=${2:?Missing architecture}; shift 2;;
  --plan) plan=true; shift;;
  *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2;;
 esac
done
version=$(python3 -B scripts/build_config.py --target vps --version "$version" --arch "$arch" --field version)
export NETGO_VERSION="$version" NETGO_ARCH="$arch"
if $plan; then python3 -B scripts/build_config.py --target vps; exit 0; fi
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Build on Linux amd64'; exit 1; }
export GOCACHE="$root/.cache/toolchain/go-build" GOPATH="$root/.cache/toolchain/go"
export GOMODCACHE="$GOPATH/pkg/mod" GOTMPDIR="$root/.cache/toolchain/tmp"
export GOMAXPROCS=${GOMAXPROCS:-2} GOFLAGS=${GOFLAGS:--p=1} GOMEMLIMIT=${GOMEMLIMIT:-2GiB}
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR" "$root/dist/vps/bin"
CGO_ENABLED=0 go test ./internal/node ./internal/deployment
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X netgo/internal/model.Version=$version" -o "$root/dist/vps/bin/netgo-vpsctl" ./cmd/netgo-vpsctl
source=$(python3 -B scripts/prepare-upstream.py --path-only)
cd "$source"
CGO_ENABLED=0 go test ./relay/cmd ./shared/relay/tls ./shared/relay/client -run 'TestNetGo|Test.*Relay' -count=1
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$root/dist/vps/bin/netbird-relay" ./relay
cd "$root"
frp=$(python3 -c 'import json;print(json.load(open("upstream/versions.lock.json"))["frp"]["version"])')
GOBIN="$root/dist/vps/bin" CGO_ENABLED=0 go install -trimpath -tags frps,noweb -ldflags '-s -w' "github.com/fatedier/frp/cmd/frps@v$frp"
"$root/dist/vps/bin/frps" --version
(cd dist/vps && sha256sum bin/frps bin/netbird-relay bin/netgo-vpsctl > SHA256SUMS)
python3 -B scripts/package-vps.py --version "$version" --arch "$arch"
echo 'VPS auxiliary: dist/vps-package'
