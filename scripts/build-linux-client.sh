#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
export GOCACHE="${GOCACHE:-$root/.cache/toolchain/go-build}" GOPATH="${GOPATH:-$root/.cache/toolchain/go}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}" GOTMPDIR="${GOTMPDIR:-$root/.cache/toolchain/tmp}"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR" .tools/native-json dist/linux-desktop-amd64
python3 -c 'import hashlib,json,pathlib,urllib.request; c=json.load(open("upstream/versions.lock.json"))["json"]; p=pathlib.Path(".tools/native-json/nlohmann/json.hpp"); p.parent.mkdir(parents=True,exist_ok=True); data=p.read_bytes() if p.exists() else urllib.request.urlopen(c["url"],timeout=30).read(); assert hashlib.sha256(data).hexdigest()==c["sha256"]; p.write_bytes(data)'
bash scripts/build-linux-native.sh
go test ./internal/client
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o dist/linux-desktop-amd64/netgo-client-service ./cmd/netgo-client-service
upstream=$(python3 -B scripts/prepare-upstream.py --path-only)
(cd "$upstream" && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$root/dist/linux-desktop-amd64/netbird" ./client)
cp apps/linux/netgo.desktop THIRD_PARTY_NOTICES.md dist/linux-desktop-amd64/
mkdir -p dist/linux-desktop-amd64/licenses
cp "$upstream/LICENSE" dist/linux-desktop-amd64/licenses/netbird.txt
cp "$(go env GOROOT)/LICENSE" dist/linux-desktop-amd64/licenses/go.txt
(cd dist/linux-desktop-amd64 && sha256sum netgo-desktop netgo-client-service netbird netgo.desktop THIRD_PARTY_NOTICES.md licenses/netbird.txt licenses/go.txt > SHA256SUMS)
tar -czf dist/NetGo-0.3.0-dev-linux-client-amd64.tar.gz -C dist linux-desktop-amd64/netgo-desktop linux-desktop-amd64/netgo-client-service linux-desktop-amd64/netbird linux-desktop-amd64/netgo.desktop linux-desktop-amd64/THIRD_PARTY_NOTICES.md linux-desktop-amd64/SHA256SUMS linux-desktop-amd64/licenses/netbird.txt linux-desktop-amd64/licenses/go.txt
printf 'Linux desktop client: dist/NetGo-0.3.0-dev-linux-client-amd64.tar.gz\n'
