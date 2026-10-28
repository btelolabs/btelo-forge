package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/project"
)

func TestEditFilesSaveAndOpenAgain(t *testing.T) {
	x := newHarness(t)
	x.a.settings.ExportDir = filepath.Join(x.dir, "out")
	x.open(x.fixture())
	x.a.split()
	x.key('S', true)
	path := x.a.doc.file
	if filepath.Dir(path) != x.a.settings.ExportDir || filepath.Ext(path) != project.EditExt {
		t.Fatalf("saved to %q", path)
	}
	// A second save goes to the same file, not a numbered copy.
	x.a.split()
	if again, err := x.a.saveEditFile(); err != nil || again != path {
		t.Fatalf("%q %v", again, err)
	}
	if _, err := x.a.saveEditFile(); err != nil {
		t.Fatal(err)
	}
	saved := x.a.p.Clone()

	x.a.closeTab(x.a.current())
	x.frame(Input{Dropped: []string{path}})
	x.pump(30*time.Second, func() bool { return x.a.p != nil && x.a.opening == "" })
	if !project.Equal(x.a.p, saved) || x.a.doc.file != path {
		t.Fatalf("the edit did not come back as saved: file %q", x.a.doc.file)
	}
	// Opening it again brings its tab forward rather than a second copy.
	tabs := len(x.a.docs)
	x.a.newTab()
	x.a.openEditFile(path)
	if x.a.doc.file != path || len(x.a.docs) != tabs+1 {
		t.Fatalf("%d tabs", len(x.a.docs))
	}

	// A file that is not an edit, or whose video is gone, is refused with a reason.
	junk := filepath.Join(x.dir, "junk"+project.EditExt)
	os.WriteFile(junk, []byte("{}"), 0o644)
	x.a.openEditFile(junk)
	x.pump(10*time.Second, func() bool { return x.a.opening == "" })
	if len(x.a.toasts) == 0 || x.a.toasts[len(x.a.toasts)-1].text != "Cannot open junk.btcut: This is not an edit file, or it is damaged" {
		t.Fatalf("%+v", x.a.toasts)
	}
}
