"""Validated project version and supported build targets shared by entry scripts."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
from linux_arch import native_arch

ROOT = Path(__file__).resolve().parents[1]
ARCHITECTURES = {
    'windows': ('amd64',), 'linux-client': ('amd64',),
    'linux-server': ('amd64', 'arm64'), 'vps': ('amd64', 'arm64'),
    'android': ('arm64',), 'macos': ('arm64', 'x86_64'), 'ios': ('arm64',),
}
VERSION_PATTERN = re.compile(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?\Z')

def version(value=None):
    value = value or os.environ.get('NETGO_VERSION') or (ROOT / 'VERSION').read_text().strip()
    match = VERSION_PATTERN.fullmatch(value)
    if not match or len(value) > 64 or any(int(n) > 65535 for n in match.groups()[:3]):
        raise ValueError('版本号需为 SemVer，例如 0.3.1 或 0.3.1-rc.1，不带 v')
    for identifier in (match[4] or '').split('.'):
        if not identifier or (identifier.isdigit() and len(identifier) > 1 and identifier.startswith('0')):
            if match[4] is not None: raise ValueError('无效预发布版本号')
    if match[5] is not None and any(not part for part in match[5].split('.')):
        raise ValueError('无效版本构建标识')
    return value

def config(target, selected_version=None, arch=None):
    if target not in ARCHITECTURES: raise ValueError('未知构建目标')
    arch = arch or os.environ.get('NETGO_ARCH') or (native_arch() if target in ('linux-server', 'vps') else ARCHITECTURES[target][0])
    if arch not in ARCHITECTURES[target]:
        raise ValueError(f'{target} 当前支持架构：{", ".join(ARCHITECTURES[target])}')
    return {'target': target, 'version': version(selected_version), 'arch': arch}

def project_ldflags(selected_version=None, extra='-s -w'):
    return extra + ' -X netgo/internal/model.Version=' + version(selected_version)

def model_contract(root=ROOT):
    source = (root / 'internal/model/model.go').read_text(encoding='utf-8')
    constants = re.findall(r'^const (Protocol|Schema) = ([0-9]+)$', source, re.MULTILINE)
    if len(constants) != 2 or {name for name, _ in constants} != {'Protocol', 'Schema'}:
        raise ValueError('Cannot resolve the protocol/schema from the Go model')
    return {name.lower(): int(value) for name, value in constants}

def source_commit(require_git=False):
    result = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=ROOT, capture_output=True, text=True)
    if result.returncode == 0: return result.stdout.strip()
    if require_git: raise ValueError('发布需要可核验的 Git 提交')
    return 'local-source-snapshot'

def write_buildinfo(folder, target, selected_version=None, arch=None, **details):
    data = config(target, selected_version, arch)
    data.update(schema=1, source_commit=source_commit(), **details)
    path = Path(folder) / 'BUILDINFO.json'
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    return path

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', choices=tuple(ARCHITECTURES), required=True)
    parser.add_argument('--version')
    parser.add_argument('--arch')
    parser.add_argument('--field', choices=('version', 'arch', 'target', 'ldflags'))
    parser.add_argument('--buildinfo', type=Path)
    args = parser.parse_args()
    try:
        data = config(args.target, args.version, args.arch)
        if args.buildinfo: write_buildinfo(args.buildinfo,args.target,data['version'],data['arch'])
        print(project_ldflags(data['version']) if args.field == 'ldflags' else data[args.field] if args.field else json.dumps(data))
    except ValueError as error: parser.error(str(error))

if __name__ == '__main__': main()
