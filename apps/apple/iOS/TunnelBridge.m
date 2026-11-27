#import "TunnelBridge.h"
#import <NetGoMobile/Mobile.objc.h>
#import <Security/Security.h>

@interface TunnelBridge () <MobilePlatform>
@property(atomic,strong) MobileClient *client;
@property(nonatomic) int fd;
@property(nonatomic,copy) NSString *interface;
@property(atomic) BOOL cancelled;
@end
@implementation TunnelBridge
- (NSString *)startRoot:(NSString *)root profile:(NSString *)profile alias:(NSString *)alias password:(NSString *)password importProfile:(BOOL)importProfile importID:(NSString *)importID trusted:(BOOL)trusted replace:(BOOL)replace internet:(BOOL)internet tunnelFD:(int)fd interfaceName:(NSString *)name {
 self.fd=fd;self.interface=name;NSError *error=nil;
 self.client=MobileNewClient(root,self,17,[[NSProcessInfo processInfo] hostName],&error);
 if(error||!self.client)return error.localizedDescription?:@"客户端启动失败";
 // Only explicit file import can change trust; reconnect uses the canonical persisted profile.
 NSDictionary *site=[NSJSONSerialization JSONObjectWithData:[profile dataUsingEncoding:NSUTF8StringEncoding] options:0 error:&error];
 if(![site[@"ID"] isKindOfClass:[NSString class]])return @"无效站点";
 NSString *receipt=[@"netgo.import." stringByAppendingString:site[@"ID"]];
 if(importProfile && ![[[NSUserDefaults standardUserDefaults] stringForKey:receipt] isEqualToString:importID]){
  if(importID.length==0)return @"缺少导入操作标识";
  if(!(trusted ? [self.client importProfileTrusted:profile error:&error] : [self.client importProfile:profile error:&error]))return error.localizedDescription?:@"站点导入失败";
  // Persist before login can synchronize to a newer server profile. A lost UI
  // acknowledgement must not repeat an already accepted import on reconnect.
  [[NSUserDefaults standardUserDefaults] setObject:importID forKey:receipt];
  [[NSUserDefaults standardUserDefaults] synchronize];
 }
 if(self.cancelled)return @"VPN 已停止";
 if(!site||![self.client connectMode:site[@"ID"] alias:alias password:password replace:replace internet:internet error:&error])return error.localizedDescription?:@"VPN 连接失败";
 if(self.cancelled){[self.client cancel];return @"VPN 已停止";}
 return @"";
}
- (NSData *)readKey:(NSString *)site error:(NSError **)error {
 NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"org.netgo.ios.device",(__bridge id)kSecAttrAccount:site,(__bridge id)kSecReturnData:@YES};
 CFTypeRef result=NULL;OSStatus status=SecItemCopyMatching((__bridge CFDictionaryRef)query,&result);
 if(status==errSecItemNotFound)return [NSData data];
 if(status!=errSecSuccess){if(error)*error=[NSError errorWithDomain:NSOSStatusErrorDomain code:status userInfo:nil];return nil;}
 return CFBridgingRelease(result);
}
- (BOOL)writeKey:(NSString *)site key:(NSData *)key error:(NSError **)error {
 NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"org.netgo.ios.device",(__bridge id)kSecAttrAccount:site};
 OSStatus status=SecItemUpdate((__bridge CFDictionaryRef)query,(__bridge CFDictionaryRef)@{(__bridge id)kSecValueData:key});
 if(status==errSecItemNotFound){NSMutableDictionary *insert=[query mutableCopy];insert[(__bridge id)kSecValueData]=key;insert[(__bridge id)kSecAttrAccessible]=(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly;status=SecItemAdd((__bridge CFDictionaryRef)insert,NULL);}
 if(status!=errSecSuccess&&error)*error=[NSError errorWithDomain:NSOSStatusErrorDomain code:status userInfo:nil];return status==errSecSuccess;
}
- (long)tunnelFD{return self.fd;}
- (NSString *)interfaceName{return self.interface;}
- (BOOL)configureNetwork:(NSString *)address address6:(NSString *)address6 dns:(NSString *)dns routes:(NSString *)routes error:(NSError **)error {if(self.applyNetwork)self.applyNetwork(address,address6,dns,routes);return YES;}
- (NSData *)message:(NSData *)data {
 MobileClient *client=self.client;
 NSError *error=nil;NSDictionary *request=[NSJSONSerialization JSONObjectWithData:data options:0 error:&error];
 if(!client||!request)return [NSJSONSerialization dataWithJSONObject:@{@"error":@"VPN 未连接"} options:0 error:nil];
 NSString *path=request[@"path"]?:@"", *body=request[@"body"]?:@"", *result=nil;
 if([path isEqualToString:@"/desktop/status"])result=[client status];
 else if([path isEqualToString:@"/desktop/profiles"])result=[client profiles:&error];
 else if([path hasPrefix:@"/desktop/remote/context"]){NSURLComponents *parts=[NSURLComponents componentsWithString:[@"http://localhost" stringByAppendingString:path]];NSString *site=@"";for(NSURLQueryItem *item in parts.queryItems)if([item.name isEqualToString:@"site"])site=item.value;result=[client remoteContext:site error:&error];}
 else {
  NSDictionary *value=[NSJSONSerialization JSONObjectWithData:[body dataUsingEncoding:NSUTF8StringEncoding] options:0 error:&error];
  if([path isEqualToString:@"/desktop/remote/call"]){NSString *json=@"";if(value[@"Body"]&&value[@"Body"]!=[NSNull null])json=[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:value[@"Body"] options:NSJSONWritingFragmentsAllowed error:&error] encoding:NSUTF8StringEncoding];result=[client remoteCall:value[@"Site"] method:value[@"Method"] path:value[@"Path"] body:json key:value[@"Idempotency"]?:@"" error:&error];}
  else if([path isEqualToString:@"/desktop/remote/sign"]){NSString *text=[value[@"Message"] stringByReplacingOccurrencesOfString:@"-" withString:@"+"];text=[text stringByReplacingOccurrencesOfString:@"_" withString:@"/"];while(text.length%4)text=[text stringByAppendingString:@"="];NSData *signature=[client signPossession:value[@"Site"] message:[[NSData alloc]initWithBase64EncodedString:text options:0] error:&error];if(signature)result=[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:@{@"signature":[[[signature base64EncodedStringWithOptions:0] stringByReplacingOccurrencesOfString:@"+" withString:@"-"] stringByReplacingOccurrencesOfString:@"/" withString:@"_"]} options:0 error:&error] encoding:NSUTF8StringEncoding];}
  else if([path isEqualToString:@"/desktop/disconnect"]){[client disconnect:&error];result=[client status];}
 }
 if(error||!result)return [NSJSONSerialization dataWithJSONObject:@{@"error":error.localizedDescription?:@"无效请求"} options:0 error:nil];
 return [result dataUsingEncoding:NSUTF8StringEncoding];
}
- (void)networkAvailable:(BOOL)available{[self.client networkChanged:available dns:@""];}
- (void)cancel{self.cancelled=YES;[self.client cancel];}
- (void)stop{NSError *error=nil;MobileClient *client=self.client;self.client=nil;[client cancel];[client close:&error];}
@end
