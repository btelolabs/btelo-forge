package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// filesHarness opens a file manager tab on a folder of test files.
func filesHarness(t *testing.T, layout map[string]string) (*harness, string) {
	t.Helper()
	x := newHarness(t)
	root := filepath.Join(x.dir, "work")
	for rel, body := range layout {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	x.a.newFilesTab(root, false)
	x.waitFiles()
	if x.a.files == nil {
		t.Fatal("no file manager tab")
	}
	return x, root
}

// waitFiles draws frames until the lists are read and no file work runs.
func (x *harness) waitFiles() {
	x.t.Helper()
	x.pump(20*time.Second, func() bool {
		fd := x.a.files
		if fd == nil {
			return true
		}
		for _, p := range fd.panes {
			if p.reading {
				return false
			}
		}
		return !x.a.fileWork()
	})
}

// press taps a key with modifiers written as key does ("c", "s", "cs").
func (x *harness) press(code uint16, mods string) {
	k := key(code, mods)
	x.frame(Input{Keys: []Key{*k}})
}

// typeText types text into the focused box.
func (x *harness) typeText(s string) { x.frame(Input{Typed: []rune(s)}) }

// names are the names a list shows, in order.
func names(p *filesPane) []string {
	var out []string
	for _, r := range p.rows {
		out = append(out, p.entries[r].Name)
	}
	return out
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestFilesNavigateWithKeys(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"b.txt": "b", "a/inner.txt": "i", "file 10.txt": "", "file 9.txt": "", "c/": ""})
	fd := x.a.files
	p := fd.pane()
	if got := names(p); !slices.Equal(got, []string{"a", "c", "b.txt", "file 9.txt", "file 10.txt"}) {
		t.Fatalf("listed %v", got)
	}
	x.press('J', "")
	x.press(keyDown, "")
	if p.cursor != 2 {
		t.Errorf("j and ↓ moved the cursor to %d", p.cursor)
	}
	x.press('K', "")
	x.press('K', "")
	x.press('L', "") // into a
	x.waitFiles()
	if p.dir != filepath.Join(root, "a") || !slices.Equal(names(p), []string{"inner.txt"}) {
		t.Fatalf("l went to %s showing %v", p.dir, names(p))
	}
	x.press('H', "")
	x.waitFiles()
	if p.dir != root || p.current().Name != "a" {
		t.Errorf("h went to %s with the cursor on %v", p.dir, p.current())
	}
	x.press('G', "s")
	if p.cursor != len(p.rows)-1 {
		t.Errorf("G put the cursor on %d", p.cursor)
	}
	x.press(keyHome, "")
	if p.cursor != 0 {
		t.Errorf("Home put the cursor on %d", p.cursor)
	}
	if x.a.doc.title(x.a) != "work" {
		t.Errorf("the tab is called %q", x.a.doc.title(x.a))
	}
}

func TestFilesMouseMovesTheCursorOnlyWhenItMoves(t *testing.T) {
	x, _ := filesHarness(t, map[string]string{"1.txt": "", "2.txt": "", "3.txt": "", "4.txt": "", "5.txt": ""})
	p := x.a.files.pane()
	rowH := x.a.u.fileRowH()
	// The fourth row, below the folder's bar under the tab strip.
	x.mx, x.my = 300, x.a.u.px(40)+x.a.u.px(38)+rowH*3+rowH/2
	x.frame(Input{})
	x.frame(Input{})
	if p.cursor != 3 {
		t.Fatalf("pointing at the fourth row put the cursor on %d", p.cursor)
	}
	x.press('J', "")
	x.frame(Input{})
	x.frame(Input{})
	if p.cursor != 4 {
		t.Errorf("the resting mouse took the cursor back: %d", p.cursor)
	}
	x.my -= rowH
	x.frame(Input{})
	if p.cursor != 2 {
		t.Errorf("moving the mouse up a row put the cursor on %d", p.cursor)
	}
}

