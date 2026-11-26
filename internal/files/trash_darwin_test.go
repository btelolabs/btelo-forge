//go:build darwin

package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestTrashAndRestore sends a folder to the real macOS Trash via NSFileManager and puts it back, leaving the Trash
// clean as it was.
func TestTrashAndRestore(t *testing.T) {
	root := tree(t, map[string]string{"bin-test/inner.txt": "keep me"})
	path := filepath.Join(root, "bin-test")
	if !CanTrash(path) {
		t.Skip("the temporary folder is on a volume without a Trash")
	}
	tr, err := Trash(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if tr.Stored != "" && exists(tr.Stored) {
			os.RemoveAll(tr.Stored)
		}
	})
	if exists(path) {
		t.Fatal("the folder is still there after Trash")
	}
	if tr.Stored == "" || !exists(tr.Stored) {
		t.Fatalf("the trashed item was not found at Stored=%q", tr.Stored)
	}
	to, err := Restore(tr)
	if err != nil {
		t.Fatal(err)
	}
	if !SamePath(to, path) || read(t, filepath.Join(path, "inner.txt")) != "keep me" || exists(tr.Stored) {
		t.Errorf("restored to %q (want %q); trash still has %v", to, path, exists(tr.Stored))
	}
}

func TestUFHiddenFlagIsHonored(t *testing.T) {
	root := tree(t, map[string]string{"visible.txt": "v", "flagged.txt": "h"})
	flagged := filepath.Join(root, "flagged.txt")
	if err := unix.Chflags(flagged, ufHidden); err != nil {
		t.Skipf("chflags UF_HIDDEN not supported here: %v", err)
	}
	t.Cleanup(func() { _ = unix.Chflags(flagged, 0) })

	es, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Entry{}
	for _, e := range es {
		byName[e.Name] = e
	}
	if !byName["flagged.txt"].Hidden || byName["visible.txt"].Hidden {
		t.Fatalf("UF_HIDDEN detection wrong: %+v", es)
	}
}

func TestDecodeLegacyZipNames(t *testing.T) {
	want := "中文文件夹/测试报告.txt"
	gbkBytes, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeLegacyName(string(gbkBytes)); got != want {
		t.Errorf("decodeLegacyName(GBK) = %q, want %q", got, want)
	}
	if got := decodeLegacyName("already-utf8-你好.txt"); got != "already-utf8-你好.txt" {
		t.Errorf("decodeLegacyName(UTF-8) = %q", got)
	}
}

func BenchmarkScanAPFSBulkVsFallback(b *testing.B) {
	root := b.TempDir()
	// Build a realistic 1,000-file, 25-subdirectory tree.
	payload := []byte(strings.Repeat("x", 1024))
	for d := range 25 {
		sub := filepath.Join(root, fmt.Sprintf("dir-%02d", d))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := range 40 {
			if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("file-%03d.dat", f)), payload, 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}

	b.Run("getattrlistbulk", func(b *testing.B) {
		for range b.N {
			n, err := Scan(context.Background(), root, nil)
			if err != nil || n.Items != 1025 {
				b.Fatalf("scan got %v, %d items", err, n.Items)
			}
		}
	})

	b.Run("ReadDirPlusLstat", func(b *testing.B) {
		for range b.N {
			var count int64
			_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if info, ierr := d.Info(); ierr == nil && !info.IsDir() {
					count += info.Size()
				}
				return nil
			})
			if count != 1000*1024 {
				b.Fatalf("count=%d", count)
			}
		}
	})
}

func BenchmarkCopyAPFSClonefile(b *testing.B) {
	root := b.TempDir()
	src := filepath.Join(root, "video.bin")
	// 32 MiB file to measure APFS O(1) CoW clonefile vs user-space streaming.
	if err := os.WriteFile(src, make([]byte, 32<<20), 0o644); err != nil {
		b.Fatal(err)
	}
	dstDir := filepath.Join(root, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		dst := filepath.Join(dstDir, fmt.Sprintf("copy-%d.bin", i))
		info, _ := os.Lstat(src)
		if err := copyFile(context.Background(), src, dst, info, &Progress{}); err != nil {
			b.Fatal(err)
		}
		_ = os.Remove(dst)
	}
}
