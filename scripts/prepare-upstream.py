#!/usr/bin/env python3
"""Prepare only dedicated checkouts and apply checked-in patches reproducibly."""
import argparse,json,pathlib,subprocess
p=argparse.ArgumentParser();p.add_argument('--output',default='.cache/upstream-build');a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[1];out=(root/a.output).resolve();out.mkdir(parents=True,exist_ok=True)
repos=[('dex','https://github.com/netbirdio/dex.git','0319b501f1a6822b9f941dcac82d0d4edca5018b'),('netbird','https://github.com/netbirdio/netbird.git','6d4814d22726c16961be79409dfbf6a2b2f7537b')]
def run(args,cwd):return subprocess.run(args,cwd=cwd,check=True,capture_output=True,text=True).stdout.strip()
for name,url,commit in repos:
    dest=out/name
    if not dest.exists():
        dest.mkdir();run(['git','init'],dest);run(['git','remote','add','origin',url],dest);run(['git','fetch','--depth','1','origin',commit],dest);run(['git','checkout','--detach','FETCH_HEAD'],dest)
    if run(['git','rev-parse','HEAD'],dest)!=commit: raise RuntimeError('unexpected upstream revision')
    patch=root/'upstream/patches'/f'{name}.patch'
    applied=subprocess.run(['git','apply','--reverse','--check',str(patch)],cwd=dest,capture_output=True).returncode==0
    if not applied:
        if run(['git','status','--porcelain'],dest):raise RuntimeError('checkout contains unrelated modifications')
        run(['git','apply','--check',str(patch)],dest);run(['git','apply',str(patch)],dest)
run(['go','mod','edit','-replace','github.com/netbirdio/dex=../dex'],out/'netbird')
print(json.dumps({'netbird':str(out/'netbird'),'dex':str(out/'dex'),'status':'patches applied; compile and test before use'}))
