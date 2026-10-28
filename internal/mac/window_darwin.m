#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#include <stdlib.h>
#include <string.h>
#include "window.h"
#include "_cgo_export.h"

static NSWindow *window;
static int closed;
static NSCursor *currentCursor;

// Modifier bits shared with the Go side: 1 Command or Control, 2 Shift, 4 Option.
static int modifierBits(NSEventModifierFlags f) {
	int m = 0;
	if (f & (NSEventModifierFlagCommand | NSEventModifierFlagControl)) m |= 1;
	if (f & NSEventModifierFlagShift) m |= 2;
	if (f & NSEventModifierFlagOption) m |= 4;
	return m;
}

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
	return NO;
}
- (void)windowDidResize:(NSNotification *)note {
	goRefresh();
}
- (void)windowDidChangeBackingProperties:(NSNotification *)note {
	window.contentView.layer.contentsScale = window.backingScaleFactor;
	goRefresh();
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
- (void)mouseDown:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 0, 1, e.clickCount == 2);
}
- (void)mouseUp:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 0, 0, 0);
}
- (void)rightMouseDown:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 1, 1, 0);
}
- (void)rightMouseUp:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goPointer(p.x, p.y, 1, 0, 0);
}
- (void)mouseMoved:(NSEvent *)e {
	NSPoint p = [self pixels:e];
	goMove(p.x, p.y);
}
- (void)mouseDragged:(NSEvent *)e {
	[self mouseMoved:e];
}
- (void)rightMouseDragged:(NSEvent *)e {
	[self mouseMoved:e];
}
- (void)scrollWheel:(NSEvent *)e {
	CGFloat dy = e.scrollingDeltaY;
	if (e.hasPreciseScrollingDeltas) {
		dy /= 40;
	}
	goWheel(dy, modifierBits(e.modifierFlags));
}
- (void)keyDown:(NSEvent *)e {
	if (self.typing && !(e.modifierFlags & NSEventModifierFlagCommand)) {
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
	NSTrackingAreaOptions o = NSTrackingMouseMoved | NSTrackingActiveAlways | NSTrackingCursorUpdate | NSTrackingInVisibleRect;
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
	static const struct {
		const char *name;
		int key, mods;
	} commands[] = {
	    {"deleteBackward:", 0x08, 0},  {"deleteForward:", 0x2e, 0},     {"insertNewline:", 0x0d, 0},
	    {"insertLineBreak:", 0x0d, 0}, {"insertTab:", 0x09, 0},         {"cancelOperation:", 0x1b, 0},
	    {"moveLeft:", 0x25, 0},        {"moveRight:", 0x27, 0},         {"moveUp:", 0x26, 0},
	    {"moveDown:", 0x28, 0},        {"moveToBeginningOfLine:", 0x24, 0}, {"moveToEndOfLine:", 0x23, 0},
	    {"moveLeftAndModifySelection:", 0x25, 2}, {"moveRightAndModifySelection:", 0x27, 2},
	    {"moveToBeginningOfLineAndModifySelection:", 0x24, 2}, {"moveToEndOfLineAndModifySelection:", 0x23, 2},
	    {"moveUpAndModifySelection:", 0x26, 2}, {"moveDownAndModifySelection:", 0x28, 2},
	    {"pageUp:", 0x21, 0}, {"pageDown:", 0x22, 0}, {"scrollPageUp:", 0x21, 0}, {"scrollPageDown:", 0x22, 0},
	    {"moveToBeginningOfDocument:", 0x24, 1}, {"moveToEndOfDocument:", 0x23, 1},
	    {"moveToBeginningOfDocumentAndModifySelection:", 0x24, 3}, {"moveToEndOfDocumentAndModifySelection:", 0x23, 3},
	};
	const char *name = sel_getName(sel);
	for (size_t i = 0; i < sizeof commands / sizeof commands[0]; i++) {
		if (strcmp(name, commands[i].name) == 0) {
			goKeyCode(commands[i].key, commands[i].mods);
			return;
		}
	}
}
@end

int forge_open(const char *title, int width, int height, int background) {
	@autoreleasepool {
		[NSApplication sharedApplication];
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
		ForgeView *view = [[ForgeView alloc] initWithFrame:frame];
		view.wantsLayer = YES;
		view.layer.contentsGravity = kCAGravityResize;
		view.layer.contentsScale = window.backingScaleFactor;
		view.layer.backgroundColor = NSColor.blackColor.CGColor;
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
	return window.isKeyWindow ? 1 : 0;
}

void forge_wake(void) {
	@autoreleasepool {
		NSEvent *e = [NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
		[NSApp postEvent:e atStart:NO];
	}
}

void forge_size(int *width, int *height) {
	NSSize s = window.contentView.bounds.size;
	CGFloat k = window.backingScaleFactor;
	*width = (int)(s.width * k);
	*height = (int)(s.height * k);
}

double forge_scale(void) {
	return window.backingScaleFactor;
}

void forge_present(const void *rgba, int width, int height) {
	@autoreleasepool {
		CGColorSpaceRef space = CGColorSpaceCreateDeviceRGB();
		CFDataRef data = CFDataCreate(NULL, rgba, (CFIndex)width * height * 4);
		CGDataProviderRef provider = CGDataProviderCreateWithCFData(data);
		CGImageRef image = CGImageCreate(width, height, 8, 32, (size_t)width * 4, space, kCGImageAlphaNoneSkipLast | kCGBitmapByteOrderDefault,
		                                 provider, NULL, false, kCGRenderingIntentDefault);
		[CATransaction begin];
		[CATransaction setDisableActions:YES];
		window.contentView.layer.contents = (__bridge id)image;
		[CATransaction commit];
		CGImageRelease(image);
		CGDataProviderRelease(provider);
		CFRelease(data);
		CGColorSpaceRelease(space);
	}
}

void forge_title(const char *title) {
	window.title = @(title);
}

void forge_cursor(int kind) {
	switch (kind) {
	case 1:
		currentCursor = NSCursor.pointingHandCursor;
		break;
	case 2:
		currentCursor = NSCursor.IBeamCursor;
		break;
	case 3:
		currentCursor = NSCursor.resizeLeftRightCursor;
		break;
	case 4:
		currentCursor = NSCursor.openHandCursor;
		break;
	default:
		currentCursor = NSCursor.arrowCursor;
	}
	[currentCursor set];
}

void forge_text_input(int on, double x, double y, double height) {
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

char *forge_open_panel(const char *title, const char *extensions, int multiple, int folders) {
	@autoreleasepool {
		NSOpenPanel *panel = [NSOpenPanel openPanel];
		panel.message = @(title);
		panel.allowsMultipleSelection = multiple != 0;
		panel.canChooseDirectories = folders != 0;
		panel.canChooseFiles = folders == 0;
		panel.canCreateDirectories = folders != 0;
		if (!folders && extensions && *extensions) {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
			panel.allowedFileTypes = [@(extensions) componentsSeparatedByString:@";"];
#pragma clang diagnostic pop
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
	[window orderOut:nil];
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
