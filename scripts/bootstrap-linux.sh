#!/usr/bin/env bash
# Prepare runtime dependencies, optionally the exact source-build toolchain.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
mode=${1:---runtime}
[[ $# -le 1 && ( $mode == --runtime || $mode == --build || $mode == --plan ) ]] || { echo 'Usage: bootstrap-linux.sh [--runtime|--build|--plan]' >&2; exit 2; }
if [[ $mode == --plan ]]; then
 printf '%s\n' 'Debian 12/13 amd64/arm64: install missing runtime packages. --build also prepares SHA256-pinned Go/Node in .tools; no global toolchain links or service activation.'
 exit 0
fi
[[ $(uname -s) == Linux && ( $(uname -m) == x86_64 || $(uname -m) == aarch64 ) && $EUID -eq 0 ]] || { echo 'Requires root on Debian 12/13 amd64/arm64' >&2; exit 1; }
os_id=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
os_version=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | tr -d '"')
[[ $os_id == debian && ( $os_version == 12 || $os_version == 13 ) ]] || { echo 'Supported: Debian 12/13 amd64/arm64' >&2; exit 1; }
packages=(ca-certificates curl python3 nftables conntrack iproute2 iptables procps util-linux openssl systemd unzip)
if [[ $mode == --build ]]; then packages+=(build-essential pkg-config libssl-dev git python3-cryptography xz-utils); fi
missing=()
for package in "${packages[@]}"; do
 [[ $(dpkg-query -W -f='${Status}' "$package" 2>/dev/null || true) == 'install ok installed' ]] || missing+=("$package")
done
if ((${#missing[@]})); then
 apt-get update
 DEBIAN_FRONTEND=noninteractive apt-get install -y --no-upgrade --no-install-recommends "${missing[@]}"
fi
if [[ $mode == --build ]]; then
 python3 - "$root" <<'PY'
import hashlib, json, os
from pathlib import Path
import shlex, subprocess, tempfile, sys
root=Path(__import__('sys').argv[1])
sys.path.insert(0,str(root/'scripts'))
from archive_utils import extract_tar
from linux_arch import native_arch
arch=native_arch()
lock=json.loads((root/'upstream/versions.lock.json').read_text())
tools=root/'.tools'
tools.mkdir(mode=0o700,exist_ok=True)
bins=[]
for name in ('go','node'):
    item=lock['linux_toolchain_archives'][arch][name]
    destination=tools/(name+'-linux-'+arch+'-'+lock['toolchain'][name])
    executable=destination/'bin'/name
    stamp=destination/'.archive-sha256'
    if not (executable.is_file() and stamp.is_file() and stamp.read_text().strip()==item['sha256']):
        if destination.exists():
            raise SystemExit('Unrecognized toolchain directory; inspect before retrying: '+str(destination))
        archive=tools/item['filename']
        if not archive.is_file() or hashlib.file_digest(archive.open('rb'),'sha256').hexdigest()!=item['sha256']:
            partial=archive.with_suffix(archive.suffix+'.part')
            subprocess.run(['curl','--fail','--location','--silent','--show-error','--proto','=https','--proto-redir','=https',
                            '--retry','2','--connect-timeout','20','--max-time','600',item['url'],'--output',str(partial)],check=True)
            with partial.open('rb') as handle:
                if hashlib.file_digest(handle,'sha256').hexdigest()!=item['sha256']:
                    raise SystemExit('Toolchain SHA256 mismatch: '+name)
            partial.replace(archive)
        with tempfile.TemporaryDirectory(prefix='netgo-toolchain-',dir=tools) as stage:
            extract_tar(archive,stage,allow_links=True)
            members=list(Path(stage).iterdir())
            if len(members)!=1 or not (members[0]/'bin'/name).is_file():
                raise SystemExit('Unexpected toolchain archive layout')
            members[0].rename(destination)
        stamp.write_text(item['sha256']+'\n')
    bins.append(str(destination/'bin'))
(tools/'linux.env').write_text('export PATH='+shlex.quote(':'.join(bins))+':"$PATH"\nexport GOTOOLCHAIN=local\n'
    'export GOPROXY="${GOPROXY:-https://goproxy.cn|https://proxy.golang.org}"\n'
    'export GOFLAGS="${GOFLAGS:--p=1}"\nexport GOMAXPROCS="${GOMAXPROCS:-2}"\nexport GOMEMLIMIT="${GOMEMLIMIT:-2GiB}"\n')
print('Pinned build environment: source '+str(tools/'linux.env'))
PY
fi
python3 -B "$root/scripts/linux_preflight.py" --mode install
