"""Shared pinned toolchain helpers; platform build entry points remain separate."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import urllib.request

ROOT = Path(__file__).resolve().parents[1]

def run(args, cwd=ROOT, env=None):
    print('Running:', ' '.join(map(str,args)),flush=True)
    subprocess.run(list(map(str,args)),cwd=cwd,env=env,check=True)

def environment():
    if os.uname().sysname != 'Darwin': raise ValueError('Apple builds require macOS with stable Xcode')
    env=dict(os.environ);cache=ROOT/'.cache/toolchain'
    env.update(GOCACHE=str(cache/'go-build'),GOPATH=str(cache/'go'),GOMODCACHE=str(cache/'go/pkg/mod'),GOTMPDIR=str(cache/'tmp'),GOBIN=str(ROOT/'.tools/bin'),GOMAXPROCS='2',GOMEMLIMIT='2GiB',GOFLAGS='-mod=mod -p=1')
    for key in ('GOCACHE','GOPATH','GOMODCACHE','GOTMPDIR','GOBIN'):Path(env[key]).mkdir(parents=True,exist_ok=True)
    env['PATH']=env['GOBIN']+os.pathsep+env['PATH']
    version=subprocess.check_output(['xcodebuild','-version'],text=True)
    if 'beta' in version.lower():raise ValueError('Use stable Xcode, not a beta installation')
    return env

def json_headers():
    item=json.loads((ROOT/'upstream/versions.lock.json').read_text())['json']
    target=ROOT/'.tools/native-json/nlohmann/json.hpp';target.parent.mkdir(parents=True,exist_ok=True)
    data=target.read_bytes() if target.exists() else urllib.request.urlopen(item['url'],timeout=30).read()
    if hashlib.sha256(data).hexdigest()!=item['sha256']:raise ValueError('JSON checksum mismatch')
    target.write_bytes(data)

def native(sdk,arch,deployment,env):
    json_headers();lock=json.loads((ROOT/'upstream/versions.lock.json').read_text())['openssl']
    source=ROOT/'.cache/openssl-apple-source'
    if not source.exists():run(['git','clone','--depth','1','--branch','openssl-'+lock['version'],lock['source'],source],env=env)
    if subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip()!=lock['commit']:raise ValueError('OpenSSL source mismatch')
    sdkroot=subprocess.check_output(['xcrun','--sdk',sdk,'--show-sdk-path'],text=True).strip()
    prefix=ROOT/'.tools'/('openssl-'+sdk+'-'+arch)
    if not (prefix/'lib/libssl.a').exists():
        build=ROOT/'.cache'/('openssl-build-'+sdk+'-'+arch);build.mkdir(parents=True,exist_ok=True)
        flag={'macosx':'-mmacosx-version-min=','iphoneos':'-miphoneos-version-min=','iphonesimulator':'-mios-simulator-version-min='}[sdk]+deployment
        local=dict(env,CC='xcrun --sdk '+sdk+' clang',CFLAGS='-arch '+arch+' -isysroot '+sdkroot+' '+flag)
        target='darwin64-arm64-cc' if arch=='arm64' else 'darwin64-x86_64-cc'
        run(['perl',source/'Configure',target,'no-shared','no-tests','no-module','no-asm','no-legacy','--prefix='+str(prefix),'--libdir=lib'],cwd=build,env=local)
        run(['make','-j4','build_libs'],cwd=build,env=local);run(['make','install_dev'],cwd=build,env=local)
    output=ROOT/'.cache'/('apple-native-'+sdk+'-'+arch)
    args=['cmake','-S',ROOT/'apps/apple/Bridge','-B',output,'-G','Ninja','-DCMAKE_BUILD_TYPE=Release','-DCMAKE_OSX_SYSROOT='+sdkroot,'-DCMAKE_OSX_ARCHITECTURES='+arch,'-DCMAKE_OSX_DEPLOYMENT_TARGET='+deployment,'-DNETGO_OPENSSL_ROOT='+str(prefix)]
    if sdk!='macosx':args+=['-DCMAKE_SYSTEM_NAME=iOS']
    run(args,env=env);run(['cmake','--build',output,'-j4'],env=env)
    archive=output/'libNetGoApple.a'
    libraries=[output/'libnetgo-apple.a',output/'remote-client/libnetgo-remote-client.a',output/'remote-core/libnetgo-remote-core.a',output/'remote-ticket/libnetgo-remote-ticket.a',output/'remote-transport/libnetgo-remote-transport.a',prefix/'lib/libssl.a',prefix/'lib/libcrypto.a']
    run(['xcrun','libtool','-static','-o',archive,*libraries],env=env)
    return archive

def sums(folder):
    folder=Path(folder)
    (folder/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.relative_to(folder).as_posix()+'\n' for p in sorted(folder.rglob('*')) if p.is_file() and p.name!='SHA256SUMS'),encoding='utf-8')
