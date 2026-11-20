package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/files"
)

// heldScans makes the file manager's analyses read their folder whole and then wait to be let go, so a test sees
// them running with everything found; release lets every one held, and those to come, finish.
func heldScans(x *harness) (release func()) {
	gate := make(chan struct{})
	x.a.fm.scan = func(ctx context.Context, root string, p *files.ScanProgress) (*files.Node, error) {
		n, err := files.Scan(ctx, root, p)
		select {
		case <-gate:
		case <-ctx.Done():
			return n, ctx.Err()
		}
		return n, err
	}
	released := false
	release = func() {
		if !released {
			released = true
			close(gate)
		}
	}
	x.t.Cleanup(release)
	return release
}

// analysedLayout is a folder of three folders of different sizes and a file.
var analysedLayout = map[string]string{
	"small/a.bin":    strings.Repeat("s", 1_000),
	"big/a.bin":      strings.Repeat("b", 90_000),
	"big/deep/b.bin": strings.Repeat("b", 9_000),
	"mid/a.bin":      strings.Repeat("m", 20_000),
	"loose.txt":      "x",
}

// rowNames are the names of a list's rows, in order.
func rowNames(p *filesPane) []string {
	var out []string
	for _, r := range p.rows {
		out = append(out, p.entries[r].Name)
	}
	return out
}

func TestAnAnalysisShowsWhatItFindsAsItRuns(t *testing.T) {
	x, _ := filesHarness(t, analysedLayout)
	release := heldScans(x)
	p := x.a.files.pane()
	x.press('I', "")
	x.pump(10*time.Second, func() bool { return p.analysed && x.a.paneAnalysis(p) != nil && x.a.paneAnalysis(p).Size >= 120_001 })
	if x.a.scanFor(p) == nil {
		t.Fatal("the analysis is not running")
	}
	live := x.a.paneAnalysis(p)
	if c := live.Child("big"); c == nil || c.Size != 99_000 || c.Items != 3 {
		t.Errorf("big shows %+v while the analysis runs", c)
	}
	if got := strings.Join(rowNames(p)[:3], " "); got != "big mid small" {
		t.Errorf("the list is in the order %q while the analysis runs", got)
	}
	if x.a.analysis(p.dir) != nil {
		t.Error("what was found so far was kept as an analysis")
	}
	// Its pie has the inner ring alone: nothing further out is known yet, not even that it is loose files.
	for _, arc := range pieArcs(live, x.a.pieWedges(p, live)) {
		if arc.ring > 0 {
			t.Fatalf("the pie of an analysis running has %+v", arc)
		}
	}
	release()
	x.waitFiles()
	if n := x.a.paneAnalysis(p); n == nil || n == live || n.Child("big").Child("deep") == nil || !p.analysed {
		t.Fatalf("once done the list shows %+v", n)
	}
}

func TestStoppingAnAnalysisAsItRunsShowsThePlainList(t *testing.T) {
	x, _ := filesHarness(t, analysedLayout)
	heldScans(x)
	p := x.a.files.pane()
	x.press('I', "")
	x.pump(10*time.Second, func() bool { return x.a.paneAnalysis(p) != nil })
	x.press('I', "")
	x.waitFiles()
	if p.analysed || x.a.paneAnalysis(p) != nil || x.a.analysis(p.dir) != nil || p.sort != listByName {
		t.Fatalf("after stopping, the list is analysed %v, sorted %v", p.analysed, p.sort)
	}
}

// restartAnalyses makes the app as it is after a restart: the lists plain, the analyses read from the file again, of
// an earlier run.
func restartAnalyses(x *harness) {
	for _, p := range x.a.files.panes {
		x.a.leaveAnalysis(p)
	}
	x.a.fm.trees, x.a.fm.treesLoaded, x.a.fm.treesRead = nil, false, loadAhead[[]*files.Node]{}
	x.a.fm.refreshed = nil
	x.a.fm.since = time.Now()
}

