#import <Accelerate/Accelerate.h>
#import <Carbon/Carbon.h>
#import <Cocoa/Cocoa.h>
#import <IOSurface/IOSurface.h>
#import <QuartzCore/QuartzCore.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <math.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include "window.h"
#include "_cgo_export.h"

// ---- Triple-buffered IOSurface + Accelerate/vImage SIMD RGBA->BGRA presentation ----

#define FORGE_SURFACE_RING 3

typedef struct {
	IOSurfaceRef    surfaces[FORGE_SURFACE_RING];
	int             width;
	int             height;
	int             nextIdx;
	pthread_mutex_t mu;
} ForgeSurface;

static void forge_surface_init(ForgeSurface *s) {
	memset(s, 0, sizeof(*s));
	pthread_mutex_init(&s->mu, NULL);
}

static void forge_surface_reset_locked(ForgeSurface *s) {
	for (int i = 0; i < FORGE_SURFACE_RING; i++) {
		if (s->surfaces[i]) {
			CFRelease(s->surfaces[i]);
			s->surfaces[i] = NULL;
		}
	}
	s->width = 0;
	s->height = 0;
	s->nextIdx = 0;
}

static void forge_surface_destroy(ForgeSurface *s) {
	pthread_mutex_lock(&s->mu);
	forge_surface_reset_locked(s);
	pthread_mutex_unlock(&s->mu);
	pthread_mutex_destroy(&s->mu);
}

static IOSurfaceRef forge_create_iosurface(int width, int height) {
	size_t bpr = ((size_t)width * 4 + 63) & ~(size_t)63;
	size_t allocSize = bpr * (size_t)height;
	NSDictionary *props = @{
		(__bridge NSString *)kIOSurfaceWidth : @(width),
		(__bridge NSString *)kIOSurfaceHeight : @(height),
		(__bridge NSString *)kIOSurfaceBytesPerElement : @4,
		(__bridge NSString *)kIOSurfaceBytesPerRow : @(bpr),
		(__bridge NSString *)kIOSurfaceAllocSize : @(allocSize),
		(__bridge NSString *)kIOSurfacePixelFormat : @(0x42475241), // 'BGRA'
	};
	return IOSurfaceCreate((__bridge CFDictionaryRef)props);
}

// forge_surface_write writes RGBA pixels into the next BGRA IOSurface in the ring using SIMD permutation.
// Returns a retained IOSurfaceRef (caller must CFRelease) or NULL on allocation error.
static IOSurfaceRef forge_surface_write(ForgeSurface *s, const void *rgba, int width, int height) {
	pthread_mutex_lock(&s->mu);
	if (s->width != width || s->height != height || !s->surfaces[0]) {
		forge_surface_reset_locked(s);
		for (int i = 0; i < FORGE_SURFACE_RING; i++) {
			s->surfaces[i] = forge_create_iosurface(width, height);
			if (!s->surfaces[i]) {
				forge_surface_reset_locked(s);
				pthread_mutex_unlock(&s->mu);
				return NULL;
			}
		}
		s->width = width;
		s->height = height;
	}
	IOSurfaceRef surf = s->surfaces[s->nextIdx];
	s->nextIdx = (s->nextIdx + 1) % FORGE_SURFACE_RING;

	IOSurfaceLock(surf, 0, NULL);
	vImage_Buffer src = {
		.data = (void *)rgba,
		.height = (vImagePixelCount)height,
		.width = (vImagePixelCount)width,
		.rowBytes = (size_t)width * 4,
	};
	vImage_Buffer dst = {
		.data = IOSurfaceGetBaseAddress(surf),
		.height = (vImagePixelCount)height,
		.width = (vImagePixelCount)width,
		.rowBytes = IOSurfaceGetBytesPerRow(surf),
	};
	const uint8_t permute[4] = {2, 1, 0, 3}; // RGBA -> BGRA
	vImagePermuteChannels_ARGB8888(&src, &dst, permute, kvImageNoFlags);
	IOSurfaceUnlock(surf, 0, NULL);

	CFRetain(surf);
	pthread_mutex_unlock(&s->mu);
	return surf;
}

// ---- Cursors ----

static NSCursor *cursorForKind(int kind) {
	switch (kind) {
	case 1:
		return NSCursor.pointingHandCursor;
	case 2:
		return NSCursor.IBeamCursor;
	case 3:
		return NSCursor.resizeLeftRightCursor;
	case 4:
		return NSCursor.openHandCursor;
	case 5: {
		// Hidden cursor over the window alone.
		static NSCursor *hidden;
		if (!hidden) {
			hidden = [[NSCursor alloc] initWithImage:[[NSImage alloc] initWithSize:NSMakeSize(1, 1)] hotSpot:NSZeroPoint];
		}
		return hidden;
	}
	case 6:
		return NSCursor.crosshairCursor;
	case 7: {
		// Diagonal NW-SE double-headed resize arrow drawn crisply in vector coordinates.
		static NSCursor *nwse;
		if (!nwse) {
			NSImage *img = [NSImage imageWithSize:NSMakeSize(18, 18)
			                              flipped:NO
			                       drawingHandler:^BOOL(NSRect dstRect) {
				NSBezierPath *p = [NSBezierPath bezierPath];
				// Top-left arrowhead (3, 15) to bottom-right arrowhead (15, 3) in non-flipped coords.
				[p moveToPoint:NSMakePoint(3, 15)];
				[p lineToPoint:NSMakePoint(9, 15)];
				[p lineToPoint:NSMakePoint(7, 13)];
				[p lineToPoint:NSMakePoint(13, 7)];
				[p lineToPoint:NSMakePoint(15, 9)];
				[p lineToPoint:NSMakePoint(15, 3)];
				[p lineToPoint:NSMakePoint(9, 3)];
				[p lineToPoint:NSMakePoint(11, 5)];
				[p lineToPoint:NSMakePoint(5, 11)];
				[p lineToPoint:NSMakePoint(3, 9)];
				[p closePath];
				[NSColor.whiteColor setStroke];
				p.lineWidth = 2.2;
				[p stroke];
				[NSColor.blackColor setFill];
				[p fill];
				return YES;
			}];
			nwse = [[NSCursor alloc] initWithImage:img hotSpot:NSMakePoint(9, 9)];
		}
		return nwse;
	}
	default:
		return NSCursor.arrowCursor;
	}
}

// ---- Shared desktop display coordinate contract (aligned with internal/capture) ----

typedef struct {
	double gx, gy, gw, gh; // CoreGraphics top-left points
	double scale;          // backing pixels per point
	int    px, py, pw, ph; // desktop backing pixels
} ForgeDisplay;

