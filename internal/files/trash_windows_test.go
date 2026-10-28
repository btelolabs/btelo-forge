package files

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
)

func TestReadBinInfo(t *testing.T) {
	path := `C:\Users\me\old.txt`
	name := utf16.Encode([]rune(path + "\x00"))
	at := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	ft := syscall.NsecToFiletime(at.UnixNano())
	b := binary.LittleEndian.AppendUint64(nil, 2)
	b = binary.LittleEndian.AppendUint64(b, 1234)
	b = binary.LittleEndian.AppendUint32(b, ft.LowDateTime)
	b = binary.LittleEndian.AppendUint32(b, ft.HighDateTime)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(name)))
	for _, c := range name {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	file := filepath.Join(t.TempDir(), "$IABC123.txt")
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, when, ok := readBinInfo(file)
	if !ok || got != path || !when.Equal(at) {
		t.Errorf("read %q at %v (%v)", got, when, ok)
	}
}

// TestTrashAndRestore sends a folder to the real Recycle Bin and puts it back, leaving the bin as it was.
func TestTrashAndRestore(t *testing.T) {
	root := tree(t, map[string]string{"bin-test/inner.txt": "keep me"})
	path := filepath.Join(root, "bin-test")
	if !CanTrash(path) {
		t.Skip("the temporary folder is on a drive without a Recycle Bin")
	}
	tr, err := Trash(path)
	if err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Fatal("the folder is still there")
	}
	if tr.Stored == "" {
		t.Fatal("the item was not found in the Recycle Bin")
	}
	to, err := Restore(tr)
	if err != nil {
		t.Fatal(err)
	}
	if long, _ := longPath(path); to != long || read(t, filepath.Join(path, "inner.txt")) != "keep me" || exists(tr.Info) || exists(tr.Stored) {
		t.Errorf("restored to %q; the bin still has %v %v", to, exists(tr.Info), exists(tr.Stored))
	}
	if CanTrash(`\server\share\x`) {
		t.Error("a network share was taken for a drive with a Recycle Bin")
	}
}
