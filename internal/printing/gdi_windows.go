package printing

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Printing through Windows GDI, without cgo: the display list as path brackets, geometric pens and DIBs. Drivers do
// not agree on blending, so a page is flattened first (see Flatten) and everything GDI sees is opaque.

var (
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	winspool = syscall.NewLazyDLL("winspool.drv")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	createDC         = gdi32.NewProc("CreateDCW")
	deleteDC         = gdi32.NewProc("DeleteDC")
	resetDC          = gdi32.NewProc("ResetDCW")
	startDoc         = gdi32.NewProc("StartDocW")
	endDoc           = gdi32.NewProc("EndDoc")
	abortDoc         = gdi32.NewProc("AbortDoc")
	startPage        = gdi32.NewProc("StartPage")
	endPage          = gdi32.NewProc("EndPage")
	getDeviceCaps    = gdi32.NewProc("GetDeviceCaps")
	setGraphicsMode  = gdi32.NewProc("SetGraphicsMode")
	setWorldXform    = gdi32.NewProc("SetWorldTransform")
	saveDC           = gdi32.NewProc("SaveDC")
	restoreDC        = gdi32.NewProc("RestoreDC")
	beginPath        = gdi32.NewProc("BeginPath")
	endPath          = gdi32.NewProc("EndPath")
	polyDraw         = gdi32.NewProc("PolyDraw")
	fillPath         = gdi32.NewProc("FillPath")
	strokePath       = gdi32.NewProc("StrokePath")
	selectClipPath   = gdi32.NewProc("SelectClipPath")
	setPolyFillMode  = gdi32.NewProc("SetPolyFillMode")
	selectObject     = gdi32.NewProc("SelectObject")
	deleteObject     = gdi32.NewProc("DeleteObject")
	getStockObject   = gdi32.NewProc("GetStockObject")
	createSolidBrush = gdi32.NewProc("CreateSolidBrush")
	extCreatePen     = gdi32.NewProc("ExtCreatePen")
	setMiterLimit    = gdi32.NewProc("SetMiterLimit")
	stretchDIBits    = gdi32.NewProc("StretchDIBits")
	setStretchMode   = gdi32.NewProc("SetStretchBltMode")
	setBrushOrg      = gdi32.NewProc("SetBrushOrgEx")

	printDlgEx = comdlg32.NewProc("PrintDlgExW")

	openPrinter        = winspool.NewProc("OpenPrinterW")
	closePrinter       = winspool.NewProc("ClosePrinter")
	documentProperties = winspool.NewProc("DocumentPropertiesW")
	enumPrinters       = winspool.NewProc("EnumPrintersW")

	globalAlloc  = kernel32.NewProc("GlobalAlloc")
	globalLock   = kernel32.NewProc("GlobalLock")
	globalUnlock = kernel32.NewProc("GlobalUnlock")
	globalFree   = kernel32.NewProc("GlobalFree")
	globalSize   = kernel32.NewProc("GlobalSize")
	moveMemory   = kernel32.NewProc("RtlMoveMemory")
)

// Windows' numbers.
const (
	capHorzRes      = 8
	capVertRes      = 10
	capLogPixelsX   = 88
	capLogPixelsY   = 90
	capPhysWidth    = 110
	capPhysHeight   = 111
	capPhysOffsetX  = 112
	capPhysOffsetY  = 113
	gmAdvanced      = 2
	alternate       = 1
	winding         = 2
	rgnAnd          = 1
	nullPen         = 8
	ptCloseFigure   = 1
	ptLineTo        = 2
	ptBezierTo      = 4
	ptMoveTo        = 6
	psGeometric     = 0x00010000
	psEndcapRound   = 0x00000000
	psEndcapSquare  = 0x00000100
	psEndcapFlat    = 0x00000200
	psJoinRound     = 0x00000000
	psJoinBevel     = 0x00001000
	psJoinMiter     = 0x00002000
	bsSolid         = 0
	dibRGBColors    = 0
	srcCopy         = 0x00CC0020
	halftone        = 4
	dmOrientation   = 0x00000001
	dmPaperSize     = 0x00000002
	orientPortrait  = 1
	orientLandscape = 2

	pdNoSelection     = 0x00000004
	pdNoPageNums      = 0x00000008
	pdCollate         = 0x00000010
	pdNoCurrentPage   = 0x00800000
	startPageGeneral  = 0xFFFFFFFF
	pdResultPrint     = 1
	dmOutBuffer       = 2
	printerEnumLocal  = 2
	printerEnumConn   = 4
	devModeFields     = 72
	devModeOrient     = 76
	devModePaperSize  = 78
	devModeSize       = 68
	devModeDriverSize = 70
)