func TestFilesMoveBetweenListsAndUndo(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"from/x.txt": "x", "from/y.txt": "y", "to/": ""})
	fd := x.a.files
	left := fd.pane()
	x.press('L', "") // into from
	x.waitFiles()
	x.press('E', "")
	x.waitFiles()
	if len(fd.panes) != 2 || fd.at != 1 {
		t.Fatalf("e made %d lists, at %d", len(fd.panes), fd.at)
	}
	right := fd.pane()
	x.press('H', "")
	x.waitFiles()
	right.cursorTo("to")
	x.press('L', "")
	x.waitFiles()
	x.press(keyBracket, "") // back to the left list
	if fd.at != 0 {
		t.Fatalf("[ went to list %d", fd.at)
	}
	x.press('M', "")
	x.press('M', "")
	if len(left.marked) != 2 {
		t.Fatalf("m marked %v", left.marked)
	}
	x.press('X', "")
	x.press(keyClose, "")
	x.press('V', "")
	x.waitFiles()
	if exists(filepath.Join(root, "from", "x.txt")) || !exists(filepath.Join(root, "to", "y.txt")) {
		t.Fatal("x then v did not move the files")
	}
	if got := names(right); !slices.Equal(got, []string{"x.txt", "y.txt"}) {
		t.Errorf("the right list shows %v", got)
	}
	x.press('U', "")
	x.waitFiles()
	if !exists(filepath.Join(root, "from", "x.txt")) || exists(filepath.Join(root, "to", "x.txt")) {
		t.Error("u did not put the files back")
	}
	x.press('W', "")
	if len(fd.panes) != 1 {
		t.Errorf("w left %d lists", len(fd.panes))
	}
}

func TestFilesCopyPasteAndPath(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"a.txt": "a"})
	x.press('C', "")
	x.press('V', "")
	x.waitFiles()
	if !exists(filepath.Join(root, "a (2).txt")) {
		t.Fatal("pasting a copy beside the original did not name it a (2).txt")
	}
	x.a.files.pane().cursorTo("a.txt")
	x.press('Y', "")
	if x.h.clip != filepath.Join(root, "a.txt") {
		t.Errorf("y copied %q", x.h.clip)
	}
}

func TestFilesDeleteForGoodAsksTwice(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"junk/a/b.bin": strings.Repeat("x", 4096), "keep.txt": "k"})
	fd := x.a.files
	fd.pane().cursorTo("junk")
	x.press('D', "s")
	if fd.confirm == nil || !fd.confirm.forever {
		t.Fatal("D did not ask")
	}
	x.frame(Input{})
	x.shot("files-confirm")
	x.press('J', "") // keys other than the dialog's do nothing while it asks
	if fd.pane().cursor != 0 || fd.confirm == nil {
		t.Fatal("a key went past the dialog")
	}
	// Enter held down: the press answers the first question, the keyboard's repeats do not answer the second.
	x.frame(Input{Keys: []Key{{Code: keyEnter}}, Up: []uint16{}})
	if fd.confirm == nil || !fd.confirm.again || !exists(filepath.Join(root, "junk")) {
		t.Fatal("the first Enter deleted for good without asking again")
	}
	x.frame(Input{Keys: []Key{{Code: keyEnter}}, Up: []uint16{}})
	x.frame(Input{Up: []uint16{keyEnter}})
	if fd.confirm == nil || !exists(filepath.Join(root, "junk")) {
		t.Fatal("a held Enter answered both questions")
	}
	x.shot("files-confirm-again")
	x.press(keyEnter, "")
	x.waitFiles()
	if exists(filepath.Join(root, "junk")) || !exists(filepath.Join(root, "keep.txt")) {
		t.Error("the wrong things were deleted")
	}
	if len(x.a.fm.undo) != 0 {
		t.Error("deleting for good was offered for undo")
	}
}

