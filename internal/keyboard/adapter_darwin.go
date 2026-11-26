package keyboard

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices
#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#import <pthread.h>

// sentMark tells the keys this app sends from the ones pressed: the event tap sees both.
static const int64_t sentMark = 0x4254454c4f;

// Device-dependent modifier masks in the low 16 bits of CGEventFlags distinguish left and right keys
// when both sides are held simultaneously.
enum {
	forgeDevLCtrl  = 0x00000001,
	forgeDevLShift = 0x00000002,
	forgeDevRShift = 0x00000004,
	forgeDevLCmd   = 0x00000008,
	forgeDevRCmd   = 0x00000010,
	forgeDevLAlt   = 0x00000020,
	forgeDevRAlt   = 0x00000040,
	forgeDevRCtrl  = 0x00002000,
	forgeDevMask   = 0x0000ffff,
};

extern int goKeyEvent(int code, int down);

static pthread_mutex_t tapLock = PTHREAD_MUTEX_INITIALIZER;
static CFMachPortRef tap;
static CFRunLoopRef tapLoop;
static int tapStopping;

static int modifierIsDown(int code, CGEventFlags flags) {
	int hasDev = (flags & forgeDevMask) != 0;
	switch (code) {
	case 55: return hasDev ? (flags & forgeDevLCmd) != 0   : (flags & kCGEventFlagMaskCommand) != 0;
	case 54: return hasDev ? (flags & forgeDevRCmd) != 0   : (flags & kCGEventFlagMaskCommand) != 0;
	case 56: return hasDev ? (flags & forgeDevLShift) != 0 : (flags & kCGEventFlagMaskShift) != 0;
	case 60: return hasDev ? (flags & forgeDevRShift) != 0 : (flags & kCGEventFlagMaskShift) != 0;
	case 58: return hasDev ? (flags & forgeDevLAlt) != 0   : (flags & kCGEventFlagMaskAlternate) != 0;
	case 61: return hasDev ? (flags & forgeDevRAlt) != 0   : (flags & kCGEventFlagMaskAlternate) != 0;
	case 59: return hasDev ? (flags & forgeDevLCtrl) != 0  : (flags & kCGEventFlagMaskControl) != 0;
	case 62: return hasDev ? (flags & forgeDevRCtrl) != 0  : (flags & kCGEventFlagMaskControl) != 0;
	}
	return -1;
}

static CGEventRef onEvent(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *info) {
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		if (tap != NULL) {
			CGEventTapEnable(tap, true); // the system takes a tap away when it answers late; it is put back at once
		}
		return event;
	}
	if (CGEventGetIntegerValueField(event, kCGEventSourceUserData) == sentMark) {
		return event;
	}
	int code = (int)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
	int down = type == kCGEventKeyDown;
	if (type == kCGEventFlagsChanged) {
		down = modifierIsDown(code, CGEventGetFlags(event));
		if (down < 0) {
			return event;
		}
	}
	return goKeyEvent(code, down) ? NULL : event;
}

// forge_trusted checks Accessibility trust, prompting the user only when prompt != 0.
static int forge_trusted(int prompt) {
	if (AXIsProcessTrusted()) {
		return 1;
	}
	if (!prompt) {
		return 0;
	}
	NSDictionary *options = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
	return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options) ? 1 : 0;
}

static int forge_tap_create(void) {
	pthread_mutex_lock(&tapLock);
	tapStopping = 0;
	tapLoop = NULL;
	CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) | CGEventMaskBit(kCGEventKeyUp) | CGEventMaskBit(kCGEventFlagsChanged);
	tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault, mask, onEvent, NULL);
	int ok = tap != NULL;
	pthread_mutex_unlock(&tapLock);
	return ok;
}

// forge_tap_run stays on its thread until forge_tap_stop, safely handling Stop called before CFRunLoopRun begins.
static void forge_tap_run(void) {
	CFRunLoopRef loop = CFRunLoopGetCurrent();
	pthread_mutex_lock(&tapLock);
	if (tapStopping || tap == NULL) {
		if (tap != NULL) {
			CFMachPortInvalidate(tap);
			CFRelease(tap);
			tap = NULL;
		}
		tapLoop = NULL;
		tapStopping = 0;
		pthread_mutex_unlock(&tapLock);
		return;
	}
	CFRunLoopSourceRef source = CFMachPortCreateRunLoopSource(NULL, tap, 0);
	tapLoop = loop;
	CFRunLoopAddSource(loop, source, kCFRunLoopCommonModes);
	CGEventTapEnable(tap, true);
	pthread_mutex_unlock(&tapLock);

	CFRunLoopRun();

	pthread_mutex_lock(&tapLock);
	CFRunLoopRemoveSource(loop, source, kCFRunLoopCommonModes);
	CFRelease(source);
	if (tap != NULL) {
		CFMachPortInvalidate(tap);
		CFRelease(tap);
		tap = NULL;
	}
	tapLoop = NULL;
	tapStopping = 0;
	pthread_mutex_unlock(&tapLock);
}