// logicalUnits is how many of GDI's whole numbers make a point.
const logicalUnits = 100

// xform is Windows' XFORM.
type xform struct{ M11, M12, M21, M22, Dx, Dy float32 }

func toXform(m Matrix) xform {
	return xform{float32(m[0]), float32(m[1]), float32(m[2]), float32(m[3]), float32(m[4]), float32(m[5])}
}

// Target is a printer and how it is set up.
type Target struct {
	Name    string
	DevMode []byte // the driver's settings; empty uses its defaults
	Copies  int
	Collate bool
	Output  string // a file to print to instead of what the printer does with a job
}

// Available says this system has a print system this package drives.
const Available = true

type printPageRange struct{ From, To uint32 }

// printDlgExW is PRINTDLGEXW.
type printDlgExW struct {
	Size           uint32
	Owner          uintptr
	DevMode        uintptr
	DevNames       uintptr
	DC             uintptr
	Flags          uint32
	Flags2         uint32
	ExclusionFlags uint32
	NPageRanges    uint32
	NMaxPageRanges uint32
	PageRanges     *printPageRange
	MinPage        uint32
	MaxPage        uint32
	Copies         uint32
	Instance       uintptr
	Template       *uint16
	Callback       uintptr
	NPropertyPages uint32
	PropertyPages  uintptr
	StartPage      uint32
	ResultAction   uint32
}

// ChoosePrinter shows the system's print dialog, set up as last is left it, and returns what was chosen; nil, nil
// when it was cancelled. It belongs on the window's own thread, and blocks it as the system's other dialogs do.
func ChoosePrinter(owner uintptr, last *Target) (*Target, error) {
	const apartment = 0x2
	syscall.NewLazyDLL("ole32.dll").NewProc("CoInitializeEx").Call(0, apartment)
	d := printDlgExW{Owner: owner, Flags: pdNoSelection | pdNoPageNums | pdNoCurrentPage, Copies: 1, StartPage: startPageGeneral}
	d.Size = uint32(unsafe.Sizeof(d))
	if last != nil {
		d.Copies = uint32(max(last.Copies, 1))
		if last.Collate {
			d.Flags |= pdCollate
		}
		if len(last.DevMode) > 0 {
			d.DevMode = globalBytes(last.DevMode)
		}
		if last.Name != "" {
			d.DevNames = devNames(last.Name)
		}
	}
	defer func() {
		if d.DevMode != 0 {
			globalFree.Call(d.DevMode)
		}
		if d.DevNames != 0 {
			globalFree.Call(d.DevNames)
		}
	}()
	r, _, _ := printDlgEx.Call(uintptr(unsafe.Pointer(&d)))
	if int32(r) != 0 {
		return nil, fmt.Errorf("the print dialog failed: 0x%08x", uint32(r))
	}
	if d.ResultAction != pdResultPrint {
		return nil, nil
	}
	t := &Target{Copies: max(int(d.Copies), 1), Collate: d.Flags&pdCollate != 0}
	if names := globalCopy(d.DevNames); len(names) >= 4 {
		// The offsets are in characters, from the start.
		off := int(names[2]) | int(names[3])<<8
		var name []uint16
		for i := off * 2; i+1 < len(names) && (names[i] != 0 || names[i+1] != 0); i += 2 {
			name = append(name, uint16(names[i])|uint16(names[i+1])<<8)
		}
		t.Name = string(utf16.Decode(name))
	}
	t.DevMode = globalCopy(d.DevMode)
	if t.Name == "" {
		return nil, errors.New("the print dialog named no printer")
	}
	return t, nil
}

