#!/usr/bin/env python3
"""Package only the auxiliary binaries and its independent maintenance tools."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
def source_revision():
    result = subprocess.run(['git','rev-parse','HEAD'],cwd=ROOT,capture_output=True,text=True)
    return result.stdout.strip() if result.returncode == 0 else 'local-source-snapshot'
def sums(root):
    (root / 'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.relative_to(root).as_posix() + '\n' for p in sorted(root.rglob('*')) if p.is_file() and p != root / 'SHA256SUMS'),encoding='utf-8')

def main():
    output = ROOT / 'dist/vps-package'; output.mkdir(parents=True, exist_ok=True)
    cache = ROOT / '.cache'; cache.mkdir(exist_ok=True)
    lock = json.loads((ROOT / 'upstream/versions.lock.json').read_text())
    prepared = Path(subprocess.check_output([sys.executable,'-B',str(ROOT / 'scripts/prepare-upstream.py'),'--path-only'],text=True).strip())
    gomod = Path(subprocess.check_output(['go','env','GOMODCACHE'],text=True).strip())
    goroot = Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
    with tempfile.TemporaryDirectory(prefix='vps-package-',dir=cache) as temporary:
        stage = Path(temporary)
        shutil.copytree(ROOT / 'dist/vps', stage / 'dist/vps')
        (stage / 'scripts').mkdir(); (stage / 'licenses').mkdir()
        for name in ('install-vps.sh','THIRD_PARTY_NOTICES.md','scripts/vps_setup.py'):
            shutil.copy2(ROOT / name,stage / name)
        for source,name in [(prepared / 'relay/LICENSE','netbird-relay.txt'),(gomod / ('github.com/fatedier/frp@v'+lock['frp']['version']) / 'LICENSE','frp.txt'),(goroot / 'LICENSE','go.txt')]:
            shutil.copy2(source,stage / 'licenses' / name)
        (stage / 'BUILDINFO.json').write_text(json.dumps({'version':'0.3.0-dev','platform':'linux-amd64','role':'VPS auxiliary','source':source_revision(),'netbird':lock['netbird']['version'],'frp':lock['frp']['version']},indent=2)+'\n')
        sums(stage)
        with zipfile.ZipFile(output / 'NetGo-0.3.0-dev-vps-linux-amd64.zip','w',zipfile.ZIP_DEFLATED) as z:
            for path in sorted(stage.rglob('*')):
                if path.is_file(): z.write(path,path.relative_to(stage).as_posix())
    sums(output)

if __name__ == '__main__': main()
