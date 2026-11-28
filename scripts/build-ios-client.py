#!/usr/bin/env python3
"""Build only native iPhone/iPad clients and their Packet Tunnel extension."""
import argparse
import importlib.util
import json
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
from apple_toolchain import ROOT, environment, native, run, sums, licenses
from apple_project import create
from build_config import config, project_ldflags, write_buildinfo

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sdk',choices=('iphonesimulator','iphoneos'),default='iphonesimulator')
    parser.add_argument('--team',default='')
    parser.add_argument('--version')
    parser.add_argument('--arch',choices=('arm64',),default='arm64')
    parser.add_argument('--build-number',type=int,default=1)
    parser.add_argument('--plan',action='store_true');args=parser.parse_args()
    build_config=config('ios',args.version,args.arch)
    if args.build_number<1:parser.error('build number must be positive')
    if args.plan:print(json.dumps(dict(build_config,sdk=args.sdk,build_number=args.build_number)));print('iOS 17+: SwiftUI app, Packet Tunnel extension, Keychain device identity, Go VPN runtime and native viewer; simulator build or team-signed device build.');return
    env=environment()
    tests=ROOT/'.cache/apple-tests';tests.mkdir(parents=True,exist_ok=True)
    run(['xcrun','swiftc','-swift-version','5','-parse-as-library',ROOT/'apps/apple/iOS/TunnelRequestQueue.swift',ROOT/'apps/apple/tests/TunnelRequestQueueTests.swift','-o',tests/'tunnel-requests'],env=env)
    run([tests/'tunnel-requests'],env=env)
    archive=native(args.sdk,'arm64','17.0',env)
    version='v0.0.0-20260816165457-f98cc9b3c733'
    run(['go','install','golang.org/x/mobile/cmd/gomobile@'+version],env=env)
    run(['go','install','golang.org/x/mobile/cmd/gobind@'+version],env=env)
    output=ROOT/'dist/ios-client';output.mkdir(parents=True,exist_ok=True)
    framework=ROOT/'.cache'/('mobile-'+args.sdk)/'NetGoMobile.xcframework'
    framework.parent.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='ios-go-',dir=ROOT/'.cache') as temporary:
        source=Path(temporary)/'source'
        run(['python3','-B',ROOT/'scripts/prepare-mobile-inputs.py','--output',source,'--go',shutil.which('go')],env=env)
        target='ios/arm64' if args.sdk=='iphoneos' else 'iossimulator/arm64'
        run([Path(env['GOBIN'])/'gomobile','bind','-target='+target,'-iosversion=17','-ldflags='+project_ldflags(build_config['version'],'-checklinkname=0'),'-o',framework,'./mobile'],cwd=source,env=env)
        upstream=Path(subprocess.check_output(['python3','-B',str(ROOT/'scripts/prepare-upstream.py'),'--path-only'],env=env,text=True).strip())
        resources=licenses(ROOT/('.cache/ios-legal-'+args.sdk)/'licenses',upstream,env)
    info=plistlib.loads((framework/'Info.plist').read_bytes())
    identifier=next(item['LibraryIdentifier'] for item in info['AvailableLibraries'] if item['SupportedPlatform']=='ios' and (item.get('SupportedPlatformVariant')=='simulator')==(args.sdk=='iphonesimulator'))
    library=next((framework/identifier).glob('*.framework'))
    project=create('iOS',ROOT/('.cache/project-'+args.sdk),archive,library,resources,selected_version=build_config['version'],build_number=args.build_number)
    build=ROOT/('.cache/xcode-'+args.sdk)
    flags=['CODE_SIGNING_ALLOWED=NO'] if not args.team else ['CODE_SIGNING_ALLOWED=YES','DEVELOPMENT_TEAM='+args.team,'-allowProvisioningUpdates']
    run(['xcodebuild','-project',project,'-target','NetGo','-configuration','Release','-sdk',args.sdk,'ARCHS=arm64','SYMROOT='+str(build),*flags,'build'],env=env)
    app=build/('Release-'+args.sdk)/'NetGo.app'
    if args.sdk=='iphonesimulator':
        run(['ditto','-c','-k','--keepParent',app,output/f"NetGo-{build_config['version']}-ios-{args.arch}-simulator.zip"],env=env)
    else:
        payload=ROOT/'.cache/ios-payload';payload.mkdir(exist_ok=True)
        shutil.copytree(app,payload/'Payload/NetGo.app',dirs_exist_ok=True)
        suffix='signed' if args.team else 'unsigned'
        run(['ditto','-c','-k',payload,output/('NetGo-'+build_config['version']+'-ios-'+args.arch+'-'+suffix+'.ipa')],env=env)
    write_buildinfo(output,'ios',build_config['version'],args.arch,sdk=args.sdk,build_number=args.build_number,signed=bool(args.team))
    sums(output)

if __name__=='__main__':main()