func globalBytes(b []byte) uintptr {
	const moveable = 0x0002
	h, _, _ := globalAlloc.Call(moveable, uintptr(len(b)))
	if h == 0 {
		return 0
	}
	if p, _, _ := globalLock.Call(h); p != 0 {
		moveMemory.Call(p, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
		globalUnlock.Call(h)
	}
	return h
}

// globalCopy is what a block of global memory holds.
func globalCopy(h uintptr) []byte {
	if h == 0 {
		return nil
	}
	n, _, _ := globalSize.Call(h)
	p, _, _ := globalLock.Call(h)
	if p == 0 || n == 0 {
		return nil
	}
	defer globalUnlock.Call(h)
	out := make([]byte, n)
	moveMemory.Call(uintptr(unsafe.Pointer(&out[0])), p, n)
	return out
}

// devNames is a DEVNAMES holding just the printer's name.
func devNames(name string) uintptr {
	w, err := syscall.UTF16FromString(name)
	if err != nil {
		return 0
	}
	const header = 8
	b := make([]byte, header+len(w)*2+2)
	off := uint16(header / 2)
	b[2], b[3] = byte(off), byte(off>>8)
	for i, c := range w {
		b[header+i*2], b[header+i*2+1] = byte(c), byte(c>>8)
	}
	return globalBytes(b)
}

// Printers lists the printers of this computer.
func Printers() []string {
	const level = 4 // PRINTER_INFO_4: name, server, attributes
	var size, count uint32
	flags := uintptr(printerEnumLocal | printerEnumConn)
	enumPrinters.Call(flags, 0, level, 0, 0, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)))
	if size == 0 {
		return nil
	}
	buf := make([]uint64, size/8+1)
	r, _, _ := enumPrinters.Call(flags, 0, level, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)))
	if r == 0 {
		return nil
	}
	type info struct {
		Name, Server *uint16
		Attributes   uint32
	}
	list := unsafe.Slice((*info)(unsafe.Pointer(&buf[0])), count)
	out := make([]string, 0, count)
	for _, p := range list {
		out = append(out, syscall.UTF16ToString(unsafe.Slice(p.Name, 512)))
	}
	return out
}

// PrinterTarget is a printer by its name, as its driver is set up by default.
func PrinterTarget(name string) (*Target, error) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var h uintptr
	if r, _, e := openPrinter.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&h)), 0); r == 0 {
		return nil, fmt.Errorf("the printer %s cannot be opened: %w", name, e)
	}
	defer closePrinter.Call(h)
	size, _, _ := documentProperties.Call(0, h, uintptr(unsafe.Pointer(n)), 0, 0, 0)
	if int32(size) <= 0 {
		return nil, fmt.Errorf("the printer %s gives no settings", name)
	}
	dm := make([]byte, size)
	if r, _, _ := documentProperties.Call(0, h, uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&dm[0])), 0, dmOutBuffer); int32(r) < 0 {
		return nil, fmt.Errorf("the printer %s gives no settings", name)
	}
	return &Target{Name: name, DevMode: dm, Copies: 1}, nil
}

type docInfo struct {
	Size     int32
	Name     *uint16
	Output   *uint16
	Datatype *uint16
	Type     uint32
}

// paperSizes are Windows' numbers for common papers, by their size in points.
var paperSizes = []struct {
	w, h float64
	n    uint16
}{{612, 792, 1}, {612, 1008, 5}, {842, 1191, 8}, {595, 842, 9}, {420, 595, 11}, {516, 729, 13}, {792, 1224, 3}}

// paperOf is the number of the paper that fits a page, 0 for none.
func paperOf(w, h float64) uint16 {
	w, h = min(w, h), max(w, h)
	for _, p := range paperSizes {
		if math.Abs(p.w-w) < 3 && math.Abs(p.h-h) < 3 {
			return p.n
		}
	}
	return 0
}

