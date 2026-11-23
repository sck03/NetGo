#!/usr/bin/env python3
"""Stage Go mobile sources so gomobile can create its own module safely."""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]

def prepare(output, go):
    output = Path(output).resolve()
    if output.exists():
        raise ValueError('mobile source output must be a new directory')
    if ROOT not in output.parents or '.cache' not in output.parts:
        raise ValueError('mobile source staging must stay in the project cache')
    spec = importlib.util.spec_from_file_location('netgo_upstream', ROOT / 'scripts/prepare-upstream.py')
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    inputs = module.prepare()
    output.mkdir(parents=True)
    for name in ('internal', 'mobile'):
        shutil.copytree(ROOT / name, output / name)
    for name in ('go.mod', 'go.sum'):
        shutil.copy2(ROOT / name, output / name)
    subprocess.run([go, 'mod', 'edit', '-replace=github.com/netbirdio/netbird=' + str(inputs),
                    '-replace=github.com/netbirdio/dex=' + str(inputs.parent / 'dex'),
                    '-require=golang.org/x/mobile@v0.0.0-20260816165457-f98cc9b3c733'], cwd=output, check=True)
    graph = json.loads(subprocess.check_output([go, 'mod', 'edit', '-json'], cwd=inputs, text=True))
    for item in graph.get('Replace') or []:
        old, new = item['Old'], item['New']
        target = new['Path']
        if target.startswith('.'):
            target = str((inputs / target).resolve())
        if new.get('Version'):
            target += '@' + new['Version']
        subprocess.run([go, 'mod', 'edit', '-replace=' + old['Path'] + '=' + target], cwd=output, check=True)
    return output

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--go', default='go')
    args = parser.parse_args()
    print(prepare(args.output, args.go))
