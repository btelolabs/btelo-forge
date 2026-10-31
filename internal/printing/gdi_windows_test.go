package printing

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

var (
	createEMF  = gdi32.NewProc("CreateEnhMetaFileW")
	closeEMF   = gdi32.NewProc("CloseEnhMetaFile")
	deleteEMF  = gdi32.NewProc("DeleteEnhMetaFile")
	enumEMF    = gdi32.NewProc("EnumEnhMetaFile")
	user32     = syscall.NewLazyDLL("user32.dll")
	findWindow = user32.NewProc("FindWindowExW")
	postMsg    = user32.NewProc("PostMessageW")
	createWin  = user32.NewProc("CreateWindowExW")
	destroyWin = user32.NewProc("DestroyWindow")
	getWinText = user32.NewProc("GetClassNameW")
	enumWins   = user32.NewProc("EnumThreadWindows")
)

// Record types of an enhanced metafile.
const (
	emrPolyDraw      = 56
	emrPolyDraw16    = 92
	emrBeginPath     = 59
	emrEndPath       = 60
	emrFillPath      = 62
	emrStrokePath    = 64
	emrSelectClip    = 67
	emrStretchDIBits = 81
)

// metafileRecords draws a page into an enhanced metafile, as a printer's spooler would keep it, and counts its records.
func metafileRecords(t *testing.T, p *Page) map[uint32]int {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dc, _, e := createEMF.Call(0, 0, 0, 0)
	if dc == 0 {
		t.Fatal(e)
	}
	if err := drawPage(dc, Flatten(p, 300), Scale(300.0/72, 300.0/72)); err != nil {
		t.Fatal(err)
	}
	h, _, _ := closeEMF.Call(dc)
	if h == 0 {
		t.Fatal("no metafile")
	}
	defer deleteEMF.Call(h)
	counts := map[uint32]int{}
	cb := syscall.NewCallback(func(hdc, table, record, n, data uintptr) uintptr {
		var kind uint32
		moveMemory.Call(uintptr(unsafe.Pointer(&kind)), record, 4)
		counts[kind]++
		return 1
	})
	enumEMF.Call(0, h, cb, 0, 0)
	return counts
}

// vectorPage is shapes and words, translucent ones among them over a plain page.
func vectorPage(t *testing.T) *Page {
	pg := &Page{W: 595, H: 842}
	pg.Add(&Fill{Path: RoundRectPath(50, 50, 300, 120, 12), Color: color.NRGBA{40, 110, 220, 255}})
	pg.Add(&Stroke{Path: EllipsePath(60, 200, 300, 120), Width: 3, Color: color.NRGBA{200, 30, 30, 255}, Join: JoinRound})
	pg.Add(&Stroke{Path: RectPath(60, 340, 200, 80), Width: 2, Dash: []float64{6, 3}, Color: color.NRGBA{A: 255}})
	pg.Add(&Fill{Path: EllipsePath(380, 400, 100, 100), Color: color.NRGBA{0, 160, 0, 120}})
	pg.Add(&Clip{Path: EllipsePath(100, 500, 200, 200)})
	pg.Add(&Shade{Path: RectPath(100, 500, 200, 200), X0: 100, X1: 300, Stops: []Stop{{0, color.NRGBA{255, 0, 0, 255}}, {1, color.NRGBA{0, 0, 255, 255}}}, Extend: [2]bool{true, true}, M: Identity})
	pg.Add(&Unclip{})
	c := Canvas(pg)
	c.Text(font(t).Face(160), 500, 7200, "Hello 世界 printing", gfx.Hex(0x202020))
	return pg
}

func TestGDIPathsNotPixels(t *testing.T) {
	n := metafileRecords(t, vectorPage(t))
	if n[emrStretchDIBits] != 0 {
		t.Fatalf("%d pictures on a page of shapes", n[emrStretchDIBits])
	}
	if n[emrBeginPath] < 10 || n[emrFillPath] < 5 || n[emrStrokePath] < 1 || n[emrSelectClip] < 1 {
		t.Fatalf("records: %v", n)
	}
	if n[emrPolyDraw]+n[emrPolyDraw16] < 10 {
		t.Fatalf("no curves went through: %v", n)
	}
	t.Logf("records: %v", n)
}

