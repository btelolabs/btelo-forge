package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/chart"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
)

// Office documents: a tab holding a Word document, a spreadsheet, a presentation or a PDF, kept in the file's own
// format. Each format brings an editor (officeEditor) and registers it in officeFormats; this file is what they
// share: opening, saving, the history keys, the status line and the new tab's cards. A document saves itself a moment
// after each change, as a canvas does, and because the file is the user's own, what it held when the app first
// overwrote it is kept in the backups folder.

// Icons of the Office tabs.
const (
	icOfficeWord   = "" // description
	icOfficeSheet  = "" // table_view
	icOfficeSlides = "" // present_to_all... a screen with an arrow
	icOfficePDF    = "" // picture_as_pdf
)

// officeEditor is one open document.
type officeEditor interface {
	// draw draws the document in r and handles what is done in it, keys included: an editor holding the caret sets
	// u.focus to its own id and u.focusDrawn to true each frame, so that the window's shortcuts stay out of it and the
	// system's input methods work.
	draw(r gfx.Rect)
	// snapshot takes what is to be saved, here on the window's goroutine, and returns the work of writing it out,
	// which runs on another. It finishes what is being typed first.
	snapshot() (write func() ([]byte, error), err error)
	// version changes with every change to the document.
	version() uint64
	// holding reports that a drag or a word being typed is under way, so that saving waits.
	holding() bool
	undo()
	redo()
	// wait is how soon the editor wants another frame, zero for none.
	wait() time.Duration
	// dropped takes files dropped on the tab, and says whether it used them.
	dropped(paths []string) bool
	// closing is told that the tab is going away.
	closing()
}

// officeFormat is one kind of document.
type officeFormat struct {
	id    string   // for the tools: "word", "sheet", "slides", "pdf"
	exts  []string // the first is what new files get
	glyph string
	// open reads a document.
	open func(a *App, od *officeDoc, data []byte) (officeEditor, error)
	// blank is a new, empty document, nil where the format has none.
	blank func(a *App) ([]byte, error)
	// name is the base name of a new file, a message with a %s for the time.
	name string
}

// officeFormats are the formats, in the order of the new tab's cards; each format's file adds itself.
var officeFormats []*officeFormat

func registerOffice(f *officeFormat) {
	officeFormats = append(officeFormats, f)
	// Files initialise in the order of their names; the cards go in the order people know these programs.
	slices.SortStableFunc(officeFormats, func(a, b *officeFormat) int {
		return slices.Index(officeOrder, a.id) - slices.Index(officeOrder, b.id)
	})
}

var officeOrder = []string{"word", "sheet", "slides", "pdf"}

// officeFormatOf is the format of a file by its name, nil when it has none.
func officeFormatOf(path string) *officeFormat {
	ext := strings.ToLower(filepath.Ext(path))
	for _, f := range officeFormats {
		if slices.Contains(f.exts, ext) {
			return f
		}
	}
	return nil
}

// officeLegacyExts are Office's old binary formats, which are not read: they say so rather than open as something else.
var officeLegacyExts = []string{".doc", ".xls", ".ppt"}

// officeDoc is an Office tab.
type officeDoc struct {
	path   string
	format *officeFormat
	ed     officeEditor

	dirty    bool
	changed  time.Time
	saving   bool
	saveErr  error
	saved    uint64 // the document's version when it was last saved
	backedUp bool   // the file as it was opened is in the backups folder, or there was none to keep
}

// title is the tab's: the file's name, with its extension as the kinds look alike.
func (od *officeDoc) title() string { return filepath.Base(od.path) }

// officeBook is the fonts documents are laid out and drawn with; a document names families, and they load once.
func (a *App) officeBook() *flow.Book {
	if a.officeFonts == nil {
		a.officeFonts = flow.NewBook(a.u.fonts)
		chart.SeriesName = func(n int) string { return a.u.tf("Series %d", n) }
	}
	return a.officeFonts
}

// openOffice shows a document in a tab, the current one when it is empty.
func (a *App) openOffice(f *officeFormat, path string, data []byte) (*officeDoc, error) {
	od := &officeDoc{path: path, format: f}
	ed, err := f.open(a, od, data)
	if err != nil {
		return nil, err
	}
	od.ed, od.saved = ed, ed.version()
	if a.occupied() {
		a.newTab()
	}
	a.office = od
	a.requestFrame()
	return od, nil
}

