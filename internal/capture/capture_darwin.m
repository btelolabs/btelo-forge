#import <Accelerate/Accelerate.h>
#import <AppKit/AppKit.h>
#import <AudioToolbox/AudioToolbox.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreAudio/CoreAudio.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <Foundation/Foundation.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#include <dispatch/dispatch.h>
#include <mach/mach_time.h>
#include <math.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "capture_darwin.h"
#include "_cgo_export.h"

static void set_err(char *buf, int len, const char *msg) {
	if (!buf || len <= 0) return;
	if (!msg) msg = "unknown error";
	strlcpy(buf, msg, (size_t)len);
}

static void set_nserr(char *buf, int len, NSError *err) {
	if (!err) {
		set_err(buf, len, "unknown error");
		return;
	}
	set_err(buf, len, err.localizedDescription.UTF8String);
}

// ---- Clocks ----

static mach_timebase_info_data_t g_timebase;
static dispatch_once_t g_timebase_once;

static inline int64_t host_to_100ns(uint64_t t) {
	dispatch_once(&g_timebase_once, ^{
		mach_timebase_info(&g_timebase);
	});
	__uint128_t ns = ((__uint128_t)t * (__uint128_t)g_timebase.numer) / (__uint128_t)g_timebase.denom;
	return (int64_t)(ns / 100);
}

int64_t cap_host_100ns(void) {
	return host_to_100ns(mach_absolute_time());
}

static inline int64_t cmtime_to_100ns(CMTime t) {
	if (!CMTIME_IS_NUMERIC(t) || t.timescale <= 0) {
		return cap_host_100ns();
	}
	__int128_t v = ((__int128_t)t.value * 10000000) / (__int128_t)t.timescale;
	return (int64_t)v;
}

// ---- System & Permissions ----

int cap_macos_version_ok(int *major, int *minor) {
	@autoreleasepool {
		NSOperatingSystemVersion v = NSProcessInfo.processInfo.operatingSystemVersion;
		if (major) *major = (int)v.majorVersion;
		if (minor) *minor = (int)v.minorVersion;
		return (v.majorVersion > 12 || (v.majorVersion == 12 && v.minorVersion >= 3)) ? 1 : 0;
	}
}

int cap_is_app_bundle(void) {
	@autoreleasepool {
		return ([[NSBundle mainBundle] bundleIdentifier] != nil) ? 1 : 0;
	}
}

int cap_has_screen_permission(void) {
	return CGPreflightScreenCaptureAccess() ? 1 : 0;
}

int cap_request_screen_permission(void) {
	return CGRequestScreenCaptureAccess() ? 1 : 0;
}

int cap_camera_auth_status(int *has_usage_desc) {
	@autoreleasepool {
		if (has_usage_desc) {
			id v = [[NSBundle mainBundle] objectForInfoDictionaryKey:@"NSCameraUsageDescription"];
			*has_usage_desc = (v != nil) ? 1 : 0;
		}
		return (int)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];
	}
}

int cap_request_camera_permission(void) {
	@autoreleasepool {
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		__block BOOL granted = NO;
		[AVCaptureDevice requestAccessForMediaType:AVMediaTypeVideo completionHandler:^(BOOL ok) {
			granted = ok;
			dispatch_semaphore_signal(sem);
		}];
		dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_SEC));
		return granted ? 1 : 0;
	}
}

int cap_mic_auth_status(int *has_usage_desc) {
	@autoreleasepool {
		if (has_usage_desc) {
			id v = [[NSBundle mainBundle] objectForInfoDictionaryKey:@"NSMicrophoneUsageDescription"];
			*has_usage_desc = (v != nil) ? 1 : 0;
		}
		return (int)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
	}
}

int cap_request_mic_permission(void) {
	@autoreleasepool {
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		__block BOOL granted = NO;
		[AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio completionHandler:^(BOOL ok) {
			granted = ok;
			dispatch_semaphore_signal(sem);
		}];
		dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_SEC));
		return granted ? 1 : 0;
	}
}

// ---- Display Enumeration ----

static double display_scale(CGDirectDisplayID id, double gw) {
	CGDisplayModeRef mode = CGDisplayCopyDisplayMode(id);
	if (mode) {
		size_t pw = CGDisplayModeGetPixelWidth(mode);
		CGDisplayModeRelease(mode);
		if (pw > 0 && gw > 0) {
			return (double)pw / gw;
		}
	}
	return 1.0;
}

static void display_friendly_name(CGDirectDisplayID id, char *out, size_t out_len) {
	out[0] = '\0';
	// Avoid touching NSScreen from background threads when AppKit has not been initialized;
	// on the main thread (or when NSApp is already running), NSScreen.localizedName gives the exact label.
	if ([NSThread isMainThread]) {
		for (NSScreen *s in [NSScreen screens]) {
			NSNumber *num = s.deviceDescription[@"NSScreenNumber"];
			if (num && num.unsignedIntValue == id && s.localizedName.length > 0) {
				strlcpy(out, s.localizedName.UTF8String, out_len);
				return;
			}
		}
	}
	if (CGDisplayIsBuiltin(id)) {
		strlcpy(out, "Built-in Display", out_len);
	}
}

