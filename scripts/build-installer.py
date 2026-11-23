#!/usr/bin/env python3
"""Build a native self-contained Windows installer from an exact staged payload."""
import argparse
import hashlib
from pathlib import Path
import shutil
import subprocess

ROOT=Path(__file__).resolve().parents[1]
def run(args): subprocess.run(list(map(str,args)),cwd=ROOT,check=True)
def build(payload,output,work,network_only=False):
    payload=payload.resolve();output=output.resolve();work=work.resolve()
    if not (payload/'bin/SHA256SUMS').is_file(): raise ValueError('verified Windows payload required')
    work.mkdir(parents=True,exist_ok=True)
    native=work/'native'
    configure=['cmake','-S',ROOT/'apps/windows/installer','-B',native,'-G','Ninja','-DCMAKE_BUILD_TYPE=Release',f'-DNETGO_DEFAULT_NETWORK={"ON" if network_only else "OFF"}']
    run(configure)
    run(['cmake','--build',native,'--target','netgo-maintenance'])
    shutil.copy2(native/'netgo-maintenance.exe',payload/'bin/netgo-maintenance.exe')
    # Recreate the inner manifest after adding the native maintenance program.
    binaries=sorted(p for p in (payload/'bin').iterdir() if p.is_file() and p.name!='SHA256SUMS')
    (payload/'bin/SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in binaries),encoding='utf-8')
    # The private maintenance program is part of the installer, not an extra
    # end-user download and not a service image.
    cabinet=work/'payload.cab'
    lines=['.OPTION EXPLICIT','.Set Cabinet=ON','.Set Compress=ON','.Set CompressionType=LZX','.Set CompressionMemory=21','.Set MaxDiskSize=0','.Set CabinetNameTemplate=payload.cab',f'.Set DiskDirectoryTemplate="{work}"',f'.Set RptFileName="{work / "cabinet.rpt"}"',f'.Set InfFileName="{work / "cabinet.inf"}"']
    for file in sorted(payload.rglob('*')):
        if file.is_symlink():raise ValueError('payload links are forbidden')
        if file.is_file():
            rel=str(file.relative_to(payload)).replace('/','\\')
            if '"' in str(file) or '\n' in str(file):raise ValueError('invalid payload filename')
            lines.append(f'"{file}" "{rel}"')
    ddf=work/'payload.ddf';ddf.write_text('\n'.join(lines)+'\n',encoding='utf-8')
    with (work/'makecab.log').open('wb') as log:
        subprocess.run(['makecab.exe','/F',str(ddf)],cwd=ROOT,stdout=log,stderr=subprocess.STDOUT,check=True)
    run([*configure,f'-DNETGO_PAYLOAD={cabinet}'])
    run(['cmake','--build',native,'--target','netgo-setup'])
    output.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(native/'netgo-setup.exe',output)
    print(f'Setup: {output} ({output.stat().st_size} bytes) SHA256 {hashlib.sha256(output.read_bytes()).hexdigest()}')
    return output
if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--payload',type=Path,required=True);parser.add_argument('--output',type=Path,required=True);parser.add_argument('--work',type=Path,required=True)
    a=parser.parse_args();build(a.payload,a.output,a.work)
