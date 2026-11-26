//go:build darwin

package files

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// forge_trash_item moves path to the macOS Trash using NSFileManager, supporting both the user's ~/.Trash and
// external volumes' .Trashes/<uid>, while recording Finder "Put Back" metadata and returning the trashed path.
static int forge_trash_item(const char *path, char **outStored, char **outErr) {
	*outStored = NULL;
	*outErr = NULL;
	@autoreleasepool {
		NSString *nsPath = [NSString stringWithUTF8String:path];
		NSURL *srcURL = [NSURL fileURLWithPath:nsPath];
		NSURL *resultURL = nil;
		NSError *error = nil;
		BOOL ok = [[NSFileManager defaultManager] trashItemAtURL:srcURL resultingItemURL:&resultURL error:&error];
		if (!ok) {
			NSString *msg = error ? error.localizedDescription : @"the Trash did not take it";
			*outErr = strdup([msg UTF8String]);
			return 0;
		}
		if (resultURL != nil && resultURL.path != nil) {
			*outStored = strdup([resultURL.path UTF8String]);
		}
		return 1;
	}
}
*/
import "C"

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ErrNoBin is an item on a volume without a Trash (such as a network share or read-only volume).
var ErrNoBin = errors.New("this drive has no Trash")

// CanTrash reports whether path resides on a local, writable macOS volume that supports Trash.
func CanTrash(path string) bool {
	if path == "" {
		return false
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		// Broken symlink or not-yet-resolved child: check parent directory.
		if err := unix.Statfs(filepath.Dir(path), &st); err != nil {
			return false
		}
	}
	return st.Flags&unix.MNT_LOCAL != 0 && st.Flags&unix.MNT_RDONLY == 0
}

// Trash moves one item to the macOS Trash via NSFileManager, preserving Finder's "Put Back" metadata and returning
// the stored path in the Trash so Restore can put it back on Undo.
func Trash(path string) (Trashed, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Trashed{}, err
	}
	if !CanTrash(abs) {
		return Trashed{}, ErrNoBin
	}
	if _, err := os.Lstat(abs); err != nil {
		return Trashed{}, err
	}
	cPath := C.CString(abs)
	defer C.free(unsafe.Pointer(cPath))

	var cStored *C.char
	var cErr *C.char
	ok := C.forge_trash_item(cPath, &cStored, &cErr)
	if cErr != nil {
		defer C.free(unsafe.Pointer(cErr))
	}
	if cStored != nil {
		defer C.free(unsafe.Pointer(cStored))
	}
	if ok == 0 {
		if cErr != nil {
			return Trashed{}, errors.New(C.GoString(cErr))
		}
		return Trashed{}, errors.New("the Trash did not take it")
	}
	stored := ""
	if cStored != nil {
		stored = C.GoString(cStored)
	}
	return Trashed{Original: abs, Stored: stored}, nil
}