int cap_list_displays(cap_display_info *out, int max_count) {
	@autoreleasepool {
		if (!out || max_count <= 0) return 0;
		CGDirectDisplayID ids[32];
		uint32_t limit = max_count < 32 ? (uint32_t)max_count : 32;
		uint32_t count = 0;
		if (CGGetActiveDisplayList(limit, ids, &count) != kCGErrorSuccess || count == 0) {
			return 0;
		}
		CGDirectDisplayID mainID = CGMainDisplayID();
		CGRect mainBounds = CGDisplayBounds(mainID);
		double s0 = display_scale(mainID, mainBounds.size.width);
		if (s0 <= 0) s0 = 1.0;

		int written = 0;
		for (uint32_t i = 0; i < count; i++) {
			CGDirectDisplayID did = ids[i];
			CGRect b = CGDisplayBounds(did);
			if (b.size.width <= 0 || b.size.height <= 0) continue;
			double sd = display_scale(did, b.size.width);
			if (sd <= 0) sd = 1.0;
			int pw = (int)llround(b.size.width * sd);
			int ph = (int)llround(b.size.height * sd);
			// Desktop pixel coordinate contract shared with internal/mac:
			// primary sits at (0,0), non-negative offsets scale by s0, negative offsets anchor by right/bottom edge.
			int dx = (b.origin.x >= 0) ? (int)llround(b.origin.x * s0) : (int)llround((b.origin.x + b.size.width) * s0) - pw;
			int dy = (b.origin.y >= 0) ? (int)llround(b.origin.y * s0) : (int)llround((b.origin.y + b.size.height) * s0) - ph;

			cap_display_info *info = &out[written++];
			memset(info, 0, sizeof(*info));
			info->display_id = did;
			info->x = dx;
			info->y = dy;
			info->width = pw;
			info->height = ph;
			info->bounds_x = b.origin.x;
			info->bounds_y = b.origin.y;
			info->bounds_w = b.size.width;
			info->bounds_h = b.size.height;
			info->scale = sd;
			info->primary = (did == mainID) ? 1 : 0;
			display_friendly_name(did, info->name, sizeof(info->name));
		}
		return written;
	}
}

// ---- CoreAudio Microphone Enumeration ----

static int copy_cfstring(CFStringRef s, char *out, int out_len) {
	if (!s || !out || out_len <= 0) return 0;
	if (CFStringGetCString(s, out, out_len, kCFStringEncodingUTF8)) {
		return 1;
	}
	out[0] = '\0';
	return 0;
}

int cap_default_mic_uid(char *out, int out_len) {
	if (!out || out_len <= 0) return 0;
	out[0] = '\0';
	AudioDeviceID defIn = kAudioObjectUnknown;
	UInt32 size = sizeof(defIn);
	AudioObjectPropertyAddress defAddr = {
		kAudioHardwarePropertyDefaultInputDevice,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain
	};
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &defAddr, 0, NULL, &size, &defIn) != noErr || defIn == kAudioObjectUnknown) {
		return 0;
	}
	CFStringRef uid = NULL;
	size = sizeof(uid);
	AudioObjectPropertyAddress uidAddr = {
		kAudioDevicePropertyDeviceUID,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain
	};
	if (AudioObjectGetPropertyData(defIn, &uidAddr, 0, NULL, &size, &uid) != noErr || !uid) {
		return 0;
	}
	int ok = copy_cfstring(uid, out, out_len);
	CFRelease(uid);
	return ok;
}

int cap_list_mics(cap_audio_device_info *out, int max_count) {
	if (!out || max_count <= 0) return 0;
	AudioObjectPropertyAddress addr = {
		kAudioHardwarePropertyDevices,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain
	};
	UInt32 size = 0;
	if (AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &addr, 0, NULL, &size) != noErr || size == 0) {
		return 0;
	}
	int total = (int)(size / sizeof(AudioDeviceID));
	AudioDeviceID *ids = (AudioDeviceID *)malloc(size);
	if (!ids) return 0;
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &addr, 0, NULL, &size, ids) != noErr) {
		free(ids);
		return 0;
	}
	AudioDeviceID defIn = kAudioObjectUnknown;
	UInt32 ds = sizeof(defIn);
	AudioObjectPropertyAddress defAddr = {
		kAudioHardwarePropertyDefaultInputDevice,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain
	};
	AudioObjectGetPropertyData(kAudioObjectSystemObject, &defAddr, 0, NULL, &ds, &defIn);

	int written = 0;
	for (int i = 0; i < total && written < max_count; i++) {
		AudioObjectPropertyAddress inAddr = {
			kAudioDevicePropertyStreamConfiguration,
			kAudioDevicePropertyScopeInput,
			kAudioObjectPropertyElementMain
		};
		UInt32 blSize = 0;
		if (AudioObjectGetPropertyDataSize(ids[i], &inAddr, 0, NULL, &blSize) != noErr || blSize == 0) {
			continue;
		}
		AudioBufferList *bl = (AudioBufferList *)malloc(blSize);
		if (!bl) continue;
		UInt32 ch = 0;
		if (AudioObjectGetPropertyData(ids[i], &inAddr, 0, NULL, &blSize, bl) == noErr) {
			for (UInt32 b = 0; b < bl->mNumberBuffers; b++) {
				ch += bl->mBuffers[b].mNumberChannels;
			}
		}
		free(bl);
		if (ch == 0) continue;

		CFStringRef uid = NULL, name = NULL;
		UInt32 ps = sizeof(CFStringRef);
		AudioObjectPropertyAddress uAddr = { kAudioDevicePropertyDeviceUID, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain };
		AudioObjectPropertyAddress nAddr = { kAudioObjectPropertyName, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain };
		AudioObjectGetPropertyData(ids[i], &uAddr, 0, NULL, &ps, &uid);
		ps = sizeof(CFStringRef);
		AudioObjectGetPropertyData(ids[i], &nAddr, 0, NULL, &ps, &name);

		cap_audio_device_info *info = &out[written];
		memset(info, 0, sizeof(*info));
		int hasUID = copy_cfstring(uid, info->id, sizeof(info->id));
		copy_cfstring(name, info->name, sizeof(info->name));
		if (uid) CFRelease(uid);
		if (name) CFRelease(name);
		if (!hasUID || info->id[0] == '\0') continue;
		if (info->name[0] == '\0') {
			strlcpy(info->name, info->id, sizeof(info->name));
		}
		info->is_default = (ids[i] == defIn) ? 1 : 0;
		written++;
	}
	free(ids);
	return written;
}

// ---- AVFoundation Camera Enumeration ----

static NSArray<AVCaptureDevice *> *discover_cameras(void) {
	NSMutableArray<AVCaptureDeviceType> *types = [NSMutableArray arrayWithObject:AVCaptureDeviceTypeBuiltInWideAngleCamera];
	if (@available(macOS 14.0, *)) {
		[types addObject:AVCaptureDeviceTypeExternal];
		[types addObject:AVCaptureDeviceTypeContinuityCamera];
		[types addObject:AVCaptureDeviceTypeDeskViewCamera];
	} else {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
		[types addObject:AVCaptureDeviceTypeExternalUnknown];
#pragma clang diagnostic pop
	}
	AVCaptureDeviceDiscoverySession *sess = [AVCaptureDeviceDiscoverySession
		discoverySessionWithDeviceTypes:types
		mediaType:AVMediaTypeVideo
		position:AVCaptureDevicePositionUnspecified];
	return sess.devices ?: @[];
}

