#!/usr/bin/env python3
"""Package only the Windows client: Setup and a single-entry portable ZIP."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]

def sums(folder):
    files = [p for p in sorted(folder.rglob('*')) if p.is_file() and p != folder / 'SHA256SUMS']
    (folder / 'SHA256SUMS').write_text(''.join(hashlib.file_digest(p.open('rb'), 'sha256').hexdigest() + '  ' + p.relative_to(folder).as_posix() + '\n' for p in files), encoding='utf-8')

def archive(folder, output):
    sums(folder)
    with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as z:
        for p in sorted(folder.rglob('*')):
            if p.is_file(): z.write(p, p.relative_to(folder).as_posix())
    with zipfile.ZipFile(output) as z:
        if z.testzip() is not None: raise ValueError('client archive CRC failed')

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', default='0.3.0-dev')
    parser.add_argument('--output', type=Path, default=ROOT / 'dist/windows-client')
    args = parser.parse_args(); args.output.mkdir(parents=True, exist_ok=True)
    source = ROOT / 'dist/windows-amd64'
    required = ('NetGo.exe', 'netbird.exe', 'wintun.dll')
    for name in required:
        if not (source / name).is_file(): raise ValueError('missing Windows client artifact: ' + name)
    spec = importlib.util.spec_from_file_location('installer', ROOT / 'scripts/build-installer.py')
    installer = importlib.util.module_from_spec(spec); spec.loader.exec_module(installer)
    cache = ROOT / '.cache'; cache.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='windows-client-package-', dir=cache) as temporary:
        temporary = Path(temporary); payload = temporary / 'payload'; (payload / 'bin').mkdir(parents=True)
        for name in required: shutil.copy2(source / name, payload / 'bin' / name)
        sums(payload / 'bin')
        for name in ('install-client.ps1', 'THIRD_PARTY_NOTICES.md'): shutil.copy2(ROOT / name, payload / name)
        shutil.copytree(ROOT / 'deploy/windows', payload / 'deploy/windows')
        (payload / 'licenses').mkdir()
        prepared = Path(subprocess.check_output([shutil.which('python') or 'python', '-B', str(ROOT / 'scripts/prepare-upstream.py'), '--path-only'], text=True).strip())
        for file, name in [(prepared / 'LICENSE', 'netbird.txt'), (ROOT / '.tools/openssl-source/LICENSE.txt', 'openssl.txt'), (ROOT / '.tools/wintun-package/wintun/LICENSE.txt', 'wintun.txt')]:
            if not file.is_file(): raise ValueError('license material missing: ' + str(file))
            shutil.copy2(file, payload / 'licenses' / name)
        setup = args.output / f'NetGo-{args.version}-windows-Setup.exe'
        installer.build(payload, setup, temporary / 'installer')
        subprocess.run([str(setup), '--check'], check=True)
        portable = temporary / 'portable'; portable.mkdir(); shutil.copy2(source / 'NetGo.exe', portable / 'NetGo.exe')
        (portable / 'netgo-portable.json').write_text('{"schema":1,"data":"data","credentials":"Windows Credential Manager"}\n', encoding='utf-8')
        network = temporary / 'network'; (network / 'bin').mkdir(parents=True)
        for name in ('netbird.exe', 'wintun.dll'): shutil.copy2(source / name, network / 'bin' / name)
        sums(network / 'bin'); shutil.copy2(ROOT / 'install-client.ps1', network / 'install-client.ps1'); shutil.copytree(ROOT / 'deploy/windows', network / 'deploy/windows')
        (portable / '.netgo').mkdir(); helper = portable / '.netgo/NetGo-Network-Setup.exe'
        installer.build(network, helper, temporary / 'network-installer', network_only=True)
        subprocess.run([str(helper), '--check'], check=True)
        shutil.copytree(payload / 'licenses', portable / 'licenses'); shutil.copy2(payload / 'THIRD_PARTY_NOTICES.md', portable / 'THIRD_PARTY_NOTICES.md')
        (portable / 'README.txt').write_text('NetGo Windows 绿色版\n\n双击 NetGo.exe。主界面、连接后台与远控由同一程序内部运行，无需分别打开多个 EXE。\n首次 VPN 连接需要一次管理员授权，客户端会调用 .netgo 内的网络服务准备程序。\n站点设置在 data 内，设备密钥由本机 Windows Credential Manager 保存。\n本开发包不代表五平台或全部远控设备验收完成。\n', encoding='utf-8-sig')
        archive(portable, args.output / f'NetGo-{args.version}-windows-portable.zip')
    sums(args.output)
    print(json.dumps({'platform':'windows','version':args.version,'entry':'NetGo.exe','output':str(args.output)}, ensure_ascii=False))

if __name__ == '__main__': main()
