#!/usr/bin/env python3
"""Bind build artifacts to complete checks and their exact source inputs."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import zipfile

from fingerprint import ROOT, fingerprint

PRODUCTS = ('netgo-server', 'netgo-helper', 'netgoctl', 'netgo-client-service', 'netgo-ddns')
UPSTREAM = ('netbird-server', 'netbird', 'netbird-relay', 'caddy', 'frpc', 'frps')
WINDOWS = ('netgo-desktop.exe', 'netgo-agent.exe', 'netbird.exe', 'wintun.dll')
COMMON_CHECKS = frozenset(('tool-versions', 'source-inputs', 'tools-tests', 'go-race',
                           'go-vet', 'frontend', 'scripts', 'certificates', 'upstream',
                           'product-build', 'deploy-validators', 'release-manifest', 'artifact-inputs'))
CHECKS = {'windows': COMMON_CHECKS | {'cli-build', 'native'}, 'linux': COMMON_CHECKS}


def sha256(path):
    with Path(path).open('rb') as handle:
        return hashlib.file_digest(handle, 'sha256').hexdigest()


def read_json(path):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('duplicate JSON key: ' + key)
            result[key] = value
        return result
    return json.loads(Path(path).read_text(encoding='utf-8-sig'), object_pairs_hook=unique)


def artifact_paths(platform):
    if platform not in CHECKS:
        raise ValueError('unsupported verification platform')
    files = ['dist/linux-amd64/' + name for name in (*PRODUCTS, 'release.json', 'SHA256SUMS')]
    folder, binaries = ('windows-amd64', WINDOWS) if platform == 'windows' else ('upstream-linux-amd64', UPSTREAM)
    return sorted(files + ['dist/' + folder + '/' + name for name in (*binaries, 'SHA256SUMS')])


def artifact_inputs(root, platform):
    root = Path(root).resolve()
    files = []
    for name in artifact_paths(platform):
        path = root / name
        if not path.is_file() or any(p.is_symlink() for p in (path, *path.parents) if p != root and root in p.parents):
            raise ValueError('build artifact must be a regular owned file: ' + name)
        files.append({'path': name, 'bytes': path.stat().st_size, 'sha256': sha256(path)})
    return {'schema': 1, 'platform': platform, 'files': files}


def verify_archive(path, expected_files=None):
    """Check the root and owned nested manifests, plus verified source/build bytes."""
    with zipfile.ZipFile(path) as archive:
        names = [entry.filename for entry in archive.infolist() if not entry.is_dir()]
        available = set(names)
        if len(names) != len(available):
            raise ValueError('duplicate archive entry')
        for name in names:
            item = PurePosixPath(name)
            if item.is_absolute() or '..' in item.parts or '\\' in name or ':' in name or item.as_posix() != name:
                raise ValueError('unsafe archive path')
        if archive.testzip() is not None:
            raise ValueError('archive CRC failed')
        manifests = [name for name in ('SHA256SUMS', 'bin/SHA256SUMS', 'dist/SHA256SUMS',
                                      'dist/linux-amd64/SHA256SUMS') if name in names]
        if 'SHA256SUMS' not in manifests:
            raise ValueError('root archive checksum manifest missing')
        for manifest in manifests:
            seen = set()
            for line in archive.read(manifest).decode('utf-8-sig').splitlines():
                match = re.fullmatch(r'([0-9a-f]{64})  (.+)', line)
                if not match:
                    raise ValueError('invalid archive checksum entry')
                target = (PurePosixPath(manifest).parent / match[2]).as_posix()
                if target not in available or target in seen or target == manifest:
                    raise ValueError('ambiguous archive checksum target')
                with archive.open(target) as stream:
                    actual = hashlib.file_digest(stream, 'sha256').hexdigest()
                if actual != match[1]:
                    raise ValueError('archive checksum mismatch: ' + target)
                seen.add(target)
            if manifest == 'SHA256SUMS' and seen != available - {'SHA256SUMS'}:
                raise ValueError('archive contains files absent from its checksum manifest')
        for name, expected in (expected_files or {}).items():
            if name not in available:
                raise ValueError('verified input is absent from archive: ' + name)
            with archive.open(name) as stream:
                if hashlib.file_digest(stream, 'sha256').hexdigest() != expected:
                    raise ValueError('archive differs from verified input: ' + name)
    return {'name': Path(path).name, 'sha256': sha256(path), 'bytes': Path(path).stat().st_size,
            'files': len(names), 'verified_inputs': len(expected_files or {}), 'crc': 'PASS', 'checksums': 'PASS'}


def validate_report(root, report_path, platform):
    """Reject stale sources, changed binaries, skipped checks or damaged evidence."""
    root, report_path = Path(root).resolve(), Path(report_path).resolve()
    report = read_json(report_path)
    if (report.get('schema') != 2 or report.get('status') != 'PASS' or
            report.get('platform') != platform or platform not in CHECKS):
        raise ValueError('a complete schema-2 verification report for this platform is required')
    checks = report.get('checks')
    if not isinstance(checks, list) or len(checks) != len(CHECKS[platform]):
        raise ValueError('verification check set is incomplete')
    found = set()
    for check in checks:
        name = check.get('name')
        if name not in CHECKS[platform] or name in found or check.get('status') != 'PASS':
            raise ValueError('verification check missing, duplicated or not passed')
        if check.get('log') != name + '.txt' or not re.fullmatch(r'[0-9a-f]{64}', check.get('sha256', '')):
            raise ValueError('invalid verification log reference')
        path = report_path.parent / check['log']
        if path.is_symlink() or not path.is_file() or sha256(path) != check['sha256']:
            raise ValueError('verification log checksum mismatch: ' + name)
        found.add(name)
    current = fingerprint(root)
    recorded = read_json(report_path.parent / 'source-inputs.txt')
    if (report.get('code_sha256') != current['code_sha256'] or recorded != current):
        raise ValueError('source inputs changed after verification; rerun the complete checks')
    artifacts = artifact_inputs(root, platform)
    if read_json(report_path.parent / 'artifact-inputs.txt') != artifacts:
        raise ValueError('build artifacts changed after verification; rebuild and verify before packaging')
    return {'schema': 1, 'platform': platform, 'code_sha256': current['code_sha256'],
            'verification_sha256': sha256(report_path), 'artifact_inputs': artifacts,
            'real_network_acceptance': 'NOT-RUN'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('snapshot', 'check'))
    parser.add_argument('--platform', choices=tuple(CHECKS), required=True)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    if args.action == 'check' and not args.report:
        parser.error('--report is required for check')
    try:
        result = artifact_inputs(ROOT, args.platform) if args.action == 'snapshot' else validate_report(ROOT, args.report, args.platform)
    except (OSError, ValueError, TypeError, AttributeError) as error:
        parser.exit(1, 'Verification rejected: ' + str(error) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
