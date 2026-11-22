#!/usr/bin/env python3
"""Read-only Debian 13 preflight; never installs packages or changes networking."""
import argparse
import json
from pathlib import Path
import platform
import re
import shlex
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def parse_os_release(text):
    values = {}
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        key, separator, value = line.partition('=')
        if not separator or not re.fullmatch(r'[A-Z][A-Z0-9_]*', key) or key in values:
            raise ValueError('invalid or duplicate os-release field')
        tokens = shlex.split(value, comments=False)
        if len(tokens) != 1:
            raise ValueError('invalid os-release value')
        values[key] = tokens[0]
    return values


def version(command):
    result = subprocess.run(command, check=True, capture_output=True, text=True, timeout=10)
    return result.stdout.strip().splitlines()[0]


def inspect_environment(mode, release, system, machine, which=shutil.which, run=version):
    checks = []
    def check(name, ok, detail):
        checks.append({'name': name, 'status': 'PASS' if ok else 'FAIL', 'detail': detail})
    check('os', system == 'Linux' and release.get('ID') == 'debian' and release.get('VERSION_ID', '').split('.')[0] == '13',
          'Required: Debian 13; observed: ' + release.get('PRETTY_NAME', system))
    check('architecture', machine in ('x86_64', 'amd64'), 'Required: amd64; observed: ' + machine)
    commands = (['go', 'node', 'npm', 'gcc', 'git', 'python3', 'pkg-config'] if mode == 'build' else
                ['nft', 'conntrack', 'systemctl', 'python3', 'sha256sum', 'runuser', 'ip', 'openssl'])
    for command in commands:
        check('command_' + command, bool(which(command)), 'Required executable: ' + command)
    if mode == 'build':
        lock = json.loads((ROOT / 'upstream/versions.lock.json').read_text(encoding='utf-8'))
        for name, args, pattern in [('go', ['go', 'version'], r'\bgo([0-9.]+)\b'),
                                    ('node', ['node', '--version'], r'^v([0-9.]+)$')]:
            try:
                observed = run(args)
                match = re.search(pattern, observed)
                check('version_' + name, bool(match and match[1] == lock['toolchain'][name]), observed)
            except (OSError, subprocess.SubprocessError, IndexError):
                check('version_' + name, False, 'Cannot read pinned ' + name + ' version')
    return {'schema': 1, 'target': 'Debian 13 amd64', 'mode': mode,
            'status': 'PASS' if all(item['status'] == 'PASS' for item in checks) else 'FAIL',
            'checks': checks, 'system_changed': False, 'real_network_acceptance': 'NOT-RUN'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=['build', 'install'], default='install')
    args = parser.parse_args()
    try:
        release = parse_os_release(Path('/etc/os-release').read_text(encoding='utf-8'))
    except (OSError, ValueError):
        release = {}
    report = inspect_environment(args.mode, release, platform.system(), platform.machine())
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if report['status'] == 'PASS' else 1


if __name__ == '__main__':
    raise SystemExit(main())
