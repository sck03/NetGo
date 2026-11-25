#import <Foundation/Foundation.h>
NS_ASSUME_NONNULL_BEGIN
typedef void (^NetGoApplyNetwork)(NSString *,NSString *,NSString *,NSString *);
@interface TunnelBridge : NSObject
@property(nonatomic,copy) NetGoApplyNetwork applyNetwork;
- (NSString *)startRoot:(NSString *)root profile:(NSString *)profile alias:(NSString *)alias password:(NSString *)password importProfile:(BOOL)importProfile importID:(NSString *)importID trusted:(BOOL)trusted replace:(BOOL)replace internet:(BOOL)internet tunnelFD:(int)fd interfaceName:(NSString *)name;
- (NSData *)message:(NSData *)data;
- (void)networkAvailable:(BOOL)available;
- (void)stop;
@end
NS_ASSUME_NONNULL_END
