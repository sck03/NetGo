#!/usr/bin/env python3
"""Build only native iPhone/iPad clients and their Packet Tunnel extension."""
import argparse
import importlib.util
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
from apple_toolchain import ROOT, environment, native, run, sums
from apple_project import create

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sdk',choices=('iphonesimulator','iphoneos'),default='iphonesimulator')
    parser.add_argument('--team',default='')
    parser.add_argument('--plan',action='store_true');args=parser.parse_args()
    if args.plan:print('iOS 17+: SwiftUI app, Packet Tunnel extension, Keychain device identity, Go VPN runtime and native viewer; simulator build or team-signed device build.');return
    env=environment();archive=native(args.sdk,'arm64','17.0',env)
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
        run([Path(env['GOBIN'])/'gomobile','bind','-target='+target,'-iosversion=17','-ldflags=-checklinkname=0','-o',framework,'./mobile'],cwd=source,env=env)
    info=plistlib.loads((framework/'Info.plist').read_bytes())
    identifier=next(item['LibraryIdentifier'] for item in info['AvailableLibraries'] if item['SupportedPlatform']=='ios' and (item.get('SupportedPlatformVariant')=='simulator')==(args.sdk=='iphonesimulator'))
    library=next((framework/identifier).glob('*.framework'))
    project=create('iOS',ROOT/('.cache/project-'+args.sdk),archive,library)
    build=ROOT/('.cache/xcode-'+args.sdk)
    flags=['CODE_SIGNING_ALLOWED=NO'] if not args.team else ['CODE_SIGNING_ALLOWED=YES','DEVELOPMENT_TEAM='+args.team,'-allowProvisioningUpdates']
    run(['xcodebuild','-project',project,'-target','NetGo','-configuration','Release','-sdk',args.sdk,'ARCHS=arm64','SYMROOT='+str(build),*flags,'build'],env=env)
    app=build/('Release-'+args.sdk)/'NetGo.app'
    if args.sdk=='iphonesimulator':
        run(['ditto','-c','-k','--keepParent',app,output/'NetGo-0.3.0-dev-ios-simulator.zip'],env=env)
    else:
        payload=ROOT/'.cache/ios-payload';payload.mkdir(exist_ok=True)
        shutil.copytree(app,payload/'Payload/NetGo.app',dirs_exist_ok=True)
        suffix='signed' if args.team else 'unsigned'
        run(['ditto','-c','-k',payload,output/('NetGo-0.3.0-dev-ios-'+suffix+'.ipa')],env=env)
    sums(output)

if __name__=='__main__':main()