func TestFilesRecycleAndUndo(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"old/": "", "old/f.txt": "f"})
	fd := x.a.files
	if !fd.panes[0].entries[0].Dir {
		t.Fatal("no folder to recycle")
	}
	x.press('D', "")
	if fd.confirm == nil {
		t.Fatal("d did not ask")
	}
	if !fd.confirm.canBin {
		t.Skip("the test folder is on a drive without a Recycle Bin")
	}
	x.press(keyEnter, "")
	x.waitFiles()
	if exists(filepath.Join(root, "old")) {
		t.Fatal("the folder is still there")
	}
	x.press('U', "")
	x.waitFiles()
	if !exists(filepath.Join(root, "old", "f.txt")) {
		t.Error("u did not bring it back from the Recycle Bin")
	}
}

func TestFilesBusyItemsCannotBeEnteredOrActedOn(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"big/f": "x", "other/": ""})
	a := x.a
	j := &fileJob{kind: jobDelete, title: "Deleting big", paths: []string{filepath.Join(root, "big")}, cancel: func() {}}
	a.fm.jobs = append(a.fm.jobs, j)
	p := a.files.pane()
	p.cursorTo("big")
	x.press('L', "")
	x.frame(Input{})
	if p.dir != root {
		t.Error("a folder being deleted was entered")
	}
	x.press('X', "")
	if len(a.fm.clip.paths) != 0 {
		t.Error("a folder being deleted was cut")
	}
	x.frame(Input{})
	x.shot("files-busy")
	if a.busyJob(filepath.Join(root, "big", "f")) != j || a.busyJob(filepath.Join(root, "other")) != nil {
		t.Error("busy does not follow the folder")
	}
	a.fm.jobs = nil
}

func TestFilesAnalyseSortsBySize(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"small/a": "x", "large/b": strings.Repeat("y", 10000), "mid.bin": strings.Repeat("z", 500)})
	p := x.a.files.pane()
	x.press('I', "")
	x.waitFiles()
	if got := names(p); !slices.Equal(got, []string{"large", "mid.bin", "small"}) {
		t.Errorf("after i the list is %v", got)
	}
	n := x.a.analysis(root)
	if n == nil || n.Size != 10501 {
		t.Fatalf("the analysis is %+v", n)
	}
	x.frame(Input{})
	x.shot("files-analysed")
	// Deleting takes the folder off the analysis.
	p.cursorTo("large")
	x.press('D', "s")
	x.press(keyEnter, "")
	x.press(keyEnter, "")
	x.waitFiles()
	if n.Size != 501 {
		t.Errorf("after deleting large the analysis holds %d", n.Size)
	}
	x.press('N', "s")
	if got := names(p); !slices.Equal(got, []string{"small", "mid.bin"}) {
		t.Errorf("sorted by name: %v", got)
	}
	// i again leaves the analysis; once more shows it at once, as it was kept.
	x.press('S', "s")
	x.press('I', "")
	if p.analysed || x.a.paneAnalysis(p) != nil || p.sort != listByName {
		t.Fatalf("a second i kept the analysis: analysed %v, order %v", p.analysed, p.sort)
	}
	if got := names(p); !slices.Equal(got, []string{"small", "mid.bin"}) {
		t.Errorf("out of the analysis the list is %v", got)
	}
	x.press('I', "")
	if !p.analysed || x.a.fileWork() {
		t.Fatalf("a third i did not show the kept analysis at once: analysed %v, working %v", p.analysed, x.a.fileWork())
	}
	// F5 in an analysed list analyses it again, finding what changed outside the app.
	if err := os.WriteFile(filepath.Join(root, "small", "more"), []byte(strings.Repeat("m", 99)), 0o644); err != nil {
		t.Fatal(err)
	}
	x.press(keyF5, "")
	x.waitFiles()
	if n := x.a.analysis(root); n == nil || n.Size != 600 {
		t.Fatalf("after F5 the analysis is %+v", n)
	}
}

