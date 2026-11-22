#!/usr/bin/env python3
"""Prepare dedicated checkouts and verify the complete patched source tree."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
REPLACEMENT = 'github.com/netbirdio/dex=../dex'


def run(args, cwd, env=None):
    return subprocess.run(args, cwd=cwd, env=env, check=True, capture_output=True, text=True).stdout


def assert_inputs(checkout, commit, patch, replacement=None):
    """Compare every input against a temporary index of the locked tree plus patch."""
    checkout, patch = Path(checkout).resolve(), Path(patch).resolve()
    if run(['git', 'rev-parse', 'HEAD'], checkout).strip() != commit:
        raise ValueError('unexpected upstream revision')
    with tempfile.TemporaryDirectory(prefix='netgo-upstream-index-') as temporary:
        directory = Path(temporary)
        env = dict(os.environ, GIT_INDEX_FILE=str(directory / 'index'))
        run(['git', 'read-tree', commit], checkout, env)
        run(['git', 'apply', '--cached', '--whitespace=nowarn', str(patch)], checkout, env)
        if replacement:
            module = directory / 'go.mod'
            module.write_text(run(['git', 'show', ':go.mod'], checkout, env), encoding='utf-8', newline='\n')
            run(['go', 'mod', 'edit', '-replace', replacement, str(module)], directory)
            blob = run(['git', 'hash-object', '-w', str(module)], checkout, env).strip()
            run(['git', 'update-index', '--cacheinfo', '100644', blob, 'go.mod'], checkout, env)
        changed = subprocess.run(['git', 'diff', '--quiet', '--no-ext-diff'], cwd=checkout, env=env, capture_output=True)
        # Include ignored files: an ignored .go file is still a compiler input.
        extra = run(['git', 'ls-files', '--others', '-z'], checkout, env)
        if changed.returncode != 0 or extra:
            raise ValueError('upstream checkout differs from locked source plus patches; retain it for review and use a new --output directory')


def prepare(output=None):
    lock = json.loads((ROOT / 'upstream/versions.lock.json').read_text(encoding='utf-8'))
    digest = hashlib.sha256()
    for name in ('dex', 'netbird'):
        digest.update(lock[name]['commit'].encode())
        digest.update((ROOT / 'upstream/patches' / (name + '.patch')).read_bytes())
    out = (ROOT / (output or '.cache/upstream-verified-' + digest.hexdigest()[:16])).resolve()
    out.mkdir(parents=True, exist_ok=True)
    for name in ('dex', 'netbird'):
        commit, url = lock[name]['commit'], lock[name]['source'] + '.git'
        dest = out / name
        if not dest.exists():
            cached = ROOT / '.cache' / name
            available = cached.is_dir() and subprocess.run(['git', 'cat-file', '-e', commit], cwd=cached, capture_output=True).returncode == 0
            if available:
                run(['git', 'clone', '--no-hardlinks', '--no-checkout', str(cached), str(dest)], out)
                run(['git', 'checkout', '--detach', commit], dest)
            else:
                dest.mkdir()
                run(['git', 'init'], dest)
                run(['git', 'remote', 'add', 'origin', url], dest)
                run(['git', 'fetch', '--depth', '1', 'origin', commit], dest)
                run(['git', 'checkout', '--detach', 'FETCH_HEAD'], dest)
        if run(['git', 'rev-parse', 'HEAD'], dest).strip() != commit:
            raise ValueError('unexpected upstream revision')
        patch = ROOT / 'upstream/patches' / (name + '.patch')
        applied = subprocess.run(['git', 'apply', '--reverse', '--check', str(patch)], cwd=dest, capture_output=True).returncode == 0
        if not applied:
            if run(['git', 'status', '--porcelain'], dest).strip():
                raise ValueError('checkout contains unrelated modifications')
            run(['git', 'apply', '--check', str(patch)], dest)
            run(['git', 'apply', str(patch)], dest)
        if name == 'netbird':
            run(['go', 'mod', 'edit', '-replace', REPLACEMENT], dest)
        assert_inputs(dest, commit, patch, REPLACEMENT if name == 'netbird' else None)
    return out / 'netbird'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output')
    parser.add_argument('--path-only', action='store_true')
    args = parser.parse_args()
    try:
        source = prepare(args.output)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, 'Upstream preparation failed: ' + str(error) + '\n')
    print(str(source) if args.path_only else json.dumps({'netbird': str(source), 'dex': str(source.parent / 'dex'),
                                                      'status': 'locked source and complete patched tree verified; compile and test before use'}))


if __name__ == '__main__':
    main()
