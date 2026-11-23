#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
frontend=true; checks=true
for arg in "$@"; do
  case "$arg" in
    --skip-frontend) frontend=false ;;
    --skip-checks) checks=false ;;
    *) printf 'Unknown argument: %s\n' "$arg" >&2; exit 2 ;;
  esac
done
python3 -B scripts/linux_preflight.py --mode build
if $frontend; then npm ci --no-fund; npm run build; npm test; fi
if $checks; then CGO_ENABLED=1 go test -race ./internal/... ./cmd/...; go vet ./internal/... ./cmd/...; fi
mkdir -p dist/linux-amd64
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns; do
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o "dist/linux-amd64/$name" "./cmd/$name"
done
go run ./cmd/netgoctl version | python3 -c 'import json,sys; v=json.load(sys.stdin); json.dump(dict(version=v["version"],protocol=v["protocol"],schema=v["schema"],os="linux",arch="amd64"),open("dist/linux-amd64/release.json","w"))'
(cd dist/linux-amd64 && sha256sum netgo-* netgoctl release.json > SHA256SUMS)
(cd dist && sha256sum linux-amd64/netgo-* linux-amd64/netgoctl linux-amd64/release.json > SHA256SUMS)