static double displayScale(CGDirectDisplayID did, double gw) {
	CGDisplayModeRef mode = CGDisplayCopyDisplayMode(did);
	double s = 1.0;
	if (mode) {
		size_t pw = CGDisplayModeGetPixelWidth(mode);
		CGDisplayModeRelease(mode);
		if (gw > 0 && pw > 0) {
			s = (double)pw / gw;
		}
	}
	return s > 0 ? s : 1.0;
}

static int listDisplays(ForgeDisplay *out, int maxCount, double *outMainH, double *outMainScale) {
	CGDirectDisplayID mainID = CGMainDisplayID();
	CGRect mainRect = CGDisplayBounds(mainID);
	double mainH = mainRect.size.height;
	double mainScale = displayScale(mainID, mainRect.size.width);
	if (outMainH) *outMainH = mainH;
	if (outMainScale) *outMainScale = mainScale;

	CGDirectDisplayID ids[32];
	uint32_t count = 0;
	if (CGGetActiveDisplayList(32, ids, &count) != kCGErrorSuccess || count == 0) {
		if (maxCount > 0) {
			out[0].gx = 0;
			out[0].gy = 0;
			out[0].gw = mainRect.size.width > 0 ? mainRect.size.width : 1440;
			out[0].gh = mainH > 0 ? mainH : 900;
			out[0].scale = mainScale;
			out[0].px = 0;
			out[0].py = 0;
			out[0].pw = (int)llround(out[0].gw * mainScale);
			out[0].ph = (int)llround(out[0].gh * mainScale);
			return 1;
		}
		return 0;
	}
	int n = 0;
	for (uint32_t i = 0; i < count && n < maxCount; i++) {
		CGRect r = CGDisplayBounds(ids[i]);
		double sd = displayScale(ids[i], r.size.width);
		ForgeDisplay d;
		d.gx = r.origin.x;
		d.gy = r.origin.y;
		d.gw = r.size.width;
		d.gh = r.size.height;
		d.scale = sd;
		d.pw = (int)llround(d.gw * sd);
		d.ph = (int)llround(d.gh * sd);
		d.px = d.gx >= 0 ? (int)llround(d.gx * mainScale) : (int)llround((d.gx + d.gw) * mainScale) - d.pw;
		d.py = d.gy >= 0 ? (int)llround(d.gy * mainScale) : (int)llround((d.gy + d.gh) * mainScale) - d.ph;
		out[n++] = d;
	}
	return n;
}

static NSRect desktopPixelsToCocoaRect(int x, int y, int width, int height) {
	ForgeDisplay ds[32];
	double mainH = 900, mainScale = 1.0;
	int n = listDisplays(ds, 32, &mainH, &mainScale);
	int best = 0;
	long long bestArea = -1;
	int x2 = x + width, y2 = y + height;
	for (int i = 0; i < n; i++) {
		int ox0 = x > ds[i].px ? x : ds[i].px;
		int oy0 = y > ds[i].py ? y : ds[i].py;
		int ox1 = x2 < ds[i].px + ds[i].pw ? x2 : ds[i].px + ds[i].pw;
		int oy1 = y2 < ds[i].py + ds[i].ph ? y2 : ds[i].py + ds[i].ph;
		if (ox1 > ox0 && oy1 > oy0) {
			long long area = (long long)(ox1 - ox0) * (long long)(oy1 - oy0);
			if (area > bestArea) {
				bestArea = area;
				best = i;
			}
		}
	}
	ForgeDisplay d = ds[best];
	double sd = d.scale > 0 ? d.scale : 1.0;
	double cocoaX = d.gx + ((double)(x - d.px)) / sd;
	double cocoaW = ((double)width) / sd;
	double cocoaH = ((double)height) / sd;
	double cocoaY = mainH - (d.gy + ((double)(y - d.py)) / sd) - cocoaH;
	return NSMakeRect(cocoaX, cocoaY, cocoaW, cocoaH);
}

int forge_has_gui_session(void) {
	CFDictionaryRef d = CGSessionCopyCurrentDictionary();
	if (!d) {
		return 0;
	}
	CFRelease(d);
	return 1;
}

void forge_pointer_at(int *outX, int *outY) {
	@autoreleasepool {
		ForgeDisplay ds[32];
		double mainH = 900, mainScale = 1.0;
		int n = listDisplays(ds, 32, &mainH, &mainScale);
		NSPoint loc = NSEvent.mouseLocation; // Cocoa bottom-left global points
		double cgx = loc.x;
		double cgy = mainH - loc.y;
		int best = 0;
		for (int i = 0; i < n; i++) {
			if (cgx >= ds[i].gx && cgx < ds[i].gx + ds[i].gw &&
			    cgy >= ds[i].gy && cgy < ds[i].gy + ds[i].gh) {
				best = i;
				break;
			}
		}
		ForgeDisplay d = ds[best];
		*outX = d.px + (int)llround((cgx - d.gx) * d.scale);
		*outY = d.py + (int)llround((cgy - d.gy) * d.scale);
	}
}

// ---- Main Window & App Delegate ----

static NSWindow *window;
static int closed;
static NSCursor *currentCursor;
static ForgeSurface mainSurface;
static int mainSurfaceInited;

NSView *forge_content_view(void) { return window.contentView; }

// Modifier bits shared with the Go side: 1 Command or Control, 2 Shift, 4 Option.
static int modifierBits(NSEventModifierFlags f) {
	int m = 0;
	if (f & (NSEventModifierFlagCommand | NSEventModifierFlagControl)) m |= 1;
	if (f & NSEventModifierFlagShift) m |= 2;
	if (f & NSEventModifierFlagOption) m |= 4;
	return m;
}

