#!/usr/bin/env bash
# Source-tree compatibility entry for build checks; runtime uses the root installer.
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/../.." && pwd)
if [[ ${1:-} == --plan ]];then printf '%s\n' 'Plan only: Go server installer; no system changes.';exit 0;fi
exec "$root/dist/linux-amd64/netgoctl" server install --source "$root" --install-only
