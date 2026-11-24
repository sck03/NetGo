#!/usr/bin/env python3
"""Build a separate source archive without runtime state or build artifacts."""
import hashlib
from pathlib import Path
import subprocess
import zipfile

ROOT=Path(__file__).resolve().parents[1]
def main():
    revision=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
    names=subprocess.check_output(['git','ls-files','-z'],cwd=ROOT).decode().split('\0')
    output=ROOT/'dist/source';output.mkdir(parents=True,exist_ok=True)
    target=output/('NetGo-source-'+revision[:12]+'.zip')
    with zipfile.ZipFile(target,'w',zipfile.ZIP_DEFLATED) as z:
        for name in sorted(n for n in names if n):
            path=ROOT/name
            if path.is_file() and not path.is_symlink():z.write(path,name)
    (output/'SHA256SUMS').write_text(hashlib.sha256(target.read_bytes()).hexdigest()+'  '+target.name+'\n')
    print(target)
if __name__=='__main__':main()
