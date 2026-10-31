package keyboard

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices -mmacosx-version-min=11.0
#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>

// sentMark tells the keys this app sends from the ones pressed: the event tap sees both.
static const int64_t sentMark = 0x4254454c4f;

extern int goKeyEvent(int code, int down);

static CFMachPortRef tap;
static CFRunLoopRef tapLoop;

static CGEventRef onEvent(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *info) {
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		CGEventTapEnable(tap, true); // the system takes a tap away when it answers late; it is put back at once
		return event;
	}
	if (CGEventGetIntegerValueField(event, kCGEventSourceUserData) == sentMark) {
		return event;
	}
	int code = (int)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
	int down = type == kCGEventKeyDown;
	if (type == kCGEventFlagsChanged) {
		CGEventFlags flags = CGEventGetFlags(event);
		switch (code) {
		case 54: case 55: down = (flags & kCGEventFlagMaskCommand) != 0; break;
		case 56: case 60: down = (flags & kCGEventFlagMaskShift) != 0; break;
		case 58: case 61: down = (flags & kCGEventFlagMaskAlternate) != 0; break;
		case 59: case 62: down = (flags & kCGEventFlagMaskControl) != 0; break;
		default: return event;
		}
	}
	return goKeyEvent(code, down) ? NULL : event;
}

// forge_trusted says whether the app may watch keys, and asks the user the first time.
static int forge_trusted(void) {
	NSDictionary *options = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
	return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options) ? 1 : 0;
}

static int forge_tap_create(void) {
	CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) | CGEventMaskBit(kCGEventKeyUp) | CGEventMaskBit(kCGEventFlagsChanged);
	tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault, mask, onEvent, NULL);
	return tap != NULL;
}

// forge_tap_run stays on its thread until forge_tap_stop.
static void forge_tap_run(void) {
	CFRunLoopSourceRef source = CFMachPortCreateRunLoopSource(NULL, tap, 0);
	tapLoop = CFRunLoopGetCurrent();
	CFRunLoopAddSource(tapLoop, source, kCFRunLoopCommonModes);
	CGEventTapEnable(tap, true);
	CFRunLoopRun();
	CFRunLoopRemoveSource(tapLoop, source, kCFRunLoopCommonModes);
	CFRelease(source);
	CFMachPortInvalidate(tap);
	CFRelease(tap);
	tap = NULL;
	tapLoop = NULL;
}

static void forge_tap_stop(CFRunLoopRef loop) { CFRunLoopStop(loop); }
static CFRunLoopRef forge_tap_loop(void) { return tapLoop; }

static CGEventFlags forge_flag(int code) {
	switch (code) {
	case 54: case 55: return kCGEventFlagMaskCommand;
	case 56: case 60: return kCGEventFlagMaskShift;
	case 58: case 61: return kCGEventFlagMaskAlternate;
	case 59: case 62: return kCGEventFlagMaskControl;
	}
	return 0;
}

// forge_send posts keys in order; each carries the modifiers down at that moment, as posted keys do not add theirs.
static void forge_send(const uint16_t *codes, const int *downs, int count) {
	CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
	CGEventFlags flags = CGEventSourceFlagsState(kCGEventSourceStateHIDSystemState);
	for (int i = 0; i < count; i++) {
		CGEventFlags flag = forge_flag(codes[i]);
		flags = downs[i] ? (flags | flag) : (flags & ~flag);
		CGEventRef event = CGEventCreateKeyboardEvent(source, codes[i], downs[i] != 0);
		CGEventSetFlags(event, flags);
		CGEventSetIntegerValueField(event, kCGEventSourceUserData, sentMark);
		CGEventPost(kCGHIDEventTap, event);
		CFRelease(event);
	}
	if (source) CFRelease(source);
}

