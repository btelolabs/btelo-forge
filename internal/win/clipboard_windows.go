package win

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	setClipboardData    = user32.NewProc("SetClipboardData")
	emptyClipboard      = user32.NewProc("EmptyClipboard")
	registerFormat      = user32.NewProc("RegisterClipboardFormatW")
	formatAvailable     = user32.NewProc("IsClipboardFormatAvailable")
	globalAlloc         = kernel32.NewProc("GlobalAlloc")
	globalSize          = kernel32.NewProc("GlobalSize")
	globalFree          = kernel32.NewProc("GlobalFree")
	shBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	shGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	coTaskMemFree       = syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")
	coInitialize        = syscall.NewLazyDLL("ole32.dll").NewProc("CoInitializeEx")
)

const (
	cfUnicodeText = 13
	cfDIB         = 8
	cfHDROP       = 15
)

// SetClipboard puts text on the clipboard.
func (w *Window) SetClipboard(text string) error {
	units := utf16.Encode([]rune(strings.ReplaceAll(text, "\n", "\r\n")))
	units = append(units, 0)
	const moveable = 0x0002
	h, _, e := globalAlloc.Call(moveable, uintptr(len(units)*2))
	if h == 0 {
		return e
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		globalFree.Call(h)
		return errors.New("clipboard memory lock failed")
	}
	moveMemory.Call(p, uintptr(unsafe.Pointer(&units[0])), uintptr(len(units)*2))
	globalUnlock.Call(h)
	if r, _, _ := openClipboard.Call(w.Handle); r == 0 {
		globalFree.Call(h)
		return errors.New("clipboard busy")
	}
	defer closeClipboard.Call()
	emptyClipboard.Call()
	if r, _, e := setClipboardData.Call(cfUnicodeText, h); r == 0 {
		globalFree.Call(h)
		return e
	}
	return nil
}

// clipboardBytes copies the clipboard's data in format, or nil when it holds none.
func clipboardBytes(format uintptr) []byte {
	if r, _, _ := formatAvailable.Call(format); r == 0 {
		return nil
	}
	h, _, _ := clipboardData.Call(format)
	if h == 0 {
		return nil
	}
	n, _, _ := globalSize.Call(h)
	p, _, _ := globalLock.Call(h)
	if p == 0 || n == 0 {
		return nil
	}
	defer globalUnlock.Call(h)
	data := make([]byte, n)
	moveMemory.Call(uintptr(unsafe.Pointer(&data[0])), p, n)
	return data
}

// Paste reads what the clipboard offers: an image (PNG or bitmap), copied files, or text.
func (w *Window) Paste() (image.Image, []string, string) {
	if r, _, _ := openClipboard.Call(w.Handle); r == 0 {
		return nil, nil, ""
	}
	defer closeClipboard.Call()
	name, _ := syscall.UTF16PtrFromString("PNG")
	if f, _, _ := registerFormat.Call(uintptr(unsafe.Pointer(name))); f != 0 {
		if data := clipboardBytes(f); data != nil {
			if img, err := png.Decode(bytes.NewReader(data)); err == nil {
				return img, nil, ""
			}
		}
	}
	if data := clipboardBytes(cfDIB); data != nil {
		if img, err := decodeDIB(data); err == nil {
			return img, nil, ""
		}
	}
	if r, _, _ := formatAvailable.Call(cfHDROP); r != 0 {
		if h, _, _ := clipboardData.Call(cfHDROP); h != 0 {
			var files []string
			count, _, _ := dragQueryFile.Call(h, 0xffffffff, 0, 0)
			for i := uintptr(0); i < count; i++ {
				n, _, _ := dragQueryFile.Call(h, i, 0, 0)
				buf := make([]uint16, n+1)
				if got, _, _ := dragQueryFile.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), n+1); got != 0 {
					files = append(files, syscall.UTF16ToString(buf))
				}
			}
			if len(files) > 0 {
				return nil, files, ""
			}
		}
	}
	if data := clipboardBytes(cfUnicodeText); len(data) >= 2 {
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(data[2*i:])
		}
		text := syscall.UTF16ToString(units)
		return nil, nil, strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	}
	return nil, nil, ""
}

// decodeDIB reads a packed device-independent bitmap of 24 or 32 bits per pixel.
func decodeDIB(data []byte) (image.Image, error) {
	if len(data) < 40 {
		return nil, errors.New("bitmap too short")
	}
	headerSize := int(binary.LittleEndian.Uint32(data[0:]))
	width := int(int32(binary.LittleEndian.Uint32(data[4:])))
	height := int(int32(binary.LittleEndian.Uint32(data[8:])))
	bits := int(binary.LittleEndian.Uint16(data[14:]))
	compression := binary.LittleEndian.Uint32(data[16:])
	if width <= 0 || height == 0 || (bits != 24 && bits != 32) || (compression != 0 && compression != 3) {
		return nil, errors.New("unsupported bitmap")
	}
	offset := headerSize
	if compression == 3 && headerSize == 40 {
		offset += 12 // three colour masks follow a plain header
	}
	topDown := height < 0
	if topDown {
		height = -height
	}
	stride := (width*bits/8 + 3) &^ 3
	if offset+stride*height > len(data) {
		return nil, errors.New("bitmap data truncated")
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	// Many programs leave the alpha byte of 32-bit bitmaps at zero; such a bitmap is opaque.
	opaque := true
	if bits == 32 {
	scan:
		for y := 0; y < height; y++ {
			row := data[offset+y*stride:]
			for x := 0; x < width; x++ {
				if row[x*4+3] != 0 {
					opaque = false
					break scan
				}
			}
		}
	}
	for y := 0; y < height; y++ {
		sy := height - 1 - y
		if topDown {
			sy = y
		}
		row := data[offset+sy*stride:]
		for x := 0; x < width; x++ {
			i := img.PixOffset(x, y)
			if bits == 24 {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = row[x*3+2], row[x*3+1], row[x*3], 255
				continue
			}
			alpha := row[x*4+3]
			if opaque {
				alpha = 255
			}
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = row[x*4+2], row[x*4+1], row[x*4], alpha
		}
	}
	return img, nil
}

// ChooseFolder shows the system folder picker and returns the chosen folder, or "" when cancelled.
func (w *Window) ChooseFolder(title string) string {
	const apartment = 0x2
	coInitialize.Call(0, apartment)
	t, _ := syscall.UTF16PtrFromString(title)
	display := make([]uint16, 260)
	info := struct {
		Owner    uintptr
		Root     uintptr
		Display  *uint16
		Title    *uint16
		Flags    uint32
		Callback uintptr
		Param    uintptr
		Image    int32
	}{Owner: w.Handle, Display: &display[0], Title: t, Flags: 0x0001 | 0x0040} // BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE
	list, _, _ := shBrowseForFolder.Call(uintptr(unsafe.Pointer(&info)))
	w.input.Down = [3]bool{}
	if list == 0 {
		return ""
	}
	defer coTaskMemFree.Call(list)
	path := make([]uint16, 1024)
	if r, _, _ := shGetPathFromIDList.Call(list, uintptr(unsafe.Pointer(&path[0]))); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(path)
}