int cap_list_cameras(cap_camera_info *out, int max_count) {
	@autoreleasepool {
		if (!out || max_count <= 0) return 0;
		NSArray<AVCaptureDevice *> *devices = discover_cameras();
		int written = 0;
		for (AVCaptureDevice *d in devices) {
			if (written >= max_count) break;
			cap_camera_info *info = &out[written++];
			memset(info, 0, sizeof(*info));
			if (d.uniqueID) strlcpy(info->id, d.uniqueID.UTF8String, sizeof(info->id));
			if (d.localizedName) strlcpy(info->name, d.localizedName.UTF8String, sizeof(info->name));
		}
		return written;
	}
}

// ---- ScreenCaptureKit Helper ----

static int is_sck_permission_error(NSError *err) API_AVAILABLE(macos(12.3)) {
	if (!err) return 0;
	if ([err.domain isEqualToString:SCStreamErrorDomain] && err.code == SCStreamErrorUserDeclined) {
		return 1;
	}
	return 0;
}

static int classify_sck_error(const char *op, NSError *err, int *err_code, char *err_buf, int err_len) API_AVAILABLE(macos(12.3)) {
	NSString *bundleID = [[NSBundle mainBundle] bundleIdentifier] ?: @"(none)";
	NSLog(@"btelo-forge capture: %s failed: domain=%@ code=%ld preflight=%d bundle=%@ desc=%@",
		op, err.domain ?: @"(nil)", (long)err.code, (int)CGPreflightScreenCaptureAccess(), bundleID, err.localizedDescription ?: @"(none)");
	if (is_sck_permission_error(err)) {
		if (cap_is_app_bundle() && !CGPreflightScreenCaptureAccess()) {
			CGRequestScreenCaptureAccess();
		}
		if (err_code) *err_code = CAP_ERR_PERMISSION;
		set_err(err_buf, err_len, "screen recording permission denied");
		return CAP_ERR_PERMISSION;
	}
	if (err_code) *err_code = CAP_ERR_GENERIC;
	if (err) {
		snprintf(err_buf, (size_t)err_len, "%s (%s %ld): %s", op,
			err.domain ? err.domain.UTF8String : "error", (long)err.code,
			err.localizedDescription ? err.localizedDescription.UTF8String : "unknown error");
	} else {
		set_err(err_buf, err_len, "unknown ScreenCaptureKit error");
	}
	return CAP_ERR_GENERIC;
}

static SCShareableContent *get_shareable_content(int *err_code, char *err_buf, int err_len) API_AVAILABLE(macos(12.3)) {
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);
	__block SCShareableContent *result = nil;
	__block NSError *outErr = nil;
	[SCShareableContent getShareableContentExcludingDesktopWindows:NO
		onScreenWindowsOnly:YES
		completionHandler:^(SCShareableContent *content, NSError *error) {
			result = content;
			outErr = error;
			dispatch_semaphore_signal(sem);
		}];
	if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 15 * NSEC_PER_SEC)) != 0) {
		if (err_code) *err_code = CAP_ERR_GENERIC;
		set_err(err_buf, err_len, "timed out waiting for ScreenCaptureKit");
		return nil;
	}
	if (!result) {
		classify_sck_error("SCShareableContent", outErr, err_code, err_buf, err_len);
	}
	return result;
}

static SCDisplay *find_sc_display(SCShareableContent *content, uint32_t display_id) API_AVAILABLE(macos(12.3)) {
	if (display_id == 0) {
		display_id = CGMainDisplayID();
	}
	for (SCDisplay *d in content.displays) {
		if (d.displayID == display_id) {
			return d;
		}
	}
	return content.displays.firstObject;
}

// ---- Native Screenshot (SCScreenshotManager, macOS 14+) ----

int cap_grab_screenshot(uint32_t display_id, double src_x, double src_y, double src_w, double src_h,
	int out_w, int out_h, int cursor, int flush, uint8_t *rgba_out, char *err_buf, int err_len) {
	@autoreleasepool {
		if (!cap_is_app_bundle() && !CGPreflightScreenCaptureAccess()) {
			set_err(err_buf, err_len, "screen recording permission denied");
			return CAP_ERR_PERMISSION;
		}
		if (out_w <= 0 || out_h <= 0 || !rgba_out) {
			set_err(err_buf, err_len, "empty screenshot region");
			return CAP_ERR_GENERIC;
		}
		if (flush) {
			// Give WindowServer one vsync to apply NSWindowSharingNone changes.
			usleep(20000);
		}
		if (@available(macOS 14.0, *)) {
			int scErr = CAP_OK;
			SCShareableContent *content = get_shareable_content(&scErr, err_buf, err_len);
			if (!content) return scErr != CAP_OK ? scErr : CAP_ERR_GENERIC;
			SCDisplay *disp = find_sc_display(content, display_id);
			if (!disp) {
				set_err(err_buf, err_len, "display not found");
				return CAP_ERR_NO_DEVICE;
			}
			SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:disp excludingWindows:@[]];
			SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
			if (src_w > 0 && src_h > 0 && (src_x > 0 || src_y > 0 || src_w < (double)disp.width || src_h < (double)disp.height)) {
				cfg.sourceRect = CGRectMake(src_x, src_y, src_w, src_h);
			}
			cfg.width = (size_t)out_w;
			cfg.height = (size_t)out_h;
			cfg.showsCursor = cursor ? YES : NO;
			cfg.pixelFormat = kCVPixelFormatType_32BGRA;
			cfg.scalesToFit = YES;

			dispatch_semaphore_t sem = dispatch_semaphore_create(0);
			__block CGImageRef captured = NULL;
			__block NSError *capErr = nil;
			[SCScreenshotManager captureImageWithFilter:filter
				configuration:cfg
				completionHandler:^(CGImageRef image, NSError *error) {
					if (image) captured = CGImageRetain(image);
					capErr = error;
					dispatch_semaphore_signal(sem);
				}];
			if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC)) != 0) {
				set_err(err_buf, err_len, "screenshot timed out");
				return CAP_ERR_GENERIC;
			}
			if (!captured) {
				int code = CAP_ERR_GENERIC;
				return classify_sck_error("SCScreenshotManager", capErr, &code, err_buf, err_len);
			}
			CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
			CGContextRef ctx = CGBitmapContextCreate(rgba_out, (size_t)out_w, (size_t)out_h, 8, (size_t)out_w * 4,
				cs, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
			CGColorSpaceRelease(cs);
			if (!ctx) {
				CGImageRelease(captured);
				set_err(err_buf, err_len, "CGBitmapContextCreate failed");
				return CAP_ERR_GENERIC;
			}
			// Fill opaque black first so every pixel ends up with alpha=255.
			CGContextSetRGBFillColor(ctx, 0, 0, 0, 1);
			CGContextFillRect(ctx, CGRectMake(0, 0, out_w, out_h));
			CGContextSetInterpolationQuality(ctx, kCGInterpolationHigh);
			CGContextDrawImage(ctx, CGRectMake(0, 0, out_w, out_h), captured);
			CGContextRelease(ctx);
			CGImageRelease(captured);
			return CAP_OK;
		}
		set_err(err_buf, err_len, "SCScreenshotManager requires macOS 14 or later");
		return CAP_ERR_UNSUPPORTED;
	}
}

