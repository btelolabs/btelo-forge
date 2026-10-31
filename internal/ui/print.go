package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

// Printing and exporting to PDF. Each format brings a printSource, which draws its pages into the display list of
// internal/printing and offers its own choices; this file is what they share: the Print dialog with its preview,
// which is the same list drawn on the screen, and the jobs, which build their pages a few at a time on the window's
// goroutine, where the document may be read, and then go off to the printer or the file on another, with a task card
// to follow and cancel them.

const (
	icPrint    = ""
	icPrevPage = ""
	icNextPage = ""
)

// printSource is a document as it prints.
type printSource interface {
	// name is the document's name, for the job and the file.
	name() string
	// options draws the format's own choices from y down, in a column w wide, and says where it ended.
	options(p *printDialog, x, y, w float32) float32
	// count is how many pages there are as the choices stand.
	count() int
	// page draws one. It runs on the window's goroutine.
	page(i int) (*printing.Page, error)
}

// fileSource is a source that is a file a viewer can show as it is: it needs no exporting.
type fileSource interface {
	printSource
	file() string
}

// apartSource is a source whose pages can be drawn away from the window.
type apartSource interface {
	printSource
	apart() bool
}

// printerHost is a window that can ask which printer to use.
type printerHost interface {
	ChoosePrinter(last *printing.Target) (*printing.Target, error)
}

// printPaper is a sheet to lay pages out on, for the formats that have none of their own.
type printPaper struct {
	name string
	w, h float64
}

var printPapers = []printPaper{{"A4", 595.28, 841.89}, {"Letter", 612, 792}, {"Legal", 612, 1008}, {"A3", 841.89, 1190.55}, {"A5", 419.53, 595.28}}

func printPaperNames() []string {
	out := make([]string, len(printPapers))
	for i, p := range printPapers {
		out[i] = p.name
	}
	return out
}

// printDialog is the open Print dialog.
type printDialog struct {
	a   *App
	src printSource

	span  string // the pages asked for, as typed: "1-3, 5"
	at    int    // the page shown, among those asked for
	gen   int    // goes up as the pages change
	pages map[int]*printing.Page
	err   error

	preview    *image.RGBA
	previewFor [3]int // gen, page, width in pixels
	asked      [3]int
	loading    bool
	drawing    [2]int // the generation and page being drawn away from the window

	run *printRun
}

// printRun is pages being built for a job, a few each frame.
type printRun struct {
	list    []int
	next    int
	pages   []*printing.Page
	done    func(*printing.Doc)
	started bool // the pages are being drawn on another goroutine
}

// printFrameBudget is how long a frame may spend building pages.
const printFrameBudget = 12 * time.Millisecond

// openPrint shows the Print dialog for a document.
func (a *App) openPrint(src printSource) {
	a.print = &printDialog{a: a, src: src, pages: map[int]*printing.Page{}}
	a.requestFrame()
}

// changed is told that the choices changed, so the pages are drawn again.
func (p *printDialog) changed() {
	p.gen++
	clear(p.pages)
	p.err = nil
	p.at = 0
}

// parsePages reads "1-3, 5" into pages counted from zero, in the order asked; an empty string is all of them, and
// false means the words cannot be read or name no page.
func parsePages(s string, total int) ([]int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		out := make([]int, total)
		for i := range out {
			out[i] = i
		}
		return out, true
	}
	var out []int
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == ' ' }) {
		from, to, ranged := strings.Cut(part, "-")
		a, err := strconv.Atoi(from)
		if err != nil {
			return nil, false
		}
		b := a
		if ranged {
			if b, err = strconv.Atoi(to); err != nil {
				return nil, false
			}
		}
		if a < 1 || b < a {
			return nil, false
		}
		for i := a; i <= min(b, total); i++ {
			out = append(out, i-1)
		}
	}
	return out, len(out) > 0
}

// chosen is the pages asked for.
func (p *printDialog) chosen() ([]int, bool) { return parsePages(p.span, p.src.count()) }

// apart reports that the source's pages are drawn away from the window.
func (p *printDialog) apart() bool {
	s, ok := p.src.(apartSource)
	return ok && s.apart()
}