static int mapCommandSelector(SEL sel, int *outKey, int *outMods) {
	static const struct {
		const char *name;
		int key, mods;
	} commands[] = {
	    {"deleteBackward:", 0x08, 0},
	    {"deleteWordBackward:", 0x08, 1},
	    {"deleteToBeginningOfLine:", 0x08, 1},
	    {"deleteForward:", 0x2e, 0},
	    {"deleteWordForward:", 0x2e, 1},
	    {"insertNewline:", 0x0d, 0},
	    {"insertLineBreak:", 0x0d, 0},
	    {"insertTab:", 0x09, 0},
	    {"insertBacktab:", 0x09, 2},
	    {"cancelOperation:", 0x1b, 0},
	    {"moveLeft:", 0x25, 0},
	    {"moveRight:", 0x27, 0},
	    {"moveUp:", 0x26, 0},
	    {"moveDown:", 0x28, 0},
	    {"moveWordLeft:", 0x25, 1},
	    {"moveWordRight:", 0x27, 1},
	    {"moveToBeginningOfLine:", 0x24, 0},
	    {"moveToEndOfLine:", 0x23, 0},
	    {"moveToLeftEndOfLine:", 0x24, 0},
	    {"moveToRightEndOfLine:", 0x23, 0},
	    {"moveLeftAndModifySelection:", 0x25, 2},
	    {"moveRightAndModifySelection:", 0x27, 2},
	    {"moveWordLeftAndModifySelection:", 0x25, 3},
	    {"moveWordRightAndModifySelection:", 0x27, 3},
	    {"moveToBeginningOfLineAndModifySelection:", 0x24, 2},
	    {"moveToEndOfLineAndModifySelection:", 0x23, 2},
	    {"moveToLeftEndOfLineAndModifySelection:", 0x24, 2},
	    {"moveToRightEndOfLineAndModifySelection:", 0x23, 2},
	    {"moveUpAndModifySelection:", 0x26, 2},
	    {"moveDownAndModifySelection:", 0x28, 2},
	    {"pageUp:", 0x21, 0},
	    {"pageDown:", 0x22, 0},
	    {"scrollPageUp:", 0x21, 0},
	    {"scrollPageDown:", 0x22, 0},
	    {"moveToBeginningOfDocument:", 0x24, 1},
	    {"moveToEndOfDocument:", 0x23, 1},
	    {"moveToBeginningOfDocumentAndModifySelection:", 0x24, 3},
	    {"moveToEndOfDocumentAndModifySelection:", 0x23, 3},
	};
	const char *name = sel_getName(sel);
	for (size_t i = 0; i < sizeof commands / sizeof commands[0]; i++) {
		if (strcmp(name, commands[i].name) == 0) {
			*outKey = commands[i].key;
			*outMods = commands[i].mods;
			return 1;
		}
	}
	return 0;
}

@interface ForgeAppDelegate : NSObject <NSApplicationDelegate>
@end

@implementation ForgeAppDelegate
- (void)themeChanged:(NSNotification *)note {
	forge_wake();
}
- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object change:(NSDictionary<NSKeyValueChangeKey, id> *)change context:(void *)context {
	if ([keyPath isEqualToString:@"effectiveAppearance"]) {
		forge_wake();
		return;
	}
	[super observeValueForKeyPath:keyPath ofObject:object change:change context:context];
}
- (void)application:(NSApplication *)application openURLs:(NSArray<NSURL *> *)urls {
	for (NSURL *u in urls) {
		if (u.isFileURL && u.path.length > 0) {
			goOpenFile((char *)u.path.UTF8String);
		}
	}
}
- (void)application:(NSApplication *)sender openFiles:(NSArray<NSString *> *)filenames {
	for (NSString *path in filenames) {
		if (path.length > 0) {
			goOpenFile((char *)path.UTF8String);
		}
	}
	[sender replyToOpenOrPrint:NSApplicationDelegateReplySuccess];
}
- (BOOL)application:(NSApplication *)sender openFile:(NSString *)filename {
	if (filename.length > 0) {
		goOpenFile((char *)filename.UTF8String);
		return YES;
	}
	return NO;
}
@end

@interface ForgeView : NSView <NSTextInputClient>
@property(nonatomic) BOOL typing;
@property(nonatomic) NSRect caret;
@property(nonatomic, strong) NSMutableAttributedString *marked;
@end

@interface ForgeDelegate : NSObject <NSWindowDelegate>
@end

@implementation ForgeDelegate
- (BOOL)windowShouldClose:(NSWindow *)sender {
	closed = 1;
	forge_wake();
	return NO;
}
- (void)windowDidResize:(NSNotification *)note {
	goRefresh();
}
- (void)windowDidChangeBackingProperties:(NSNotification *)note {
	window.contentView.layer.contentsScale = window.backingScaleFactor;
	goRefresh();
}
- (void)windowDidBecomeKey:(NSNotification *)note {
	goFocusChange(1);
}
- (void)windowDidResignKey:(NSNotification *)note {
	goFocusChange(0);
}
- (void)windowDidMiniaturize:(NSNotification *)note {
	forge_wake();
}
- (void)windowDidDeminiaturize:(NSNotification *)note {
	forge_wake();
}
@end

@implementation ForgeView
- (BOOL)acceptsFirstResponder {
	return YES;
}
- (BOOL)acceptsFirstMouse:(NSEvent *)e {
	return YES;
}
- (BOOL)isFlipped {
	return YES;
}
- (BOOL)wantsUpdateLayer {
	return YES;
}
- (NSPoint)pixels:(NSEvent *)e {
	NSPoint p = [self convertPoint:e.locationInWindow fromView:nil];
	CGFloat s = self.window.backingScaleFactor;
	return NSMakePoint(p.x * s, p.y * s);
}
- (void)reclaimFocus {
	if (self.window.firstResponder != self) {
		[self.window makeFirstResponder:self];
	}
}
- (void)mouseDown:(NSEvent *)e {
	[self reclaimFocus];
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 0, 1, e.clickCount == 2, modifierBits(e.modifierFlags));
}
- (void)mouseUp:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 0, 0, 0, modifierBits(e.modifierFlags));
}
- (void)rightMouseDown:(NSEvent *)e {
	[self reclaimFocus];
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 1, 1, 0, modifierBits(e.modifierFlags));
}
- (void)rightMouseUp:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 1, 0, 0, modifierBits(e.modifierFlags));
}
- (void)otherMouseDown:(NSEvent *)e {
	[self reclaimFocus];
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 2, 1, 0, modifierBits(e.modifierFlags));
}
- (void)otherMouseUp:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 2, 0, 0, modifierBits(e.modifierFlags));
}
- (void)mouseMoved:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goMove(p.x, p.y, modifierBits(e.modifierFlags));
}
- (void)mouseDragged:(NSEvent *)e {
	[self mouseMoved:e];
}
- (void)rightMouseDragged:(NSEvent *)e {
	[self mouseMoved:e];
}
- (void)otherMouseDragged:(NSEvent *)e {
	[self mouseMoved:e];
}
- (void)mouseExited:(NSEvent *)e {
	goMouseLeave();
}
- (void)flagsChanged:(NSEvent *)e {
	goModifiers(modifierBits(e.modifierFlags));
}
- (void)scrollWheel:(NSEvent *)e {
	CGFloat dy = e.scrollingDeltaY;
	if (e.hasPreciseScrollingDeltas) {
		dy /= 40;
	}
	goWheel(dy, modifierBits(e.modifierFlags));
}
- (void)keyDown:(NSEvent *)e {
	if (self.typing && !(e.modifierFlags & (NSEventModifierFlagCommand | NSEventModifierFlagControl))) {
		[self interpretKeyEvents:@[ e ]];
		return;
	}
	goKey(e.keyCode, (char *)e.charactersIgnoringModifiers.UTF8String, modifierBits(e.modifierFlags));
}
- (void)keyUp:(NSEvent *)e {
	goKeyUp(e.keyCode, (char *)e.charactersIgnoringModifiers.UTF8String);
}
- (void)cursorUpdate:(NSEvent *)e {
	[(currentCursor ? currentCursor : NSCursor.arrowCursor) set];
}
- (void)updateTrackingAreas {
	for (NSTrackingArea *a in self.trackingAreas) {
		[self removeTrackingArea:a];
	}
	NSTrackingAreaOptions o = NSTrackingMouseEnteredAndExited | NSTrackingMouseMoved |
	                          NSTrackingActiveAlways | NSTrackingCursorUpdate | NSTrackingInVisibleRect;
	[self addTrackingArea:[[NSTrackingArea alloc] initWithRect:NSZeroRect options:o owner:self userInfo:nil]];
	[super updateTrackingAreas];
}