// ---- ScreenCaptureKit Video Stream ----

API_AVAILABLE(macos(12.3))
@interface ForgeScreenStream : NSObject <SCStreamOutput, SCStreamDelegate> {
@public
	_Atomic int _stopping;
}
@property(nonatomic, strong) SCStream *stream;
@property(nonatomic, strong) dispatch_queue_t queue;
@property(nonatomic) uintptr_t handle;
@end

@implementation ForgeScreenStream

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer ofType:(SCStreamOutputType)type {
	if (atomic_load(&_stopping) || type != SCStreamOutputTypeScreen) return;
	if (!CMSampleBufferIsValid(sampleBuffer)) return;

	CFArrayRef attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, false);
	if (attachments && CFArrayGetCount(attachments) > 0) {
		CFDictionaryRef dict = (CFDictionaryRef)CFArrayGetValueAtIndex(attachments, 0);
		if (dict) {
			CFNumberRef statusNum = (CFNumberRef)CFDictionaryGetValue(dict, (__bridge const void *)SCStreamFrameInfoStatus);
			if (statusNum) {
				NSInteger status = [(__bridge NSNumber *)statusNum integerValue];
				if (status != SCFrameStatusComplete) return;
			}
		}
	}

	CVPixelBufferRef pb = CMSampleBufferGetImageBuffer(sampleBuffer);
	if (!pb || !CVPixelBufferIsPlanar(pb) || CVPixelBufferGetPlaneCount(pb) < 2) return;
	if (CVPixelBufferLockBaseAddress(pb, kCVPixelBufferLock_ReadOnly) != kCVReturnSuccess) return;

	size_t w = CVPixelBufferGetWidth(pb);
	size_t h = CVPixelBufferGetHeight(pb);
	void *yPtr = CVPixelBufferGetBaseAddressOfPlane(pb, 0);
	size_t yStride = CVPixelBufferGetBytesPerRowOfPlane(pb, 0);
	void *uvPtr = CVPixelBufferGetBaseAddressOfPlane(pb, 1);
	size_t uvStride = CVPixelBufferGetBytesPerRowOfPlane(pb, 1);
	int64_t ts = cmtime_to_100ns(CMSampleBufferGetPresentationTimeStamp(sampleBuffer));

	if (yPtr && uvPtr && w > 0 && h > 0) {
		goScreenFrame((uintptr_t)_handle, yPtr, (int)yStride, uvPtr, (int)uvStride, (int)w, (int)h, ts);
	}
	CVPixelBufferUnlockBaseAddress(pb, kCVPixelBufferLock_ReadOnly);
}

- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
	if (atomic_load(&_stopping)) return;
	const char *msg = error ? error.localizedDescription.UTF8String : "screen capture stopped";
	goScreenStopped((uintptr_t)_handle, (char *)msg);
}

@end

void *cap_screen_start(uint32_t display_id, double src_x, double src_y, double src_w, double src_h,
	int out_w, int out_h, int fps, int cursor, uintptr_t handle, int *err_code, char *err_buf, int err_len) {
	@autoreleasepool {
		if (!cap_is_app_bundle() && !CGPreflightScreenCaptureAccess()) {
			if (err_code) *err_code = CAP_ERR_PERMISSION;
			set_err(err_buf, err_len, "screen recording permission denied");
			return NULL;
		}
		if (@available(macOS 12.3, *)) {
			int scErr = CAP_OK;
			SCShareableContent *content = get_shareable_content(&scErr, err_buf, err_len);
			if (!content) {
				if (err_code) *err_code = scErr != CAP_OK ? scErr : CAP_ERR_GENERIC;
				return NULL;
			}
			SCDisplay *disp = find_sc_display(content, display_id);
			if (!disp) {
				if (err_code) *err_code = CAP_ERR_NO_DEVICE;
				set_err(err_buf, err_len, "display not found");
				return NULL;
			}
			SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:disp excludingWindows:@[]];
			SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
			if (src_w > 0 && src_h > 0 && (src_x > 0 || src_y > 0 || src_w < (double)disp.width || src_h < (double)disp.height)) {
				cfg.sourceRect = CGRectMake(src_x, src_y, src_w, src_h);
			}
			cfg.width = (size_t)out_w;
			cfg.height = (size_t)out_h;
			cfg.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
			cfg.minimumFrameInterval = CMTimeMake(1, fps > 0 ? fps : 30);
			cfg.showsCursor = cursor ? YES : NO;
			cfg.queueDepth = 3;
			cfg.scalesToFit = YES;
			if (@available(macOS 13.0, *)) {
				cfg.capturesAudio = NO;
			}

			ForgeScreenStream *holder = [[ForgeScreenStream alloc] init];
			holder.handle = handle;
			atomic_store(&holder->_stopping, 0);
			holder.queue = dispatch_queue_create("com.btelo.forge.capture.screen", DISPATCH_QUEUE_SERIAL);
			holder.stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:holder];

			NSError *addErr = nil;
			if (![holder.stream addStreamOutput:holder type:SCStreamOutputTypeScreen sampleHandlerQueue:holder.queue error:&addErr]) {
				if (err_code) *err_code = CAP_ERR_GENERIC;
				set_nserr(err_buf, err_len, addErr);
				return NULL;
			}

			dispatch_semaphore_t sem = dispatch_semaphore_create(0);
			__block NSError *startErr = nil;
			[holder.stream startCaptureWithCompletionHandler:^(NSError *error) {
				startErr = error;
				dispatch_semaphore_signal(sem);
			}];
			if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC)) != 0) {
				if (err_code) *err_code = CAP_ERR_GENERIC;
				set_err(err_buf, err_len, "timed out starting screen capture");
				return NULL;
			}
			if (startErr) {
				classify_sck_error("SCStream.startCapture(screen)", startErr, err_code, err_buf, err_len);
				return NULL;
			}
			if (err_code) *err_code = CAP_OK;
			return (void *)CFBridgingRetain(holder);
		}
		if (err_code) *err_code = CAP_ERR_UNSUPPORTED;
		set_err(err_buf, err_len, "ScreenCaptureKit requires macOS 12.3 or later");
		return NULL;
	}
}

