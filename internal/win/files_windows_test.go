package win

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestClipboardFilesRoundTrip puts two files on the clipboard as Explorer does and reads them back. It replaces what
// is on the clipboard, so it runs only when FORGE_CLIPBOARD_TEST is set.
func TestClipboardFilesRoundTrip(t *testing.T) {
	if os.Getenv("FORGE_CLIPBOARD_TEST") == "" {
		t.Skip("set FORGE_CLIPBOARD_TEST to let the test replace the clipboard")
	}
	w, err := Open("clipboard test", 100, 100, Background)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "一.txt"), filepath.Join(dir, "b c.png")}
	if err := w.SetClipboardFiles(paths); err != nil {
		t.Fatal(err)
	}
	if _, files, _ := w.Paste(); !slices.Equal(files, paths) {
		t.Fatalf("read back %q", files)
	}
}

// TestTrashMovesToTheRecycleBin puts a file of its own in the Recycle Bin.
func TestTrashMovesToTheRecycleBin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "btelo-forge trash test.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Trash([]string{path}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file is still there: %v", err)
	}
}
