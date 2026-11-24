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
version=$(python3 -B scripts/build_config.py --target linux-server --version "$version" --arch "$arch" --field version)
export NETGO_VERSION="$version" NETGO_ARCH="$arch"
if $plan; then python3 -B scripts/build_config.py --target linux-server; exit 0; fi
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Build on Linux amd64'; exit 1; }
export GOCACHE="$root/.cache/toolchain/go-build" GOPATH="$root/.cache/toolchain/go"
export GOMODCACHE="$GOPATH/pkg/mod" GOTMPDIR="$root/.cache/toolchain/tmp" npm_config_cache="$root/.cache/toolchain/npm-cache"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR" "$npm_config_cache"
report=".cache/linux-server-$(date -u +%Y%m%d-%H%M%S)-$$"
if ! python3 -B scripts/verify-linux.py --output "$report"; then
 python3 - "$report" <<'PY'
import json,sys
from pathlib import Path
root=Path(sys.argv[1]);manifest=root/'verification.json'
if manifest.exists():
 for check in json.loads(manifest.read_text())['checks']:
  if check['status']=='FAIL':
   print('Failed check: '+check['name'],file=sys.stderr)
   log=root/check['log']
   if log.exists():print('\n'.join(log.read_text(errors='replace').splitlines()[-60:]),file=sys.stderr)
PY
 exit 1
fi
python3 -B scripts/package-server.py --version "$version" --arch "$arch" --verification "$report/verification.json" --output dist/linux-server
printf 'Linux server: dist/linux-server\n'