void cap_screen_stop(void *ctx) {
	if (!ctx) return;
	@autoreleasepool {
		if (@available(macOS 12.3, *)) {
			ForgeScreenStream *holder = (ForgeScreenStream *)CFBridgingRelease(ctx);
			atomic_store(&holder->_stopping, 1);
			if (holder.stream) {
				dispatch_semaphore_t sem = dispatch_semaphore_create(0);
				[holder.stream stopCaptureWithCompletionHandler:^(NSError *error) {
					(void)error;
					dispatch_semaphore_signal(sem);
				}];
				dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC));
				[holder.stream removeStreamOutput:holder type:SCStreamOutputTypeScreen error:nil];
				holder.stream = nil;
			}
			// Drain serial queue so no in-flight callback accesses Go handle after return.
			if (holder.queue) {
				dispatch_sync(holder.queue, ^{});
			}
		}
	}
}

// ---- System Audio via ScreenCaptureKit (macOS 13+) ----

API_AVAILABLE(macos(13.0))
@interface ForgeSysAudioStream : NSObject <SCStreamOutput, SCStreamDelegate> {
@public
	_Atomic int _stopping;
@private
	float *_interleaved;
	size_t _interleavedCap;
}
@property(nonatomic, strong) SCStream *stream;
@property(nonatomic, strong) dispatch_queue_t queue;
@property(nonatomic) uintptr_t handle;
@end

@implementation ForgeSysAudioStream

- (void)dealloc {
	free(_interleaved);
}

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer ofType:(SCStreamOutputType)type {
	if (atomic_load(&_stopping) || type != SCStreamOutputTypeAudio) return;
	if (!CMSampleBufferIsValid(sampleBuffer)) return;

	CMFormatDescriptionRef fmt = CMSampleBufferGetFormatDescription(sampleBuffer);
	const AudioStreamBasicDescription *asbd = fmt ? CMAudioFormatDescriptionGetStreamBasicDescription(fmt) : NULL;
	if (!asbd || !(asbd->mFormatFlags & kAudioFormatFlagIsFloat)) return;

	CMItemCount frames = CMSampleBufferGetNumSamples(sampleBuffer);
	if (frames <= 0) return;

	size_t listSize = 0;
	CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(sampleBuffer, &listSize, NULL, 0, NULL, NULL, 0, NULL);
	if (listSize == 0) return;

	AudioBufferList *abl = (AudioBufferList *)malloc(listSize);
	if (!abl) return;
	CMBlockBufferRef blockBuf = NULL;
	OSStatus st = CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(
		sampleBuffer, NULL, abl, listSize, NULL, NULL,
		kCMSampleBufferFlag_AudioBufferList_Assure16ByteAlignment, &blockBuf);
	if (st != noErr) {
		free(abl);
		return;
	}

	size_t need = (size_t)frames * 2;
	if (need > _interleavedCap) {
		float *grown = (float *)realloc(_interleaved, need * sizeof(float));
		if (!grown) {
			if (blockBuf) CFRelease(blockBuf);
			free(abl);
			return;
		}
		_interleaved = grown;
		_interleavedCap = need;
	}

	int ok = 0;
	if (abl->mNumberBuffers >= 2 && abl->mBuffers[0].mData && abl->mBuffers[1].mData) {
		// Non-interleaved stereo (ScreenCaptureKit default).
		const float *l = (const float *)abl->mBuffers[0].mData;
		const float *r = (const float *)abl->mBuffers[1].mData;
		size_t avail = abl->mBuffers[0].mDataByteSize / sizeof(float);
		if ((size_t)frames > avail) frames = (CMItemCount)avail;
		for (CMItemCount i = 0; i < frames; i++) {
			_interleaved[i * 2] = l[i];
			_interleaved[i * 2 + 1] = r[i];
		}
		ok = 1;
	} else if (abl->mNumberBuffers == 1 && abl->mBuffers[0].mData) {
		const float *src = (const float *)abl->mBuffers[0].mData;
		if (abl->mBuffers[0].mNumberChannels >= 2) {
			size_t avail = abl->mBuffers[0].mDataByteSize / (2 * sizeof(float));
			if ((size_t)frames > avail) frames = (CMItemCount)avail;
			memcpy(_interleaved, src, (size_t)frames * 2 * sizeof(float));
			ok = 1;
		} else if (abl->mBuffers[0].mNumberChannels == 1) {
			size_t avail = abl->mBuffers[0].mDataByteSize / sizeof(float);
			if ((size_t)frames > avail) frames = (CMItemCount)avail;
			for (CMItemCount i = 0; i < frames; i++) {
				_interleaved[i * 2] = src[i];
				_interleaved[i * 2 + 1] = src[i];
			}
			ok = 1;
		}
	}

	if (ok && frames > 0) {
		int64_t ts = cmtime_to_100ns(CMSampleBufferGetPresentationTimeStamp(sampleBuffer));
		goAudioSamples((uintptr_t)_handle, _interleaved, (int)frames, ts);
	}
	if (blockBuf) CFRelease(blockBuf);
	free(abl);
}

- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
	if (atomic_load(&_stopping)) return;
	const char *msg = error ? error.localizedDescription.UTF8String : "system audio stopped";
	goAudioError((uintptr_t)_handle, (char *)msg);
}

@end

void *cap_sysaudio_start(uintptr_t handle, int *err_code, char *err_buf, int err_len) {
	@autoreleasepool {
		if (!cap_is_app_bundle() && !CGPreflightScreenCaptureAccess()) {
			if (err_code) *err_code = CAP_ERR_PERMISSION;
			set_err(err_buf, err_len, "screen recording permission denied");
			return NULL;
		}
		if (@available(macOS 13.0, *)) {
			int scErr = CAP_OK;
			SCShareableContent *content = get_shareable_content(&scErr, err_buf, err_len);
			if (!content) {
				if (err_code) *err_code = scErr != CAP_OK ? scErr : CAP_ERR_GENERIC;
				return NULL;
			}
			SCDisplay *disp = find_sc_display(content, 0);
			if (!disp) {
				if (err_code) *err_code = CAP_ERR_NO_DEVICE;
				set_err(err_buf, err_len, "no display found for system audio");
				return NULL;
			}
			SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:disp excludingWindows:@[]];
			SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
			cfg.capturesAudio = YES;
			cfg.excludesCurrentProcessAudio = NO;
			cfg.sampleRate = 48000;
			cfg.channelCount = 2;
			// Small valid dummy video dimensions since we only attach an audio output.
			cfg.width = 128;
			cfg.height = 128;
			cfg.minimumFrameInterval = CMTimeMake(1, 1);

			ForgeSysAudioStream *holder = [[ForgeSysAudioStream alloc] init];
			holder.handle = handle;
			atomic_store(&holder->_stopping, 0);
			holder.queue = dispatch_queue_create("com.btelo.forge.capture.sysaudio", DISPATCH_QUEUE_SERIAL);
			holder.stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:holder];

			NSError *addErr = nil;
			if (![holder.stream addStreamOutput:holder type:SCStreamOutputTypeAudio sampleHandlerQueue:holder.queue error:&addErr]) {
				if (err_code) *err_code = CAP_ERR_GENERIC;
				set_nserr(err_buf, err_len, addErr);
				return NULL;
			}

			dispatch_semaphore_t sem = dispatch_semaphore_create(0);
			__block NSError *startErr = nil;
			[holder.stream startCaptureWithCompletionHandler:^(NSError *error) {
				startErr = error;
				dispatch_semaphore_signal(sem);
			}];
			if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC)) != 0) {
				if (err_code) *err_code = CAP_ERR_GENERIC;
				set_err(err_buf, err_len, "timed out starting system audio capture");
				return NULL;
			}
			if (startErr) {
				classify_sck_error("SCStream.startCapture(audio)", startErr, err_code, err_buf, err_len);
				return NULL;
			}
			if (err_code) *err_code = CAP_OK;
			return (void *)CFBridgingRetain(holder);
		}
		if (err_code) *err_code = CAP_ERR_UNSUPPORTED;
		set_err(err_buf, err_len, "system audio capture requires macOS 13 or later");
		return NULL;
	}
}

void cap_sysaudio_stop(void *ctx) {
	if (!ctx) return;
	@autoreleasepool {
		if (@available(macOS 13.0, *)) {
			ForgeSysAudioStream *holder = (ForgeSysAudioStream *)CFBridgingRelease(ctx);
			atomic_store(&holder->_stopping, 1);
			if (holder.stream) {
				dispatch_semaphore_t sem = dispatch_semaphore_create(0);
				[holder.stream stopCaptureWithCompletionHandler:^(NSError *error) {
					(void)error;
					dispatch_semaphore_signal(sem);
				}];
				dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC));
				[holder.stream removeStreamOutput:holder type:SCStreamOutputTypeAudio error:nil];
				holder.stream = nil;
			}
			if (holder.queue) {
				dispatch_sync(holder.queue, ^{});
			}
		}
	}
}

// ---- CoreAudio AudioQueue Microphone Capture ----

typedef struct {
	AudioQueueRef queue;
	AudioQueueBufferRef bufs[4];
	uintptr_t handle;
	_Atomic int stopping;
} mic_ctx;

static void mic_input_callback(void *inUserData, AudioQueueRef inAQ, AudioQueueBufferRef inBuffer,
	const AudioTimeStamp *inStartTime, UInt32 inNumPackets, const AudioStreamPacketDescription *inPacketDesc) {
	(void)inNumPackets;
	(void)inPacketDesc;
	mic_ctx *c = (mic_ctx *)inUserData;
	if (!c || atomic_load(&c->stopping)) return;
	int frames = (int)(inBuffer->mAudioDataByteSize / (2 * sizeof(float)));
	if (frames > 0) {
		int64_t ts = (inStartTime && (inStartTime->mFlags & kAudioTimeStampHostTimeValid))
			? host_to_100ns(inStartTime->mHostTime)
			: cap_host_100ns();
		goAudioSamples(c->handle, (float *)inBuffer->mAudioData, frames, ts);
	}
	if (!atomic_load(&c->stopping)) {
		AudioQueueEnqueueBuffer(inAQ, inBuffer, 0, NULL);
	}
}

