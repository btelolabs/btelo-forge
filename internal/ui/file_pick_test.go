package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// fromSystem answers the file picker open with what the system's dialog gives, as its button does.
func (x *harness) fromSystem() {
	x.t.Helper()
	x.frame(Input{})
	if x.a.filePicker == nil {
		x.t.Fatal("no file picker is open")
	}
	x.a.pickSystem(x.a.filePicker)
	x.frame(Input{})
}

// pickerAt makes a folder of files for the picker to show, and the request that opens it there.
func pickerAt(t *testing.T, names ...string) (string, pickRequest) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, pickRequest{key: "test", title: "Choose", kinds: "Models", exts: []string{".glb"}, dir: dir}
}

// listed waits for the picker's list to be read and returns the names it shows.
func (x *harness) listed() []string {
	x.t.Helper()
	x.frame(Input{})
	p := x.a.filePicker.fd.pane()
	x.pump(10*time.Second, func() bool { return !p.reading })
	x.frame(Input{})
	return names(p)
}

// pickCard is where the picker's list shows the item called name.
func (x *harness) pickCard(name string) gfx.Rect {
	x.t.Helper()
	x.frame(Input{})
	p := x.a.filePicker.fd.pane()
	row := p.rowOf(name)
	if row < 0 {
		x.t.Fatalf("%s is not shown: %v", name, names(p))
	}
	u := &x.a.u
	_, _, body, _ := x.a.pickLayout()
	pad := u.gridPad()
	c := p.g.layout.cards[row]
	return gfx.Rect{X: body.X + pad + c.X, Y: body.Y + u.px(38) + p.g.barH + pad + c.Y - p.g.scroll, W: c.W, H: c.H - u.assetLabelH()}
}

// The picker shows the folder's folders and the files asked for, or all of them; a click chooses a file and pointing
// at another does not change that; a double click answers.
func TestFilePickerOpensAFile(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir, r := pickerAt(t, "a.glb", "b.glb", "c.txt")
	var got []string
	a.pickFiles(r, func(paths []string) { got = paths })
	if shown := x.listed(); !slices.Equal(sorted(shown), []string{"a.glb", "b.glb", "inner"}) {
		t.Fatalf("the list shows %v", shown)
	}
	x.shot(string(a.u.lang) + "-file-picker-open")
	pk := a.filePicker
	x.click(x.pickCard("a.glb"))
	if c := pk.pickFilesChosen(); len(c) != 1 || filepath.Base(c[0]) != "a.glb" {
		t.Fatalf("chosen: %v", c)
	}
	other := x.pickCard("b.glb")
	x.mx, x.my = other.X+other.W/2, other.Y+other.H/2
	x.frame(Input{})
	x.frame(Input{})
	if c := pk.pickFilesChosen(); len(c) != 1 || filepath.Base(c[0]) != "a.glb" {
		t.Fatalf("pointing changed what is chosen: %v", c)
	}
	a.pickShowAll(pk, true)
	if shown := x.listed(); !slices.Contains(shown, "c.txt") {
		t.Fatalf("all files shows %v", shown)
	}
	a.pickShowAll(pk, false)
	x.listed()
	card := x.pickCard("b.glb")
	x.mx, x.my = card.X+card.W/2, card.Y+card.H/2
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true, Double: true}}})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if a.filePicker != nil || len(got) != 1 || got[0] != filepath.Join(dir, "b.glb") {
		t.Fatalf("the double click answered %v", got)
	}
	if a.settings.Files.PickDirs["test"] != dir {
		t.Errorf("the folder kept is %q", a.settings.Files.PickDirs["test"])
	}
}

// Enter goes into a folder, keys stay with the dialog, Esc cancels.
func TestFilePickerKeepsTheKeysAndCancels(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir, r := pickerAt(t, "a.glb")
	tabs := len(a.docs)
	called := false
	a.pickFiles(r, func([]string) { called = true })
	x.listed()
	a.filePicker.fd.pane().cursorTo("inner")
	x.key(keyEnter, false)
	x.listed()
	if p := a.filePicker.fd.pane(); p.dir != filepath.Join(dir, "inner") {
		t.Fatalf("Enter went to %q", p.dir)
	}
	x.key('T', true) // a new tab, behind the dialog
	if len(a.docs) != tabs {
		t.Error("a shortcut reached the window behind the dialog")
	}
	x.key(keyEscape, false)
	x.frame(Input{})
	if a.filePicker != nil || called {
		t.Fatalf("Esc: open %v, answered %v", a.filePicker != nil, called)
	}
}