// devModeFor is the settings for a page: turned its way, on its paper when that is a usual one and it is not to be
// scaled to fill whatever the user chose.
func devModeFor(base []byte, pg *Page) []byte {
	if len(base) < devModePaperSize+2 {
		return nil
	}
	dm := append([]byte(nil), base...)
	fields := uint32(dm[devModeFields]) | uint32(dm[devModeFields+1])<<8 | uint32(dm[devModeFields+2])<<16 | uint32(dm[devModeFields+3])<<24
	orient := uint16(orientPortrait)
	if pg.W > pg.H {
		orient = orientLandscape
	}
	dm[devModeOrient], dm[devModeOrient+1] = byte(orient), byte(orient>>8)
	fields |= dmOrientation
	if n := paperOf(pg.W, pg.H); n != 0 && !pg.Fit {
		dm[devModePaperSize], dm[devModePaperSize+1] = byte(n), byte(n>>8)
		fields |= dmPaperSize
	}
	dm[devModeFields], dm[devModeFields+1], dm[devModeFields+2], dm[devModeFields+3] = byte(fields), byte(fields>>8), byte(fields>>16), byte(fields>>24)
	return dm
}

// Print sends the document to the printer. It ties up the thread it runs on until it ends, and can run on any;
// progress, which may be nil, hears how many sheets have gone.
func (t *Target) Print(ctx context.Context, d *Doc, progress func(done, total int)) (err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if len(d.Pages) == 0 {
		return errors.New("there is nothing to print")
	}
	driver, _ := syscall.UTF16PtrFromString("WINSPOOL")
	device, err := syscall.UTF16PtrFromString(t.Name)
	if err != nil {
		return err
	}
	var dm unsafe.Pointer
	if len(t.DevMode) > 0 {
		dm = unsafe.Pointer(&t.DevMode[0])
	}
	dc, _, e := createDC.Call(uintptr(unsafe.Pointer(driver)), uintptr(unsafe.Pointer(device)), 0, uintptr(dm))
	if dc == 0 {
		return fmt.Errorf("the printer %s cannot be reached: %w", t.Name, e)
	}
	defer deleteDC.Call(dc)
	title, _ := syscall.UTF16PtrFromString(d.Title)
	info := docInfo{Name: title}
	info.Size = int32(unsafe.Sizeof(info))
	if t.Output != "" {
		info.Output, _ = syscall.UTF16PtrFromString(t.Output)
	}
	if r, _, e := startDoc.Call(dc, uintptr(unsafe.Pointer(&info))); int32(r) <= 0 {
		return fmt.Errorf("the printer %s would not start the job: %w", t.Name, e)
	}
	defer func() {
		if err != nil {
			abortDoc.Call(dc)
			return
		}
		if r, _, e := endDoc.Call(dc); int32(r) <= 0 {
			err = fmt.Errorf("the printer %s did not finish the job: %w", t.Name, e)
		}
	}()
	// Copies: whole sets one after the other when collating, each page so many times otherwise.
	copies := max(t.Copies, 1)
	var order []int
	if t.Collate {
		for range copies {
			for i := range d.Pages {
				order = append(order, i)
			}
		}
	} else {
		for i := range d.Pages {
			for range copies {
				order = append(order, i)
			}
		}
	}
	var dpi float64
	lastFlat, lastIndex := (*Page)(nil), -1
	var mode string
	for n, i := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		pg := d.Pages[i]
		if dmNew := devModeFor(t.DevMode, pg); dmNew != nil && string(dmNew) != mode {
			mode = string(dmNew)
			resetDC.Call(dc, uintptr(unsafe.Pointer(&dmNew[0])))
		}
		if r, _, e := startPage.Call(dc); int32(r) <= 0 {
			return fmt.Errorf("the printer %s refused a page: %w", t.Name, e)
		}
		dpi = deviceDPI(dc)
		if i != lastIndex {
			lastFlat, lastIndex = Flatten(pg, min(dpi, 300)), i
		}
		m := deviceMatrix(dc, pg)
		if err := drawPage(dc, lastFlat, m); err != nil {
			return err
		}
		if r, _, e := endPage.Call(dc); int32(r) <= 0 {
			return fmt.Errorf("the printer %s refused a page: %w", t.Name, e)
		}
		if progress != nil {
			progress(n+1, len(order))
		}
	}
	return nil
}

func caps(dc uintptr, index int) float64 {
	r, _, _ := getDeviceCaps.Call(dc, uintptr(index))
	return float64(int32(r))
}

