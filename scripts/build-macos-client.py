#!/usr/bin/env python3
"""Build only the native macOS client and its required network components."""
import argparse
import importlib.util
import os
import json
from pathlib import Path
import platform
import shutil
import subprocess
from apple_toolchain import ROOT, environment, native, run, sums, licenses
from apple_project import create
from build_config import config, project_ldflags, write_buildinfo

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arch',choices=('arm64','x86_64'),default='arm64' if platform.machine()=='arm64' else 'x86_64')
    parser.add_argument('--version')
    parser.add_argument('--build-number',type=int,default=1)
    parser.add_argument('--plan',action='store_true');args=parser.parse_args()
    build_config=config('macos',args.version,args.arch)
    if args.build_number<1:parser.error('build number must be positive')
    if args.plan:print(json.dumps(dict(build_config,build_number=args.build_number)));print('macOS: SwiftUI app, Keychain-backed local controller, native viewer, independently managed NetBird daemon; macOS 14+; stable Xcode required.');return
    env=environment()
    run(['go','test','./internal/ipc'],env=env)
    tests=ROOT/'.cache/apple-tests';tests.mkdir(parents=True,exist_ok=True)
    run(['xcrun','swiftc','-swift-version','5','-parse-as-library',ROOT/'apps/apple/Shared/ControlResponse.swift',ROOT/'apps/apple/tests/ControlResponseTests.swift','-o',tests/'control-response'],env=env)
    run([tests/'control-response'],env=env)
    run(['xcrun','swiftc','-swift-version','5','-parse-as-library',ROOT/'apps/apple/Shared/DesktopConnectionCoordinator.swift',ROOT/'apps/apple/tests/ConnectionCoordinatorTests.swift','-o',tests/'connections'],env=env)
    run([tests/'connections'],env=env)
    archive=native('macosx',args.arch,'14.0',env)
    target='arm64' if args.arch=='arm64' else 'amd64'
    env.update(GOOS='darwin',GOARCH=target,CGO_ENABLED='1',CC='clang -arch '+args.arch)
    binaries=ROOT/'.cache/macos-binaries';binaries.mkdir(exist_ok=True)
    run(['go','build','-trimpath','-ldflags='+project_ldflags(build_config['version']),'-o',binaries/'netgo-client-service','./cmd/netgo-client-service'],env=env)
    upstream=Path(subprocess.check_output(['python3','-B',str(ROOT/'scripts/prepare-upstream.py'),'--path-only'],env=env,text=True).strip())
    run(['go','build','-trimpath','-ldflags=-s -w','-o',binaries/'netbird','./client'],cwd=upstream,env=env)
    resources=licenses(ROOT/'.cache/macos-legal/licenses',upstream,env)
    project=create('macOS',ROOT/'.cache/project-macos',archive,resources=resources,selected_version=build_config['version'],build_number=args.build_number)
    build=ROOT/'.cache/xcode-macos'
    run(['xcodebuild','-project',project,'-target','NetGo','-configuration','Release','-sdk','macosx','ARCHS='+args.arch,'SYMROOT='+str(build),'CODE_SIGNING_ALLOWED=NO','build'],env=env)
    output=ROOT/'dist/macos-client';output.mkdir(parents=True,exist_ok=True)
    app=output/'NetGo.app'
    if app.exists():raise ValueError('Retain or move the previous App before a fresh packaging run')
    shutil.copytree(build/'Release/NetGo.app',app)
    resources=app/'Contents/Resources';(resources/'bin').mkdir(parents=True);(resources/'maintenance').mkdir()
    for name in ('netbird','netgo-client-service'):shutil.copy2(binaries/name,resources/'bin'/name)
    sums(resources/'bin')
    shutil.copy2(ROOT/'apps/apple/macOS/install-network.sh',resources/'maintenance/install-network.sh')
    for name in ('netbird','netgo-client-service'):run(['codesign','--force','--sign','-',resources/'bin'/name],env=env)
    sums(resources/'bin');run(['codesign','--force','--sign','-',app],env=env)
    destination=output/('NetGo-'+build_config['version']+'-macos-'+args.arch+'.zip')
    run(['ditto','-c','-k','--sequesterRsrc','--keepParent',app,destination],env=env)
    write_buildinfo(output,'macos',build_config['version'],args.arch,build_number=args.build_number)
    sums(output);run([app/'Contents/MacOS/NetGo','--smoke'],env=env)

if __name__=='__main__':main()