// Drag and drop of files.
- (NSDragOperation)draggingEntered:(id<NSDraggingInfo>)sender {
	return NSDragOperationCopy;
}
- (BOOL)performDragOperation:(id<NSDraggingInfo>)sender {
	NSArray<NSURL *> *urls = [sender.draggingPasteboard readObjectsForClasses:@[ NSURL.class ]
	                                                                   options:@{NSPasteboardURLReadingFileURLsOnlyKey : @YES}];
	for (NSURL *u in urls) {
		goDrop((char *)u.path.UTF8String);
	}
	return urls.count > 0;
}

// Text input, including input methods.
- (void)insertText:(id)string replacementRange:(NSRange)range {
	self.marked = nil;
	NSString *s = [string isKindOfClass:NSAttributedString.class] ? [string string] : string;
	goTyped((char *)s.UTF8String);
}
- (void)setMarkedText:(id)string selectedRange:(NSRange)selected replacementRange:(NSRange)range {
	self.marked = [string isKindOfClass:NSAttributedString.class] ? [[NSMutableAttributedString alloc] initWithAttributedString:string]
	                                                              : [[NSMutableAttributedString alloc] initWithString:string];
}
- (void)unmarkText {
	self.marked = nil;
}
- (BOOL)hasMarkedText {
	return self.marked.length > 0;
}
- (NSRange)markedRange {
	return self.marked.length > 0 ? NSMakeRange(0, self.marked.length) : NSMakeRange(NSNotFound, 0);
}
- (NSRange)selectedRange {
	return NSMakeRange(NSNotFound, 0);
}
- (NSAttributedString *)attributedSubstringForProposedRange:(NSRange)range actualRange:(NSRangePointer)actual {
	return nil;
}
- (NSArray<NSAttributedStringKey> *)validAttributesForMarkedText {
	return @[];
}
- (NSUInteger)characterIndexForPoint:(NSPoint)point {
	return NSNotFound;
}
- (NSRect)firstRectForCharacterRange:(NSRange)range actualRange:(NSRangePointer)actual {
	CGFloat s = self.window.backingScaleFactor;
	NSRect r = NSMakeRect(self.caret.origin.x / s, self.caret.origin.y / s, 1, self.caret.size.height / s);
	return [self.window convertRectToScreen:[self convertRect:r toView:nil]];
}
- (void)doCommandBySelector:(SEL)sel {
	int key = 0, mods = 0;
	if (mapCommandSelector(sel, &key, &mods)) {
		goKeyCode(key, mods);
	}
}
@end

// ---- Global Hotkeys via Carbon RegisterEventHotKey ----

#define FORGE_HOTKEY_SIG 'BTFG'
#define FORGE_MAX_HOTKEYS 32

static EventHandlerRef hotkeyHandler;
static EventHotKeyRef hotkeyRefs[FORGE_MAX_HOTKEYS];
static int hotkeyCount;

static OSStatus forgeHotkeyCallback(EventHandlerCallRef next, EventRef event, void *userData) {
	EventHotKeyID hkID;
	if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(hkID), NULL, &hkID) == noErr) {
		if (hkID.signature == FORGE_HOTKEY_SIG && hkID.id > 0) {
			goHotkey((int)hkID.id);
		}
	}
	return noErr;
}

static void ensureHotkeyHandler(void) {
	if (hotkeyHandler) {
		return;
	}
	EventTypeSpec spec = {kEventClassKeyboard, kEventHotKeyPressed};
	InstallApplicationEventHandler(&forgeHotkeyCallback, 1, &spec, NULL, &hotkeyHandler);
}

void forge_clear_hotkeys(void) {
	for (int i = 0; i < hotkeyCount; i++) {
		if (hotkeyRefs[i]) {
			UnregisterEventHotKey(hotkeyRefs[i]);
			hotkeyRefs[i] = NULL;
		}
	}
	hotkeyCount = 0;
}

int forge_register_hotkey(int id, uint32_t macKeyCode, int ctrl, int alt, int shift) {
	ensureHotkeyHandler();
	if (hotkeyCount >= FORGE_MAX_HOTKEYS) {
		return -1;
	}
	UInt32 mods = 0;
	// Map Ctrl -> Control (not Command) so default Ctrl+Shift+3/4 never collides with macOS Cmd+Shift+3/4 screenshots.
	if (ctrl) mods |= controlKey;
	if (alt) mods |= optionKey;
	if (shift) mods |= shiftKey;
	EventHotKeyID hkID = {FORGE_HOTKEY_SIG, (UInt32)id};
	EventHotKeyRef ref = NULL;
	OSStatus st = RegisterEventHotKey(macKeyCode, mods, hkID, GetApplicationEventTarget(), 0, &ref);
	if (st == noErr && ref) {
		hotkeyRefs[hotkeyCount++] = ref;
	}
	return (int)st;
}

static NSColor *forgeBackgroundColor(int dark) {
	if (dark) {
		return [NSColor colorWithSRGBRed:12.0 / 255.0 green:13.0 / 255.0 blue:16.0 / 255.0 alpha:1.0];
	}
	return [NSColor colorWithSRGBRed:242.0 / 255.0 green:243.0 / 255.0 blue:246.0 / 255.0 alpha:1.0];
}

int forge_system_dark(void) {
	@autoreleasepool {
		if (NSApp && NSApp.appearance == nil) {
			NSAppearanceName match = [NSApp.effectiveAppearance bestMatchFromAppearancesWithNames:@[
				NSAppearanceNameAqua,
				NSAppearanceNameDarkAqua,
			]];
			if (match != nil) {
				return [match isEqualToString:NSAppearanceNameDarkAqua] ? 1 : 0;
			}
		}
		NSString *style = [NSUserDefaults.standardUserDefaults stringForKey:@"AppleInterfaceStyle"];
		return (style && [style caseInsensitiveCompare:@"Dark"] == NSOrderedSame) ? 1 : 0;
	}
}

