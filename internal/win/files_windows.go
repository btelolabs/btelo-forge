package win

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var shFileOperation = shell32.NewProc("SHFileOperationW")

// SetClipboardFiles puts files on the clipboard as Explorer copies them, so pasting in Explorer, a chat or a mail
// copies the files themselves.
func (w *Window) SetClipboardFiles(paths []string) error {
	if len(paths) == 0 {
		return errors.New("no files to copy")
	}
	// DROPFILES: the offset of the names, a point, whether it is in the non-client area, and wide characters; then
	// each name ending in a zero, and a zero after the last.
	var names []uint16
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		names = append(names, utf16.Encode([]rune(abs))...)
		names = append(names, 0)
	}
	names = append(names, 0)
	const header = 20
	data := make([]byte, header+len(names)*2)
	binary.LittleEndian.PutUint32(data[0:], header)
	binary.LittleEndian.PutUint32(data[16:], 1) // fWide
	for i, u := range names {
		binary.LittleEndian.PutUint16(data[header+2*i:], u)
	}
	hDrop, err := globalBytes(data)
	if err != nil {
		return err
	}
	// "Preferred DropEffect" tells the paste to copy, not move.
	effect := make([]byte, 4)
	const dropEffectCopy = 1
	binary.LittleEndian.PutUint32(effect, dropEffectCopy)
	hEffect, err := globalBytes(effect)
	if err != nil {
		globalFree.Call(hDrop)
		return err
	}
	name, _ := syscall.UTF16PtrFromString("Preferred DropEffect")
	effectFormat, _, _ := registerFormat.Call(uintptr(unsafe.Pointer(name)))
	if r, _, _ := openClipboard.Call(w.Handle); r == 0 {
		globalFree.Call(hDrop)
		globalFree.Call(hEffect)
		return errors.New("clipboard busy")
	}
	defer closeClipboard.Call()
	emptyClipboard.Call()
	if r, _, e := setClipboardData.Call(cfHDROP, hDrop); r == 0 {
		globalFree.Call(hDrop)
		globalFree.Call(hEffect)
		return e
	}
	if effectFormat == 0 {
		globalFree.Call(hEffect)
	} else if r, _, _ := setClipboardData.Call(effectFormat, hEffect); r == 0 {
		globalFree.Call(hEffect)
	}
	return nil
}

// shFileOpStruct is SHFILEOPSTRUCTW.
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// Trash moves files and folders to the Recycle Bin, from where they can be restored, without asking. Where the
// Recycle Bin cannot take them (a network drive, say) Windows asks before deleting them for good.
func Trash(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	var from []uint16
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		from = append(from, utf16.Encode([]rune(abs))...)
		from = append(from, 0)
	}
	from = append(from, 0)
	const (
		foDelete          = 3
		fofSilent         = 0x0004
		fofNoConfirmation = 0x0010
		fofAllowUndo      = 0x0040
		fofNoErrorUI      = 0x0400
		fofWantNukeWarn   = 0x4000 // asks before deleting for good what the Recycle Bin cannot take
	)
	op := shFileOpStruct{wFunc: foDelete, pFrom: &from[0],
		fFlags: fofSilent | fofNoConfirmation | fofAllowUndo | fofNoErrorUI | fofWantNukeWarn}
	r, _, _ := shFileOperation.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return fmt.Errorf("moving to the Recycle Bin failed (code %#x)", r)
	}
	if op.fAnyOperationsAborted != 0 {
		return errors.New("moving to the Recycle Bin was cancelled")
	}
	return nil
}
