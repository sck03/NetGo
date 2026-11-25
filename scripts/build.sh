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
version=$(python3 -B scripts/build_config.py --target linux-server --field version)
project_ldflags=$(python3 -B scripts/build_config.py --target linux-server --field ldflags)
if $frontend; then npm ci --no-fund; npm run build; npm test; fi
if $checks; then CGO_ENABLED=1 go test -race ./internal/... ./cmd/...; go vet ./internal/... ./cmd/...; fi
arch=$(python3 -B scripts/linux_arch.py)
export NETGO_ARCH="$arch"
mkdir -p "dist/linux-$arch"
for name in netgo-server netgo-helper netgoctl netgo-ddns; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "$project_ldflags" -o "dist/linux-$arch/$name" "./cmd/$name"
done
go run -ldflags "$project_ldflags" ./cmd/netgoctl version | python3 -c 'import json,sys,os; arch=os.environ["NETGO_ARCH"]; v=json.load(sys.stdin); json.dump(dict(version=v["version"],protocol=v["protocol"],schema=v["schema"],os="linux",arch=arch),open("dist/linux-"+arch+"/release.json","w"))'
(cd dist/linux-$arch && sha256sum netgo-server netgo-helper netgoctl netgo-ddns release.json > SHA256SUMS)
(cd dist && sha256sum linux-$arch/netgo-server linux-$arch/netgo-helper linux-$arch/netgoctl linux-$arch/netgo-ddns linux-$arch/release.json > SHA256SUMS)