static void forge_tap_stop(void) {
	pthread_mutex_lock(&tapLock);
	tapStopping = 1;
	if (tapLoop != NULL) {
		CFRunLoopStop(tapLoop);
		CFRunLoopWakeUp(tapLoop);
	}
	pthread_mutex_unlock(&tapLock);
}

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

static void forge_post_return(CGEventSourceRef source) {
	const CGKeyCode kReturn = 36;
	for (int down = 1; down >= 0; down--) {
		CGEventRef event = CGEventCreateKeyboardEvent(source, kReturn, down != 0);
		CGEventSetFlags(event, 0);
		CGEventSetIntegerValueField(event, kCGEventSourceUserData, sentMark);
		CGEventPost(kCGHIDEventTap, event);
		CFRelease(event);
	}
}

// forge_type_chunk enters text without line breaks, a piece at a time: an event carries at most 20 UTF-16 units.
static void forge_type_chunk(CGEventSourceRef source, const uint16_t *text, int count) {
	for (int at = 0; at < count; at += 20) {
		int n = count - at < 20 ? count - at : 20;
		for (int down = 1; down >= 0; down--) {
			CGEventRef event = CGEventCreateKeyboardEvent(source, 0, down != 0);
			CGEventKeyboardSetUnicodeString(event, n, text + at);
			CGEventSetFlags(event, 0);
			CGEventSetIntegerValueField(event, kCGEventSourceUserData, sentMark);
			CGEventPost(kCGHIDEventTap, event);
			CFRelease(event);
		}
	}
}

// forge_type enters UTF-16 text whatever the keyboard layout; line breaks ('\n') are posted as real Return keys.
static void forge_type(const uint16_t *text, int count) {
	CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
	int start = 0;
	for (int i = 0; i < count; i++) {
		if (text[i] == '\n' || text[i] == '\r') {
			if (i > start) {
				forge_type_chunk(source, text + start, i - start);
			}
			if (text[i] == '\r' && i + 1 < count && text[i + 1] == '\n') {
				i++;
			}
			forge_post_return(source);
			start = i + 1;
		}
	}
	if (count > start) {
		forge_type_chunk(source, text + start, count - start);
	}
	if (source) CFRelease(source);
}

static int forge_key_down(int code) {
	return CGEventSourceKeyState(kCGEventSourceStateHIDSystemState, code) ? 1 : 0;
}

*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf16"
	"unsafe"
)

type macAdapter struct {
	mu       sync.Mutex
	on       bool
	done     chan struct{}
	prompted atomic.Bool
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
	prompt := C.int(0)
	if a.prompted.CompareAndSwap(false, true) {
		prompt = 1
	}
	if C.forge_trusted(prompt) == 0 {
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
	C.forge_tap_stop()
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

// splitArgs splits a command-line argument string, respecting single and double quotes.
func splitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inWord = true
		case unicode.IsSpace(r):
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args
}

// openPlan builds the command to open target with optional args on macOS:
//   - URLs ("https://...", "mailto:...") use `open <url>`
//   - Existing executable regular files with args run directly (detached)
//   - Existing files/folders/.app bundles use `open <path> [--args ...]`
//   - Bare application names ("Safari", "Terminal") use `open -a <target> [--args ...]`
func openPlan(target, args string) (prog string, argv []string, detached bool) {
	parsed := splitArgs(args)
	if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
		cmd := []string{target}
		if len(parsed) > 0 {
			cmd = append(cmd, append([]string{"--args"}, parsed...)...)
		}
		return "open", cmd, false
	}
	if st, err := os.Stat(target); err == nil {
		if !st.IsDir() && st.Mode().Perm()&0o111 != 0 && len(parsed) > 0 {
			return target, parsed, true
		}
		cmd := []string{target}
		if len(parsed) > 0 {
			cmd = append(cmd, append([]string{"--args"}, parsed...)...)
		}
		return "open", cmd, false
	}
	cmd := []string{"-a", target}
	if len(parsed) > 0 {
		cmd = append(cmd, append([]string{"--args"}, parsed...)...)
	}
	return "open", cmd, false
}

func (a *macAdapter) Open(target, args string) error {
	prog, argv, detached := openPlan(target, args)
	if detached {
		cmd := exec.Command(prog, argv...)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		return cmd.Process.Release()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, prog, argv...).CombinedOutput()
	if err != nil {
		// If `open -a` failed for a CLI command on PATH (e.g. "code"), try launching it directly.
		if prog == "open" && len(argv) >= 2 && argv[0] == "-a" {
			if bin, lerr := exec.LookPath(target); lerr == nil {
				cmd := exec.Command(bin, splitArgs(args)...)
				if serr := cmd.Start(); serr == nil {
					return cmd.Process.Release()
				}
			}
		}
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
