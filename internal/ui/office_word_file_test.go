package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWordOpenFile shows the pages of the file named by FORGE_WORD_FILE, into FORGE_SHOTS: a way to look at any document
// as the window draws it.
func TestWordOpenFile(t *testing.T) {
	path := os.Getenv("FORGE_WORD_FILE")
	dir := os.Getenv("FORGE_SHOTS")
	if path == "" || dir == "" {
		t.Skip("set FORGE_WORD_FILE and FORGE_SHOTS")
	}
	x := newHarness(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(x.dir, filepath.Base(path))
	if err := os.WriteFile(copied, data, 0o644); err != nil {
		t.Fatal(err)
	}
	od, err := x.a.openOffice(wordFormat(t), copied, data)
	if err != nil {
		t.Fatal(err)
	}
	e := wordEditorOf(od)
	e.d.Finish()
	x.settle(5 * time.Second)
	x.frame(Input{})
	x.shot("file-window")
	for i := range e.d.Pages() {
		img, err := e.renderWord(i, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-page-%02d.png", i+1)), img, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d pages", len(e.d.Pages()))
}
