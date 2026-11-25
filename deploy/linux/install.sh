#!/usr/bin/env bash
# Source-tree compatibility entry for build checks; runtime uses the root installer.
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/../.." && pwd)
if [[ ${1:-} == --plan ]];then printf '%s\n' 'Plan only: Go server installer; no system changes.';exit 0;fi
case $(uname -m) in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo "Unsupported architecture" >&2;exit 1;; esac
exec "$root/dist/linux-$arch/netgoctl" server install --source "$root" --install-only