void *cap_mic_start(const char *uid, uintptr_t handle, int *err_code, char *err_buf, int err_len) {
	mic_ctx *c = (mic_ctx *)calloc(1, sizeof(mic_ctx));
	if (!c) {
		if (err_code) *err_code = CAP_ERR_GENERIC;
		set_err(err_buf, err_len, "out of memory");
		return NULL;
	}
	c->handle = handle;
	atomic_store(&c->stopping, 0);

	AudioStreamBasicDescription asbd = {0};
	asbd.mSampleRate = 48000.0;
	asbd.mFormatID = kAudioFormatLinearPCM;
	asbd.mFormatFlags = kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked;
	asbd.mFramesPerPacket = 1;
	asbd.mChannelsPerFrame = 2;
	asbd.mBitsPerChannel = 32;
	asbd.mBytesPerFrame = 2 * sizeof(float);
	asbd.mBytesPerPacket = asbd.mBytesPerFrame;

	OSStatus st = AudioQueueNewInput(&asbd, mic_input_callback, c, NULL, NULL, 0, &c->queue);
	if (st != noErr || !c->queue) {
		free(c);
		if (err_code) *err_code = CAP_ERR_NO_DEVICE;
		snprintf(err_buf, (size_t)err_len, "AudioQueueNewInput: %d", (int)st);
		return NULL;
	}

	if (uid && uid[0] != '\0') {
		CFStringRef cfUID = CFStringCreateWithCString(kCFAllocatorDefault, uid, kCFStringEncodingUTF8);
		if (cfUID) {
			st = AudioQueueSetProperty(c->queue, kAudioQueueProperty_CurrentDevice, &cfUID, sizeof(cfUID));
			CFRelease(cfUID);
			if (st != noErr) {
				AudioQueueDispose(c->queue, true);
				free(c);
				if (err_code) *err_code = CAP_ERR_NO_DEVICE;
				snprintf(err_buf, (size_t)err_len, "AudioQueueSetProperty(CurrentDevice): %d", (int)st);
				return NULL;
			}
		}
	}

	const UInt32 bufBytes = 480 * 2 * sizeof(float); // 10 ms at 48 kHz stereo float32
	for (int i = 0; i < 4; i++) {
		st = AudioQueueAllocateBuffer(c->queue, bufBytes, &c->bufs[i]);
		if (st != noErr) {
			AudioQueueDispose(c->queue, true);
			free(c);
			if (err_code) *err_code = CAP_ERR_GENERIC;
			snprintf(err_buf, (size_t)err_len, "AudioQueueAllocateBuffer: %d", (int)st);
			return NULL;
		}
		AudioQueueEnqueueBuffer(c->queue, c->bufs[i], 0, NULL);
	}

	st = AudioQueueStart(c->queue, NULL);
	if (st != noErr) {
		AudioQueueDispose(c->queue, true);
		free(c);
		if (err_code) *err_code = CAP_ERR_NO_DEVICE;
		snprintf(err_buf, (size_t)err_len, "AudioQueueStart: %d", (int)st);
		return NULL;
	}
	if (err_code) *err_code = CAP_OK;
	return c;
}

void cap_mic_stop(void *ctx) {
	if (!ctx) return;
	mic_ctx *c = (mic_ctx *)ctx;
	atomic_store(&c->stopping, 1);
	if (c->queue) {
		AudioQueueStop(c->queue, true);
		AudioQueueDispose(c->queue, true);
		c->queue = NULL;
	}
	free(c);
}

// ---- AVFoundation Camera Capture ----

@interface ForgeCameraStream : NSObject <AVCaptureVideoDataOutputSampleBufferDelegate> {
@public
	_Atomic int _stopping;
@private
	uint8_t *_scaled;
	uint8_t *_mirrored;
	uint8_t *_rgba;
	int _outW, _outH, _mirror;
}
@property(nonatomic, strong) AVCaptureSession *session;
@property(nonatomic, strong) dispatch_queue_t queue;
@property(nonatomic, strong) id errObserver;
@property(nonatomic, strong) id discObserver;
@property(nonatomic) uintptr_t handle;
@end

@implementation ForgeCameraStream

- (instancetype)initWithWidth:(int)w height:(int)h mirror:(int)mirror handle:(uintptr_t)handle {
	self = [super init];
	if (self) {
		_outW = w;
		_outH = h;
		_mirror = mirror;
		_handle = handle;
		atomic_store(&_stopping, 0);
		size_t bytes = (size_t)w * (size_t)h * 4;
		_scaled = (uint8_t *)malloc(bytes);
		_rgba = (uint8_t *)malloc(bytes);
		if (mirror) {
			_mirrored = (uint8_t *)malloc(bytes);
		}
	}
	return self;
}

- (void)dealloc {
	free(_scaled);
	free(_mirrored);
	free(_rgba);
}

- (void)captureOutput:(AVCaptureOutput *)output didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer fromConnection:(AVCaptureConnection *)connection {
	(void)output;
	(void)connection;
	if (atomic_load(&_stopping) || !_scaled || !_rgba) return;
	CVPixelBufferRef pb = CMSampleBufferGetImageBuffer(sampleBuffer);
	if (!pb) return;
	if (CVPixelBufferLockBaseAddress(pb, kCVPixelBufferLock_ReadOnly) != kCVReturnSuccess) return;

	size_t srcW = CVPixelBufferGetWidth(pb);
	size_t srcH = CVPixelBufferGetHeight(pb);
	size_t srcStride = CVPixelBufferGetBytesPerRow(pb);
	uint8_t *base = (uint8_t *)CVPixelBufferGetBaseAddress(pb);

	if (base && srcW > 0 && srcH > 0) {
		// Center-crop source to match _outW:_outH aspect ratio.
		size_t cropW = srcW;
		size_t cropH = srcH;
		if (srcW * (size_t)_outH > srcH * (size_t)_outW) {
			cropW = srcH * (size_t)_outW / (size_t)_outH;
		} else {
			cropH = srcW * (size_t)_outH / (size_t)_outW;
		}
		if (cropW < 1) cropW = 1;
		if (cropH < 1) cropH = 1;
		size_t cropX = (srcW - cropW) / 2;
		size_t cropY = (srcH - cropH) / 2;

		vImage_Buffer srcBuf = {
			.data = base + cropY * srcStride + cropX * 4,
			.height = cropH,
			.width = cropW,
			.rowBytes = srcStride,
		};
		vImage_Buffer dstBuf = {
			.data = _scaled,
			.height = (vImagePixelCount)_outH,
			.width = (vImagePixelCount)_outW,
			.rowBytes = (size_t)_outW * 4,
		};
		vImageScale_ARGB8888(&srcBuf, &dstBuf, NULL, kvImageNoFlags);

		vImage_Buffer *srcForPermute = &dstBuf;
		vImage_Buffer mirrorBuf = {
			.data = _mirrored,
			.height = (vImagePixelCount)_outH,
			.width = (vImagePixelCount)_outW,
			.rowBytes = (size_t)_outW * 4,
		};
		if (_mirror && _mirrored) {
			vImageHorizontalReflect_ARGB8888(&dstBuf, &mirrorBuf, kvImageNoFlags);
			srcForPermute = &mirrorBuf;
		}

		vImage_Buffer rgbaBuf = {
			.data = _rgba,
			.height = (vImagePixelCount)_outH,
			.width = (vImagePixelCount)_outW,
			.rowBytes = (size_t)_outW * 4,
		};
		const uint8_t permuteMap[4] = { 2, 1, 0, 3 }; // BGRA -> RGBA
		vImagePermuteChannels_ARGB8888(srcForPermute, &rgbaBuf, permuteMap, kvImageNoFlags);
		// Ensure alpha is opaque (255).
		vImageOverwriteChannelsWithScalar_ARGB8888(255, &rgbaBuf, &rgbaBuf, 0x1, kvImageNoFlags);

		goCameraFrame(_handle, _rgba, _outW * _outH * 4);
	}
	CVPixelBufferUnlockBaseAddress(pb, kCVPixelBufferLock_ReadOnly);
}

