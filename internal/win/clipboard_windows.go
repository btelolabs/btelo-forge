package win

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var clipboardMu sync.Mutex

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
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	if !openClipboardRetry(w.Handle) {
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

// ForgetButtons drops the mouse buttons held as a system dialog opened, as the dialog ate their release.
func (w *Window) ForgetButtons() { w.input.Down = [3]bool{} }

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

// globalBytes copies data into memory the clipboard can own.
func globalBytes(data []byte) (uintptr, error) {
	const moveable = 0x0002
	h, _, e := globalAlloc.Call(moveable, uintptr(len(data)))
	if h == 0 {
		return 0, e
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		globalFree.Call(h)
		return 0, errors.New("clipboard memory lock failed")
	}
	moveMemory.Call(p, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	globalUnlock.Call(h)
	return h, nil
}

// openClipboardRetry opens the clipboard with brief retries in case another program temporarily holds it,
// safe to call from any goroutine (serialized by clipboardMu).
func openClipboardRetry(hwnd uintptr) bool {
	for i := range 5 {
		if r, _, _ := openClipboard.Call(hwnd); r != 0 {
			return true
		}
		if i < 4 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return false
}

// SetClipboardImage puts a picture on the clipboard: as a 32-bit bitmap over white, and, when it has any
// non-opaque pixel, also as PNG so programs that read PNG keep its transparency.
// Safe to call from a background goroutine so encoding 4K screenshots never blocks the UI thread.
func (w *Window) SetClipboardImage(img image.Image) error {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	if width == 0 || height == 0 {
		return errors.New("the picture is empty")
	}
	// A BITMAPINFOHEADER and 32-bit rows from the bottom up, in BGRX order.
	dib := make([]byte, 40+width*height*4)
	binary.LittleEndian.PutUint32(dib[0:], 40)
	binary.LittleEndian.PutUint32(dib[4:], uint32(width))
	binary.LittleEndian.PutUint32(dib[8:], uint32(height))
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], 32)
	binary.LittleEndian.PutUint32(dib[20:], uint32(width*height*4))
	opaque := fillClipboardDIB(dib[40:], img, b, width, height)

	var hPNG, pngFormat uintptr
	if !opaque {
		var pngData bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&pngData, img); err != nil {
			return err
		}
		name, _ := syscall.UTF16PtrFromString("PNG")
		pngFormat, _, _ = registerFormat.Call(uintptr(unsafe.Pointer(name)))
		if pngFormat != 0 {
			var err error
			if hPNG, err = globalBytes(pngData.Bytes()); err != nil {
				return err
			}
		}
	}
	hDIB, err := globalBytes(dib)
	if err != nil {
		if hPNG != 0 {
			globalFree.Call(hPNG)
		}
		return err
	}
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	if !openClipboardRetry(w.Handle) {
		if hPNG != 0 {
			globalFree.Call(hPNG)
		}
		globalFree.Call(hDIB)
		return errors.New("clipboard busy")
	}
	defer closeClipboard.Call()
	emptyClipboard.Call()
	// Once set, the clipboard owns the memory; what it refused is freed here.
	if hPNG != 0 {
		if r, _, _ := setClipboardData.Call(pngFormat, hPNG); r == 0 {
			globalFree.Call(hPNG)
		}
	}
	if r, _, e := setClipboardData.Call(cfDIB, hDIB); r == 0 {
		globalFree.Call(hDIB)
		return e
	}
	return nil
}

// fillClipboardDIB writes bottom-up BGRX rows over white into dst and reports whether every pixel was opaque.
// Large images are converted in parallel bands using 32-bit word writes for opaque pixels.
func fillClipboardDIB(dst []byte, img image.Image, b image.Rectangle, width, height int) bool {
	var hasAlpha atomic.Bool
	runBands := func(fn func(y0, y1 int)) {
		const bandPixels = 1 << 18
		if width*height <= bandPixels || height < 4 {
			fn(0, height)
			return
		}
		rowsPerBand := max(bandPixels/width, 1)
		var wg sync.WaitGroup
		for y := 0; y < height; y += rowsPerBand {
			y0, y1 := y, min(y+rowsPerBand, height)
			wg.Add(1)
			go func() {
				defer wg.Done()
				fn(y0, y1)
			}()
		}
		wg.Wait()
	}

	switch src := img.(type) {
	case *image.RGBA:
		runBands(func(y0, y1 int) {
			localAlpha := false
			for y := y0; y < y1; y++ {
				sOff := (b.Min.Y+y-src.Rect.Min.Y)*src.Stride + (b.Min.X-src.Rect.Min.X)*4
				dOff := (height - 1 - y) * width * 4
				sw := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(src.Pix[sOff:]))), width)
				dw := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(dst[dOff:]))), width)
				for i, v := range sw {
					if v >= 0xff000000 {
						dw[i] = v&0x0000ff00 | (v>>16)&0xff | (v&0xff)<<16 | 0xff000000
						continue
					}
					localAlpha = true
					r, g, bl, a := byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
					white := 255 - a
					dw[i] = uint32(bl+white) | uint32(g+white)<<8 | uint32(r+white)<<16 | 0xff000000
				}
			}
			if localAlpha {
				hasAlpha.Store(true)
			}
		})
	case *image.NRGBA:
		runBands(func(y0, y1 int) {
			localAlpha := false
			for y := y0; y < y1; y++ {
				sOff := (b.Min.Y+y-src.Rect.Min.Y)*src.Stride + (b.Min.X-src.Rect.Min.X)*4
				dOff := (height - 1 - y) * width * 4
				sw := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(src.Pix[sOff:]))), width)
				dw := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(dst[dOff:]))), width)
				for i, v := range sw {
					if v >= 0xff000000 {
						dw[i] = v&0x0000ff00 | (v>>16)&0xff | (v&0xff)<<16 | 0xff000000
						continue
					}
					localAlpha = true
					r, g, bl, a := uint32(v&0xff), uint32((v>>8)&0xff), uint32((v>>16)&0xff), uint32(v>>24)
					ww := (255 - a) * 255
					bOut := (bl*a + ww + 127) / 255
					gOut := (g*a + ww + 127) / 255
					rOut := (r*a + ww + 127) / 255
					dw[i] = bOut | gOut<<8 | rOut<<16 | 0xff000000
				}
			}
			if localAlpha {
				hasAlpha.Store(true)
			}
		})
	default:
		opaque := true
		for y := 0; y < height; y++ {
			row := dst[(height-1-y)*width*4:]
			for x := 0; x < width; x++ {
				r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA() // premultiplied, 16 bits
				if a != 0xffff {
					opaque = false
				}
				white := 0xffff - a
				row[x*4] = uint8((bl + white) >> 8)
				row[x*4+1] = uint8((g + white) >> 8)
				row[x*4+2] = uint8((r + white) >> 8)
				row[x*4+3] = 0xff
			}
		}
		return opaque
	}
	return !hasAlpha.Load()
}