// officeError says what is wrong with a file that could not be opened.
func (a *App) officeError(path string, err error) string {
	u := &a.u
	switch {
	case errors.Is(err, opc.ErrEncrypted):
		return u.tf("%s is protected with a password, or is in an old Office format. Open it in Office and save it without a password as .docx, .xlsx or .pptx.", filepath.Base(path))
	case errors.Is(err, opc.ErrNotOffice):
		return u.tf("%s is not an Office document, whatever its name says.", filepath.Base(path))
	}
	return u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err))
}

// openOfficeFile opens a document, or shows the tab that has it open. The file is read away from the window.
func (a *App) openOfficeFile(path string) {
	if slices.Contains(officeLegacyExts, strings.ToLower(filepath.Ext(path))) {
		a.toast(a.u.tf("%s is in an old Office format. Open it in Office and save it as .docx, .xlsx or .pptx.", filepath.Base(path)), true)
		return
	}
	f := officeFormatOf(path)
	if f == nil {
		return
	}
	if a.showOffice(path) || a.showRestoring(path) {
		return
	}
	a.background(func(ctx context.Context) {
		data, err := os.ReadFile(path)
		a.post(func() {
			if err == nil {
				if a.showOffice(path) {
					return // opened twice before the first was read
				}
				_, err = a.openOffice(f, path, data)
			}
			if err != nil {
				a.toast(a.officeError(path, err), true)
			}
		})
	})
}

