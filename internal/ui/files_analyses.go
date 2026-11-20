package ui

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/files"
)

// The folders analysed are kept between runs in the cache folder: read in the background as the app starts, so a
// folder analysed before is shown at once when i is pressed, and written a moment after they change. An analysis of
// an earlier run is read again in the background while a list shows it, and replaces what was shown once it is done.

// analysesPath is where the analyses are kept.
func (a *App) analysesPath() string { return filepath.Join(a.cacheDir, "analyses.bin") }

// loadAnalyses reads the analyses of earlier runs; a damaged file is as none, and is written anew with the next.
func (a *App) loadAnalyses() []*files.Node {
	trees, err := files.LoadAnalyses(a.analysesPath())
	if err != nil {
		return nil
	}
	return trees
}

// analysisTrees are the folders analysed, those of earlier runs among them once they are read.
func (a *App) analysisTrees() []*files.Node {
	if !a.fm.treesLoaded {
		a.fm.treesLoaded = true
		// Nothing is kept before they are read, as keeping goes through here: those read are all there is.
		a.fm.trees = a.fm.treesRead.get(a.loadAnalyses)
	}
	return a.fm.trees
}

// analysesChanged notes that the analyses are to be written, a moment later with any other change.
func (a *App) analysesChanged() {
	a.fm.treesDirty, a.fm.treesAt = true, time.Now()
}

// saveAnalyses writes the analyses once a change has settled, or at once when now is set. They are put into bytes
// here, as the lists go on changing them, and written in the background.
func (a *App) saveAnalyses(now bool) {
	if !a.fm.treesDirty || !now && time.Since(a.fm.treesAt) < 2*time.Second {
		return
	}
	a.fm.treesDirty = false
	data, seq := files.EncodedAnalyses(a.fm.trees), a.fm.treesSaver.next()
	path := a.analysesPath()
	a.background(func(context.Context) {
		if err := a.fm.treesSaver.write(path, seq, data); err != nil {
			a.post(func() { a.toast(a.u.tf("The analyses could not be kept: %v", err), true) })
		}
	})
}

// analysesSaver writes the analyses one at a time, the newest last: a write that comes after a newer one is dropped.
type analysesSaver struct {
	mu      sync.Mutex
	made    uint64 // on the UI goroutine
	written uint64
}

func (s *analysesSaver) next() uint64 {
	s.made++
	return s.made
}

func (s *analysesSaver) write(path string, seq uint64, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq <= s.written {
		return nil
	}
	if err := files.SaveAnalyses(path, data); err != nil {
		return err
	}
	s.written = seq
	return nil
}

// refreshAnalysis reads again, in the background, the analysis a list shows when it is of an earlier run: once per
// folder, so that one stopped or failing is not started again and again. What was shown stays until it is done.
func (a *App) refreshAnalysis(p *filesPane) {
	if !p.analysed || p.search != nil || p.atDrives() || a.scanOf(p.dir) != nil {
		return
	}
	n := a.analysis(p.dir)
	if n == nil || !n.Analysed().Before(a.fm.since) {
		return
	}
	key := filepath.Clean(p.dir)
	if a.fm.refreshed[key] {
		return
	}
	if a.fm.refreshed == nil {
		a.fm.refreshed = map[string]bool{}
	}
	a.fm.refreshed[key] = true
	a.scanFolder(p)
}
