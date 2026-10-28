package files

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	shell32       = syscall.NewLazyDLL("shell32.dll")
	shFileOp      = shell32.NewProc("SHFileOperationW")
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	getDriveTypeW = kernel32.NewProc("GetDriveTypeW")
	getLongPathW  = kernel32.NewProc("GetLongPathNameW")
)

// ErrNoBin is an item on a drive without a Recycle Bin, such as a network share or a USB stick.
var ErrNoBin = errors.New("this drive has no Recycle Bin")

// CanTrash reports whether the drive path is on keeps a Recycle Bin.
func CanTrash(path string) bool {
	vol := filepath.VolumeName(path)
	if len(vol) != 2 || vol[1] != ':' { // a network share
		return false
	}
	root, _ := syscall.UTF16PtrFromString(vol + `\`)
	const fixed = 3
	t, _, _ := getDriveTypeW.Call(uintptr(unsafe.Pointer(root)))
	return t == fixed
}

// Trash moves one item to the Recycle Bin as Explorer does, without asking or showing anything, and finds where the
// bin keeps it, so it can be put back.
func Trash(path string) (Trashed, error) {
	path, err := longPath(path)
	if err != nil {
		return Trashed{}, err
	}
	if !CanTrash(path) {
		return Trashed{}, ErrNoBin
	}
	if _, err := os.Lstat(path); err != nil {
		return Trashed{}, err
	}
	from := utf16.Encode([]rune(path))
	from = append(from, 0, 0)
	const foDelete = 3
	const fofSilent, fofNoConfirmation, fofAllowUndo, fofNoErrorUI = 0x4, 0x10, 0x40, 0x400
	op := struct {
		Hwnd         uintptr
		Func         uint32
		From, To     *uint16
		Flags        uint16
		Aborted      int32
		NameMappings uintptr
		Title        *uint16
	}{Func: foDelete, From: &from[0], Flags: fofSilent | fofNoConfirmation | fofAllowUndo | fofNoErrorUI}
	began := time.Now().Add(-2 * time.Second)
	runtime.LockOSThread()
	r, _, _ := shFileOp.Call(uintptr(unsafe.Pointer(&op)))
	runtime.UnlockOSThread()
	if r != 0 || op.Aborted != 0 {
		return Trashed{}, fmt.Errorf("the Recycle Bin did not take it (error 0x%x)", r)
	}
	if _, err := os.Lstat(path); err == nil {
		return Trashed{}, errors.New("the Recycle Bin did not take it")
	}
	// The shell notes the item in the bin a moment after it returns: look until it has, or for long enough that the
	// item must have been too large for the bin, and so deleted for good.
	for wait := time.Now().Add(binNoteWait); ; time.Sleep(25 * time.Millisecond) {
		if t, ok := findInBin(path, began); ok {
			return t, nil
		}
		if time.Now().After(wait) {
			return Trashed{Original: path}, nil
		}
	}
}

// binNoteWait is how long Trash looks for the bin's note of an item it took.
const binNoteWait = 3 * time.Second

// longPath is the whole path with each folder's long name, as the bin notes it, where a short name such as
// ADMINI~1 may have been given.
func longPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 1024)
	for {
		n, _, _ := getLongPathW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		switch {
		case n == 0:
			return path, nil // it cannot be read: the bin will say why
		case int(n) > len(buf):
			buf = make([]uint16, n)
			continue
		}
		return syscall.UTF16ToString(buf[:n]), nil
	}
}

// findInBin finds the item the Recycle Bin took from path since a moment: its $I file notes the original path and
// when it was deleted, and its $R file with the same suffix is the item.
func findInBin(path string, since time.Time) (Trashed, bool) {
	sid, err := userSID()
	if err != nil {
		return Trashed{}, false
	}
	bin := filepath.Join(filepath.VolumeName(path)+`\`, "$Recycle.Bin", sid)
	des, err := os.ReadDir(bin)
	if err != nil {
		return Trashed{}, false
	}
	var best Trashed
	var bestAt time.Time
	for _, de := range des {
		name := de.Name()
		if !strings.HasPrefix(name, "$I") {
			continue
		}
		info := filepath.Join(bin, name)
		original, at, ok := readBinInfo(info)
		if !ok || !strings.EqualFold(original, path) || at.Before(since) || !at.After(bestAt) {
			continue
		}
		stored := filepath.Join(bin, "$R"+name[2:])
		if _, err := os.Lstat(stored); err != nil {
			continue
		}
		best, bestAt = Trashed{Original: path, Stored: stored, Info: info}, at
	}
	return best, best.Stored != ""
}

// readBinInfo reads a $I file: a version, the size, when it was deleted and the original path, written whole in
// version 1 (Windows Vista to 8) and with its length in version 2 (Windows 10 on).
func readBinInfo(path string) (string, time.Time, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 24 {
		return "", time.Time{}, false
	}
	version := binary.LittleEndian.Uint64(b[0:])
	ft := syscall.Filetime{LowDateTime: binary.LittleEndian.Uint32(b[16:]), HighDateTime: binary.LittleEndian.Uint32(b[20:])}
	at := time.Unix(0, ft.Nanoseconds())
	var name []byte
	switch version {
	case 1:
		name = b[24:]
	case 2:
		if len(b) < 28 {
			return "", time.Time{}, false
		}
		n := int(binary.LittleEndian.Uint32(b[24:]))
		if 28+2*n > len(b) {
			return "", time.Time{}, false
		}
		name = b[28 : 28+2*n]
	default:
		return "", time.Time{}, false
	}
	u := make([]uint16, len(name)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(name[2*i:])
	}
	return syscall.UTF16ToString(u), at, true
}

// userSID is the signed-in user's security identifier, which names their folder in each drive's bin.
func userSID() (string, error) {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String()
}
