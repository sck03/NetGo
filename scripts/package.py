#!/usr/bin/env python3
"""Package verified development artifacts without promoting network acceptance."""
import argparse, hashlib, json, pathlib, re, shutil, subprocess, tempfile, zipfile
from verification import validate_report, verify_archive, read_json

parser=argparse.ArgumentParser()
parser.add_argument('--version',default='0.2.3-dev')
parser.add_argument('--toolchain',default='D:/msys64/ucrt64')
parser.add_argument('--verification',type=pathlib.Path,required=True,help='Complete verification.json for the exact current source and artifacts')
args=parser.parse_args()
root=pathlib.Path(__file__).resolve().parents[1]
version=args.version
if not version or any(c not in 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-' for c in version):
    raise SystemExit('invalid version')
try:
    verified=validate_report(root,args.verification,'windows')
    verified_source=read_json(args.verification.resolve().parent/'source-inputs.txt')['files']
except (OSError,ValueError,TypeError,AttributeError) as error:
    raise SystemExit('Packaging rejected: '+str(error))
windows=root/'dist/windows-amd64'
required=['netgo-desktop.exe','netgo-agent.exe','netbird.exe','wintun.dll']
if any(not (windows/name).is_file() for name in required):
    raise SystemExit('build Windows artifacts first')

def verify_inputs(folder, expected_names):
    entries={}
    for line in (folder/'SHA256SUMS').read_text(encoding='utf-8-sig').splitlines():
        match=re.fullmatch(r'([0-9a-fA-F]{64})  ([A-Za-z0-9_.-]+)',line)
        if not match or match[2] in entries:
            raise SystemExit('invalid input checksum manifest')
        file=folder/match[2]
        if file.is_symlink() or not file.is_file() or hashlib.sha256(file.read_bytes()).hexdigest()!=match[1].lower():
            raise SystemExit('input checksum mismatch: '+match[2])
        entries[match[2]]=match[1]
    if not set(expected_names).issubset(entries):
        raise SystemExit('required binary missing from input checksums')

verify_inputs(windows,required)
verify_inputs(root/'dist/linux-amd64',['netgoctl','netgo-server','netgo-helper','netgo-client-service','netgo-ddns','release.json'])
release=json.loads((root/'dist/linux-amd64/release.json').read_text(encoding='utf-8-sig'))
if release!=dict(version=version,protocol=1,schema=1,os='linux',arch='amd64'):
    raise SystemExit('package version or compatibility differs from built products')
for file in windows.glob('*.exe'):
    if file.name not in required:
        raise SystemExit('unexpected legacy Windows executable: '+file.name)
out=root/'dist/packages'
out.mkdir(parents=True,exist_ok=True)
lock=json.loads((root/'upstream/versions.lock.json').read_text(encoding='utf-8'))

def hashes(folder):
    entries=[]
    for file in sorted(folder.rglob('*')):
        if file.is_file() and file!=folder/'SHA256SUMS':
            entries.append(hashlib.sha256(file.read_bytes()).hexdigest()+'  '+file.relative_to(folder).as_posix())
    (folder/'SHA256SUMS').write_text('\n'.join(entries)+'\n',encoding='utf-8')

def archive(folder,name,expected=None):
    destination=out/name
    with zipfile.ZipFile(destination,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
        for file in sorted(folder.rglob('*')):
            if file.is_file():
                z.write(file,file.relative_to(folder).as_posix())
    delivered.append(verify_archive(destination,expected))
    return destination

metadata={'version':version,'date':'2026-11-22','timezone':'Asia/Shanghai','status':'development build; network acceptance pending','windows_language':'C++20 / Win32','network_engine':'separate patched NetBird','inputs':lock}
metadata['verification']=verified
delivered=[]
components=[
    {'type':'application','name':'NetGo native Windows client','version':version},
    {'type':'library','name':'OpenSSL','version':'4.1.0','licenses':[{'license':{'id':'Apache-2.0'}}]},
    {'type':'library','name':'nlohmann/json','version':'3.12.0','licenses':[{'license':{'id':'MIT'}}]},
    {'type':'library','name':'Wintun signed DLL','version':'0.14.1','licenses':[{'license':{'name':'Wintun prebuilt binaries license'}}]},
]
seen=set()
for binary in [windows/'netbird.exe',root/'dist/linux-amd64/netgo-server']:
    info=subprocess.check_output(['go','version','-m',str(binary)],text=True)
    for line in info.splitlines():
        fields=line.split()
        if len(fields)>=3 and fields[0]=='dep' and (fields[1],fields[2]) not in seen:
            seen.add((fields[1],fields[2]))
            components.append({'type':'library','name':fields[1],'version':fields[2]})
npm=json.loads((root/'package-lock.json').read_text(encoding='utf-8'))
for name,package in npm.get('packages',{}).items():
    if name and package.get('version'):
        components.append({'type':'library','name':name.removeprefix('node_modules/'),'version':package['version'],'scope':'optional' if package.get('dev') else 'required'})
bom={'bomFormat':'CycloneDX','specVersion':'1.5','version':1,'metadata':{'component':{'type':'application','name':'NetGo development build','version':version}},'components':components}
(out/'SBOM.cdx.json').write_text(json.dumps(bom,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
with tempfile.TemporaryDirectory(prefix='netgo-package-') as temporary:
    stage=pathlib.Path(temporary)/'windows'
    (stage/'bin').mkdir(parents=True)
    for name in required: shutil.copy2(windows/name,stage/'bin'/name)
    hashes(stage/'bin')
    (stage/'deploy/windows').mkdir(parents=True)
    for name in ['install.ps1','uninstall.ps1','common.ps1']: shutil.copy2(root/'deploy/windows'/name,stage/'deploy/windows'/name)
    (stage/'docs').mkdir()
    shutil.copy2(root/'docs/OPERATIONS.zh-CN.md',stage/'docs/OPERATIONS.zh-CN.md')
    shutil.copy2(root/'THIRD_PARTY_NOTICES.md',stage/'THIRD_PARTY_NOTICES.md')
    license_sources={
        'openssl.txt':root/'.tools/openssl-source/LICENSE.txt',
        'wintun.txt':root/'.tools/wintun-package/wintun/LICENSE.txt',
        'netbird-client.txt':root/'.cache/netbird/LICENSE',
        'json-mit.txt':root/'.tools/native-json/LICENSE.MIT',
        'go-bsd.txt':root/'.tools/go/LICENSE',
        'gcc-runtime-exception.txt':pathlib.Path(args.toolchain)/'share/licenses/gcc-libs/COPYING.RUNTIME',
        'gcc-gpl3.txt':pathlib.Path(args.toolchain)/'share/licenses/gcc-libs/COPYING3',
    }
    (stage/'licenses').mkdir()
    for name,source in license_sources.items():
        if not source.is_file(): raise RuntimeError('missing license '+str(source))
        shutil.copy2(source,stage/'licenses'/name)
    shutil.copy2(root/'upstream/versions.lock.json',stage/'BUILDINFO-inputs.json')
    shutil.copy2(out/'SBOM.cdx.json',stage/'SBOM.cdx.json')
    (stage/'BUILDINFO.json').write_text(json.dumps(metadata,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    hashes(stage)
    archive(stage,f'NetGo-{version}-windows-amd64.zip',{
        'bin/'+pathlib.PurePosixPath(item['path']).name:item['sha256'] for item in verified['artifact_inputs']['files']
        if item['path'].startswith('dist/windows-amd64/') and pathlib.PurePosixPath(item['path']).name in required})
    linux=pathlib.Path(temporary)/'linux-product'
    (linux/'dist/linux-amd64').mkdir(parents=True)
    for file in (root/'dist/linux-amd64').iterdir():
        if file.is_file():shutil.copy2(file,linux/'dist/linux-amd64'/file.name)
    shutil.copytree(root/'deploy',linux/'deploy')
    shutil.copytree(root/'examples',linux/'examples')
    shutil.copytree(root/'scripts',linux/'scripts')
    shutil.copytree(root/'tests/network',linux/'tests/network',ignore=shutil.ignore_patterns('__pycache__','*.pyc'))
    shutil.copytree(root/'tests/tools',linux/'tests/tools',ignore=shutil.ignore_patterns('__pycache__','*.pyc'))
    shutil.copytree(root/'upstream',linux/'upstream')
    shutil.copy2(root/'THIRD_PARTY_NOTICES.md',linux/'THIRD_PARTY_NOTICES.md')
    shutil.copy2(out/'SBOM.cdx.json',linux/'SBOM.cdx.json')
    (linux/'docs').mkdir()
    shutil.copy2(root/'docs/OPERATIONS.zh-CN.md',linux/'docs/OPERATIONS.zh-CN.md')
    shutil.copy2(root/'docs/NETWORK_ACCEPTANCE.zh-CN.md',linux/'docs/NETWORK_ACCEPTANCE.zh-CN.md')
    shutil.copy2(root/'docs/MULTI_WAN.zh-CN.md',linux/'docs/MULTI_WAN.zh-CN.md')
    (linux/'BUILDINFO.json').write_text(json.dumps(metadata,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    (linux/'README.txt').write_text('NetGo product binaries were cross-compiled on Windows. Build patched NetBird/Dex natively on Linux with CGO before installation. This is not a complete production server bundle.\n',encoding='utf-8')
    hashes(linux/'dist')
    hashes(linux)
    archive(linux,f'NetGo-{version}-linux-product-development.zip',{
        item['path']:item['sha256'] for item in verified['artifact_inputs']['files'] if item['path'].startswith('dist/linux-amd64/')})
    source=pathlib.Path(temporary)/'source'
    source.mkdir()
    excluded={'.git','.cache','.tools','node_modules','dist','runtime','__pycache__'}
    for file in root.rglob('*'):
        relative=file.relative_to(root)
        if not file.is_file() or any(p in excluded for p in relative.parts) or file.suffix in {'.exe','.dll','.db','.key','.pyc'}:
            continue
        target=source/relative
        target.parent.mkdir(parents=True,exist_ok=True)
        shutil.copy2(file,target)
    # Include exact complete upstream source and apply the delivered patches.
    for name,checkout in [('netbird',root/'.cache/netbird'),('dex',root/'.cache/dex'),('openssl',root/'.tools/openssl-source')]:
        commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=checkout,text=True).strip()
        if commit!=lock[name]['commit']:
            raise RuntimeError('source archive revision differs from version lock: '+name)
        destination=source/'upstream-source'/name
        destination.mkdir(parents=True)
        tar=pathlib.Path(temporary)/(name+'.tar')
        subprocess.run(['git','archive','HEAD','--format=tar','--output',str(tar)],cwd=checkout,check=True)
        import tarfile
        with tarfile.open(tar) as handle:handle.extractall(destination,filter='data')
        if name in ('netbird','dex'):
            subprocess.run(['git','apply',str(root/'upstream/patches'/f'{name}.patch')],cwd=destination,check=True)
    (source/'SOURCEINFO.json').write_text(json.dumps(metadata,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    hashes(source)
    archive(source,f'NetGo-{version}-source.zip',{item['path']:item['sha256'] for item in verified_source})
# A source edit or rebuild during a long archive must not produce a valid delivery marker.
validate_report(root,args.verification,'windows')
(out/f'NetGo-{version}-delivery-verification.json').write_text(json.dumps({
    'schema':1,'version':version,'status':'PASS','verification':verified,'packages':delivered,
    'sbom_sha256':hashlib.sha256((out/'SBOM.cdx.json').read_bytes()).hexdigest(),
    'real_network_acceptance':'NOT-RUN'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
hashes(out)
print(json.dumps({'packages':[p.name for p in sorted(out.glob('*.zip'))],'status':'development; A01-A33 not run'},ensure_ascii=False))