// pageAt is a page, nil while it is being drawn away from the window.
func (p *printDialog) pageAt(i int) *printing.Page {
	if pg, ok := p.pages[i]; ok {
		return pg
	}
	if p.apart() {
		if p.drawing != [2]int{p.gen + 1, i} {
			p.drawing = [2]int{p.gen + 1, i}
			gen := p.gen
			p.a.background(func(context.Context) {
				pg, err := p.src.page(i)
				p.a.post(func() {
					if gen == p.gen {
						p.keep(i, pg, err)
					}
				})
			})
		}
		return nil
	}
	pg, err := p.src.page(i)
	return p.keep(i, pg, err)
}

// keep holds a page drawn.
func (p *printDialog) keep(i int, pg *printing.Page, err error) *printing.Page {
	if err != nil {
		p.err = err
	}
	if pg == nil {
		pg = &printing.Page{W: 595, H: 842}
	}
	if len(p.pages) > 64 {
		clear(p.pages)
	}
	p.pages[i] = pg
	return pg
}

// choice is a labelled dropdown of the dialog.
func (p *printDialog) choice(id, label string, options []string, selected int, x float32, y *float32, w float32, pick func(int)) {
	a := p.a
	u := &a.u
	u.label(u.face(12), gfx.Rect{X: x, Y: *y, W: w, H: u.px(20)}, label, colDim, 0)
	*y += u.px(21)
	a.dropdown("print/"+id, gfx.Rect{X: x, Y: *y, W: w, H: u.px(30)}, options, selected, nil, func(i int) {
		pick(i)
		p.changed()
	})
	*y += u.px(38)
}

// check is a tick box of the dialog.
func (p *printDialog) check(id, text string, on *bool, x float32, y *float32, w float32) {
	u := &p.a.u
	next, h := u.checkboxWrapped("print/"+id, x, *y, w, text, *on)
	if next != *on {
		*on = next
		p.changed()
	}
	*y += h + u.px(6)
}

// note is a line of quiet words of the dialog.
func (p *printDialog) note(text string, x float32, y *float32, w float32) {
	u := &p.a.u
	f := u.face(11.5)
	for _, line := range f.Wrap(text, w) {
		u.label(f, gfx.Rect{X: x, Y: *y, W: w, H: u.px(17)}, line, colFaint, 0)
		*y += u.px(17)
	}
	*y += u.px(6)
}

// drawPrintDialog draws the dialog, when one is open.
func (a *App) drawPrintDialog() {
	p := a.print
	if p == nil {
		return
	}
	u := &a.u
	u.c.Fill(a.screen, gfxShade)
	box := rectCentered(a.screen, min(u.px(900), a.screen.W-u.px(32)), min(u.px(640), a.screen.H-u.px(32)))
	panel(u, box)
	side := u.px(280)
	in := box.Inset(u.px(20))
	view := gfx.Rect{X: in.X, Y: in.Y, W: in.W - side - u.px(20), H: in.H}
	x, w := view.Right()+u.px(20), side
	held := u.blocked
	u.blocked = a.menu != nil || p.run != nil
	defer func() { u.blocked = held }()
	u.label(u.bold(16), gfx.Rect{X: x, Y: in.Y, W: w, H: u.px(24)}, u.t("Print"), colText, 0)
	u.label(u.face(12), gfx.Rect{X: x, Y: in.Y + u.px(24), W: w, H: u.px(20)}, p.src.name(), colDim, 0)
	y := in.Y + u.px(54)
	y = p.src.options(p, x, y, w)
	total := p.src.count()
	u.label(u.face(12), gfx.Rect{X: x, Y: y, W: w, H: u.px(20)}, u.tf("Pages (%d in all)", total), colDim, 0)
	y += u.px(21)
	if changed, _ := u.textBox("print/span", gfx.Rect{X: x, Y: y, W: w, H: u.px(32)}, &p.span, u.t("All, or as 1-3, 5"), false); changed {
		p.at = 0
	}
	y += u.px(38)
	list, ok := p.chosen()
	if !ok {
		p.note(u.t("That does not name a page of the document."), x, &y, w)
		list = nil
	}
	if p.err != nil {
		p.note(a.describe(p.err), x, &y, w)
	}
	p.drawPreview(view, list)
	bh := u.px(36)
	foot := in.Bottom() - bh
	if p.run != nil {
		p.step()
		if a.print == nil {
			return
		}
		frac := float32(p.run.next) / float32(max(len(p.run.list), 1))
		u.label(u.face(12), gfx.Rect{X: x, Y: foot - u.px(26), W: w, H: u.px(20)}, u.tf("Preparing pages: %d of %d", p.run.next, len(p.run.list)), colDim, 0)
		bar := gfx.Rect{X: x, Y: foot - u.px(4), W: w, H: u.px(4)}
		u.c.Fill(bar, colSurfaceHi)
		u.c.Fill(gfx.Rect{X: bar.X, Y: bar.Y, W: bar.W * frac, H: bar.H}, colAccent)
	}
	third := (w - u.px(12)) / 3
	go1 := gfx.Rect{X: x + 2*(third+u.px(6)), Y: foot, W: third, H: bh}
	pdfB := gfx.Rect{X: x + third + u.px(6), Y: foot, W: third, H: bh}
	cancel := gfx.Rect{X: x, Y: foot, W: third, H: bh}
	saved := u.blocked
	u.blocked = a.menu != nil
	if u.button("print/cancel", cancel, "", u.t("Cancel"), ghost, true) {
		a.print = nil
		return
	}
	u.blocked = saved
	ok = ok && len(list) > 0
	if _, isFile := p.src.(fileSource); !isFile && u.button("print/pdf", pdfB, "", u.t("Save PDF"), plain, ok && p.run == nil) {
		a.printToPDF(p, list)
		return
	}
	if u.button("print/go", go1, icPrint, u.t("Print…"), primary, ok && p.run == nil) {
		a.printToPrinter(p, list)
		return
	}
	if u.focus == "" {
		for _, k := range u.keys {
			if k.Code == keyEscape {
				a.print = nil
				return
			}
		}
	}
}