func deviceDPI(dc uintptr) float64 { return max(caps(dc, capLogPixelsX), 72) }

// paperOfDC is the sheet the device has, in points.
func paperOfDC(dc uintptr) Paper {
	dx, dy := max(caps(dc, capLogPixelsX), 1), max(caps(dc, capLogPixelsY), 1)
	p := Paper{W: caps(dc, capPhysWidth) * 72 / dx, H: caps(dc, capPhysHeight) * 72 / dy,
		Left: caps(dc, capPhysOffsetX) * 72 / dx, Top: caps(dc, capPhysOffsetY) * 72 / dy}
	p.Right = p.W - p.Left - caps(dc, capHorzRes)*72/dx
	p.Bottom = p.H - p.Top - caps(dc, capVertRes)*72/dy
	return p
}

// deviceMatrix maps a page onto the device's pixels, whose origin is at the corner of what it can mark.
func deviceMatrix(dc uintptr, pg *Page) Matrix {
	p := paperOfDC(dc)
	dx, dy := max(caps(dc, capLogPixelsX), 1), max(caps(dc, capLogPixelsY), 1)
	return Scale(dx/72, dy/72).Mul(Translate(-p.Left, -p.Top)).Mul(Placement(pg.W, pg.H, pg.Fit, p))
}

// gdiPath turns a path into GDI's whole numbers.
type gdiPath struct {
	pts   []int32 // x, y, x, y…
	types []byte
}

func (g *gdiPath) set(p *Path) bool {
	g.pts, g.types = g.pts[:0], g.types[:0]
	i := 0
	add := func(t byte) {
		q := p.Pts[i]
		i++
		g.pts = append(g.pts, int32(math.Round(q.X*logicalUnits)), int32(math.Round(q.Y*logicalUnits)))
		g.types = append(g.types, t)
	}
	for _, op := range p.Ops {
		switch op {
		case 'M':
			add(ptMoveTo)
		case 'L':
			add(ptLineTo)
		case 'C':
			add(ptBezierTo)
			add(ptBezierTo)
			add(ptBezierTo)
		case 'Z':
			if n := len(g.types); n > 0 {
				g.types[n-1] |= ptCloseFigure
			}
		}
	}
	return len(g.types) > 0
}

// bracket records the path as the device's current path.
func (g *gdiPath) bracket(dc uintptr) {
	beginPath.Call(dc)
	polyDraw.Call(dc, uintptr(unsafe.Pointer(&g.pts[0])), uintptr(unsafe.Pointer(&g.types[0])), uintptr(len(g.types)))
	endPath.Call(dc)
}

// drawPage draws a flattened page through m, from points to device pixels.
func drawPage(dc uintptr, p *Page, m Matrix) error {
	setGraphicsMode.Call(dc, gmAdvanced)
	world := toXform(m.Mul(Scale(1.0/logicalUnits, 1.0/logicalUnits)))
	if r, _, e := setWorldXform.Call(dc, uintptr(unsafe.Pointer(&world))); r == 0 {
		return fmt.Errorf("the printer cannot place a page: %w", e)
	}
	setStretchMode.Call(dc, halftone)
	setBrushOrg.Call(dc, 0, 0, 0)
	pen, _, _ := getStockObject.Call(nullPen)
	selectObject.Call(dc, pen)
	var path gdiPath
	depth := 0
	for _, op := range p.Ops {
		switch o := op.(type) {
		case *Fill:
			gdiFill(dc, &path, o.Path, o.EvenOdd, o.Color.R, o.Color.G, o.Color.B)
		case *Text:
			gdiFill(dc, &path, TextPath(o), false, o.Color.R, o.Color.G, o.Color.B)
		case *Stroke:
			if len(o.Dash) > 0 {
				gdiFill(dc, &path, StrokeOutline(o, max(m.Size(), 2)), false, o.Color.R, o.Color.G, o.Color.B)
			} else {
				gdiStroke(dc, &path, o)
			}
		case *Clip:
			saveDC.Call(dc)
			depth++
			target := o.Path
			if !path.set(target) {
				target = RectPath(0, 0, 0, 0)
				path.set(target)
			}
			mode := winding
			if o.EvenOdd {
				mode = alternate
			}
			setPolyFillMode.Call(dc, uintptr(mode))
			path.bracket(dc)
			selectClipPath.Call(dc, rgnAnd)
		case *Unclip:
			if depth > 0 {
				depth--
				restoreDC.Call(dc, ^uintptr(0))
			}
		case *Picture:
			gdiPicture(dc, o, m)
			setWorldXform.Call(dc, uintptr(unsafe.Pointer(&world)))
		}
	}
	for ; depth > 0; depth-- {
		restoreDC.Call(dc, ^uintptr(0))
	}
	return nil
}