@end

void *cap_camera_start(const char *uid, int width, int height, int mirror, uintptr_t handle,
	int *err_code, char *err_buf, int err_len) {
	@autoreleasepool {
		AVCaptureDevice *dev = nil;
		if (uid && uid[0] != '\0') {
			dev = [AVCaptureDevice deviceWithUniqueID:[NSString stringWithUTF8String:uid]];
		}
		if (!dev) {
			NSArray<AVCaptureDevice *> *all = discover_cameras();
			dev = all.firstObject;
		}
		if (!dev) {
			if (err_code) *err_code = CAP_ERR_NO_DEVICE;
			set_err(err_buf, err_len, "camera not found");
			return NULL;
		}

		NSError *inErr = nil;
		AVCaptureDeviceInput *input = [AVCaptureDeviceInput deviceInputWithDevice:dev error:&inErr];
		if (!input) {
			if (err_code) *err_code = CAP_ERR_NO_DEVICE;
			set_nserr(err_buf, err_len, inErr);
			return NULL;
		}

		ForgeCameraStream *holder = [[ForgeCameraStream alloc] initWithWidth:width height:height mirror:mirror handle:handle];
		holder.queue = dispatch_queue_create("com.btelo.forge.capture.camera", DISPATCH_QUEUE_SERIAL);
		AVCaptureSession *session = [[AVCaptureSession alloc] init];
		[session beginConfiguration];
		if (width <= 640 && height <= 480 && [session canSetSessionPreset:AVCaptureSessionPreset640x480]) {
			session.sessionPreset = AVCaptureSessionPreset640x480;
		} else if ([session canSetSessionPreset:AVCaptureSessionPreset1280x720]) {
			session.sessionPreset = AVCaptureSessionPreset1280x720;
		}
		if (![session canAddInput:input]) {
			[session commitConfiguration];
			if (err_code) *err_code = CAP_ERR_NO_DEVICE;
			set_err(err_buf, err_len, "cannot add camera input");
			return NULL;
		}
		[session addInput:input];

		AVCaptureVideoDataOutput *output = [[AVCaptureVideoDataOutput alloc] init];
		output.alwaysDiscardsLateVideoFrames = YES;
		output.videoSettings = @{
			(NSString *)kCVPixelBufferPixelFormatTypeKey: @(kCVPixelFormatType_32BGRA),
		};
		[output setSampleBufferDelegate:holder queue:holder.queue];
		if (![session canAddOutput:output]) {
			[session commitConfiguration];
			if (err_code) *err_code = CAP_ERR_GENERIC;
			set_err(err_buf, err_len, "cannot add camera output");
			return NULL;
		}
		[session addOutput:output];
		[session commitConfiguration];
		holder.session = session;

		__weak ForgeCameraStream *weakHolder = holder;
		NSNotificationCenter *nc = [NSNotificationCenter defaultCenter];
		holder.errObserver = [nc addObserverForName:AVCaptureSessionRuntimeErrorNotification
			object:session queue:nil usingBlock:^(NSNotification *note) {
				ForgeCameraStream *h = weakHolder;
				if (!h || atomic_load(&h->_stopping)) return;
				NSError *e = note.userInfo[AVCaptureSessionErrorKey];
				const char *msg = e ? e.localizedDescription.UTF8String : "camera runtime error";
				goCameraError(h.handle, (char *)msg);
			}];
		holder.discObserver = [nc addObserverForName:AVCaptureDeviceWasDisconnectedNotification
			object:dev queue:nil usingBlock:^(NSNotification *note) {
				(void)note;
				ForgeCameraStream *h = weakHolder;
				if (!h || atomic_load(&h->_stopping)) return;
				goCameraError(h.handle, (char *)"camera disconnected");
			}];

		[session startRunning];
		if (err_code) *err_code = CAP_OK;
		return (void *)CFBridgingRetain(holder);
	}
}

void cap_camera_stop(void *ctx) {
	if (!ctx) return;
	@autoreleasepool {
		ForgeCameraStream *holder = (ForgeCameraStream *)CFBridgingRelease(ctx);
		atomic_store(&holder->_stopping, 1);
		NSNotificationCenter *nc = [NSNotificationCenter defaultCenter];
		if (holder.errObserver) [nc removeObserver:holder.errObserver];
		if (holder.discObserver) [nc removeObserver:holder.discObserver];
		holder.errObserver = nil;
		holder.discObserver = nil;
		if (holder.session) {
			[holder.session stopRunning];
			holder.session = nil;
		}
		if (holder.queue) {
			dispatch_sync(holder.queue, ^{});
		}
	}
}
