#!/usr/bin/env python3
"""Export only the auxiliary node configuration and its TLS key pairs."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil

FILES = {'auxiliary/frps.toml', 'auxiliary/relay.env', 'auxiliary/haproxy.cfg', 'auxiliary/relay-port.nft'}

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def export(rendered, tls, output):
    rendered, tls, output = Path(rendered), Path(tls), Path(output)
    manifest = json.loads((rendered / 'deployment.json').read_text(encoding='utf-8'))
    if manifest.get('schema') != 1 or not manifest.get('auxiliary'):
        raise ValueError('Render a site with an auxiliary node first')
    files = {}
    for name in sorted(FILES):
        if name not in manifest['files']: continue
        path = rendered / name
        if path.is_symlink() or digest(path) != manifest['files'][name]:
            raise ValueError('Rendered configuration changed: ' + name)
        files[name] = path
    for name in ('relay.crt', 'relay.key', 'tunnel.crt', 'tunnel.key'):
        path = tls / name
        if not path.is_file() or path.is_symlink(): raise ValueError('Missing TLS file: ' + name)
        files['tls/' + name] = path
    if (tls / 'ca.crt').is_file(): files['tls/ca.crt'] = tls / 'ca.crt'
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', manifest['site_id']): raise ValueError('Invalid site ID')
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for name, source in files.items():
        target = output / name; target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        shutil.copyfile(source, target); target.chmod(0o600)
    data = {'schema':1, 'owner':'NetGo VPS deployment', 'site_id':manifest['site_id'],
            'auxiliary':manifest['auxiliary'], 'files':{n:digest(output / n) for n in files}}
    path = output / 'vps.json'; path.write_text(json.dumps(data, indent=2) + '\n', encoding='utf-8'); path.chmod(0o600)
    return data

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--rendered', required=True, type=Path)
    p.add_argument('--tls-dir', required=True, type=Path)
    p.add_argument('--output', required=True, type=Path)
    a = p.parse_args(); data = export(a.rendered, a.tls_dir, a.output)
    print('VPS-only bundle:', a.output, '; site:', data['site_id'])

if __name__ == '__main__': main()
