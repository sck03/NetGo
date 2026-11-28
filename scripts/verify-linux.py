#!/usr/bin/env python3
"""Run the complete Debian 12/13 build checks without installing or starting services."""
import argparse
import ast
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

from fingerprint import ROOT, fingerprint
from linux_arch import target_arch
from verification import CHECKS, sha256, validate_report

BASH_FILES = ('install-server.sh', 'scripts/bootstrap-linux.sh', 'scripts/build.sh', 'scripts/build-upstream.sh',
                'deploy/linux/deploy-certificate.sh')


def steps(output, python):
    arch = target_arch()
    ctl = str(ROOT / f'dist/linux-{arch}/netgoctl')
    upstream = ROOT / f'dist/upstream-linux-{arch}'
    haproxy = shutil.which('haproxy') or '/usr/sbin/haproxy'
    # Dynamic manifest hashing runs only after the product-build step.
    release = (f'import hashlib,pathlib,subprocess; p=pathlib.Path("dist/linux-{arch}"); '
               'h=hashlib.sha256((p/"SHA256SUMS").read_bytes()).hexdigest(); '
               'subprocess.run([str(p.resolve()/"netgoctl"),"release","verify",'
               '"--input",str(p.resolve()),"--sha256",h],check=True)')
    return [
        ('tool-versions', [[python, '-B', 'scripts/linux_preflight.py', '--mode', 'build'], ['openssl', 'version'], [haproxy, '-v']]),
        ('source-inputs', [[python, '-B', 'scripts/fingerprint.py']]),
        ('tools-tests', [[python, '-B', '-m', 'unittest', 'discover', '-s', 'tests/tools', '-v']]),
        ('remote-core', [[python, '-B', 'scripts/verify-remote.py', '--output', str(output / 'remote-core-build')]]),
        # go:embed requires the current frontend output in a clean checkout.
        ('frontend', [['npm', 'ci', '--no-fund'], ['npm', 'test'], ['npm', 'run', 'build']]),
        ('go-race', [['go', 'test', '-race', './internal/...', './cmd/...']]),
        ('go-vet', [['go', 'vet', './internal/...', './cmd/...']]),
        ('scripts', [['bash', '-n', name] for name in BASH_FILES] +
                    [['bash', 'install-server.sh', 'install', '--plan'],
                     ['bash', 'deploy/linux/deploy-certificate.sh', '--plan', 'control', 'control.site.example']]),
        ('certificates', [[python, '-B', 'scripts/verify-certificates.py', '--bash', shutil.which('bash') or 'bash']]),
        ('upstream', [['bash', 'scripts/build-upstream.sh']]),
        ('product-build', [['bash', 'scripts/build.sh', '--skip-frontend', '--skip-checks']]),
        ('deploy-validators', [[python, '-B', 'scripts/verify-deploy.py', '--netgoctl', ctl,
                               '--caddy', str(upstream / 'caddy'), '--frpc', str(upstream / 'frpc'),
                               '--frps', str(upstream / 'frps'), '--haproxy', haproxy,
                               '--output', str(output / 'deployment')]]),
        ('release-manifest', [[python, '-B', '-c', release]]),
        ('artifact-inputs', [[python, '-B', 'scripts/verification.py', 'snapshot', '--platform', 'linux']]),
    ]


def run(output, commands):
    if set(name for name, _ in commands) != CHECKS['linux']:
        raise ValueError('incomplete Linux verification definition')
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    report = {'schema': 2, 'platform': 'linux', 'arch': target_arch(), 'status': 'FAIL', 'checks': [],
              'code_sha256': None, 'real_network_acceptance': 'NOT-RUN',
              'limits': ['Builds, unit tests and configuration parsing do not prove installation or network acceptance',
                         'G1-G6, A01-A33, cross-user installation and 72-hour reliability require the target lab']}
    env = dict(os.environ, CGO_ENABLED='1')
    env.pop('GOOS', None)
    env.pop('GOARCH', None)
    try:
        initial = fingerprint()
        report['code_sha256'] = initial['code_sha256']
        for name, actions in commands:
            log = output / (name + '.txt')
            started = time.monotonic()
            status = 'FAIL'
            print('Running ' + name, flush=True)
            try:
                with log.open('w', encoding='utf-8') as stream:
                    for action in actions:
                        subprocess.run(action, cwd=ROOT, env=env, stdout=stream,
                                       stderr=subprocess.STDOUT, check=True, timeout=1800)
                    if name == 'scripts':
                        for folder in ('scripts', 'tests/network', 'tests/tools', 'apps/windows/tests'):
                            for path in sorted((ROOT / folder).glob('*.py')):
                                ast.parse(path.read_text(encoding='utf-8'))
                                stream.write('PASS Python syntax: ' + path.relative_to(ROOT).as_posix() + '\n')
                status = 'PASS'
            except BaseException as error:
                with log.open('a', encoding='utf-8') as stream:
                    stream.write('Verification error: ' + str(error) + '\n')
                raise
            finally:
                report['checks'].append({'name': name, 'status': status, 'seconds': round(time.monotonic() - started, 3),
                                         'log': log.name, 'sha256': sha256(log)})
                print(status + ' ' + name, flush=True)
        if fingerprint() != initial:
            raise ValueError('source inputs changed during verification')
        report['status'] = 'PASS'
    finally:
        report['generated_utc'] = datetime.now(timezone.utc).isoformat()
        (output / 'verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    try:
        validate_report(ROOT, output / 'verification.json', 'linux')
    except (OSError, ValueError, TypeError, AttributeError):
        report['status'] = 'FAIL'
        (output / 'verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--plan', action='store_true', help='Print the commands without executing them')
    args = parser.parse_args()
    output = (args.output or ROOT / '.cache' / ('verification-linux-' + datetime.now(timezone.utc).strftime('%Y%m%d-%H%M%S'))).resolve()
    commands = steps(output, sys.executable)
    if args.plan:
        print(json.dumps({'platform': 'linux', 'target': 'Debian 12/13 '+target_arch(), 'output': str(output),
                          'checks': [{'name': name, 'commands': actions} for name, actions in commands],
                          'system_changed': False, 'real_network_acceptance': 'NOT-RUN'}, ensure_ascii=False, indent=2))
        return 0
    try:
        run(output, commands)
    except (OSError, ValueError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        print('Verification failed: ' + str(error), file=sys.stderr)
        return 1
    print('Verification PASS. Report: ' + str(output / 'verification.json'))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
