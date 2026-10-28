package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// fakeAssoc is the fake host's file associations: the ProgIDs declared, and those the user made the default.
type fakeAssoc struct {
	declared map[string]bool
	defaults map[string]bool
	settings int // times Windows Settings was opened
}

var fakeAssocs = map[*fakeHost]*fakeAssoc{}

func (h *fakeHost) assoc() *fakeAssoc {
	if fakeAssocs[h] == nil {
		fakeAssocs[h] = &fakeAssoc{declared: map[string]bool{}, defaults: map[string]bool{}}
	}
	return fakeAssocs[h]
}

func (h *fakeHost) RegisterFileTypes(types []FileType) error {
	for _, t := range types {
		h.assoc().declared[t.ProgID] = true
		if t.Own {
			h.assoc().defaults[t.ProgID] = true
		}
	}
	return nil
}

func (h *fakeHost) UnregisterFileTypes(types []FileType) error {
	for _, t := range types {
		delete(h.assoc().declared, t.ProgID)
		delete(h.assoc().defaults, t.ProgID)
	}
	return nil
}

func (h *fakeHost) FileTypeRegistered(t FileType) bool { return h.assoc().declared[t.ProgID] }

func (h *fakeHost) FileTypeDefaults(t FileType) int {
	if h.assoc().defaults[t.ProgID] {
		return len(t.Exts)
	}
	return 0
}

func (h *fakeHost) OpenDefaultApps() error {
	h.assoc().settings++
	return nil
}

func TestFileTypesAreChosenInSettings(t *testing.T) {
	x := newHarness(t)
	a := x.a
	groups := fileTypeGroups()
	a.setFileTypes(groups[0], true) // the app's own formats
	a.setFileTypes(groups[1], true) // videos
	if !slices.Equal(a.settings.FileTypes, []string{"own", "video"}) || !x.h.assoc().declared["BteloForge.Video"] {
		t.Fatalf("%v %v", a.settings.FileTypes, x.h.assoc().declared)
	}
	a.settingsOpen, a.settingsPage = true, settingsFileTypesPage
	x.frame(Input{})
	x.shot("settings-file-types")
	// A declaration that went missing, as when the app moved, is made again at the next start.
	delete(x.h.assoc().declared, "BteloForge.Video")
	a.refreshFileTypes()
	if !x.h.assoc().declared["BteloForge.Video"] {
		t.Fatal("the video type was not declared again")
	}
	a.setFileTypes(groups[1], false)
	if slices.Contains(a.settings.FileTypes, "video") || x.h.assoc().declared["BteloForge.Video"] {
		t.Fatal("turning videos off left them on")
	}
}

func TestFilesOpenedFromTheSystemGoWhereTheyBelong(t *testing.T) {
	x := newHarness(t)
	a := x.a
	dir := assetFolder(t, x)
	// A video opens in a tab of its own, even while another edit is open.
	x.open(x.fixture())
	tabs := len(a.docs)
	x.frame(Input{Opened: []string{filepath.Join(dir, "holiday.mp4")}})
	x.pump(30*time.Second, func() bool { return a.p != nil && a.opening == "" })
	if len(a.docs) != tabs+1 || filepath.Base(a.p.Sources[0].Path) != "holiday.mp4" {
		t.Fatalf("%d tabs, showing %v", len(a.docs), a.p.Sources[0].Path)
	}
	// A text opens in the file manager as pictures, at its folder, with its preview open.
	x.frame(Input{Opened: []string{filepath.Join(dir, "README.md")}})
	x.pump(30*time.Second, func() bool { return a.files != nil && !a.files.pane().reading })
	if a.files == nil || !a.files.pane().grid || !a.files.preview || a.previewPath(a.files) != filepath.Join(dir, "README.md") {
		t.Fatalf("the text is not shown: %+v", a.files)
	}
	// A file that is gone says so.
	x.frame(Input{Opened: []string{filepath.Join(dir, "gone.mp4")}})
	if len(a.toasts) == 0 {
		t.Fatal("a missing file opened silently")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
