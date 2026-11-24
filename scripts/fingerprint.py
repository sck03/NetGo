#!/usr/bin/env python3
"""Fingerprint exact code/build/test inputs without caches, logs or secrets."""
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCOPES = ('apps', 'assets', 'cmd', 'internal', 'mobile', 'packages', 'deploy', 'scripts', 'tests', 'examples', 'upstream', '.github')
EXCLUDED = {'dist', '__pycache__', 'node_modules', '.git', 'build', '.gradle', '.cxx'}


def fingerprint(root=ROOT):
    root = Path(root).resolve()
    files = [root / name for name in ('go.mod', 'go.sum', 'package.json', 'package-lock.json', 'tsconfig.json', 'vite.config.ts', 'install-server.sh', 'install-vps.sh', 'install-client.ps1')]
    for scope in SCOPES:
        files.extend(path for path in (root / scope).rglob('*') if path.is_file()
                     and not any(part in EXCLUDED for part in path.relative_to(root).parts)
                     and path.suffix not in ('.pyc', '.aar', '.apk', '.aab', '.jks', '.keystore')
                     and path.name != 'local.properties' and not path.name.endswith('-sources.jar'))
    entries = []
    digest = hashlib.sha256()
    for path in sorted(files, key=lambda item: item.relative_to(root).as_posix()):
        data = path.read_bytes()
        name = path.relative_to(root).as_posix()
        sha = hashlib.sha256(data).hexdigest()
        entries.append({'path': name, 'bytes': len(data), 'sha256': sha})
        digest.update((sha + '  ' + name + '\n').encode('utf-8'))
    return {'schema': 1, 'code_sha256': digest.hexdigest(), 'files': entries,
            'excluded': 'generated assets, caches, runtime state, documentation and evidence'}


if __name__ == '__main__':
    print(json.dumps(fingerprint(), ensure_ascii=False, indent=2))