func TestFilesRenameNewAndHidden(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"photo.jpg": "", ".secret": ""})
	fd := x.a.files
	p := fd.pane()
	if !slices.Contains(names(p), ".secret") {
		t.Fatal("hidden files are not shown at first")
	}
	x.press(keyPeriod, "")
	if slices.Contains(names(p), ".secret") {
		t.Error(". did not hide the hidden files")
	}
	x.press(keyPeriod, "")
	p.cursorTo("photo.jpg")
	x.press('R', "")
	x.frame(Input{})
	x.typeText("holiday") // the name without its extension is chosen, so typing replaces it
	x.press(keyEnter, "")
	x.waitFiles()
	if !exists(filepath.Join(root, "holiday.jpg")) || p.current().Name != "holiday.jpg" {
		t.Fatalf("r renamed to %v", names(p))
	}
	x.press('N', "")
	x.frame(Input{})
	x.typeText("notes.md")
	x.press(keyEnter, "")
	x.press('N', "")
	x.frame(Input{})
	x.typeText("drafts")
	x.press(keyEnter, "")
	x.waitFiles()
	if info, err := os.Stat(filepath.Join(root, "notes.md")); err != nil || info.IsDir() {
		t.Error("n with an extension did not make a file")
	}
	if info, err := os.Stat(filepath.Join(root, "drafts")); err != nil || !info.IsDir() {
		t.Error("n without an extension did not make a folder")
	}
	x.press('U', "")
	x.waitFiles()
	if exists(filepath.Join(root, "drafts")) {
		t.Error("u did not take back the new folder")
	}
}

func TestFilesJumpToAFolderVisited(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"projects/btelo/": "", "music/": ""})
	p := x.a.files.pane()
	x.a.goTo(p, filepath.Join(root, "projects", "btelo"), "")
	x.waitFiles()
	x.a.goTo(p, filepath.Join(root, "music"), "")
	x.waitFiles()
	x.press('Z', "")
	x.frame(Input{})
	x.typeText("bte")
	x.frame(Input{})
	x.shot("files-jump")
	x.press(keyEnter, "")
	x.waitFiles()
	if p.dir != filepath.Join(root, "projects", "btelo") {
		t.Errorf("z bte went to %s", p.dir)
	}
	x.a.saveFileHistory(true)
	if !exists(x.a.historyPath()) {
		t.Error("the folders visited were not saved")
	}
}

func TestFilesFindByNameAndText(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"src/main.go": "package main\nfunc needle() {}\n", "docs/readme.md": "nothing", "src/util/needle.go": "x"})
	fd := x.a.files
	p := fd.pane()
	x.press('F', "")
	x.frame(Input{})
	x.typeText("needle")
	x.pump(5*time.Second, func() bool { return p.search != nil && len(p.search.hits) == 1 })
	x.frame(Input{})
	x.shot("files-find")
	x.press(keyEnter, "") // from typing to the results
	x.press(keyEnter, "") // to the file found
	x.waitFiles()
	if p.search != nil || p.dir != filepath.Join(root, "src", "util") || p.current().Name != "needle.go" {
		t.Fatalf("f found and went to %s %v", p.dir, p.current())
	}
	x.a.goTo(p, root, "")
	x.waitFiles()
	x.press('G', "")
	x.frame(Input{})
	x.typeText("needle")
	x.pump(5*time.Second, func() bool {
		return p.search != nil && !p.search.running && p.search.ran == "needle" && len(p.search.lines) == 1
	})
	x.frame(Input{})
	x.shot("files-grep")
	if l := p.search.lines[0]; filepath.ToSlash(l.Rel) != "src/main.go" || l.No != 2 {
		t.Errorf("g found %+v", l)
	}
	x.press(keyEscape, "")
	if p.search != nil {
		t.Error("Esc did not leave the search")
	}
}

func TestFilesCompressAndExtract(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"pics/a.png": "a", "pics/b.png": "b"})
	p := x.a.files.pane()
	p.cursorTo("pics")
	x.press('Z', "s")
	x.waitFiles()
	if !exists(filepath.Join(root, "pics.zip")) || p.current().Name != "pics.zip" {
		t.Fatalf("Z made %v", names(p))
	}
	x.press('U', "s")
	x.waitFiles()
	if !exists(filepath.Join(root, "pics (2)", "b.png")) {
		t.Errorf("U extracted to %v", names(p))
	}
}

