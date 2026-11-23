#!/usr/bin/env python3
"""Build only the Android client, including VPN bindings and the C ABI viewer."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
NDK = '30.0.16248370'
MOBILE = 'v0.0.0-20260816165457-f98cc9b3c733'

def run(args, cwd=ROOT, env=None):
    print('Running:', ' '.join(map(str, args)), flush=True)
    subprocess.run(list(map(str, args)), cwd=cwd, env=env, check=True)

def environment():
    env = dict(os.environ)
    cache = ROOT / '.cache/toolchain'
    env.update(GOCACHE=str(cache / 'go-build'), GOPATH=str(cache / 'go'),
               GOMODCACHE=str(cache / 'go/pkg/mod'), GOTMPDIR=str(cache / 'tmp'),
               GRADLE_USER_HOME=str(cache / 'gradle'), ANDROID_USER_HOME=str(cache / 'android-user'),
               GOBIN=str(ROOT / '.tools/bin'), GOFLAGS='-mod=mod')
    for key in ('GOCACHE','GOPATH','GOMODCACHE','GOTMPDIR','GRADLE_USER_HOME','ANDROID_USER_HOME','GOBIN'):
        Path(env[key]).mkdir(parents=True, exist_ok=True)
    env['TEMP'] = env['TMP'] = env['GOTMPDIR']
    if os.name == 'nt':
        env['LOCALAPPDATA'] = str(cache / 'android-local')
        Path(env['LOCALAPPDATA']).mkdir(parents=True, exist_ok=True)
    env['PATH'] = env['GOBIN'] + os.pathsep + str(ROOT / '.tools/go/bin') + os.pathsep + env['PATH']
    return env

def install_sdk(sdk, package, env):
    modern = sdk / ('cmdline-tools/latest/bin/android.exe' if os.name == 'nt' else 'cmdline-tools/latest/bin/android')
    legacy = sdk / ('cmdline-tools/latest/bin/sdkmanager.bat' if os.name == 'nt' else 'cmdline-tools/latest/bin/sdkmanager')
    if modern.is_file():
        run([modern, '--sdk=' + str(sdk), 'sdk', 'install', package.replace(';','/'), '--no-metrics'], env=env)
    elif legacy.is_file():
        subprocess.run([str(legacy), '--sdk_root=' + str(sdk), '--licenses'], input='y\n' * 100, text=True, env=env, check=True)
        run([legacy, '--sdk_root=' + str(sdk), package], env=env)
    else:
        raise ValueError('Android command-line tools required; set ANDROID_HOME to the SDK')

def openssl(sdk, env):
    prefix = ROOT / '.tools/openssl-android-arm64'
    if (prefix / 'lib/libssl.a').is_file() and (prefix / 'lib/libcrypto.a').is_file(): return
    source = ROOT / '.cache/openssl-android-source'
    lock = json.loads((ROOT / 'upstream/versions.lock.json').read_text())['openssl']
    if not source.exists(): run(['git','clone','--depth','1','--branch','openssl-' + lock['version'], lock['source'], source], env=env)
    actual = subprocess.check_output(['git','rev-parse','HEAD'], cwd=source, text=True).strip()
    if actual != lock['commit']: raise ValueError('OpenSSL revision differs from lock')
    host = 'windows-x86_64' if os.name == 'nt' else 'linux-x86_64'
    toolchain = sdk / 'ndk' / NDK / 'toolchains/llvm/prebuilt' / host / 'bin'
    build = ROOT / '.cache/openssl-android-build-script'; build.mkdir(parents=True, exist_ok=True)
    if os.name == 'nt':
        msys = Path(os.environ.get('NETGO_MSYS_ROOT', 'D:/msys64'))
        cygpath, bash = msys / 'usr/bin/cygpath.exe', msys / 'usr/bin/bash.exe'
        if not bash.is_file(): raise ValueError('Windows Android crypto build requires MSYS2; set NETGO_MSYS_ROOT')
        posix = lambda p: subprocess.check_output([cygpath,'-u',str(p)],text=True).strip()
        import shlex
        command = 'export PATH=' + shlex.quote(posix(toolchain)) + ':"$PATH"; export CC="clang.exe --target=aarch64-linux-android28" AR=llvm-ar.exe RANLIB=llvm-ranlib.exe; cd ' + shlex.quote(posix(build)) + '; perl ' + shlex.quote(posix(source / 'Configure')) + ' linux-aarch64 no-shared no-tests no-module no-asm no-legacy -fPIC --prefix=' + shlex.quote(posix(prefix)) + ' --libdir=lib && make -j4 && make install_sw'
        run([bash,'-lc',command], env=env)
    else:
        native = dict(env, CC=str(toolchain / 'clang') + ' --target=aarch64-linux-android28', AR=str(toolchain / 'llvm-ar'), RANLIB=str(toolchain / 'llvm-ranlib'))
        run(['perl',source / 'Configure','linux-aarch64','no-shared','no-tests','no-module','no-asm','no-legacy','-fPIC','--prefix=' + str(prefix),'--libdir=lib'],cwd=build,env=native)
        run(['make','-j4'],cwd=build,env=native); run(['make','install_sw'],cwd=build,env=native)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sdk', type=Path)
    parser.add_argument('--skip-bind', action='store_true', help='developer iteration with an already built AAR')
    args = parser.parse_args(); env = environment()
    sdk = (args.sdk or Path(env.get('ANDROID_HOME', ROOT / '.tools/android'))).resolve()
    env['ANDROID_HOME'] = env['ANDROID_SDK_ROOT'] = str(sdk)
    env['ANDROID_NDK_HOME'] = str(sdk / 'ndk' / NDK)
    for package, file in [('platforms;android-37.2', sdk / 'platforms/android-37.2/android.jar'), ('ndk;' + NDK, sdk / 'ndk' / NDK / 'source.properties'), ('cmake;3.31.6', sdk / 'cmake/3.31.6/bin/cmake')]:
        if not file.is_file() and not file.with_suffix('.exe').is_file(): install_sdk(sdk, package, env)
    openssl(sdk, env)
    project = ROOT / 'apps/android'
    (project / 'local.properties').write_text('sdk.dir=' + str(sdk).replace('\\','/').replace(':','\\:') + '\n', encoding='utf-8', newline='\n')
    go = shutil.which('go', path=env['PATH'])
    if not go: raise ValueError('the pinned Go toolchain is required')
    if not args.skip_bind:
        run([go,'install','golang.org/x/mobile/cmd/gomobile@' + MOBILE],env=env)
        run([go,'install','golang.org/x/mobile/cmd/gobind@' + MOBILE],env=env)
        libs = project / 'app/libs'; libs.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='android-go-', dir=ROOT / '.cache') as stage:
            source = Path(stage) / 'source'
            run([sys.executable,'-B',ROOT / 'scripts/prepare-mobile-inputs.py','--output',source,'--go',go],env=env)
            gomobile = Path(env['GOBIN']) / ('gomobile.exe' if os.name == 'nt' else 'gomobile')
            # anet v0.0.5 documents this flag for Go 1.23+; its zone-cache ABI
            # matches the pinned Go 1.27.4 source. It does not alter TLS checks.
            run([gomobile,'bind','-target=android/arm64','-androidapi=31','-ldflags=-checklinkname=0','-javapkg=org.netgo.runtime','-o',libs / 'netgo-mobile.aar','./mobile'],cwd=source,env=env)
    wrapper = project / ('gradlew.bat' if os.name == 'nt' else 'gradlew')
    if os.name != 'nt': wrapper.chmod(wrapper.stat().st_mode | 0o111)
    run([wrapper,'--no-daemon','assembleDebug','assembleRelease','lintDebug'],cwd=project,env=env)
    output = ROOT / 'dist/android-client'; output.mkdir(parents=True, exist_ok=True)
    for source, name in [('app/build/outputs/apk/debug/app-debug.apk','NetGo-0.3.0-dev-android-arm64-debug.apk'), ('app/build/outputs/apk/release/app-release-unsigned.apk','NetGo-0.3.0-dev-android-arm64-unsigned.apk')]:
        source = project / source
        with zipfile.ZipFile(source) as archive:
            names = archive.namelist()
            if not any(n.endswith('/libgojni.so') for n in names) or not any(n.endswith('/libnetgo-remote-jni.so') for n in names): raise ValueError('APK is missing VPN or remote libraries')
            if archive.testzip() is not None: raise ValueError('APK CRC failed')
        shutil.copy2(source, output / name)
    files = sorted(output.glob('*.apk'))
    (output / 'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name + '\n' for p in files),encoding='utf-8')
    print('Android client:', output)

if __name__ == '__main__': main()