void forge_set_dark(int dark) {
	@autoreleasepool {
		if (!window) {
			return;
		}
		window.appearance = [NSAppearance appearanceNamed:dark ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
		NSColor *bg = forgeBackgroundColor(dark);
		window.backgroundColor = bg;
		if (window.contentView && window.contentView.layer) {
			window.contentView.layer.backgroundColor = bg.CGColor;
		}
	}
}

// ---- Main Window API ----

int forge_open(const char *title, int width, int height, int background) {
	@autoreleasepool {
		closed = 0;
		if (!mainSurfaceInited) {
			forge_surface_init(&mainSurface);
			mainSurfaceInited = 1;
		}
		[NSApplication sharedApplication];
		static ForgeAppDelegate *appDelegate;
		if (!appDelegate) {
			appDelegate = [ForgeAppDelegate new];
			NSApp.delegate = appDelegate;
			[NSDistributedNotificationCenter.defaultCenter addObserver:appDelegate
			                                                  selector:@selector(themeChanged:)
			                                                      name:@"AppleInterfaceThemeChangedNotification"
			                                                    object:nil];
			[NSApp addObserver:appDelegate forKeyPath:@"effectiveAppearance" options:0 context:NULL];
		}
		[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
		NSMenu *bar = [NSMenu new];
		NSMenuItem *appItem = [NSMenuItem new];
		[bar addItem:appItem];
		NSMenu *appMenu = [NSMenu new];
		[appMenu addItemWithTitle:@"Quit" action:@selector(terminate:) keyEquivalent:@"q"];
		appItem.submenu = appMenu;
		NSApp.mainMenu = bar;
		[NSApp finishLaunching];

		NSRect frame = NSMakeRect(0, 0, width, height);
		NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
		window = [[NSWindow alloc] initWithContentRect:frame styleMask:style backing:NSBackingStoreBuffered defer:NO];
		window.title = @(title);
		window.minSize = NSMakeSize(960, 600);
		window.releasedWhenClosed = NO;
		static ForgeDelegate *delegate;
		delegate = [ForgeDelegate new];
		window.delegate = delegate;
		NSColor *initialBG = forgeBackgroundColor(forge_system_dark());
		window.backgroundColor = initialBG;
		ForgeView *view = [[ForgeView alloc] initWithFrame:frame];
		view.wantsLayer = YES;
		view.layer.opaque = YES;
		view.layer.contentsGravity = kCAGravityResize;
		view.layer.contentsScale = window.backingScaleFactor;
		view.layer.backgroundColor = initialBG.CGColor;
		[view registerForDraggedTypes:@[ NSPasteboardTypeFileURL ]];
		window.contentView = view;
		[window makeFirstResponder:view];
		[window center];
		if (background) {
			[window orderFront:nil];
		} else {
			[window makeKeyAndOrderFront:nil];
			[NSApp activateIgnoringOtherApps:YES];
		}
		return 1;
	}
}

int forge_poll(void) {
	@autoreleasepool {
		for (;;) {
			NSEvent *e = [NSApp nextEventMatchingMask:NSEventMaskAny untilDate:NSDate.distantPast inMode:NSDefaultRunLoopMode dequeue:YES];
			if (!e) {
				break;
			}
			[NSApp sendEvent:e];
		}
		goModifiers(modifierBits(NSEvent.modifierFlags));
	}
	return !closed;
}

void forge_wait(double seconds) {
	@autoreleasepool {
		NSDate *until = seconds < 0 ? [NSDate distantFuture] : [NSDate dateWithTimeIntervalSinceNow:seconds];
		[NSApp nextEventMatchingMask:NSEventMaskAny untilDate:until inMode:NSDefaultRunLoopMode dequeue:NO];
	}
}

int forge_key_window(void) {
	return (window && window.isKeyWindow) ? 1 : 0;
}

void forge_wake(void) {
	@autoreleasepool {
		NSEvent *e = [NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
		[NSApp postEvent:e atStart:NO];
	}
}

void forge_size(int *width, int *height) {
	if (!window || !window.isVisible || window.isMiniaturized) {
		*width = 0;
		*height = 0;
		return;
	}
	NSSize s = window.contentView.bounds.size;
	CGFloat k = window.backingScaleFactor;
	*width = (int)llround(s.width * k);
	*height = (int)llround(s.height * k);
}

double forge_scale(void) {
	return window ? window.backingScaleFactor : 1.0;
}

void forge_present(const void *rgba, int width, int height) {
	@autoreleasepool {
		if (!window || !mainSurfaceInited) {
			return;
		}
		IOSurfaceRef surf = forge_surface_write(&mainSurface, rgba, width, height);
		if (!surf) {
			return;
		}
		[CATransaction begin];
		[CATransaction setDisableActions:YES];
		window.contentView.layer.contents = (__bridge id)surf;
		[CATransaction commit];
		CFRelease(surf);
	}
}

void forge_title(const char *title) {
	if (window) {
		window.title = @(title);
	}
}

void forge_cursor(int kind) {
	currentCursor = cursorForKind(kind);
	[currentCursor set];
}

void forge_text_input(int on, double x, double y, double height) {
	if (!window) {
		return;
	}
	ForgeView *view = (ForgeView *)window.contentView;
	if (!on && view.typing) {
		[view.inputContext discardMarkedText];
		view.marked = nil;
	}
	view.typing = on != 0;
	view.caret = NSMakeRect(x, y, 1, height);
	if (on) {
		[view.inputContext invalidateCharacterCoordinates];
	}
}

void forge_minimize(void) {
	@autoreleasepool {
		if (window && !window.isMiniaturized) {
			[window miniaturize:nil];
		}
	}
}

void forge_restore(void) {
	@autoreleasepool {
		if (!window) {
			return;
		}
		if (window.isMiniaturized) {
			[window deminiaturize:nil];
		}
		[window makeKeyAndOrderFront:nil];
		[NSApp activateIgnoringOtherApps:YES];
	}
}

void forge_hide(void) {
	@autoreleasepool {
		if (window) {
			[window orderOut:nil];
		}
	}
}

int forge_shown(void) {
	if (!window) {
		return 0;
	}
	return (window.isVisible && !window.isMiniaturized && window.isKeyWindow && NSApp.isActive) ? 1 : 0;
}

void forge_exclude_capture(int on) {
	if (window) {
		window.sharingType = on ? NSWindowSharingNone : NSWindowSharingReadOnly;
	}
}

static char *copyString(NSString *s) {
	return s ? strdup(s.UTF8String) : NULL;
}

char *forge_languages(void) {
	@autoreleasepool {
		return copyString([NSLocale.preferredLanguages componentsJoinedByString:@"\n"]);
	}
}

char *forge_clipboard_text(void) {
	@autoreleasepool {
		return copyString([NSPasteboard.generalPasteboard stringForType:NSPasteboardTypeString]);
	}
}

void forge_set_clipboard(const char *text) {
	@autoreleasepool {
		NSPasteboard *pb = NSPasteboard.generalPasteboard;
		[pb clearContents];
		[pb setString:@(text) forType:NSPasteboardTypeString];
	}
}

void *forge_clipboard_png(int *length) {
	@autoreleasepool {
		NSPasteboard *pb = NSPasteboard.generalPasteboard;
		NSData *png = [pb dataForType:NSPasteboardTypePNG];
		if (!png) {
			NSData *tiff = [pb dataForType:NSPasteboardTypeTIFF];
			if (tiff) {
				NSBitmapImageRep *rep = [NSBitmapImageRep imageRepWithData:tiff];
				png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
			}
		}
		if (!png || png.length == 0) {
			return NULL;
		}
		void *out = malloc(png.length);
		memcpy(out, png.bytes, png.length);
		*length = (int)png.length;
		return out;
	}
}

int forge_set_clipboard_png(const void *pngData, int length) {
	@autoreleasepool {
		if (!pngData || length <= 0) {
			return 0;
		}
		NSData *data = [NSData dataWithBytes:pngData length:(NSUInteger)length];
		NSPasteboard *pb = NSPasteboard.generalPasteboard;
		[pb clearContents];
		BOOL ok = [pb setData:data forType:NSPasteboardTypePNG];
		NSBitmapImageRep *rep = [NSBitmapImageRep imageRepWithData:data];
		if (rep) {
			NSData *tiff = [rep TIFFRepresentation];
			if (tiff) {
				[pb setData:tiff forType:NSPasteboardTypeTIFF];
			}
		}
		return ok ? 1 : 0;
	}
}

char *forge_clipboard_files(void) {
	@autoreleasepool {
		NSArray<NSURL *> *urls = [NSPasteboard.generalPasteboard readObjectsForClasses:@[ NSURL.class ]
		                                                                       options:@{NSPasteboardURLReadingFileURLsOnlyKey : @YES}];
		if (urls.count == 0) {
			return NULL;
		}
		NSMutableArray *paths = [NSMutableArray array];
		for (NSURL *u in urls) {
			[paths addObject:u.path];
		}
		return copyString([paths componentsJoinedByString:@"\n"]);
	}
}

int forge_set_clipboard_files(const char *newlinePaths) {
	@autoreleasepool {
		if (!newlinePaths || !*newlinePaths) {
			return 0;
		}
		NSArray<NSString *> *parts = [@(newlinePaths) componentsSeparatedByString:@"\n"];
		NSMutableArray<NSURL *> *urls = [NSMutableArray arrayWithCapacity:parts.count];
		for (NSString *p in parts) {
			if (p.length > 0) {
				[urls addObject:[NSURL fileURLWithPath:p]];
			}
		}
		if (urls.count == 0) {
			return 0;
		}
		NSPasteboard *pb = NSPasteboard.generalPasteboard;
		[pb clearContents];
		return [pb writeObjects:urls] ? 1 : 0;
	}
}

char *forge_trash_files(const char *newlinePaths) {
	@autoreleasepool {
		if (!newlinePaths || !*newlinePaths) {
			return NULL;
		}
		NSFileManager *fm = NSFileManager.defaultManager;
		NSArray<NSString *> *parts = [@(newlinePaths) componentsSeparatedByString:@"\n"];
		for (NSString *p in parts) {
			if (p.length == 0) {
				continue;
			}
			NSURL *url = [NSURL fileURLWithPath:p];
			NSError *err = nil;
			if (![fm trashItemAtURL:url resultingItemURL:nil error:&err]) {
				NSString *msg = err ? err.localizedDescription : @"moving to Trash failed";
				return copyString(msg);
			}
		}
		return NULL;
	}
}

char *forge_open_panel(const char *title, const char *extensions, int multiple, int folders) {
	@autoreleasepool {
		NSOpenPanel *panel = [NSOpenPanel openPanel];
		panel.message = @(title);
		panel.allowsMultipleSelection = multiple != 0;
		panel.canChooseDirectories = folders != 0;
		panel.canChooseFiles = folders == 0;
		panel.canCreateDirectories = folders != 0;
		if (!folders && extensions && *extensions) {
			NSArray<NSString *> *exts = [@(extensions) componentsSeparatedByString:@";"];
			NSMutableArray<UTType *> *types = [NSMutableArray arrayWithCapacity:exts.count];
			for (NSString *ext in exts) {
				if (ext.length == 0) {
					continue;
				}
				UTType *ut = [UTType typeWithFilenameExtension:ext];
				if (ut) {
					[types addObject:ut];
				}
			}
			if (types.count > 0) {
				panel.allowedContentTypes = types;
			}
		}
		if ([panel runModal] != NSModalResponseOK) {
			return NULL;
		}
		NSMutableArray *paths = [NSMutableArray array];
		for (NSURL *u in panel.URLs) {
			[paths addObject:u.path];
		}
		return copyString([paths componentsJoinedByString:@"\n"]);
	}
}

void forge_close(void) {
	@autoreleasepool {
		if (window) {
			window.contentView.layer.contents = nil;
			[window orderOut:nil];
		}
		if (mainSurfaceInited) {
			forge_surface_destroy(&mainSurface);
			mainSurfaceInited = 0;
		}
	}
}

void forge_alert(const char *message) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		NSAlert *alert = [NSAlert new];
		alert.messageText = @"Btelo Forge";
		alert.informativeText = @(message);
		alert.alertStyle = NSAlertStyleCritical;
		[alert runModal];
	}
}

void forge_free(void *p) {
	free(p);
}

// ---- Transparent Borderless Always-On-Top Overlay Panels ----

@interface ForgeOverlayPanel : NSPanel
@property(nonatomic) BOOL acceptsKey;
@property(nonatomic) int overlayID;
@end

@implementation ForgeOverlayPanel
- (BOOL)canBecomeKeyWindow {
	return self.acceptsKey;
}
- (BOOL)canBecomeMainWindow {
	return NO;
}
@end

@interface ForgeOverlayView : NSView <NSTextInputClient>
@property(nonatomic) int overlayID;
@property(nonatomic) BOOL typing;
@property(nonatomic) NSRect caret;
@property(nonatomic, strong) NSMutableAttributedString *marked;
@property(nonatomic, strong) NSCursor *overlayCursor;
@end

@implementation ForgeOverlayView
- (BOOL)acceptsFirstResponder {
	return YES;
}
- (BOOL)acceptsFirstMouse:(NSEvent *)e {
	return YES;
}
- (BOOL)isFlipped {
	return YES;
}
- (BOOL)wantsUpdateLayer {
	return YES;
}
- (NSPoint)pixels:(NSEvent *)e {
	NSPoint p = [self convertPoint:e.locationInWindow fromView:nil];
	CGFloat s = self.window.backingScaleFactor;
	return NSMakePoint(p.x * s, p.y * s);
}
- (void)mouseDown:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goOverlayPointer(self.overlayID, p.x, p.y, 0, 1, e.clickCount == 2, modifierBits(e.modifierFlags));
}
- (void)mouseUp:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goOverlayPointer(self.overlayID, p.x, p.y, 0, 0, 0, modifierBits(e.modifierFlags));
}
- (void)rightMouseDown:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goOverlayPointer(self.overlayID, p.x, p.y, 1, 1, 0, modifierBits(e.modifierFlags));
}
- (void)rightMouseUp:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goOverlayPointer(self.overlayID, p.x, p.y, 1, 0, 0, modifierBits(e.modifierFlags));
}
- (void)otherMouseDown:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goOverlayPointer(self.overlayID, p.x, p.y, 2, 1, 0, modifierBits(e.modifierFlags));
}
- (void)otherMouseUp:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goOverlayPointer(self.overlayID, p.x, p.y, 2, 0, 0, modifierBits(e.modifierFlags));
}
- (void)mouseMoved:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goOverlayMove(self.overlayID, p.x, p.y, modifierBits(e.modifierFlags));
}
- (void)mouseDragged:(NSEvent *)e {
	[self mouseMoved:e];
}
- (void)rightMouseDragged:(NSEvent *)e {
	[self mouseMoved:e];
}
- (void)otherMouseDragged:(NSEvent *)e {
	[self mouseMoved:e];
}
- (void)mouseExited:(NSEvent *)e {
	goOverlayLeave(self.overlayID);
}
- (void)scrollWheel:(NSEvent *)e {
	CGFloat dy = e.scrollingDeltaY;
	if (e.hasPreciseScrollingDeltas) {
		dy /= 40;
	}
	goOverlayWheel(self.overlayID, dy, modifierBits(e.modifierFlags));
}
- (void)keyDown:(NSEvent *)e {
	if (self.typing && !(e.modifierFlags & (NSEventModifierFlagCommand | NSEventModifierFlagControl))) {
		[self interpretKeyEvents:@[ e ]];
		return;
	}
	goOverlayKey(self.overlayID, e.keyCode, (char *)e.charactersIgnoringModifiers.UTF8String, modifierBits(e.modifierFlags));
}
- (void)keyUp:(NSEvent *)e {
	goOverlayKeyUp(self.overlayID, e.keyCode, (char *)e.charactersIgnoringModifiers.UTF8String);
}
- (void)cursorUpdate:(NSEvent *)e {
	[(self.overlayCursor ? self.overlayCursor : NSCursor.arrowCursor) set];
}
- (void)updateTrackingAreas {
	for (NSTrackingArea *a in self.trackingAreas) {
		[self removeTrackingArea:a];
	}
	NSTrackingAreaOptions o = NSTrackingMouseEnteredAndExited | NSTrackingMouseMoved |
	                          NSTrackingActiveAlways | NSTrackingCursorUpdate | NSTrackingInVisibleRect;
	[self addTrackingArea:[[NSTrackingArea alloc] initWithRect:NSZeroRect options:o owner:self userInfo:nil]];
	[super updateTrackingAreas];
}
- (void)insertText:(id)string replacementRange:(NSRange)range {
	self.marked = nil;
	NSString *s = [string isKindOfClass:NSAttributedString.class] ? [string string] : string;
	goOverlayTyped(self.overlayID, (char *)s.UTF8String);
}
- (void)setMarkedText:(id)string selectedRange:(NSRange)selected replacementRange:(NSRange)range {
	self.marked = [string isKindOfClass:NSAttributedString.class] ? [[NSMutableAttributedString alloc] initWithAttributedString:string]
	                                                              : [[NSMutableAttributedString alloc] initWithString:string];
}
- (void)unmarkText {
	self.marked = nil;
}
- (BOOL)hasMarkedText {
	return self.marked.length > 0;
}
- (NSRange)markedRange {
	return self.marked.length > 0 ? NSMakeRange(0, self.marked.length) : NSMakeRange(NSNotFound, 0);
}
- (NSRange)selectedRange {
	return NSMakeRange(NSNotFound, 0);
}
- (NSAttributedString *)attributedSubstringForProposedRange:(NSRange)range actualRange:(NSRangePointer)actual {
	return nil;
}
- (NSArray<NSAttributedStringKey> *)validAttributesForMarkedText {
	return @[];
}
- (NSUInteger)characterIndexForPoint:(NSPoint)point {
	return NSNotFound;
}
- (NSRect)firstRectForCharacterRange:(NSRange)range actualRange:(NSRangePointer)actual {
	CGFloat s = self.window.backingScaleFactor;
	NSRect r = NSMakeRect(self.caret.origin.x / s, self.caret.origin.y / s, 1, self.caret.size.height / s);
	return [self.window convertRectToScreen:[self convertRect:r toView:nil]];
}
- (void)doCommandBySelector:(SEL)sel {
	int key = 0, mods = 0;
	if (mapCommandSelector(sel, &key, &mods)) {
		goOverlayKeyCode(self.overlayID, key, mods);
	}
}
@end

