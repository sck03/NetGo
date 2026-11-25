#import <AppKit/AppKit.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <VideoToolbox/VideoToolbox.h>
#import <ApplicationServices/ApplicationServices.h>
#include "HostMedia.h"
#include <nlohmann/json.hpp>
#include <atomic>
#include <mutex>
#include <algorithm>
#include <deque>
#include <set>
#include <vector>
#include <cstring>
struct HostMedia;
@interface NetGoScreenSink : NSObject <SCStreamOutput, SCStreamDelegate>
@property(nonatomic, assign) HostMedia *owner;
@end
struct HostMedia {
    SCStream *stream = nil;
    NetGoScreenSink *sink = nil;
    dispatch_queue_t queue = dispatch_queue_create("org.netgo.host.capture", DISPATCH_QUEUE_SERIAL);
    VTCompressionSessionRef encoder = nullptr;
    std::mutex mutex;
    std::deque<std::vector<uint8_t>> packets;
    size_t queued = 0;
    std::atomic_bool failed = false, active = false;
    uint32_t width = 0, height = 0, caps = 0;
    CGRect bounds{};
    std::set<CGKeyCode> keys;
    std::set<unsigned> buttons;
    CGPoint point{};
    CGEventFlags flags = 0;
    void release() {
        for (auto key : keys) {
            auto e = CGEventCreateKeyboardEvent(nullptr, key, false);
            CGEventSetFlags(e, 0);
            CGEventPost(kCGHIDEventTap, e);
            CFRelease(e);
        }
        keys.clear();
        for (auto b : buttons) {
            auto e = CGEventCreateMouseEvent(nullptr,
                                             b == 1   ? kCGEventLeftMouseUp
                                             : b == 2 ? kCGEventRightMouseUp
                                                      : kCGEventOtherMouseUp,
                                             point,
                                             b == 1   ? kCGMouseButtonLeft
                                             : b == 2 ? kCGMouseButtonRight
                                                      : kCGMouseButtonCenter);
            CGEventPost(kCGHIDEventTap, e);
            CFRelease(e);
        }
        buttons.clear();
        flags = 0;
    }
    void close() {
        active = false;
        if (stream) {
            dispatch_semaphore_t done = dispatch_semaphore_create(0);
            [stream stopCaptureWithCompletionHandler:^(NSError *) {
              dispatch_semaphore_signal(done);
            }];
            dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC));
            stream = nil;
        }
        dispatch_sync(queue, ^{
          sink.owner = nullptr;
        });
        if (encoder) {
            VTCompressionSessionCompleteFrames(encoder, kCMTimeInvalid);
            VTCompressionSessionInvalidate(encoder);
            CFRelease(encoder);
            encoder = nullptr;
        }
        sink = nil;
        release();
        std::lock_guard lock(mutex);
        packets.clear();
        queued = 0;
    }
    ~HostMedia() { close(); }
    static void encoded(void *ctx, void *, OSStatus status, VTEncodeInfoFlags, CMSampleBufferRef sample) {
        auto &h = *static_cast<HostMedia *>(ctx);
        if (!h.active)
            return;
        if (status || !sample || !CMSampleBufferDataIsReady(sample)) {
            h.failed = true;
            return;
        }
        try {
            std::vector<uint8_t> out;
            auto format = CMSampleBufferGetFormatDescription(sample);
            size_t count = 0;
            int prefix = 0;
            const uint8_t *parameter = nullptr;
            size_t length = 0;
            if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, 0, &parameter, &length, &count,
                                                                   &prefix) != noErr ||
                prefix != 4 || count > 8) {
                h.failed = true;
                return;
            }
            auto attachments = CMSampleBufferGetSampleAttachmentsArray(sample, false);
            bool key = !attachments ||
                       !CFDictionaryContainsKey((CFDictionaryRef)CFArrayGetValueAtIndex(attachments, 0),
                                                kCMSampleAttachmentKey_NotSync);
            if (key)
                for (size_t i = 0; i < count; ++i) {
                    if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, i, &parameter, &length,
                                                                           nullptr, nullptr) != noErr ||
                        length > 65536) {
                        h.failed = true;
                        return;
                    }
                    out.insert(out.end(), {0, 0, 0, 1});
                    out.insert(out.end(), parameter, parameter + length);
                }
            auto block = CMSampleBufferGetDataBuffer(sample);
            auto bytes = CMBlockBufferGetDataLength(block);
            if (bytes > 4 * 1024 * 1024) {
                h.failed = true;
                return;
            }
            std::vector<uint8_t> raw(bytes);
            if (CMBlockBufferCopyDataBytes(block, 0, bytes, raw.data()) != noErr) {
                h.failed = true;
                return;
            }
            for (size_t pos = 0; pos < bytes;) {
                if (bytes - pos < 4) {
                    h.failed = true;
                    return;
                }
                uint32_t n = (uint32_t(raw[pos]) << 24) | (uint32_t(raw[pos + 1]) << 16) |
                             (uint32_t(raw[pos + 2]) << 8) | raw[pos + 3];
                pos += 4;
                if (!n || n > bytes - pos) {
                    h.failed = true;
                    return;
                }
                out.insert(out.end(), {0, 0, 0, 1});
                out.insert(out.end(), raw.begin() + pos, raw.begin() + pos + n);
                pos += n;
            }
            std::lock_guard lock(h.mutex);
            if (h.packets.size() >= 8 || h.queued + out.size() > 4 * 1024 * 1024) {
                h.failed = true;
                return;
            }
            h.queued += out.size();
            h.packets.push_back(std::move(out));
        } catch (...) {
            h.failed = true;
        }
    }
    void frame(CMSampleBufferRef sample) {
        if (!active || failed)
            return;
        {
            std::lock_guard lock(mutex);
            if (packets.size() >= 3 || queued > 2 * 1024 * 1024)
                return;
        }
        auto image = CMSampleBufferGetImageBuffer(sample);
        if (!image)
            return;
        auto status =
            VTCompressionSessionEncodeFrame(encoder, image, CMSampleBufferGetPresentationTimeStamp(sample),
                                            CMTimeMake(1, 20), nullptr, nullptr, nullptr);
        if (status)
            failed = true;
    }
};
@implementation NetGoScreenSink
- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
    (void)stream;
    (void)error;
    if (self.owner)
        self.owner->failed = true;
}
- (void)stream:(SCStream *)stream
    didOutputSampleBuffer:(CMSampleBufferRef)sample
                   ofType:(SCStreamOutputType)type {
    (void)stream;
    if (type == SCStreamOutputTypeScreen && self.owner)
        self.owner->frame(sample);
}
@end
namespace {
CGKeyCode key(unsigned v) {
    if (v >= 65 && v <= 90) {
        const CGKeyCode letters[] = {0,  11, 8,  2,  14, 3, 5,  4,  34, 38, 40, 37, 46,
                                     45, 31, 35, 12, 15, 1, 17, 32, 9,  13, 7,  16, 6};
        return letters[v - 65];
    }
    if (v >= 48 && v <= 57) {
        const CGKeyCode digits[] = {29, 18, 19, 20, 21, 23, 22, 26, 28, 25};
        return digits[v - 48];
    }
    switch (v) {
    case 8:
        return 51;
    case 9:
        return 48;
    case 13:
        return 36;
    case 16:
    case 160:
        return 56;
    case 161:
        return 60;
    case 17:
    case 162:
        return 59;
    case 163:
        return 62;
    case 18:
    case 164:
        return 58;
    case 165:
        return 61;
    case 27:
        return 53;
    case 32:
        return 49;
    case 33:
        return 116;
    case 34:
        return 121;
    case 35:
        return 119;
    case 36:
        return 115;
    case 37:
        return 123;
    case 38:
        return 126;
    case 39:
        return 124;
    case 40:
        return 125;
    case 46:
        return 117;
    case 91:
        return 55;
    case 92:
        return 54;
    case 186:
        return 41;
    case 187:
        return 24;
    case 188:
        return 43;
    case 189:
        return 27;
    case 190:
        return 47;
    case 191:
        return 44;
    case 192:
        return 50;
    case 219:
        return 33;
    case 220:
        return 42;
    case 221:
        return 30;
    case 222:
        return 39;
    default:
        if (v >= 112 && v <= 123) {
            const CGKeyCode f[] = {122, 120, 99, 118, 96, 97, 98, 100, 101, 109, 103, 111};
            return f[v - 112];
        }
        return 0xffff;
    }
}
} // namespace
extern "C" void *ng_apple_host_media_create() {
    try {
        return new HostMedia;
    } catch (...) {
        return nullptr;
    }
}
extern "C" void ng_apple_host_media_destroy(void *p) { delete static_cast<HostMedia *>(p); }
extern "C" int32_t ng_apple_host_permissions() {
    bool screen = CGPreflightScreenCaptureAccess();
    if (!screen)
        screen = CGRequestScreenCaptureAccess();
    NSDictionary *options = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt : @YES};
    return screen && AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options) ? 0 : -1;
}
extern "C" int32_t ng_apple_host_media_open(void *p, uint32_t caps, uint32_t *w, uint32_t *height,
                                            uint32_t *fps) {
    @autoreleasepool {
        auto &h = *static_cast<HostMedia *>(p);
        h.close();
        if (!CGPreflightScreenCaptureAccess() || ((caps & 2) && !AXIsProcessTrusted()))
            return -1;
        h.failed = false;
        h.caps = caps;
        __block SCShareableContent *content = nil;
        dispatch_semaphore_t ready = dispatch_semaphore_create(0);
        [SCShareableContent
            getShareableContentExcludingDesktopWindows:NO
                                   onScreenWindowsOnly:YES
                                     completionHandler:^(SCShareableContent *value, NSError *) {
                                       content = value;
                                       dispatch_semaphore_signal(ready);
                                     }];
        if (dispatch_semaphore_wait(ready, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC)) ||
            !content.displays.count)
            return -1;
        SCDisplay *display = content.displays.firstObject;
        for (SCDisplay *d in content.displays)
            if (d.displayID == CGMainDisplayID()) {
                display = d;
                break;
            }
        h.bounds = CGDisplayBounds(display.displayID);
        double scale = std::min(1.0, std::min(1280.0 / display.width, 720.0 / display.height));
        h.width = uint32_t(display.width * scale) & ~1u;
        h.height = uint32_t(display.height * scale) & ~1u;
        NSDictionary *spec = @{
            (__bridge NSString *)kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder : @YES
        };
        auto result = VTCompressionSessionCreate(nullptr, int32_t(h.width), int32_t(h.height),
                                                 kCMVideoCodecType_H264, (__bridge CFDictionaryRef)spec,
                                                 nullptr, nullptr, HostMedia::encoded, &h, &h.encoder);
        if (result != noErr)
            return -1;
        VTSessionSetProperty(h.encoder, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
        VTSessionSetProperty(h.encoder, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
        VTSessionSetProperty(h.encoder, kVTCompressionPropertyKey_ProfileLevel,
                             kVTProfileLevel_H264_Baseline_AutoLevel);
        VTSessionSetProperty(h.encoder, kVTCompressionPropertyKey_AverageBitRate,
                             (__bridge CFNumberRef) @4000000);
        VTSessionSetProperty(h.encoder, kVTCompressionPropertyKey_MaxKeyFrameInterval,
                             (__bridge CFNumberRef) @20);
        VTSessionSetProperty(h.encoder, kVTCompressionPropertyKey_ExpectedFrameRate,
                             (__bridge CFNumberRef) @20);
        if (VTCompressionSessionPrepareToEncodeFrames(h.encoder) != noErr) {
            h.close();
            return -1;
        }
        SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
        SCStreamConfiguration *config = [SCStreamConfiguration new];
        config.width = h.width;
        config.height = h.height;
        config.minimumFrameInterval = CMTimeMake(1, 20);
        config.queueDepth = 3;
        config.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
        config.showsCursor = YES;
        h.sink = [NetGoScreenSink new];
        h.sink.owner = &h;
        h.stream = [[SCStream alloc] initWithFilter:filter configuration:config delegate:h.sink];
        NSError *error = nil;
        if (![h.stream addStreamOutput:h.sink
                                  type:SCStreamOutputTypeScreen
                    sampleHandlerQueue:h.queue
                                 error:&error]) {
            h.close();
            return -1;
        }
        h.active = true;
        dispatch_semaphore_t started = dispatch_semaphore_create(0);
        __block BOOL ok = NO;
        [h.stream startCaptureWithCompletionHandler:^(NSError *e) {
          ok = e == nil;
          dispatch_semaphore_signal(started);
        }];
        if (dispatch_semaphore_wait(started, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC)) || !ok) {
            h.close();
            return -1;
        }
        *w = h.width;
        *height = h.height;
        *fps = 20;
        return 0;
    }
}
extern "C" int32_t ng_apple_host_media_poll(void *p, uint32_t *kind, uint8_t *out, size_t capacity,
                                            size_t *length) {
    auto &h = *static_cast<HostMedia *>(p);
    *length = 0;
    *kind = 5;
    if (h.failed)
        return -1;
    std::lock_guard lock(h.mutex);
    if (h.packets.empty())
        return 0;
    auto &packet = h.packets.front();
    if (packet.size() > capacity)
        return -1;
    std::memcpy(out, packet.data(), packet.size());
    *length = packet.size();
    h.queued -= packet.size();
    h.packets.pop_front();
    return 0;
}
extern "C" int32_t ng_apple_host_media_input(void *p, const char *text, size_t size) {
    try {
        auto &h = *static_cast<HostMedia *>(p);
        auto e = nlohmann::json::parse(text, text + size);
        auto type = e.at("type").get<std::string>();
        if (type == "release") {
            h.release();
            return 0;
        }
        if (type == "key") {
            auto code = key(e.at("code"));
            if (code == 0xffff)
                return -1;
            bool down = e.at("down");
            if (down)
                h.keys.insert(code);
            else
                h.keys.erase(code);
            h.flags = 0;
            for (auto held : h.keys) {
                if (held == 56 || held == 60)
                    h.flags |= kCGEventFlagMaskShift;
                if (held == 59 || held == 62)
                    h.flags |= kCGEventFlagMaskControl;
                if (held == 58 || held == 61)
                    h.flags |= kCGEventFlagMaskAlternate;
                if (held == 54 || held == 55)
                    h.flags |= kCGEventFlagMaskCommand;
            }
            auto event = CGEventCreateKeyboardEvent(nullptr, code, down);
            CGEventSetFlags(event, h.flags);
            CGEventPost(kCGHIDEventTap, event);
            CFRelease(event);
            return 0;
        }
        if (type == "text") {
            auto value = e.at("text").get<std::string>();
            NSString *s = [[NSString alloc] initWithBytes:value.data()
                                                   length:value.size()
                                                 encoding:NSUTF8StringEncoding];
            if (!s)
                return -1;
            std::vector<UniChar> unicode(s.length);
            [s getCharacters:unicode.data() range:NSMakeRange(0, s.length)];
            for (bool down : {true, false}) {
                auto event = CGEventCreateKeyboardEvent(nullptr, 0, down);
                CGEventKeyboardSetUnicodeString(event, unicode.size(), unicode.data());
                CGEventPost(kCGHIDEventTap, event);
                CFRelease(event);
            }
            return 0;
        }
        if (type == "move") {
            h.point =
                CGPointMake(h.bounds.origin.x + e.at("x").get<double>() * (h.bounds.size.width - 1) / 65535,
                            h.bounds.origin.y + e.at("y").get<double>() * (h.bounds.size.height - 1) / 65535);
            auto event = CGEventCreateMouseEvent(nullptr,
                                                 h.buttons.count(1)   ? kCGEventLeftMouseDragged
                                                 : h.buttons.count(2) ? kCGEventRightMouseDragged
                                                                      : kCGEventMouseMoved,
                                                 h.point, kCGMouseButtonLeft);
            CGEventPost(kCGHIDEventTap, event);
            CFRelease(event);
            return 0;
        }
        if (type == "button") {
            unsigned b = e.at("button");
            bool down = e.at("down");
            if (down)
                h.buttons.insert(b);
            else
                h.buttons.erase(b);
            auto event =
                CGEventCreateMouseEvent(nullptr,
                                        b == 1   ? (down ? kCGEventLeftMouseDown : kCGEventLeftMouseUp)
                                        : b == 2 ? (down ? kCGEventRightMouseDown : kCGEventRightMouseUp)
                                                 : (down ? kCGEventOtherMouseDown : kCGEventOtherMouseUp),
                                        h.point,
                                        b == 1   ? kCGMouseButtonLeft
                                        : b == 2 ? kCGMouseButtonRight
                                                 : kCGMouseButtonCenter);
            CGEventPost(kCGHIDEventTap, event);
            CFRelease(event);
            return 0;
        }
        if (type == "wheel") {
            int delta = e.at("delta");
            bool horizontal = e.value("horizontal", false);
            auto event =
                CGEventCreateScrollWheelEvent(nullptr, kCGScrollEventUnitLine, 2,
                                              horizontal ? 0 : delta / 120, horizontal ? delta / 120 : 0);
            CGEventPost(kCGHIDEventTap, event);
            CFRelease(event);
            return 0;
        }
        return -1;
    } catch (...) {
        return -1;
    }
}
extern "C" void ng_apple_host_media_close(void *p) {
    if (p)
        static_cast<HostMedia *>(p)->close();
}
extern "C" int32_t ng_apple_host_clipboard(void *, int32_t set, const char *text, size_t size, char *output,
                                           size_t capacity, size_t *length) {
    @autoreleasepool {
        __block int result = -1;
        auto work = ^{
          NSPasteboard *board = NSPasteboard.generalPasteboard;
          if (set) {
              NSString *value = [[NSString alloc] initWithBytes:text
                                                         length:size
                                                       encoding:NSUTF8StringEncoding];
              if (value) {
                  [board clearContents];
                  result = [board setString:value forType:NSPasteboardTypeString] ? 0 : -1;
                  *length = 0;
              }
          } else {
              NSData *value =
                  [[board stringForType:NSPasteboardTypeString] dataUsingEncoding:NSUTF8StringEncoding];
              if (value.length <= capacity) {
                  if (value.length)
                      std::memcpy(output, value.bytes, value.length);
                  *length = value.length;
                  result = 0;
              }
          }
        };
        if (NSThread.isMainThread)
            work();
        else
            dispatch_sync(dispatch_get_main_queue(), work);
        return result;
    }
}
