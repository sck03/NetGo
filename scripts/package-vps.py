#!/usr/bin/env python3
"""Package only the auxiliary binaries and its independent maintenance tools."""
import hashlib
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import zipfile
from runtime_files import VPS_FILES
from build_config import config
from linux_arch import verify_elf

ROOT = Path(__file__).resolve().parents[1]
def source_revision():
    result = subprocess.run(['git','rev-parse','HEAD'],cwd=ROOT,capture_output=True,text=True)
    return result.stdout.strip() if result.returncode == 0 else 'local-source-snapshot'
def sums(root):
    (root / 'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.relative_to(root).as_posix() + '\n' for p in sorted(root.rglob('*')) if p.is_file() and p != root / 'SHA256SUMS'),encoding='utf-8')

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--version');parser.add_argument('--arch');args=parser.parse_args()
    build=config('vps',args.version,args.arch)
    output = ROOT / 'dist/vps-package'; output.mkdir(parents=True, exist_ok=True)
    binary_info=json.loads(subprocess.check_output([str(ROOT/'dist/vps/bin/netgo-vpsctl'),'version'],text=True))
    if binary_info['version']!=build['version'] or binary_info['arch']!=build['arch']:raise ValueError('VPS executable version/architecture differs from package')
    for name in ('netgo-vpsctl','netbird-relay','frps'): verify_elf(ROOT/'dist/vps/bin'/name,build['arch'])
    cache = ROOT / '.cache'; cache.mkdir(exist_ok=True)
    lock = json.loads((ROOT / 'upstream/versions.lock.json').read_text())
    prepared = Path(subprocess.check_output([sys.executable,'-B',str(ROOT / 'scripts/prepare-upstream.py'),'--path-only'],text=True).strip())
    gomod = Path(subprocess.check_output(['go','env','GOMODCACHE'],text=True).strip())
    goroot = Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
    with tempfile.TemporaryDirectory(prefix='vps-package-',dir=cache) as temporary:
        stage = Path(temporary)
        shutil.copytree(ROOT / 'dist/vps', stage / 'dist/vps')
        (stage / 'licenses').mkdir()
        for name in (*VPS_FILES,'THIRD_PARTY_NOTICES.md'):
            shutil.copy2(ROOT / name,stage / name)
        for source,name in [(prepared / 'relay/LICENSE','netbird-relay.txt'),(gomod / ('github.com/fatedier/frp@v'+lock['frp']['version']) / 'LICENSE','frp.txt'),(goroot / 'LICENSE','go.txt')]:
            shutil.copy2(source,stage / 'licenses' / name)
        (output / 'BUILDINFO-vps.json').write_text(json.dumps({'version':build['version'],'arch':build['arch'],'platform':'linux-'+build['arch'],'role':'VPS auxiliary','source':source_revision(),'netbird':lock['netbird']['version'],'frp':lock['frp']['version']},indent=2)+'\n')
        sums(stage)
        with zipfile.ZipFile(output / f"NetGo-{build['version']}-vps-linux-{build['arch']}.zip",'w',zipfile.ZIP_DEFLATED) as z:
            for path in sorted(stage.rglob('*')):
                if path.is_file(): z.write(path,path.relative_to(stage).as_posix())
    installer=output/('netgo-vps-installer-linux-'+build['arch']);shutil.copy2(ROOT/'dist/vps/bin/netgo-vpsctl',installer)
    (output/(installer.name+'.sha256')).write_text(hashlib.sha256(installer.read_bytes()).hexdigest()+'  '+installer.name+'\n')
    sums(output)

if __name__ == '__main__': main()