@interface ForgeOverlayHandle : NSObject <NSWindowDelegate> {
@public
	ForgeSurface surface;
}
@property(nonatomic, strong) ForgeOverlayPanel *panel;
@property(nonatomic, strong) ForgeOverlayView *view;
@property(nonatomic, strong) NSRunningApplication *previousApp;
@property(nonatomic) BOOL closed;
@end

@implementation ForgeOverlayHandle
- (instancetype)init {
	self = [super init];
	if (self) {
		forge_surface_init(&surface);
	}
	return self;
}
- (void)dealloc {
	forge_surface_destroy(&surface);
}
- (BOOL)windowShouldClose:(NSWindow *)sender {
	goOverlayClosed(self.panel.overlayID);
	return NO;
}
- (void)windowDidResignKey:(NSNotification *)note {
	goOverlayResign(self.panel.overlayID);
}
@end

void *forge_overlay_open(int id, int x, int y, int width, int height, int activate, int clickThrough, int excluded) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		NSRect frame = desktopPixelsToCocoaRect(x, y, width, height);
		NSWindowStyleMask style = NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel;
		ForgeOverlayPanel *panel = [[ForgeOverlayPanel alloc] initWithContentRect:frame
		                                                                styleMask:style
		                                                                  backing:NSBackingStoreBuffered
		                                                                    defer:NO];
		panel.overlayID = id;
		panel.acceptsKey = activate != 0;
		panel.opaque = NO;
		panel.backgroundColor = NSColor.clearColor;
		panel.hasShadow = NO;
		panel.level = NSScreenSaverWindowLevel;
		panel.hidesOnDeactivate = NO;
		panel.releasedWhenClosed = NO;
		panel.ignoresMouseEvents = clickThrough != 0;
		panel.sharingType = excluded ? NSWindowSharingNone : NSWindowSharingReadOnly;
		panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
		                           NSWindowCollectionBehaviorFullScreenAuxiliary |
		                           NSWindowCollectionBehaviorStationary;

		ForgeOverlayView *view = [[ForgeOverlayView alloc] initWithFrame:NSMakeRect(0, 0, frame.size.width, frame.size.height)];
		view.overlayID = id;
		view.wantsLayer = YES;
		view.layer.opaque = NO;
		view.layer.backgroundColor = NSColor.clearColor.CGColor;
		view.layer.contentsGravity = kCAGravityResize;
		view.layer.contentsScale = panel.backingScaleFactor;
		panel.contentView = view;

		ForgeOverlayHandle *h = [[ForgeOverlayHandle alloc] init];
		h.panel = panel;
		h.view = view;
		panel.delegate = h;

		if (activate) {
			[panel makeFirstResponder:view];
			[panel makeKeyAndOrderFront:nil];
			[NSApp activateIgnoringOtherApps:YES];
		} else {
			[panel orderFrontRegardless];
		}
		return (__bridge_retained void *)h;
	}
}

