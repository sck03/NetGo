"""Stage an explicit platform asset list, separate from logs and raw App folders."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import tarfile
import zipfile
from build_config import ROOT, config, source_commit

def checksum(path):
    with Path(path).open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()

def archive_check(path):
    def safe(name):
        parts=Path(name.replace('\\','/')).parts
        if name.startswith('/') or '\\' in name or ':' in name or '..' in parts:
            raise ValueError('归档路径不安全')
        if name.endswith(('.py','.key','.db','.sqlite','.a','.o')) or any(p in ('.cache','.git','node_modules') for p in parts):
            raise ValueError('运行包包含开发文件或私有运行数据')
    if path.suffix in ('.zip','.apk','.ipa'):
        with zipfile.ZipFile(path) as archive:
            names=set()
            for item in archive.infolist():
                safe(item.filename)
                if item.filename in names or stat.S_ISLNK(item.external_attr >> 16): raise ValueError('重复归档条目或链接')
                names.add(item.filename)
            if archive.testzip() is not None: raise ValueError('归档 CRC 检查失败')
    elif path.name.endswith('.tar.gz'):
        with tarfile.open(path, 'r:gz') as archive:
            names=set()
            for item in archive:
                safe(item.name)
                if item.name in names or not (item.isfile() or item.isdir()): raise ValueError('重复或特殊归档条目')
                names.add(item.name)

def assets(data, root=ROOT):
    target, version, arch = data['target'], data['version'], data['arch']
    prefix=f'NetGo-{version}'
    table={
        'windows': [('dist/windows-client/'+prefix+'-windows-'+arch+'-Setup.exe',None),('dist/windows-client/'+prefix+'-windows-'+arch+'-portable.zip',None),('dist/windows-client/BUILDINFO.json','BUILDINFO')],
        'linux-client': [('dist/'+prefix+'-linux-client-'+arch+'.tar.gz',None),('dist/linux-desktop-amd64/BUILDINFO.json','BUILDINFO')],
        'linux-server': [('dist/linux-server/'+prefix+'-linux-'+arch+'.zip',None),('dist/linux-server/netgo-server-installer-linux-'+arch,None),('dist/linux-server/BUILDINFO-server.json','BUILDINFO'),('dist/linux-server/SBOM-server.cdx.json','SBOM.cdx')],
        'vps': [('dist/vps-package/'+prefix+'-vps-linux-'+arch+'.zip',None),('dist/vps-package/netgo-vps-installer-linux-'+arch,None),('dist/vps-package/BUILDINFO-vps.json','BUILDINFO')],
        'android': [('dist/android-client/'+prefix+'-android-'+arch+'-debug.apk',None),('dist/android-client/'+prefix+'-android-'+arch+'-unsigned.apk',None),('dist/android-client/BUILDINFO.json','BUILDINFO')],
        'macos': [('dist/macos-client/'+prefix+'-macos-'+arch+'.zip',None),('dist/macos-client/BUILDINFO.json','BUILDINFO')],
        'ios': [('dist/ios-client/BUILDINFO.json','BUILDINFO')],
    }
    if target=='ios':
        if data['sdk'] in ('both','iphonesimulator'):table[target].append(('dist/ios-client/'+prefix+'-ios-'+arch+'-simulator.zip',None))
        if data['sdk'] in ('both','iphoneos'):table[target].append(('dist/ios-client/'+prefix+'-ios-'+arch+'-unsigned.ipa',None))
    result=[]
    for relative, kind in table[target]:
        path=Path(root)/relative
        if not path.is_file() or path.is_symlink():raise ValueError('缺少已构建的平台文件：'+relative)
        if any(parent.is_symlink() for parent in path.parents if parent!=Path(root) and Path(root) in parent.parents):raise ValueError('产物路径包含链接')
        name=path.name if kind is None else f"{prefix}-{target}-{arch}-{data['sdk']}-{kind}.json"
        if kind=='BUILDINFO':
            info=json.loads(path.read_text(encoding='utf-8-sig'))
            if info.get('version')!=version:raise ValueError('构建信息与请求版本不符')
        archive_check(path)
        result.append((path,name))
    return result

def stage(data, output, root=ROOT, commit=None):
    config(data['target'],data['version'],data['arch'])
    chosen=assets(data,root)
    commit=commit or source_commit(require_git=True)
    if os.environ.get('GITHUB_SHA') and os.environ['GITHUB_SHA']!=commit:raise ValueError('构建提交与工作流提交不符')
    output=Path(output)
    if output.exists() and any(output.iterdir()):raise ValueError('发布暂存目录非空，请保留已有产物并选择新目录')
    output.mkdir(parents=True,exist_ok=True)
    entries=[]
    for source,name in chosen:
        target=output/name;shutil.copy2(source,target)
        digest=checksum(target)
        entries.append({'name':name,'sha256':digest,'bytes':target.stat().st_size})
        sidecar=output/(name+'.sha256');sidecar.write_text(digest+'  '+name+'\n',encoding='utf-8')
        entries.append({'name':sidecar.name,'sha256':checksum(sidecar),'bytes':sidecar.stat().st_size})
    manifest={'schema':1,**data,'commit':commit,'assets':entries}
    (output/'release-manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    return manifest

def main():
    from ci_metadata import resolve
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--target',required=True);parser.add_argument('--output',type=Path,default=ROOT/'dist/release-payload');args=parser.parse_args()
    # Build jobs receive normalized values from prepare rather than raw inputs.
    env=dict(os.environ,NETGO_REQUESTED_VERSION=os.environ.get('NETGO_VERSION',''),NETGO_REQUESTED_ARCH=os.environ.get('NETGO_ARCH',''))
    try:result=stage(resolve(args.target,env),args.output)
    except (ValueError,KeyError) as error:parser.error(str(error))
    print(json.dumps({'target':result['target'],'version':result['version'],'arch':result['arch'],'assets':[x['name'] for x in result['assets']]},ensure_ascii=False))

if __name__=='__main__':main()