func TestFilesScreens(t *testing.T) {
	if os.Getenv("FORGE_SHOTS") == "" {
		t.Skip("set FORGE_SHOTS to a folder to render the screens")
	}
	x, root := filesHarness(t, map[string]string{
		"Documents/report.docx": strings.Repeat("r", 30000), "Documents/notes.md": "# Notes\n\nSome text.",
		"Videos/": "", "node_modules/pkg/index.js": strings.Repeat("j", 200000), ".git/HEAD": "ref: refs/heads/main\n",
		"build.zip": strings.Repeat("z", 90000), "main.go": "package main\n", "README.md": "# Read me\n", ".env": "X=1",
	})
	clipFile(t, filepath.Join(root, "Videos"), "holiday.mp4", "testsrc2=size=320x180", "2")
	for _, lang := range []Language{English, Chinese} {
		x.a.u.lang = lang
		x.a.refreshPanes(nil)
		x.waitFiles()
		x.settle(10 * time.Second)
		x.shot(string(lang) + "-files")
		x.press('I', "")
		x.waitFiles()
		x.press('E', "")
		x.waitFiles()
		x.a.goTo(x.a.files.pane(), filepath.Join(root, "Videos"), "")
		x.waitFiles()
		x.settle(10 * time.Second)
		x.press(keySpace, "")
		x.pump(20*time.Second, func() bool { return x.a.preview.path != "" && !x.a.preview.loading })
		// The video's first picture is decoded after the preview has loaded, without a flag to wait on.
		for until := time.Now().Add(2 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
			x.frame(Input{})
		}
		x.shot(string(lang) + "-files-split-preview")
		x.press(keySpace, "")
		x.press('W', "")
		x.frame(Input{})
	}
}

func TestFilesCloseTabWithItsButton(t *testing.T) {
	x, _ := filesHarness(t, map[string]string{"a.txt": ""})
	u := &x.a.u
	tab := rectAt(u.px(8), u.px(6), u.px(220)-u.px(2), u.px(40)-u.px(6))
	x.mx, x.my = tab.X+tab.W/2, tab.Y+tab.H/2
	x.frame(Input{})
	x.click(rectAt(tab.Right()-u.px(26), tab.Y+(tab.H-u.px(20))/2, u.px(20), u.px(20)))
	x.frame(Input{})
	if x.a.files != nil || len(x.a.docs) != 1 {
		t.Errorf("the tab is still open: %d tabs", len(x.a.docs))
	}
}

