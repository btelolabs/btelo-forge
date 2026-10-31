package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The tools write files of the document's pages, and never reach for a printer.
func TestExportTools(t *testing.T) {
	x := newHarness(t)
	s := connectMCP(t, x)
	dir := t.TempDir()
	wordOpen(t, x)
	x.typeIn("Words")
	x.a.newMindNow()
	x.frame(Input{})
	var mapped struct {
		Pages int `json:"pages"`
	}
	s.ok("mind_export_pdf", map[string]any{"to": filepath.Join(dir, "map.pdf")}, &mapped)
	if mapped.Pages != 1 {
		t.Fatalf("%+v", mapped)
	}
	x.a.newCanvasNow()
	x.frame(Input{})
	to := filepath.Join(dir, "canvas.pdf")
	var out struct {
		Path  string `json:"path"`
		Pages int    `json:"pages"`
	}
	s.ok("canvas_export_pdf", map[string]any{"to": to}, &out)
	if out.Path != to || out.Pages != 1 {
		t.Fatalf("%+v", out)
	}
	data, err := os.ReadFile(to)
	if err != nil || !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Fatalf("the file: %v", err)
	}
	for _, name := range mcpToolNames {
		if bytes.Contains([]byte(name), []byte("print")) {
			t.Errorf("a tool that prints: %s", name)
		}
	}
}
