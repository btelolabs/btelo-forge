package win

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows/registry"

	"github.com/btelolabs/btelo-forge/internal/thumb"
)

// shellThumbnail asks Windows for a file's thumbnail as Explorer does, never its icon: the DLL runs for real, in the
// process the system gives it. It returns the colour at the picture's centre.
func shellThumbnail(t *testing.T, path string, size int32) (color.RGBA, image.Point, bool) {
	t.Helper()
	var (
		ole32   = syscall.NewLazyDLL("ole32.dll")
		gdi32   = syscall.NewLazyDLL("gdi32.dll")
		user32  = syscall.NewLazyDLL("user32.dll")
		initCOM = ole32.NewProc("CoInitializeEx")
		create  = shell32.NewProc("SHCreateItemFromParsingName")
		getDIBs = gdi32.NewProc("GetDIBits")
		del     = gdi32.NewProc("DeleteObject")
		getDC   = user32.NewProc("GetDC")
		relDC   = user32.NewProc("ReleaseDC")
	)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initCOM.Call(0, 2)
	iid := syscall.GUID{Data1: 0xbcc18b79, Data2: 0xba16, Data3: 0x442f, Data4: [8]byte{0x80, 0xc4, 0x8a, 0x59, 0xc3, 0x0c, 0x46, 0x3b}} // IShellItemImageFactory
	name, _ := syscall.UTF16PtrFromString(path)
	var factory *[1]*[4]uintptr
	if hr, _, _ := create.Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&factory))); hr != 0 {
		t.Fatalf("SHCreateItemFromParsingName: %#x", hr)
	}
	defer syscall.SyscallN(factory[0][2], uintptr(unsafe.Pointer(factory)))
	const thumbnailOnly = 0x8
	var bitmap uintptr
	hr, _, _ := syscall.SyscallN(factory[0][3], uintptr(unsafe.Pointer(factory)), uintptr(size)|uintptr(size)<<32, thumbnailOnly, uintptr(unsafe.Pointer(&bitmap)))
	if hr != 0 {
		return color.RGBA{}, image.Point{}, false
	}
	defer del.Call(bitmap)
	dc, _, _ := getDC.Call(0)
	defer relDC.Call(0, dc)
	type header struct {
		size                 uint32
		w, h                 int32
		planes, bits         uint16
		compression, imgSize uint32
		xppm, yppm           int32
		used, important      uint32
	}
	h := header{size: uint32(unsafe.Sizeof(header{}))}
	if r, _, _ := getDIBs.Call(dc, bitmap, 0, 0, 0, uintptr(unsafe.Pointer(&h)), 0); r == 0 {
		t.Fatal("GetDIBits could not size the bitmap")
	}
	w, height := h.w, h.h
	if height < 0 {
		height = -height
	}
	h = header{size: h.size, w: w, h: -height, planes: 1, bits: 32}
	pix := make([]byte, 4*int(w)*int(height))
	if r, _, _ := getDIBs.Call(dc, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&pix[0])), uintptr(unsafe.Pointer(&h)), 0); r == 0 {
		t.Fatal("GetDIBits could not read the bitmap")
	}
	at := 4 * (int(height)/2*int(w) + int(w)/2)
	return color.RGBA{pix[at+2], pix[at+1], pix[at], 255}, image.Pt(int(w), int(height)), true
}

func TestExplorerShowsTheSavedPicture(t *testing.T) {
	// Not t.TempDir: the system's process for thumbnails keeps the DLL open after the test.
	dir, err := os.MkdirTemp("", "forge-thumbnails")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ext := ".btthumb" + strings.TrimPrefix(filepath.Base(dir), "forge-thumbnails")
	r := AssocRoot{Software: "Software", App: `BteloLabs\BteloForgeTest`, Name: "Btelo Forge Test", Exe: `C:\nowhere\forge-test.exe`,
		Thumbnailer: ThumbnailerPath(dir)}
	if err := InstallThumbnailer(dir); err != nil {
		t.Fatal(err)
	}
	a := Association{ProgID: "BteloForgeTest.Canvas" + filepath.Base(dir), Name: "Test", Exts: []string{ext}, Own: true, Preview: true}
	// The class is the real one: what this computer has declared goes back afterwards.
	server := r.thumbnailerKey() + `\InprocServer32`
	user, machine := getString(server, ""), getStringIn(registry.LOCAL_MACHINE, server, "")
	if err := Register(r, []Association{a}, "test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := Unregister(r, []Association{a}); err != nil {
			t.Error(err)
		}
		deleteTree(r.classes() + `\` + ext)
		for root, path := range map[registry.Key]string{registry.CURRENT_USER: user, registry.LOCAL_MACHINE: machine} {
			if path != "" {
				setStringIn(root, r.thumbnailerKey(), "", "Btelo Forge thumbnails")
				setStringIn(root, server, "", path)
				setStringIn(root, server, "ThreadingModel", "Apartment")
			}
		}
	})
	if !Registered(r, a) {
		t.Fatal("not registered")
	}

	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	draw.Draw(img, img.Rect, image.NewUniform(color.RGBA{220, 40, 30, 255}), image.Point{}, draw.Src)
	end, err := thumb.Encode(img, thumb.Meta{W: 400, H: 200})
	if err != nil {
		t.Fatal(err)
	}
	with := filepath.Join(dir, "with"+ext)
	if err := os.WriteFile(with, append([]byte("whatever the file holds"), end...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, size, ok := shellThumbnail(t, with, 256)
	if !ok {
		t.Fatal("Windows made no thumbnail from the file's end")
	}
	if size.X != 256 || size.Y != 128 {
		t.Errorf("size %v, want 256×128", size)
	}
	if got.R < 200 || got.G > 70 || got.B > 60 {
		t.Errorf("the middle of the thumbnail is %v", got)
	}
	without := filepath.Join(dir, "without"+ext)
	if err := os.WriteFile(without, []byte("a file saved before pictures"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := shellThumbnail(t, without, 256); ok {
		t.Error("a file with no picture got a thumbnail")
	}
}

func TestThumbnailerInstallKeepsOneVersion(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "thumbnailer-000000000000.dll")
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallThumbnailer(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old version stays: %v", err)
	}
	if st, err := os.Stat(ThumbnailerPath(dir)); err != nil || st.Size() != int64(len(thumbnailerDLL)) {
		t.Errorf("installed: %v, %v", st, err)
	}
}
