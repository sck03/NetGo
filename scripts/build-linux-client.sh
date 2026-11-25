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
version=$(python3 -B scripts/build_config.py --target linux-client --version "$version" --arch "$arch" --field version)
export NETGO_VERSION="$version" NETGO_ARCH="$arch"
if $plan; then python3 -B scripts/build_config.py --target linux-client; exit 0; fi
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Build on Linux amd64'; exit 1; }
export GOCACHE="${GOCACHE:-$root/.cache/toolchain/go-build}" GOPATH="${GOPATH:-$root/.cache/toolchain/go}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}" GOTMPDIR="${GOTMPDIR:-$root/.cache/toolchain/tmp}"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR" .tools/native-json dist/linux-desktop-amd64
python3 -c 'import hashlib,json,pathlib,urllib.request; c=json.load(open("upstream/versions.lock.json"))["json"]; p=pathlib.Path(".tools/native-json/nlohmann/json.hpp"); p.parent.mkdir(parents=True,exist_ok=True); data=p.read_bytes() if p.exists() else urllib.request.urlopen(c["url"],timeout=30).read(); assert hashlib.sha256(data).hexdigest()==c["sha256"]; p.write_bytes(data)'
bash scripts/build-linux-native.sh
go test ./internal/client
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X netgo/internal/model.Version=$version" -o dist/linux-desktop-amd64/netgo-client-service ./cmd/netgo-client-service
upstream=$(python3 -B scripts/prepare-upstream.py --path-only)
(cd "$upstream" && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$root/dist/linux-desktop-amd64/netbird" ./client)
cp apps/linux/netgo.desktop THIRD_PARTY_NOTICES.md dist/linux-desktop-amd64/
cp apps/linux/README.md dist/linux-desktop-amd64/README.md
mkdir -p dist/linux-desktop-amd64/licenses
cp "$upstream/LICENSE" dist/linux-desktop-amd64/licenses/netbird.txt
cp "$(go env GOROOT)/LICENSE" dist/linux-desktop-amd64/licenses/go.txt
(cd dist/linux-desktop-amd64 && sha256sum netgo-desktop netgo-client-service netbird netgo.desktop README.md THIRD_PARTY_NOTICES.md licenses/netbird.txt licenses/go.txt > SHA256SUMS)
python3 -B scripts/build_config.py --target linux-client --buildinfo dist/linux-desktop-amd64
tar -czf "dist/NetGo-${version}-linux-client-${arch}.tar.gz" -C dist linux-desktop-amd64/netgo-desktop linux-desktop-amd64/netgo-client-service linux-desktop-amd64/netbird linux-desktop-amd64/netgo.desktop linux-desktop-amd64/README.md linux-desktop-amd64/THIRD_PARTY_NOTICES.md linux-desktop-amd64/SHA256SUMS linux-desktop-amd64/licenses/netbird.txt linux-desktop-amd64/licenses/go.txt
printf 'Linux desktop client: dist/NetGo-%s-linux-client-%s.tar.gz\n' "$version" "$arch"