// drawPreview shows the page in view, drawn from the display list.
func (p *printDialog) drawPreview(view gfx.Rect, list []int) {
	a := p.a
	u := &a.u
	u.c.Fill(view, colBackground)
	nav := u.px(36)
	area := gfx.Rect{X: view.X + u.px(12), Y: view.Y + u.px(12), W: view.W - u.px(24), H: view.H - nav - u.px(12)}
	if len(list) == 0 {
		return
	}
	p.at = min(max(p.at, 0), len(list)-1)
	index := list[p.at]
	pg := p.pageAt(index)
	if pg == nil {
		a.requestFrame()
		return
	}
	k := min(float64(area.W)/pg.W, float64(area.H)/pg.H)
	w, h := float32(pg.W*k), float32(pg.H*k)
	r := gfx.Rect{X: area.X + (area.W-w)/2, Y: area.Y + (area.H-h)/2, W: w, H: h}
	want := [3]int{p.gen, index, int(w)}
	if p.previewFor != want && !p.loading && p.asked != want {
		p.loading, p.asked = true, want
		a.background(func(ctx context.Context) {
			img := printing.Render(pg, k, image.Rectangle{})
			a.post(func() {
				p.loading = false
				p.preview, p.previewFor = img, want
			})
		})
	}
	u.c.Fill(gfx.Rect{X: r.X + u.px(3), Y: r.Y + u.px(4), W: r.W, H: r.H}, gfx.Alpha(colBlack, 0.4))
	u.c.Fill(r, colWhite)
	if p.preview != nil && p.previewFor == want {
		u.c.Blit(p.preview, int(r.X), int(r.Y), true)
	}
	y := view.Bottom() - nav + u.px(4)
	mid := view.X + view.W/2
	side := u.px(28)
	if u.iconButton("print/prev", gfx.Rect{X: mid - u.px(70) - side, Y: y, W: side, H: side}, icPrevPage, u.t("Previous page"), false, p.at > 0) {
		p.at--
	}
	if u.iconButton("print/next", gfx.Rect{X: mid + u.px(70), Y: y, W: side, H: side}, icNextPage, u.t("Next page"), false, p.at+1 < len(list)) {
		p.at++
	}
	u.label(u.face(12.5), gfx.Rect{X: mid - u.px(66), Y: y, W: u.px(132), H: side}, fmt.Sprintf("%d / %d", p.at+1, len(list)), colDim, 0.5)
}