func colorRef(r, g, b uint8) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }

func gdiFill(dc uintptr, path *gdiPath, p *Path, evenOdd bool, r, g, b uint8) {
	if !path.set(p) {
		return
	}
	brush, _, _ := createSolidBrush.Call(colorRef(r, g, b))
	old, _, _ := selectObject.Call(dc, brush)
	mode := winding
	if evenOdd {
		mode = alternate
	}
	setPolyFillMode.Call(dc, uintptr(mode))
	path.bracket(dc)
	fillPath.Call(dc)
	selectObject.Call(dc, old)
	deleteObject.Call(brush)
}

func gdiStroke(dc uintptr, path *gdiPath, s *Stroke) {
	if !path.set(s.Path) {
		return
	}
	lb := struct {
		Style uint32
		Color uint32
		Hatch uintptr
	}{bsSolid, uint32(colorRef(s.Color.R, s.Color.G, s.Color.B)), 0}
	style := uint32(psGeometric)
	switch s.Cap {
	case CapRound:
		style |= psEndcapRound
	case CapSquare:
		style |= psEndcapSquare
	default:
		style |= psEndcapFlat
	}
	switch s.Join {
	case JoinRound:
		style |= psJoinRound
	case JoinBevel:
		style |= psJoinBevel
	default:
		style |= psJoinMiter
	}
	pen, _, _ := extCreatePen.Call(uintptr(style), uintptr(math.Round(s.Width*logicalUnits)), uintptr(unsafe.Pointer(&lb)), 0, 0)
	if pen == 0 {
		return
	}
	miter := float32(s.Miter)
	if miter == 0 {
		miter = 10
	}
	setMiterLimit.Call(dc, uintptr(math.Float32bits(max(miter, 1))), 0)
	old, _, _ := selectObject.Call(dc, pen)
	path.bracket(dc)
	strokePath.Call(dc)
	selectObject.Call(dc, old)
	deleteObject.Call(pen)
}

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	Used          uint32
	Important     uint32
}

// gdiPicture stretches an opaque picture over its place. The picture's own pixels are the world's units, so a turned
// one is GDI's to turn.
func gdiPicture(dc uintptr, p *Picture, m Matrix) {
	img := pictureImage(p)
	if img == nil || img.Rect.Empty() {
		return
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	bits, stride := dibOf(img)
	world := toXform(m.Mul(p.M).Mul(Scale(1/float64(w), 1/float64(h))))
	setWorldXform.Call(dc, uintptr(unsafe.Pointer(&world)))
	info := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 24, SizeImage: uint32(stride * h)}
	info.Size = uint32(unsafe.Sizeof(info))
	stretchDIBits.Call(dc, 0, 0, uintptr(w), uintptr(h), 0, 0, uintptr(w), uintptr(h),
		uintptr(unsafe.Pointer(&bits[0])), uintptr(unsafe.Pointer(&info)), dibRGBColors, srcCopy)
}

// dibOf is a picture as bottom-to-top-free rows of blue, green and red, each row a whole number of words.
func dibOf(img *image.RGBA) ([]byte, int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	stride := (w*3 + 3) &^ 3
	out := make([]byte, stride*h)
	for y := range h {
		src := img.Pix[y*img.Stride : y*img.Stride+w*4]
		dst := out[y*stride:]
		for x := range w {
			dst[x*3], dst[x*3+1], dst[x*3+2] = src[x*4+2], src[x*4+1], src[x*4]
		}
	}
	return out, stride
}