double forge_overlay_scale(void *handle) {
	ForgeOverlayHandle *h = (__bridge ForgeOverlayHandle *)handle;
	return (h && h.panel) ? h.panel.backingScaleFactor : 1.0;
}

int forge_overlay_present(void *handle, const void *rgba, int width, int height) {
	@autoreleasepool {
		ForgeOverlayHandle *h = (__bridge ForgeOverlayHandle *)handle;
		if (!h || h.closed) {
			return 0;
		}
		IOSurfaceRef surf = forge_surface_write(&h->surface, rgba, width, height);
		if (!surf) {
			return 0;
		}
		if ([NSThread isMainThread]) {
			if (!h.closed && h.view) {
				[CATransaction begin];
				[CATransaction setDisableActions:YES];
				h.view.layer.contents = (__bridge id)surf;
				[CATransaction commit];
			}
			CFRelease(surf);
		} else {
			dispatch_async(dispatch_get_main_queue(), ^{
				if (!h.closed && h.view) {
					[CATransaction begin];
					[CATransaction setDisableActions:YES];
					h.view.layer.contents = (__bridge id)surf;
					[CATransaction commit];
				}
				CFRelease(surf);
			});
		}
		return 1;
	}
}

void forge_overlay_set_click_through(void *handle, int on) {
	ForgeOverlayHandle *h = (__bridge ForgeOverlayHandle *)handle;
	if (h && h.panel) {
		h.panel.ignoresMouseEvents = on != 0;
	}
}