// step builds pages until the frame's time is up, and hands them over when all are done.
func (p *printDialog) step() {
	run := p.run
	if p.apart() {
		if !run.started {
			run.started = true
			src, a := p.src, p.a
			a.background(func(ctx context.Context) {
				var firstErr error
				for i, n := range run.list {
					if ctx.Err() != nil {
						return
					}
					pg, err := src.page(n)
					if err != nil && firstErr == nil {
						firstErr = err
					}
					a.post(func() {
						if pg == nil {
							pg = &printing.Page{W: 595, H: 842}
						}
						run.pages = append(run.pages, pg)
						run.next = i + 1
					})
				}
				a.post(func() {
					if a.print != p {
						return
					}
					if firstErr != nil {
						a.toast(a.describe(firstErr), true)
					}
					a.print = nil
					run.done(&printing.Doc{Title: src.name(), Pages: run.pages})
				})
			})
		}
		return
	}
	start := time.Now()
	for run.next < len(run.list) {
		run.pages = append(run.pages, p.pageAt(run.list[run.next]))
		run.next++
		if time.Since(start) > printFrameBudget {
			p.a.requestFrame()
			return
		}
	}
	p.a.print = nil
	run.done(&printing.Doc{Title: p.src.name(), Pages: run.pages})
}

// printToPrinter asks for a printer, then builds the pages and prints. Where this package drives no printer it goes
// by way of a PDF in the system's viewer.
func (a *App) printToPrinter(p *printDialog, list []int) {
	host, ok := a.host.(printerHost)
	if !ok || !printing.Available {
		if f, isFile := p.src.(fileSource); isFile {
			a.print = nil
			if err := a.host.Launch(f.file()); err != nil {
				a.toast(a.u.tf("Printing failed: %s", a.describe(err)), true)
				return
			}
			a.toast(a.u.t("The PDF is open in the system's viewer; print it from there."), false)
			return
		}
		p.run = &printRun{list: list, done: a.printByViewer}
		return
	}
	t, err := host.ChoosePrinter(a.printTarget)
	if err != nil {
		a.toast(a.u.tf("Printing failed: %s", a.describe(err)), true)
		return
	}
	if t == nil {
		return
	}
	a.printTarget = t
	p.run = &printRun{list: list, done: func(d *printing.Doc) { a.startPrint(d, t) }}
}

// printJob is printing or exporting under way.
type printJob struct {
	id          int
	title       string
	done, total atomic.Int64
	stopping    bool
	cancel      func()
}

// newPrintJob makes a job, listed for its task card.
func (a *App) newPrintJob(title string, total int) (context.Context, *printJob) {
	ctx, cancel := context.WithCancel(a.life)
	a.printSeq++
	j := &printJob{id: a.printSeq, title: title}
	j.total.Store(int64(total))
	j.cancel = func() {
		j.stopping = true
		cancel()
	}
	a.printJobs = append(a.printJobs, j)
	return ctx, j
}

func (a *App) endPrintJob(j *printJob) {
	for i, k := range a.printJobs {
		if k == j {
			a.printJobs = append(a.printJobs[:i], a.printJobs[i+1:]...)
			return
		}
	}
}

// printTasks are the task cards of the jobs.
func (a *App) printTasks() []task {
	var out []task
	for _, j := range a.printJobs {
		done, total := j.done.Load(), j.total.Load()
		out = append(out, task{id: fmt.Sprintf("print/%d", j.id), glyph: icPrint, title: j.title, status: a.u.tf("%d of %d pages", done, total),
			fraction: float64(done) / float64(max(total, 1)), stopping: j.stopping, cancel: j.cancel})
	}
	return out
}

// startPrint sends a document to a printer on a goroutine of its own.
func (a *App) startPrint(d *printing.Doc, t *printing.Target) {
	ctx, j := a.newPrintJob(a.u.tf("Printing %s", d.Title), len(d.Pages)*max(t.Copies, 1))
	a.background(func(context.Context) {
		err := t.Print(ctx, d, func(done, total int) {
			j.done.Store(int64(done))
			j.total.Store(int64(total))
			a.post(func() {})
		})
		a.post(func() {
			a.endPrintJob(j)
			switch {
			case errors.Is(err, context.Canceled):
				a.toast(a.u.t("Printing was cancelled."), false)
			case err != nil:
				a.toast(a.u.tf("Printing failed: %s", a.describe(err)), true)
			default:
				a.toast(a.u.tf("%s has gone to the printer.", d.Title), false)
			}
		})
	})
}

