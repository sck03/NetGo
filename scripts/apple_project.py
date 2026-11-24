"""Generate small native Xcode projects without an external project generator."""
import hashlib
import json
from pathlib import Path
import plistlib

ROOT=Path(__file__).resolve().parents[1]
def ident(name):return hashlib.sha256(name.encode()).hexdigest()[:24].upper()
def quote(value):return json.dumps(str(value))
def value(obj):
    if isinstance(obj,dict):return '{ '+ ' '.join(quote(k)+' = '+value(v)+';' for k,v in obj.items())+' }'
    if isinstance(obj,list):return '('+','.join(value(v) for v in obj)+')'
    return quote(obj)

def create(platform,output,native,framework=None):
    output=Path(output);project=output/'NetGo.xcodeproj';project.mkdir(parents=True,exist_ok=True)
    objects={};project_id=ident('project');group=ident('group');products=ident('products')
    refs=[];product_refs=[];targets=[]
    def add(name,obj):key=ident(name);objects[key]=obj;return key
    def configs(name,settings):
        configs=[]
        for kind in ('Debug','Release'):configs.append(add(name+kind,{'isa':'XCBuildConfiguration','name':kind,'buildSettings':settings}))
        return add(name+'config',{'isa':'XCConfigurationList','buildConfigurations':configs,'defaultConfigurationName':'Release','defaultConfigurationIsVisible':'0'})
    project_cfg=configs('project',{'CLANG_ENABLE_OBJC_ARC':'YES','CLANG_ENABLE_MODULES':'YES','SWIFT_VERSION':'5.0','CLANG_CXX_LANGUAGE_STANDARD':'c++17'})
    shared=[ROOT/'apps/apple/Shared'/name for name in ('ClientView.swift','ClientModel.swift','RemoteViewer.swift','MediaPlayer.swift')]
    specs=[('NetGo',shared+[ROOT/'apps/apple'/platform/'NetGoApp.swift'],'app')]
    if platform=='iOS':specs.append(('NetGoTunnel',[ROOT/'apps/apple/iOS/PacketTunnelProvider.swift',ROOT/'apps/apple/iOS/TunnelBridge.m'],'appex'))
    target_map={name:ident('target'+name) for name,files,extension in specs}
    for name,files,extension in specs:
        sources=[];libraries=[]
        for path in files:
            ref=add(str(path),{'isa':'PBXFileReference','path':str(path),'sourceTree':'<absolute>','lastKnownFileType':'sourcecode.swift' if path.suffix=='.swift' else 'sourcecode.c.objc'})
            refs.append(ref);sources.append(add(name+str(path)+'build',{'isa':'PBXBuildFile','fileRef':ref}))
        for path in ([Path(native)]+([Path(framework)] if framework and extension=='appex' else [])):
            ref=add(name+str(path),{'isa':'PBXFileReference','path':str(path),'sourceTree':'<absolute>','lastKnownFileType':'wrapper.framework' if path.suffix=='.framework' else 'archive.ar'})
            refs.append(ref);libraries.append(add(name+str(path)+'lib',{'isa':'PBXBuildFile','fileRef':ref}))
        source_phase=add(name+'sources',{'isa':'PBXSourcesBuildPhase','buildActionMask':'2147483647','files':sources,'runOnlyForDeploymentPostprocessing':'0'})
        framework_phase=add(name+'frameworks',{'isa':'PBXFrameworksBuildPhase','buildActionMask':'2147483647','files':libraries,'runOnlyForDeploymentPostprocessing':'0'})
        product=add(name+'product',{'isa':'PBXFileReference','explicitFileType':'wrapper.application' if extension=='app' else 'wrapper.app-extension','path':name+'.'+extension,'sourceTree':'BUILT_PRODUCTS_DIR'})
        product_refs.append(product);phases=[source_phase,framework_phase];dependencies=[]
        settings={'PRODUCT_NAME':name,'PRODUCT_BUNDLE_IDENTIFIER':'org.netgo.client.mac' if platform=='macOS' else ('org.netgo.client.ios' if extension=='app' else 'org.netgo.client.ios.tunnel'),
                  'GENERATE_INFOPLIST_FILE':'YES','CODE_SIGN_STYLE':'Automatic','CODE_SIGNING_ALLOWED':'NO','SWIFT_VERSION':'5.0',
                  'SWIFT_INCLUDE_PATHS':str(ROOT/'apps/apple/Bridge'),'LIBRARY_SEARCH_PATHS':str(Path(native).parent),'HEADER_SEARCH_PATHS':[str(ROOT/'apps/apple/Bridge'),str(ROOT/'packages/remote-client/include')],
                  'OTHER_LDFLAGS':['-lc++','-lresolv','-framework','Security','-framework','CoreFoundation'], 'ENABLE_HARDENED_RUNTIME':'YES'}
        if platform=='macOS':settings.update(SDKROOT='macosx',MACOSX_DEPLOYMENT_TARGET='14.0',OTHER_LDFLAGS=['-lc++','-lcurl','-framework','Security','-framework','CoreFoundation'],INFOPLIST_KEY_NSPrincipalClass='NSApplication')
        else:
            settings.update(SDKROOT='iphoneos',IPHONEOS_DEPLOYMENT_TARGET='17.0',TARGETED_DEVICE_FAMILY='1,2',CODE_SIGN_ENTITLEMENTS=str(ROOT/'apps/apple/iOS/NetGo.entitlements'))
            if extension=='appex':
                settings.update(INFOPLIST_FILE=str(ROOT/'apps/apple/iOS/TunnelInfo.plist'),GENERATE_INFOPLIST_FILE='NO',SWIFT_OBJC_BRIDGING_HEADER=str(ROOT/'apps/apple/iOS/TunnelBridge.h'),APPLICATION_EXTENSION_API_ONLY='YES',SKIP_INSTALL='YES',FRAMEWORK_SEARCH_PATHS=str(Path(framework).parent))
            else:
                settings.update(INFOPLIST_KEY_UILaunchScreen_Generation='YES',INFOPLIST_KEY_UIApplicationSceneManifest_Generation='YES')
                extension_product=ident('NetGoTunnelproduct')
                build=add('embedExtension',{'isa':'PBXBuildFile','fileRef':extension_product,'settings':{'ATTRIBUTES':['RemoveHeadersOnCopy']}})
                phases.append(add('copyExtension',{'isa':'PBXCopyFilesBuildPhase','buildActionMask':'2147483647','dstPath':'','dstSubfolderSpec':'13','files':[build],'runOnlyForDeploymentPostprocessing':'0'}))
                proxy=add('extensionProxy',{'isa':'PBXContainerItemProxy','containerPortal':project_id,'proxyType':'1','remoteGlobalIDString':target_map['NetGoTunnel'],'remoteInfo':'NetGoTunnel'})
                dependencies.append(add('extensionDependency',{'isa':'PBXTargetDependency','target':target_map['NetGoTunnel'],'targetProxy':proxy}))
        cfg=configs(name,settings)
        objects[target_map[name]]={'isa':'PBXNativeTarget','name':name,'productName':name,'productReference':product,'productType':'com.apple.product-type.application' if extension=='app' else 'com.apple.product-type.app-extension','buildConfigurationList':cfg,'buildPhases':phases,'dependencies':dependencies,'buildRules':[]}
        targets.append(target_map[name])
    objects[group]={'isa':'PBXGroup','children':refs+[products],'sourceTree':'<group>'}
    objects[products]={'isa':'PBXGroup','name':'Products','children':product_refs,'sourceTree':'<group>'}
    objects[project_id]={'isa':'PBXProject','compatibilityVersion':'Xcode 14.0','developmentRegion':'en','knownRegions':['en','Base'],'mainGroup':group,'productRefGroup':products,'projectDirPath':'','projectRoot':'','targets':targets,'buildConfigurationList':project_cfg,'attributes':{'LastUpgradeCheck':'1600'}}
    body={'archiveVersion':'1','classes':{},'objectVersion':'56','objects':objects,'rootObject':project_id}
    (project/'project.pbxproj').write_text('// !$*UTF8*$!\n'+value(body)+'\n',encoding='utf-8')
    return project
