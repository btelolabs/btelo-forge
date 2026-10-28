package ui

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/mind"
)

// A map acted on by its file, without a tab, has never been shown: a free node added without a place goes beside
// what the board holds, not to a place worked out from a view it does not have.
func TestMCPAddsAFreeNodeToAMapNotOpen(t *testing.T) {
	x := newHarness(t)
	s := connectMCPAt(t, x, "/mind")
	dir := t.TempDir()
	path := filepath.Join(dir, "closed"+mind.Ext)
	m := mind.New("closed", x.a.mindMeasure())
	if _, err := m.Add(mind.Node{Text: "Root"}, 0, -1); err != nil {
		t.Fatal(err)
	}
	data, err := mind.Encode(m, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	s.ok("mind_add", map[string]any{"path": path, "nodes": []map[string]any{{"key": "free", "text": "Free topic"}}}, nil)
	var state mindStateView
	s.ok("mind_state", map[string]any{"path": path}, &state)
	if state.Nodes != 2 {
		t.Fatalf("%d nodes, want 2", state.Nodes)
	}
	for _, n := range state.List {
		for _, v := range n.Box {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 10000 {
				t.Fatalf("%q is at %v, far from the board", n.Text, n.Box)
			}
		}
	}
}