// encodePDF is a document as a PDF file's bytes.
func encodePDF(ctx context.Context, d *printing.Doc, progress func(int)) ([]byte, error) {
	var b bytes.Buffer
	if err := printing.WritePDF(ctx, &b, d, progress); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// printToPDF asks where to save the PDF, named after the document at first, then builds the pages and writes it there.
func (a *App) printToPDF(p *printDialog, list []int) {
	r := pickRequest{key: "pdf", title: a.u.t("Save the PDF"), kinds: a.u.t("PDF files"), exts: []string{".pdf"}, name: p.src.name() + ".pdf"}
	a.pickSaveAs(r, func(to string) {
		p.run = &printRun{list: list, done: func(d *printing.Doc) { a.savePDF(d, to) }}
	})
}

// savePDF writes a document away from the window, with a task card.
func (a *App) savePDF(d *printing.Doc, to string) {
	ctx, j := a.newPrintJob(a.u.tf("Saving %s", filepath.Base(to)), len(d.Pages))
	a.background(func(context.Context) {
		data, err := encodePDF(ctx, d, func(done int) {
			j.done.Store(int64(done))
			a.post(func() {})
		})
		if err == nil {
			err = writeFileWhole(to, data)
		}
		a.post(func() {
			a.endPrintJob(j)
			switch {
			case errors.Is(err, context.Canceled):
				a.toast(a.u.t("The PDF was not saved."), false)
			case err != nil:
				a.toast(a.u.tf("The PDF could not be saved: %s", a.describe(err)), true)
			default:
				a.toastActions(a.u.tf("Saved %s", filepath.Base(to)), false, []toastAction{
					{a.u.t("Open"), func() { a.host.Launch(to) }},
					{a.u.t("Show in folder"), func() { a.host.Reveal(to) }},
				})
			}
		})
	})
}

// printByViewer opens the document as a PDF in the system's viewer, whose Print is the way to paper on this system.
func (a *App) printByViewer(d *printing.Doc) {
	ctx, j := a.newPrintJob(a.u.tf("Printing %s", d.Title), len(d.Pages))
	a.background(func(context.Context) {
		data, err := encodePDF(ctx, d, func(done int) {
			j.done.Store(int64(done))
			a.post(func() {})
		})
		to := ""
		if err == nil {
			to = filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.pdf", d.Title, time.Now().UnixNano()))
			err = writeFileWhole(to, data)
		}
		a.post(func() {
			a.endPrintJob(j)
			if err == nil {
				err = a.host.Launch(to)
			}
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					a.toast(a.u.tf("Printing failed: %s", a.describe(err)), true)
				}
				return
			}
			a.toast(a.u.t("The PDF is open in the system's viewer; print it from there."), false)
		})
	})
}

// allPages builds every page of a source at once, for the tools.
func allPages(src printSource) (*printing.Doc, error) {
	d := &printing.Doc{Title: src.name()}
	for i := range src.count() {
		pg, err := src.page(i)
		if err != nil {
			return nil, err
		}
		d.Pages = append(d.Pages, pg)
	}
	return d, nil
}

// docName is a file's name without its extension.
func docName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// fitPaper is the sheet for a picture of w × h, turned to suit it.
func fitPaper(paper printPaper, w, h float64) (float64, float64) {
	if w > h {
		return max(paper.w, paper.h), min(paper.w, paper.h)
	}
	return min(paper.w, paper.h), max(paper.w, paper.h)
}

// maxTiles bounds the pages a picture is spread over: an unbounded canvas could otherwise ask for thousands.
const maxTiles = 256

// tileScale is the scale, as near k as maxTiles allows, and the pages across and down for something cw × ch.
func tileScale(k, cw, ch, w, h float64) (float64, int, int) {
	nx, ny := tilesOver(cw*k, ch*k, w, h)
	for nx*ny > maxTiles {
		k *= 0.9
		nx, ny = tilesOver(cw*k, ch*k, w, h)
	}
	return k, nx, ny
}

// tilesOver is how many pages of size w × h it takes to cover size W × H.
func tilesOver(cw, ch, w, h float64) (int, int) {
	n := func(a, b float64) int {
		if b <= 0 {
			return 1
		}
		k := int(a / b)
		if float64(k)*b < a-0.01 {
			k++
		}
		return max(k, 1)
	}
	return n(cw, w), n(ch, h)
}