func TestGDIUsesAPictureOnlyForAPicture(t *testing.T) {
	pg := vectorPage(t)
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	pg.Add(&Picture{Img: img, M: Matrix{50, 0, 0, 50, 400, 50}})
	if n := metafileRecords(t, pg); n[emrStretchDIBits] != 1 {
		t.Fatalf("%d pictures for one", n[emrStretchDIBits])
	}
}

// A real job, to the driver that makes a file: nothing goes to paper.
func TestPrintToPDFQueue(t *testing.T) {
	const name = "Microsoft Print to PDF"
	if !slices.Contains(Printers(), name) {
		t.Skip(name + " is not installed")
	}
	tg, err := PrinterTarget(name)
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("FORGE_PRINT_OUT")
	if dir == "" {
		dir = t.TempDir()
	}
	tg.Output = filepath.Join(dir, "gdi-job.pdf")
	os.Remove(tg.Output)
	land := &Page{W: 842, H: 595}
	land.Place(vectorPage(t), Translate(100, -100))
	done := 0
	doc := &Doc{Title: "Forge print test", Pages: []*Page{vectorPage(t), land}}
	if err := tg.Print(context.Background(), doc, func(n, total int) { done = n }); err != nil {
		t.Fatal(err)
	}
	if done != 2 {
		t.Fatalf("%d pages went", done)
	}
	var size int64
	for start := time.Now(); time.Since(start) < 30*time.Second; time.Sleep(100 * time.Millisecond) {
		if fi, err := os.Stat(tg.Output); err == nil && fi.Size() > 0 {
			if fi.Size() == size {
				break
			}
			size = fi.Size()
		}
	}
	if size == 0 {
		t.Fatal("the spooler made no file")
	}
	t.Logf("%s: %d bytes", tg.Output, size)
}

func TestCopies(t *testing.T) {
	const name = "Microsoft Print to PDF"
	if !slices.Contains(Printers(), name) {
		t.Skip(name + " is not installed")
	}
	tg, err := PrinterTarget(name)
	if err != nil {
		t.Fatal(err)
	}
	tg.Output, tg.Copies = filepath.Join(t.TempDir(), "copies.pdf"), 2
	sheets := 0
	if err := tg.Print(context.Background(), &Doc{Pages: []*Page{{W: 100, H: 100}, {W: 100, H: 100}}}, func(n, total int) { sheets = total }); err != nil {
		t.Fatal(err)
	}
	if sheets != 4 {
		t.Fatalf("%d sheets", sheets)
	}
}

func TestCancelledJobStops(t *testing.T) {
	const name = "Microsoft Print to PDF"
	if !slices.Contains(Printers(), name) {
		t.Skip(name + " is not installed")
	}
	tg, err := PrinterTarget(name)
	if err != nil {
		t.Fatal(err)
	}
	tg.Output = filepath.Join(t.TempDir(), "cancelled.pdf")
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if err := tg.Print(ctx, &Doc{Pages: []*Page{{W: 100, H: 100}}}, nil); err == nil {
		t.Fatal("a cancelled job went on")
	}
}

// The system's dialog comes up with the structure as laid out here, and cancels cleanly. It needs a desktop, so it
// runs only when asked for.
func TestPrintDialogOpensAndCancels(t *testing.T) {
	if os.Getenv("FORGE_PRINT_DIALOG") == "" {
		t.Skip("set FORGE_PRINT_DIALOG to open the system's dialog")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := syscall.UTF16PtrFromString("STATIC")
	owner, _, _ := createWin.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x10000000, 0, 0, 100, 100, 0, 0, 0, 0)
	if owner == 0 {
		t.Fatal("no window")
	}
	defer destroyWin.Call(owner)
	const wmClose = 0x10
	const wmCommand = 0x111
	const idCancel = 2
	thread := currentThread()
	go func() {
		time.Sleep(1500 * time.Millisecond)
		cb := syscall.NewCallback(func(h, _ uintptr) uintptr {
			postMsg.Call(h, wmCommand, idCancel, 0)
			postMsg.Call(h, wmClose, 0, 0)
			return 1
		})
		for range 40 {
			enumWins.Call(uintptr(thread), cb, 0)
			time.Sleep(250 * time.Millisecond)
		}
	}()
	got, err := ChoosePrinter(owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("cancelled, yet it chose %+v", got)
	}
}

func currentThread() uint32 {
	r, _, _ := kernel32.NewProc("GetCurrentThreadId").Call()
	return uint32(r)
}