void forge_overlay_take_keyboard(void *handle, int on) {
	@autoreleasepool {
		ForgeOverlayHandle *h = (__bridge ForgeOverlayHandle *)handle;
		if (!h || !h.panel) {
			return;
		}
		if (on) {
			NSRunningApplication *front = NSWorkspace.sharedWorkspace.frontmostApplication;
			if (front && front.processIdentifier != NSProcessInfo.processInfo.processIdentifier) {
				h.previousApp = front;
			}
			h.panel.acceptsKey = YES;
			[h.panel makeFirstResponder:h.view];
			[h.panel makeKeyAndOrderFront:nil];
			[NSApp activateIgnoringOtherApps:YES];
		} else {
			BOOL wasKey = h.panel.isKeyWindow;
			h.panel.acceptsKey = NO;
			if (wasKey && h.previousApp && !h.previousApp.terminated) {
				[h.previousApp activateWithOptions:0];
			}
			h.previousApp = nil;
		}
	}
}

void forge_overlay_raise(void *handle) {
	@autoreleasepool {
		ForgeOverlayHandle *h = (__bridge ForgeOverlayHandle *)handle;
		if (!h || !h.panel) {
			return;
		}
		if (h.panel.acceptsKey) {
			[h.panel makeKeyAndOrderFront:nil];
		} else {
			[h.panel orderFrontRegardless];
		}
	}
}

void forge_overlay_move(void *handle, int x, int y, int width, int height) {
	@autoreleasepool {
		ForgeOverlayHandle *h = (__bridge ForgeOverlayHandle *)handle;
		if (!h || !h.panel) {
			return;
		}
		NSRect frame = desktopPixelsToCocoaRect(x, y, width, height);
		[h.panel setFrame:frame display:NO];
		h.view.layer.contentsScale = h.panel.backingScaleFactor;
	}
}

void forge_overlay_cursor(void *handle, int kind) {
	ForgeOverlayHandle *h = (__bridge ForgeOverlayHandle *)handle;
	if (h && h.view) {
		h.view.overlayCursor = cursorForKind(kind);
		[h.view.overlayCursor set];
	}
}

void forge_overlay_text_input(void *handle, int on, double x, double y, double height) {
	ForgeOverlayHandle *h = (__bridge ForgeOverlayHandle *)handle;
	if (!h || !h.view) {
		return;
	}
	if (!on && h.view.typing) {
		[h.view.inputContext discardMarkedText];
		h.view.marked = nil;
	}
	h.view.typing = on != 0;
	h.view.caret = NSMakeRect(x, y, 1, height);
	if (on) {
		[h.view.inputContext invalidateCharacterCoordinates];
	}
}

void forge_overlay_close(void *handle) {
	@autoreleasepool {
		ForgeOverlayHandle *h = (__bridge_transfer ForgeOverlayHandle *)handle;
		if (!h) {
			return;
		}
		h.closed = YES;
		if (h.view) {
			h.view.layer.contents = nil;
		}
		if (h.panel) {
			h.panel.delegate = nil;
			[h.panel orderOut:nil];
			h.panel = nil;
		}
		h.view = nil;
		h.previousApp = nil;
	}
}