// showOffice brings forward the tab that has the file, if one does.
func (a *App) showOffice(path string) bool {
	for i, d := range a.docs {
		if d.office != nil && samePath(d.office.path, path) {
			a.switchTab(i)
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// newOffice makes a new document in a tab, saved at once so it is never only in memory.
func (a *App) newOffice(f *officeFormat) (*officeDoc, error) {
	path, err := a.capturePath(categoryDocuments, a.u.t(f.name), f.exts[0], time.Now())
	if err != nil {
		return nil, err
	}
	data, err := f.blank(a)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := writeFileWhole(path, data); err != nil {
		return nil, err
	}
	od, err := a.openOffice(f, path, data)
	if err != nil {
		return nil, err
	}
	od.backedUp = true
	return od, nil
}

// newOfficeNow makes a document, telling the user when it cannot.
func (a *App) newOfficeNow(f *officeFormat) {
	if _, err := a.newOffice(f); err != nil {
		a.toast(a.u.tf("The document could not be saved: %s", a.describe(err)), true)
	}
}

// chooseOffice asks for a file of the format and opens it.
func (a *App) chooseOffice(f *officeFormat, title, kinds string) {
	a.pickFiles(pickRequest{key: "office", title: title, kinds: kinds, exts: f.exts, multiple: true}, func(paths []string) {
		for _, p := range paths {
			a.openOfficeFile(p)
		}
	})
}

// imeFontHost is a window that can show words being composed at a given size.
type imeFontHost interface {
	TextInputFont(px float32, family string)
}

// imeWords puts the input method on a line whose words are px pixels high, in family: the composition sits on the
// line's foot, as the words do, however much spacing is above them, and the candidates stay under the line.
func (u *ctx) imeWords(x, top, height, px float32, family string) {
	y := max(top, top+height-px*imeLineHeight)
	u.imeAt, u.imeFont, u.imeFamily = [3]float32{x, y, top + height - y}, px, family
}

// imeLineHeight is how tall a line of a font is, for its size.
const imeLineHeight = 1.3

// officeSaveDelay is how long after the last change a document is saved.
const officeSaveDelay = 3 * time.Second

// saveOffice writes the document: taken here, written on another goroutine, or here when wait is true (closing, or
// the user asking).
func (a *App) saveOffice(od *officeDoc, wait bool) error {
	if !od.dirty || od.saving && !wait {
		return nil
	}
	write, err := od.ed.snapshot()
	if err != nil {
		return err
	}
	od.dirty, od.saving = false, true
	version, path, backup := od.ed.version(), od.path, !od.backedUp
	dir := filepath.Join(a.stateDir, "office-backups")
	work := func() error {
		data, err := write()
		if err != nil {
			return err
		}
		if backup {
			if err := keepOfficeBackup(dir, path); err != nil {
				return err
			}
		}
		return writeFileWhole(path, data)
	}
	done := func(err error) {
		od.saving, od.saveErr = false, err
		if err != nil {
			od.dirty, od.changed = true, time.Now() // tried again after the usual delay
			a.toast(a.u.tf("The document could not be saved: %s", a.describe(err)), true)
			return
		}
		od.saved, od.backedUp = version, true
	}
	if wait {
		err := work()
		done(err)
		return err
	}
	a.background(func(context.Context) {
		err := work()
		a.post(func() { done(err) })
	})
	return nil
}

// officeBackups is how many backups are kept; the oldest go first.
const officeBackups = 200

// keepOfficeBackup copies the file as it is into dir under a name that tells two files of one name apart, and
// keeps the newest few. A file already gone has nothing to keep.
func keepOfficeBackup(dir, path string) error {
	in, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	name := fmt.Sprintf("%s-%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(sum[:4]), filepath.Base(path))
	out, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // the backup is made; pruning can wait
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for i := 0; i+officeBackups < len(entries); i++ {
		os.Remove(filepath.Join(dir, entries[i].Name()))
	}
	return nil
}

// officeSaveDue is when a document with changes is saved; ok is false when there is nothing to save or the editor
// is in the middle of something.
func officeSaveDue(od *officeDoc) (time.Time, bool) {
	if !od.dirty || od.saving || od.ed.holding() {
		return time.Time{}, false
	}
	return od.changed.Add(officeSaveDelay), true
}

// autosaveOffice notes changes made since the last frame and saves what is due.
func (a *App) autosaveOffice() {
	for _, d := range a.docs {
		od := d.office
		if od == nil {
			continue
		}
		if od.ed.version() != od.saved && !od.dirty && !od.saving {
			od.dirty, od.changed = true, time.Now()
		}
		if due, ok := officeSaveDue(od); ok && !time.Now().Before(due) {
			if err := a.saveOffice(od, false); err != nil {
				od.changed = time.Now()
				a.toast(a.u.tf("The document could not be saved: %s", a.describe(err)), true)
			}
		}
	}
}

// closeOffice saves a tab that is going away.
func (a *App) closeOffice(od *officeDoc) {
	if od.ed.version() != od.saved {
		od.dirty = true
	}
	if err := a.saveOffice(od, true); err != nil {
		a.toast(a.u.tf("The document could not be saved: %s", a.describe(err)), true)
	}
	od.ed.closing()
}

// saveOfficeTo saves the document into a folder the user chooses, and goes on in that file; the one it was in stays.
func (a *App) saveOfficeTo(od *officeDoc, dir string) error {
	to := filepath.Join(dir, filepath.Base(od.path))
	if samePath(to, od.path) {
		return nil
	}
	if _, err := os.Stat(to); err == nil {
		return errNameTaken
	}
	od.dirty = od.dirty || od.ed.version() != od.saved
	if err := a.saveOffice(od, true); err != nil {
		return err
	}
	data, err := os.ReadFile(od.path)
	if err != nil {
		return err
	}
	if err := writeFileWhole(to, data); err != nil {
		return err
	}
	od.path = to
	return nil
}

// renameOfficeFile moves the file to the new name beside it, as a tab's name is edited.
func (a *App) renameOfficeFile(od *officeDoc, name string) error {
	to := filepath.Join(filepath.Dir(od.path), name+filepath.Ext(od.path))
	if _, err := os.Stat(to); err == nil {
		return errNameTaken
	}
	od.dirty = od.ed.version() != od.saved
	if err := a.saveOffice(od, true); err != nil {
		return err
	}
	if err := os.Rename(od.path, to); err != nil {
		return err
	}
	od.path = to
	return nil
}

// drawOffice draws the tab: the editor over a status line.
func (a *App) drawOffice(r gfx.Rect) {
	u := &a.u
	od := a.office
	if od == nil {
		u.c.Fill(r, colPanel)
		return // the tab strip above closed this tab, or switched from it, in this frame
	}
	if !u.blocked {
		a.officeKeys(od)
	}
	status := gfx.Rect{X: r.X, Y: r.Bottom() - u.px(24), W: r.W, H: u.px(24)}
	od.ed.draw(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - status.H})
	u.c.Fill(status, colBackground)
	text, col := u.t("Saved"), colFaint
	switch {
	case od.saveErr != nil:
		text, col = u.t("Not saved"), colDangerText
	case od.saving || od.dirty || od.ed.version() != od.saved:
		text = u.t("Saving…")
	}
	f := u.face(11.5)
	u.label(f, gfx.Rect{X: status.X + u.px(10), Y: status.Y, W: status.W / 2, H: status.H}, od.path, colFaint, 0)
	move := u.t("Save to a folder…")
	bw := u.buttonWidth("", move)
	btn := gfx.Rect{X: status.Right() - bw - u.px(4), Y: status.Y + u.px(1), W: bw, H: status.H - u.px(2)}
	if u.button("office/saveto", btn, "", move, ghost, true) {
		a.pickDir(pickRequest{key: "office", title: move}, func(dir string) {
			if err := a.saveOfficeTo(od, dir); err != nil {
				a.toast(a.u.tf("The document could not be saved: %s", a.describe(err)), true)
			}
		})
	}
	u.label(f, gfx.Rect{X: status.X + status.W/2, Y: status.Y, W: btn.X - u.px(10) - status.X - status.W/2, H: status.H}, text, col, 1)
}

// officeKeys takes the keys every format means the same by: they are taken from the frame's keys, so an editor
// drawing after this does not see them.
func (a *App) officeKeys(od *officeDoc) {
	u := &a.u
	keep := u.keys[:0:0]
	for _, k := range u.keys {
		switch {
		case k.Ctrl && !k.Alt && k.Code == 'S':
			if od.ed.version() != od.saved {
				od.dirty = true
			}
			if err := a.saveOffice(od, true); err != nil {
				a.toast(a.u.tf("The document could not be saved: %s", a.describe(err)), true)
			}
		case k.Ctrl && !k.Alt && !k.Shift && k.Code == 'P':
			a.printOffice(od)
		case k.Ctrl && !k.Alt && !k.Shift && k.Code == 'Z':
			od.ed.undo()
		case k.Ctrl && !k.Alt && (k.Code == 'Y' || k.Shift && k.Code == 'Z'):
			od.ed.redo()
		default:
			keep = append(keep, k)
		}
	}
	u.keys = keep
}

// officeWait is how soon the Office tab wants a frame.
func (a *App) officeWait() time.Duration {
	if a.office == nil {
		return 0
	}
	return a.office.ed.wait()
}

// officeCards are the new tab's cards for the Office formats.
func (a *App) officeCards() []startCard {
	u := &a.u
	var out []startCard
	for _, f := range officeFormats {
		c := officeCardText(f.id)
		card := startCard{id: "empty/office/" + f.id, glyph: f.glyph, hue: c.hue, title: u.t(c.words[0]), text: u.t(c.words[1]), action: u.t(c.words[2]), enabled: true}
		if f.blank != nil {
			card.run = func() { a.newOfficeNow(f) }
		} else {
			card.run = func() { a.chooseOffice(f, u.t(c.words[2]), u.t(c.words[0])) }
		}
		out = append(out, card)
	}
	return out
}

// officeCard is what a format's card says: its title, text and action.
type officeCard struct {
	words []string
	hue   color.RGBA
}

// officeCardText is the card of a format.
func officeCardText(id string) officeCard {
	switch id {
	case "word":
		return officeCard{msgs("Write documents", "Pages with styles, tables and pictures, as .docx.", "New document"), gfx.Hex(0x4d8bf0)}
	case "sheet":
		return officeCard{msgs("Work in spreadsheets", "Formulas, formats and merged cells, as .xlsx.", "New spreadsheet"), gfx.Hex(0x3fb27f)}
	case "slides":
		return officeCard{msgs("Make presentations", "Slides shown full screen, saved as .pptx.", "New presentation"), gfx.Hex(0xe0764c)}
	}
	return officeCard{msgs("Edit PDFs", "Mark up, fill in, sign, and rearrange pages.", "Open a PDF…"), gfx.Hex(0xd9534f)}
}

// officeGlyph is a file's symbol in the file manager: its format's, in its colour.
func officeGlyph(name string) (string, color.RGBA) {
	if f := officeFormatOf(name); f != nil {
		return f.glyph, officeCardText(f.id).hue
	}
	return icFmFile, colDim
}

// officeExtensions are every format's extensions.
func officeExtensions() []string {
	var out []string
	for _, f := range officeFormats {
		out = append(out, f.exts...)
	}
	return out
}

// domainOffice is the Office tools' MCP server. Office has no shortcuts of its own to list, so it is not a domain of the
// keymap, and its number stays out of that range.
const domainOffice = 100

// mcpOfficeInstructions are the Office server's instructions, a part from each format.
var mcpOfficeInstructions = strings.Join([]string{wordMCPInstructions, sheetMCPInstructions, slidesMCPInstructions, pdfMCPInstructions}, "\n\n")

// officeMCPTools are the tools of the Office server.
var officeMCPTools = slices.Concat(wordMCPTools, sheetMCPTools, slidesMCPTools, pdfMCPTools)

// addOfficeTools adds every format's tools.
func (a *App) addOfficeTools(s *mcp.Server) {
	a.addWordTools(s)
	a.addSheetTools(s)
	a.addSlidesTools(s)
	a.addPDFTools(s)
	a.addWordExport(s)
	a.addSheetExport(s)
	a.addSlidesExport(s)
}
