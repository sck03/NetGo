#!/usr/bin/env python3
"""Package the complete natively verified Linux server, including patched upstream."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import zipfile

from verification import validate_report, verify_archive, sha256
from runtime_files import server_files,SERVER_BINARIES,SERVER_UPSTREAM
from build_config import config
from linux_arch import verify_elf

ROOT=Path(__file__).resolve().parents[1]


def sums(root):
    entries=[sha256(path)+'  '+path.relative_to(root).as_posix() for path in sorted(root.rglob('*'))
             if path.is_file() and path!=root/'SHA256SUMS']
    (root/'SHA256SUMS').write_text('\n'.join(entries)+'\n',encoding='utf-8')


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verification',required=True,type=Path)
    parser.add_argument('--version')
    parser.add_argument('--arch')
    parser.add_argument('--output',type=Path,default=ROOT/'dist/packages')
    args=parser.parse_args()
    build=config('linux-server',args.version,args.arch)
    arch=build['arch']
    verified=validate_report(ROOT,args.verification,'linux')
    release=json.loads((ROOT/f'dist/linux-{arch}/release.json').read_text())
    version=release['version']
    if version!=build['version']:raise ValueError('Server executable version differs from requested package version')
    if release!=dict(version=version,protocol=2,schema=2,os='linux',arch=arch):
        raise ValueError('incompatible server release')
    args.output.mkdir(parents=True,exist_ok=True)
    source=Path(subprocess.check_output([sys.executable,'-B',str(ROOT/'scripts/prepare-upstream.py'),'--path-only'],text=True).strip())
    with tempfile.TemporaryDirectory(prefix='netgo-server-package-',dir=ROOT/'.cache') as temporary:
        stage=Path(temporary)
        for name in server_files(ROOT):
            target=stage/name;target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(ROOT/name,target)
        for folder,names in ((f'linux-{arch}',(*SERVER_BINARIES,'release.json')),(f'upstream-linux-{arch}',SERVER_UPSTREAM)):
            target=stage/'dist'/folder;target.mkdir(parents=True)
            for name in names:
                if name!='release.json':verify_elf(ROOT/'dist'/folder/name,arch)
                shutil.copy2(ROOT/'dist'/folder/name,target/name)
            sums(target)
            if folder==f'linux-{arch}':shutil.copy2(ROOT/'dist'/folder/'SHA256SUMS',target/'SHA256SUMS')
        for name in ('THIRD_PARTY_NOTICES.md',):
            shutil.copy2(ROOT/name,stage/name)
        (stage/'licenses').mkdir()
        license_sources={'netbird-client.txt':source/'LICENSE','dex.txt':source.parent/'dex/LICENSE',
                         'netbird-management.txt':source/'management/LICENSE','netbird-combined.txt':source/'combined/LICENSE',
                         'netbird-relay.txt':source/'relay/LICENSE','netbird-signal.txt':source/'signal/LICENSE'}
        lock=json.loads((ROOT/'upstream/versions.lock.json').read_text())
        cache=Path(subprocess.check_output(['go','env','GOMODCACHE'],text=True).strip())
        goroot=Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
        license_sources.update({'caddy.txt':cache/('github.com/caddyserver/caddy/v2@v'+lock['caddy']['version'])/'LICENSE',
                                'frp.txt':cache/('github.com/fatedier/frp@v'+lock['frp']['version'])/'LICENSE',
                                'go.txt':goroot/'LICENSE'})
        for name,path in license_sources.items():
            if not path.is_file():raise ValueError('missing upstream license: '+name)
            shutil.copy2(path,stage/'licenses'/name)
        components=[]; seen=set()
        for folder in (f'linux-{arch}',f'upstream-linux-{arch}'):
            for binary in (stage/'dist'/folder).iterdir():
                if binary.name in ('release.json','SHA256SUMS'):continue
                for line in subprocess.check_output(['go','version','-m',str(binary)],text=True).splitlines():
                    fields=line.split()
                    if len(fields)>=3 and fields[0]=='dep' and tuple(fields[1:3]) not in seen:
                        seen.add(tuple(fields[1:3]));components.append({'type':'library','name':fields[1],'version':fields[2]})
        (args.output/'SBOM-server.cdx.json').write_text(json.dumps({'bomFormat':'CycloneDX','specVersion':'1.5','version':1,
            'metadata':{'component':{'type':'application','name':'NetGo Linux server','version':version}},'components':components},indent=2)+'\n')
        (args.output/'BUILDINFO-server.json').write_text(json.dumps({'schema':1,'version':version,'arch':arch,'platform':f'linux-{arch}',
            'status':'development; see runtime acceptance report for tested scope','verification':verified,
            'source_package':'NetGo-'+version+'-source.zip','source_tag':'https://github.com/sck03/NetGo/tree/v'+version},indent=2)+'\n')
        sums(stage)
        destination=args.output/('NetGo-'+version+f'-linux-{arch}.zip')
        with zipfile.ZipFile(destination,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as archive:
            for path in sorted(stage.rglob('*')):
                if path.is_file():archive.write(path,path.relative_to(stage).as_posix())
        expected={item['path']:item['sha256'] for item in verified['artifact_inputs']['files'] if item['path']!=f'dist/upstream-linux-{arch}/SHA256SUMS' and item['path']!=f'dist/upstream-linux-{arch}/frps'}
        checked=verify_archive(destination,expected)
    validate_report(ROOT,args.verification,'linux')
    (args.output/('NetGo-'+version+'-server-delivery.json')).write_text(json.dumps({'status':'PASS','verification':verified,'package':checked},indent=2)+'\n')
    installer=args.output/f'netgo-server-installer-linux-{arch}';shutil.copy2(ROOT/f'dist/linux-{arch}/netgoctl',installer)
    (args.output/(installer.name+'.sha256')).write_text(sha256(installer)+'  '+installer.name+'\n')
    sums(args.output)
    print(json.dumps(checked,indent=2))


if __name__=='__main__':main()