func TestAnAnalysisOfAnEarlierRunIsShownAtOnceAndReadAgain(t *testing.T) {
	x, root := filesHarness(t, analysedLayout)
	p := x.a.files.pane()
	x.press('I', "")
	x.waitFiles()
	x.a.saveAnalyses(true)
	x.pump(10*time.Second, func() bool { _, err := os.Stat(x.a.analysesPath()); return err == nil && x.a.fm.treesSaver.written > 0 })

	// The app starts again, and a folder grows while it was not running.
	restartAnalyses(x)
	if err := os.WriteFile(filepath.Join(root, "small", "more.bin"), []byte(strings.Repeat("n", 50_000)), 0o644); err != nil {
		t.Fatal(err)
	}
	release := heldScans(x)
	x.press('I', "")
	x.frame(Input{})
	old := x.a.paneAnalysis(p)
	if old == nil || old.Child("small") == nil || old.Child("small").Size != 1_000 {
		t.Fatalf("the analysis kept is not shown at once: %+v", old)
	}
	if info := x.a.paneInfo(p); !strings.Contains(info, "Analysing") || !strings.Contains(info, "as of") || x.a.scanOf(p.dir) == nil {
		t.Errorf("an analysis of an earlier run is not shown as such and read again: %q", info)
	}
	release()
	x.waitFiles()
	n := x.a.paneAnalysis(p)
	if n == nil || n.Child("small").Size != 51_000 || !p.analysed {
		t.Fatalf("read again, the list shows %+v", n)
	}
	if got := rowNames(p)[:3]; strings.Join(got, " ") != "big small mid" {
		t.Errorf("read again, the list is in the order %v", got)
	}
	if strings.Contains(x.a.paneInfo(p), "as of") {
		t.Errorf("an analysis of this run says %q", x.a.paneInfo(p))
	}

	// Stopped as it is read again, the one of the earlier run stays, saying how old it is, and is not read again.
	restartAnalyses(x)
	heldScans(x)
	x.press('I', "")
	x.frame(Input{})
	x.press('I', "") // stops the reading again
	x.waitFiles()
	x.frame(Input{})
	if !p.analysed || x.a.paneAnalysis(p) == nil || x.a.scanOf(p.dir) != nil {
		t.Fatalf("stopped, the list is analysed %v, running %v", p.analysed, x.a.scanOf(p.dir) != nil)
	}
	if !strings.Contains(x.a.paneInfo(p), "as of") {
		t.Errorf("an analysis of an earlier run does not say how old it is: %q", x.a.paneInfo(p))
	}
}

func TestAnalysesAreKeptOnceAChangeSettles(t *testing.T) {
	x, root := filesHarness(t, analysedLayout)
	x.press('I', "")
	x.waitFiles()
	if !x.a.fm.treesDirty {
		t.Fatal("a new analysis is not to be kept")
	}
	x.a.saveAnalyses(false)
	if x.a.fm.treesSaver.made != 0 {
		t.Error("the analyses were written before the change settled")
	}
	x.a.fm.treesAt = time.Now().Add(-3 * time.Second)
	x.a.saveAnalyses(false)
	x.pump(10*time.Second, func() bool { return x.a.fm.treesSaver.written == 1 })
	trees, err := files.LoadAnalyses(x.a.analysesPath())
	if err != nil || len(trees) != 1 || !files.SamePath(trees[0].Path(), root) || trees[0].Child("big").Size != 99_000 {
		t.Fatalf("kept %v: %v", trees, err)
	}
	// A folder deleted from an analysed list comes off the analysis kept.
	x.a.takeFromAnalyses(filepath.Join(root, "big"), 0)
	x.a.saveAnalyses(true)
	x.pump(10*time.Second, func() bool { return x.a.fm.treesSaver.written == 2 })
	if trees, _ := files.LoadAnalyses(x.a.analysesPath()); len(trees) != 1 || trees[0].Child("big") != nil {
		t.Errorf("the deleted folder is still kept")
	}
}

func TestADamagedFileOfAnalysesIsAsNone(t *testing.T) {
	x, _ := filesHarness(t, analysedLayout)
	if err := os.MkdirAll(filepath.Dir(x.a.analysesPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(x.a.analysesPath(), []byte("BFAN\x01\x05garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	restartAnalyses(x)
	if trees := x.a.analysisTrees(); len(trees) != 0 {
		t.Fatalf("a damaged file gave %d analyses", len(trees))
	}
	x.press('I', "")
	x.waitFiles()
	if x.a.paneAnalysis(x.a.files.pane()) == nil {
		t.Error("the folder could not be analysed after a damaged file")
	}
}

func TestAnAnalysisOfAFolderSinceGoneIsDropped(t *testing.T) {
	x, root := filesHarness(t, analysedLayout)
	p := x.a.files.pane()
	x.press('I', "")
	x.waitFiles()
	gone := filepath.Join(root, "mid")
	if x.a.analysis(gone) == nil {
		t.Fatal("mid was not analysed")
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	// Read again for a list that was in it, as an earlier run's analysis is.
	p.dir = gone
	x.a.scanFolder(p)
	x.waitFiles()
	if x.a.analysis(gone) != nil {
		t.Error("the analysis of a folder that is gone is kept")
	}
	if x.a.analysis(root) == nil || x.a.analysis(root).Size != 100_001 {
		t.Errorf("the folder it was in holds %+v", x.a.analysis(root))
	}
}
