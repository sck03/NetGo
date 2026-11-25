#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
[[ $EUID == 0 && $(uname -s) == Linux && ( $(uname -m) == x86_64 || $(uname -m) == aarch64 ) ]] || { echo 'Requires root on Debian amd64/arm64'; exit 1; }
apt-get update
apt-get install -y --no-upgrade --no-install-recommends git ca-certificates curl python3 python3-cryptography build-essential xz-utils
python3 - "$root" <<'PY'
import hashlib,json,shlex,subprocess,sys,tempfile
from pathlib import Path
root=Path(sys.argv[1]);sys.path.insert(0,str(root/'scripts'))
from archive_utils import extract_tar
from linux_arch import native_arch
arch=native_arch()
lock=json.loads((root/'upstream/versions.lock.json').read_text());item=lock['linux_toolchain_archives'][arch]['go']
tools=root/'.tools';tools.mkdir(exist_ok=True)
destination=tools/('go-vps-'+arch+'-'+lock['toolchain']['go']);stamp=destination/'.archive-sha256'
if not stamp.exists():
 if destination.exists():raise SystemExit('Unrecognized Go directory')
 with tempfile.TemporaryDirectory(prefix='vps-go-',dir=tools) as temporary:
  temporary=Path(temporary);archive=temporary/item['filename']
  subprocess.run(['curl','-fL','--proto','=https','--proto-redir','=https',item['url'],'-o',str(archive)],check=True)
  if hashlib.sha256(archive.read_bytes()).hexdigest()!=item['sha256']:raise SystemExit('Go checksum mismatch')
  extract_tar(archive,temporary/'unpacked',allow_links=True)
  (temporary/'unpacked/go').rename(destination)
  stamp.write_text(item['sha256']+'\n')
if stamp.read_text().strip()!=item['sha256']:raise SystemExit('Go toolchain stamp mismatch')
(tools/'vps.env').write_text('export PATH='+shlex.quote(str(destination/'bin'))+':"$PATH"\nexport GOTOOLCHAIN=local\n')
PY
