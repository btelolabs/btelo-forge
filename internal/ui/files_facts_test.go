package ui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A folder slow to read, as a network share or a disc spinning up is, never holds a frame: its preview says it is
// loading while the read waits, and shows what it holds once the read answers. Reading it again after a refresh
// finds what changed.
func TestAFolderSlowToReadNeverHoldsAFrame(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"inner/one.txt": "1"})
	a := x.a
	release := make(chan struct{})
	var reads atomic.Int32
	f := a.facts()
	read := f.lists.read
	f.lists.read = func(ctx context.Context, dir string) folderListing {
		reads.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return read(ctx, dir)
	}
	p := a.files.pane()
	p.cursorTo("inner")
	a.togglePreview(a.files)
	start := time.Now()
	for range 5 {
		x.frame(Input{})
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("five frames took %v while the folder was being read", took)
	}
	inner := filepath.Join(root, "inner")
	if _, known := a.listingOf(inner); known || reads.Load() == 0 {
		t.Fatalf("the preview did not ask for the folder in the background: known %v, %d reads", known, reads.Load())
	}
	if !a.factsBusy() || a.Idle() {
		t.Error("the app does not say a read is under way")
	}
	close(release)
	x.pump(10*time.Second, func() bool { _, known := a.listingOf(inner); return known })
	l, _ := a.listingOf(inner)
	if l.err != nil || len(l.entries) != 1 || l.entries[0].Name != "one.txt" {
		t.Fatalf("the preview read %+v", l)
	}
	if err := os.WriteFile(filepath.Join(inner, "two.txt"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.refreshFiles(a.files)
	x.pump(10*time.Second, func() bool { l, _ := a.listingOf(inner); return len(l.entries) == 2 })
}

// What a search found is read in the background: the preview knows at once that it is a file, as the names read
// said, and shows its size once it has been read.
func TestWhatASearchFoundIsReadInTheBackground(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"deep/down/notes.txt": "twelve bytes"})
	a := x.a
	path := filepath.Join(root, "deep", "down", "notes.txt")
	release := make(chan struct{})
	f := a.facts()
	read := f.stats.read
	f.stats.read = func(ctx context.Context, p string) pathFact {
		if p == path {
			<-release
		}
		return read(ctx, p)
	}
	x.press('F', "")
	x.frame(Input{})
	x.typeText("notes")
	x.pump(10*time.Second, func() bool { s := a.files.pane().search; return s != nil && len(s.hits) == 1 })
	a.togglePreview(a.files)
	x.frame(Input{})
	got, e := a.previewEntry(a.files)
	if got != path || e.Dir {
		t.Fatalf("the preview shows %q, a folder %v", got, e.Dir)
	}
	if e.Size != 0 {
		t.Errorf("the size is known before it was read: %d", e.Size)
	}
	close(release)
	x.pump(10*time.Second, func() bool { _, e := a.previewEntry(a.files); return e.Size == 12 })
}

// Coming back into a folder that was analysed shows its analysis at once, kept from before, without analysing it
// again; leaving the analysis with i shows names again wherever the list goes.
func TestComingBackIntoAnAnalysisShowsItAtOnce(t *testing.T) {
	x, root := filesHarness(t, map[string]string{"small/a": "x", "large/b/c": strings.Repeat("y", 10000)})
	a := x.a
	p := a.files.pane()
	x.press('I', "")
	x.waitFiles()
	tree := a.analysis(root)
	if !p.analysed || tree == nil {
		t.Fatal("the folder was not analysed")
	}
	visits := []string{filepath.Join(root, "large"), filepath.Join(root, "large", "b"), root, filepath.Dir(root), root, filepath.Join(root, "large")}
	for _, dir := range visits {
		a.goTo(p, dir, "")
		x.waitFiles()
		want := dir != filepath.Dir(root)
		if p.analysed != want || len(a.fm.scans) != 0 || a.analysis(root) != tree {
			t.Fatalf("in %s: analysed %v, want %v; %d analyses running", dir, p.analysed, want, len(a.fm.scans))
		}
	}
	a.goTo(p, root, "")
	x.waitFiles()
	if got := names(p); !slices.Equal(got, []string{"large", "small"}) || p.sort != listBySize {
		t.Errorf("back in the analysed folder the list is %v, sorted %v", got, p.sort)
	}
	x.press('I', "") // leaves the analysis
	a.goTo(p, filepath.Join(root, "large"), "")
	x.waitFiles()
	if p.analysed {
		t.Error("a list that left the analysis shows it again in a folder inside")
	}
}