func TestFilesUpFromADriveListsTheDrives(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows has drive letters")
	}
	x, root := filesHarness(t, map[string]string{"a.txt": ""})
	p := x.a.files.pane()
	vol := filepath.VolumeName(root)
	x.a.goTo(p, vol+`\`, "")
	x.waitFiles()
	x.press('H', "")
	x.waitFiles()
	if !p.atDrives() || p.current() == nil || p.current().Name != vol {
		t.Fatalf("h above %s shows %q with the cursor on %v", vol, p.dir, p.current())
	}
	if d := p.drives[vol]; d.Total <= 0 {
		t.Errorf("%s has no size: %+v", vol, d)
	}
	x.frame(Input{})
	x.shot("files-drives")
	x.press('D', "")
	x.press('N', "")
	x.press('X', "")
	if x.a.files.confirm != nil || x.a.files.prompt != nil || len(x.a.fm.clip.paths) != 0 {
		t.Error("a drive was offered for deleting, making a file in, or moving")
	}
	if x.a.doc.title(x.a) != "This PC" {
		t.Errorf("the tab is called %q", x.a.doc.title(x.a))
	}
	x.press('L', "")
	x.waitFiles()
	if p.dir != vol+`\` {
		t.Errorf("l on %s went to %q", vol, p.dir)
	}
}

func TestFilesRemembersTheLists(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"deep/er/": "", "other/": ""})
	a := x.a
	deep, other := filepath.Join(root, "deep", "er"), filepath.Join(root, "other")
	a.goTo(a.files.pane(), deep, "")
	x.waitFiles()
	x.press('E', "") // a second list, at the same folder
	x.waitFiles()
	a.goTo(a.files.pane(), other, "")
	x.waitFiles()
	x.mx, x.my = float32(x.c.Img.Rect.Dx())-20, 300 // over the second list, which pointing at keeps in use
	x.press('Q', "")                                // the second list shows pictures
	x.frame(Input{})
	x.press('W', "c") // close the tab, as the next run would find it
	b, err := os.ReadFile(a.settingsPath())
	var saved settings
	if err == nil {
		err = json.Unmarshal(b, &saved)
	}
	want := []savedPane{{Dir: deep}, {Dir: other, Grid: true}}
	if err != nil || !slices.Equal(saved.Files.Panes, want) {
		t.Fatalf("the settings hold %+v, want %+v: %v", saved.Files.Panes, want, err)
	}
	// The next resource manager opens them again; a folder gone since opens the default folder.
	a.settings.Files.Panes = append(want, savedPane{Dir: filepath.Join(root, "gone")})
	fd := a.openResourceManager()
	x.waitFiles()
	if len(fd.panes) != 3 || fd.panes[0].dir != deep || fd.panes[0].grid || fd.panes[1].dir != other || !fd.panes[1].grid ||
		fd.panes[2].dir != defaultFolder() {
		t.Fatalf("opened %d lists: %+v %+v", len(fd.panes), fd.panes[0], fd.panes[1])
	}
}

func TestFilesListButtons(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"a/": "", "b/": ""})
	fd := x.a.files
	u := &x.a.u
	// A list's bar is under the tab strip; its buttons are at its right end, × last and + before it.
	button := func(pane, fromRight int) gfx.Rect {
		w := float32(x.c.Img.Rect.Dx()) / float32(len(fd.panes))
		size := u.px(26)
		right := w*float32(pane+1) - u.px(6) - float32(fromRight)*(size+u.px(2))
		return rectAt(right-size, u.px(40)+(u.px(38)-size)/2, size, size)
	}
	x.click(button(0, 0)) // the only list has no ×: its + is last
	x.waitFiles()
	if len(fd.panes) != 2 || fd.at != 1 || fd.panes[1].dir != root {
		t.Fatalf("+ made %d lists, at %d", len(fd.panes), fd.at)
	}
	x.click(button(0, 1)) // + of the first list, beside its ×
	x.waitFiles()
	if len(fd.panes) != 3 || fd.at != 1 {
		t.Fatalf("+ of the first list made %d lists, at %d", len(fd.panes), fd.at)
	}
	x.frame(Input{})
	x.shot("files-list-buttons")
	x.click(button(2, 0)) // × of the last list
	if len(fd.panes) != 2 {
		t.Fatalf("× left %d lists", len(fd.panes))
	}
	x.press('W', "")
	if len(fd.panes) != 1 {
		t.Errorf("w left %d lists", len(fd.panes))
	}
	x.press('W', "")
	if len(fd.panes) != 1 || x.a.files == nil {
		t.Error("w closed the last list")
	}
}

func TestFilesAnalyseOnlyTheListInUse(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"small/a": "x", "large/b": strings.Repeat("y", 10000)})
	fd := x.a.files
	x.press('E', "")
	x.waitFiles()
	left, right := fd.panes[0], fd.panes[1]
	x.press('I', "") // in the right list, the one in use
	x.waitFiles()
	if !right.analysed || !slices.Equal(names(right), []string{"large", "small"}) {
		t.Errorf("the list analysed shows %v", names(right))
	}
	if left.analysed || x.a.paneAnalysis(left) != nil || left.sort != listByName {
		t.Errorf("the other list changed: analysed %v, sorted %v", left.analysed, left.sort)
	}
	x.frame(Input{})
	x.shot("files-analyse-one-list")
	// Going into a folder keeps the analysis, as gdu does; leaving it, the list goes back to names.
	right.cursorTo("large")
	x.press('L', "")
	x.waitFiles()
	if !right.analysed {
		t.Error("going into an analysed folder left the analysis")
	}
	x.a.goTo(right, filepath.Dir(root), "")
	x.waitFiles()
	if right.analysed || right.sort != listByName {
		t.Error("leaving the analysed folder kept showing an analysis")
	}
	// The other list, asked now, shows what was analysed at once.
	x.press(keyBracket, "")
	x.press('I', "")
	if !left.analysed || len(x.a.fm.scans) != 0 {
		t.Errorf("i on a folder already analysed did not show it at once: analysed %v, %d running", left.analysed, len(x.a.fm.scans))
	}
}

func TestFilesTypeOrPasteAPath(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"one/two/file.txt": "f", "other/": ""})
	fd := x.a.files
	p := fd.pane()
	// Ctrl+L turns the bar into a box with the folder chosen, so a path pasted replaces it.
	x.press('L', "c")
	x.frame(Input{})
	if fd.prompt == nil || fd.prompt.kind != promptPath || fd.prompt.text != root {
		t.Fatalf("Ctrl+L opened %+v", fd.prompt)
	}
	x.frame(Input{})
	x.shot("files-path-edit")
	x.typeText(`"` + filepath.Join(root, "one", "two", "file.txt") + `"`) // as Explorer's Copy as path gives it
	x.press(keyEnter, "")
	x.waitFiles()
	if p.dir != filepath.Join(root, "one", "two") || p.current() == nil || p.current().Name != "file.txt" {
		t.Fatalf("a file's path went to %s with the cursor on %v", p.dir, p.current())
	}
	// A relative path goes from the list's folder; one that is not there stays put and says so.
	x.press('L', "c")
	x.frame(Input{})
	x.typeText(filepath.Join("..", "..", "other"))
	x.press(keyEnter, "")
	x.waitFiles()
	if p.dir != filepath.Join(root, "other") {
		t.Fatalf("a relative path went to %s", p.dir)
	}
	x.press('L', "c")
	x.frame(Input{})
	x.typeText(filepath.Join(root, "nowhere"))
	x.press(keyEnter, "")
	x.waitFiles()
	if p.dir != filepath.Join(root, "other") || len(x.a.toasts) == 0 {
		t.Errorf("a path that is not there went to %s, toasts %v", p.dir, x.a.toasts)
	}
	// Esc leaves the box as it was; a click on the bar's empty part opens it.
	x.press('L', "c")
	x.press(keyEscape, "")
	if fd.prompt != nil {
		t.Error("Esc left the box open")
	}
	x.click(rectAt(float32(x.c.Img.Rect.Dx())/2, x.a.u.px(40)+x.a.u.px(10), x.a.u.px(20), x.a.u.px(18)))
	if fd.prompt == nil || fd.prompt.kind != promptPath {
		t.Error("a click on the bar did not open the box")
	}
}

func TestExpandPath(t *testing.T) {
	t.Setenv("FORGE_TEST_DIR", filepath.Join("C:", "x"))
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		`  "C:\a b\c"  `:           filepath.Clean(`C:\a b\c`),
		`%FORGE_TEST_DIR%\y`:       filepath.Join("C:", "x", "y"),
		`$FORGE_TEST_DIR/y`:        filepath.Join("C:", "x", "y"),
		`%NOT_SET_ANYWHERE%\y`:     filepath.Clean(`%NOT_SET_ANYWHERE%\y`),
		"~":                        home,
		filepath.Join("~", "docs"): filepath.Join(home, "docs"),
		"   ":                      "",
	} {
		if got := expandPath(in); got != want {
			t.Errorf("expandPath(%q) = %q, want %q", in, got, want)
		}
	}
}
