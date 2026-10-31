package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestStartPageGroupsTheTools checks which group each card is in, and that the groups are in the order of the folders.
func TestStartPageGroupsTheTools(t *testing.T) {
	x := newHarness(t)
	got := map[int][]string{}
	for _, c := range x.a.startCards() {
		got[c.group] = append(got[c.group], c.id)
	}
	want := map[int][]string{
		categoryTools:        {"empty/resources", "empty/record", "empty/live", "empty/keyboard"},
		categoryCreative:     {"empty/video", "empty/canvas", "empty/mind", "empty/model"},
		categoryIntelligence: {"empty/ai", "empty/mail"},
	}
	for g, ids := range want {
		if !slices.Equal(got[g], ids) {
			t.Errorf("%s holds %v, want %v", startGroups[g], got[g], ids)
		}
	}
	if len(got[categoryDocuments]) != 4 {
		t.Errorf("%d document cards: %v", len(got[categoryDocuments]), got[categoryDocuments])
	}
	if len(categoryFolders) != len(startGroups) {
		t.Errorf("%d folders for %d groups", len(categoryFolders), len(startGroups))
	}
}

// TestWorkIsSavedInItsCategory checks where each tool's new files go by default, and that they follow the folder chosen.
func TestWorkIsSavedInItsCategory(t *testing.T) {
	x := newHarness(t)
	a := x.a
	home, _ := os.UserHomeDir()
	root, err := a.resourceDir()
	if err != nil || root != filepath.Join(home, ProductName) {
		t.Fatalf("the default resource folder is %s (%v)", root, err)
	}
	check := func(root string) {
		t.Helper()
		if path, err := a.recordingPath(time.Now()); err != nil || filepath.Dir(path) != filepath.Join(root, "Tools") {
			t.Errorf("a recording goes to %s (%v)", path, err)
		}
		if path, err := a.editFilePath("clip.mp4"); err != nil || filepath.Dir(path) != filepath.Join(root, "Creative") {
			t.Errorf("an edit goes to %s (%v)", path, err)
		}
		if got := a.exportDir(); got != filepath.Join(root, "Creative") {
			t.Errorf("exports go to %s", got)
		}
		if got, err := a.aiAssetRoot(); err != nil || got != filepath.Join(root, "Intelligence") {
			t.Errorf("models' work goes to %s (%v)", got, err)
		}
		od, err := a.newOffice(officeFormats[0])
		if err != nil || filepath.Dir(od.path) != filepath.Join(root, "Documents") {
			t.Fatalf("a document went to %+v (%v)", od, err)
		}
		a.closeTab(len(a.docs) - 1)
		if err := a.newCanvas(); err != nil || filepath.Dir(a.art.path) != filepath.Join(root, "Creative") {
			t.Fatalf("a canvas goes to %s (%v)", a.art.path, err)
		}
		a.closeTab(len(a.docs) - 1)
		if err := a.newMindMap(); err != nil || filepath.Dir(a.board.path) != filepath.Join(root, "Creative") {
			t.Fatalf("a map went to %s (%v)", a.board.path, err)
		}
		a.closeTab(len(a.docs) - 1)
	}
	check(root)

	chosen := filepath.Join(x.dir, "my work")
	a.setResourceDir(chosen)
	check(chosen)
	for _, name := range categoryFolders {
		if !isFolder(filepath.Join(chosen, name)) {
			t.Errorf("no %s folder in the folder chosen", name)
		}
	}

	// An export folder of its own, and beside the original, are still the user's to choose.
	a.settings.ExportDir = filepath.Join(x.dir, "out")
	if a.exportDir() != a.settings.ExportDir {
		t.Errorf("exports go to %s, not the folder chosen", a.exportDir())
	}
	a.settings.ExportBeside = true
	if a.exportDir() != "" {
		t.Errorf("exports go to %s, not beside the original", a.exportDir())
	}
}

// TestResourceManagerGoesToTheResourceFolder checks that a manager opens there even with lists kept from before there
// was one, returns to where it was after that, and opens there again once the folder moves.
func TestResourceManagerGoesToTheResourceFolder(t *testing.T) {
	x := newHarness(t)
	a := x.a
	x.frame(Input{})
	elsewhere := x.mediaDir()
	a.settings.Files.Panes = []savedPane{{Dir: elsewhere}}

	fd := a.openResourceManager()
	x.waitFiles()
	root, _ := a.resourceDir()
	if len(fd.panes) != 2 || fd.panes[0].dir != root {
		t.Fatalf("opened %d lists at %s, want the resource folder %s", len(fd.panes), fd.panes[0].dir, root)
	}
	for _, name := range categoryFolders {
		if !isFolder(filepath.Join(root, name)) {
			t.Errorf("no %s folder", name)
		}
	}

	a.closeTab(len(a.docs) - 1)
	a.settings.Files.Panes = []savedPane{{Dir: elsewhere}}
	fd = a.openResourceManager()
	x.waitFiles()
	if len(fd.panes) != 1 || fd.panes[0].dir != elsewhere {
		t.Fatalf("opened %d lists at %s, want the one kept at %s", len(fd.panes), fd.panes[0].dir, elsewhere)
	}

	a.closeTab(len(a.docs) - 1)
	moved := filepath.Join(x.dir, "moved")
	a.setResourceDir(moved)
	fd = a.openResourceManager()
	x.waitFiles()
	if fd.panes[0].dir != moved {
		t.Fatalf("opened at %s, want the new folder %s", fd.panes[0].dir, moved)
	}
}