// What the file manager finds by name in a folder is an answer too, and the jump goes to a folder visited.
func TestFilePickerFindsAndJumps(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir, r := pickerAt(t, "a.glb")
	deep := filepath.Join(dir, "inner", "deep.glb")
	if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	a.pickFiles(r, func(paths []string) { got = paths })
	x.listed()
	x.press('Z', "")
	if pr := a.filePicker.fd.prompt; pr == nil || pr.kind != promptJump {
		t.Fatal("z did not open the jump")
	}
	x.key(keyEscape, false)
	x.frame(Input{})
	x.press('F', "")
	p := a.filePicker.fd.pane()
	if p.search == nil {
		t.Fatal("f did not open the search")
	}
	x.frame(Input{})
	x.typeText("deep")
	x.pump(10*time.Second, func() bool { return len(p.search.hits) == 1 })
	x.key(keyEnter, false) // from the box to the results
	x.frame(Input{})
	x.key(keyEnter, false)
	x.frame(Input{})
	if len(got) != 1 || got[0] != deep {
		t.Fatalf("the search answered %v", got)
	}
}

// Marked files are the answer of a dialog for several.
func TestFilePickerTakesSeveral(t *testing.T) {
	x := newHarness(t)
	a := x.a
	_, r := pickerAt(t, "a.glb", "b.glb")
	r.multiple = true
	var got []string
	a.pickFiles(r, func(paths []string) { got = paths })
	x.listed()
	p := a.filePicker.fd.pane()
	p.marked["a.glb"], p.marked["b.glb"], p.marked["inner"] = true, true, true
	x.frame(Input{})
	a.pickConfirm(a.filePicker)
	x.frame(Input{})
	if len(got) != 2 {
		t.Fatalf("answered %v", got)
	}
}

// Saving adds the extension, asks before replacing a file, and refuses a name no file can have.
func TestFilePickerSaves(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir, r := pickerAt(t, "taken.glb")
	r.name = "new.glb"
	var got string
	a.pickSaveAs(r, func(path string) { got = path })
	x.listed()
	x.shot(string(a.u.lang) + "-file-picker-save")
	x.typeText("mine") // the name without its extension is chosen, so this replaces it
	x.key(keyEnter, false)
	x.frame(Input{})
	if got != filepath.Join(dir, "mine.glb") {
		t.Fatalf("saved to %q", got)
	}

	got = ""
	r.name = "taken"
	a.pickSaveAs(r, func(path string) { got = path })
	x.listed()
	x.key(keyEnter, false)
	x.frame(Input{})
	pk := a.filePicker
	if pk == nil || pk.replace == "" || got != "" {
		t.Fatalf("no question before replacing: %q", got)
	}
	x.shot(string(a.u.lang) + "-file-picker-replace")
	x.key(keyEscape, false)
	if pk.replace != "" || a.filePicker == nil {
		t.Fatal("Esc did not go back to the dialog")
	}
	x.frame(Input{})
	pk.name = "bad?name"
	a.pickConfirm(pk)
	if pk.replace != "" || pk.ended {
		t.Fatal("a name that cannot be was taken")
	}
}

// A dialog for a folder shows folders, and answers with the one it shows.
func TestFilePickerChoosesAFolder(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir, r := pickerAt(t, "a.glb")
	var got string
	a.pickDir(r, func(d string) { got = d })
	if shown := x.listed(); !slices.Equal(shown, []string{"inner"}) {
		t.Fatalf("the list shows %v", shown)
	}
	a.pickConfirm(a.filePicker)
	x.frame(Input{})
	if got != dir {
		t.Fatalf("answered %q", got)
	}
}

// Nothing the tools open or save with reaches the system's dialogs except through the picker.
func TestOnlyThePickerOpensSystemDialogs(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range sources {
		if f == "file_pick.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if s := string(data); strings.Contains(s, "host.OpenFiles(") || strings.Contains(s, "host.ChooseFolder(") {
			t.Errorf("%s calls the system's dialog itself", f)
		}
	}
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}