// forge_type enters text whatever the layout, a piece at a time: an event carries at most 20 UTF-16 units.
static void forge_type(const uint16_t *text, int count) {
	for (int at = 0; at < count; at += 20) {
		int n = count - at < 20 ? count - at : 20;
		for (int down = 1; down >= 0; down--) {
			CGEventRef event = CGEventCreateKeyboardEvent(NULL, 0, down != 0);
			CGEventKeyboardSetUnicodeString(event, n, text + at);
			CGEventSetFlags(event, 0);
			CGEventSetIntegerValueField(event, kCGEventSourceUserData, sentMark);
			CGEventPost(kCGHIDEventTap, event);
			CFRelease(event);
		}
	}
}

static int forge_key_down(int code) {
	return CGEventSourceKeyState(kCGEventSourceStateHIDSystemState, code) ? 1 : 0;
}

*/
import "C"

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"
)

type macAdapter struct {
	mu   sync.Mutex
	on   bool
	done chan struct{}
}

// NewAdapter is the system's adapter.
func NewAdapter() Adapter { return &macAdapter{} }

var (
	tapMu      sync.Mutex
	tapHandler func(uint16, bool) bool
)

//export goKeyEvent
func goKeyEvent(code, down C.int) C.int {
	tapMu.Lock()
	handle := tapHandler
	tapMu.Unlock()
	vk, ok := macVirtualKeys[uint16(code)]
	if !ok || handle == nil || !handle(vk, down != 0) {
		return 0
	}
	return 1
}

func (a *macAdapter) Start(handle func(uint16, bool) bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.on {
		return nil
	}
	if C.forge_trusted() == 0 {
		return ErrPermission
	}
	if C.forge_tap_create() == 0 {
		return ErrPermission
	}
	tapMu.Lock()
	tapHandler = handle
	tapMu.Unlock()
	a.on, a.done = true, make(chan struct{})
	running := make(chan struct{})
	go func() {
		defer close(a.done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		close(running)
		C.forge_tap_run()
	}()
	<-running
	return nil
}

func (a *macAdapter) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.on {
		return
	}
	for {
		if loop := C.forge_tap_loop(); loop != 0 {
			C.forge_tap_stop(loop)
			break
		}
		runtime.Gosched() // the run loop's thread has only just begun
	}
	<-a.done
	tapMu.Lock()
	tapHandler = nil
	tapMu.Unlock()
	a.on = false
}

func (a *macAdapter) Send(keys []Stroke) error {
	codes := make([]C.uint16_t, 0, len(keys))
	downs := make([]C.int, 0, len(keys))
	for _, k := range keys {
		code, ok := macKeyCodes[k.VK]
		if !ok {
			continue // the key that masks Alt and Win on Windows has no part here
		}
		down := C.int(0)
		if k.Down {
			down = 1
		}
		codes, downs = append(codes, C.uint16_t(code)), append(downs, down)
	}
	if len(codes) > 0 {
		C.forge_send(&codes[0], &downs[0], C.int(len(codes)))
	}
	return nil
}

func (a *macAdapter) Type(text string) error {
	units := utf16.Encode([]rune(text))
	if len(units) > 0 {
		C.forge_type((*C.uint16_t)(unsafe.Pointer(&units[0])), C.int(len(units)))
	}
	return nil
}

func (a *macAdapter) Open(target, args string) error {
	command := []string{target}
	if args != "" {
		command = append(command, append([]string{"--args"}, strings.Fields(args)...)...)
	}
	if out, err := exec.Command("open", command...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", target, strings.TrimSpace(string(out)))
	}
	return nil
}

func (a *macAdapter) KeyDown(vk uint16) bool {
	code, ok := macKeyCodes[vk]
	return ok && C.forge_key_down(C.int(code)) != 0
}

// CanRemap turns down CapsLock: macOS changes its state before an event tap hears of it, so it cannot be kept from
// the programs. System Settings → Keyboard → Modifier Keys remaps it.
func (a *macAdapter) CanRemap(vk uint16) bool { return vk != vkCapsLock }
