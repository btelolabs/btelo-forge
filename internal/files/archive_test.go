package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompressAndExtract(t *testing.T) {
	root := tree(t, map[string]string{"proj/a.txt": "alpha", "proj/sub/b.txt": strings.Repeat("b", 5000), "proj/empty/": "", "note.md": "n"})
	zipPath := filepath.Join(root, "out.zip")
	var p Progress
	if err := Compress(context.Background(), []string{filepath.Join(root, "proj"), filepath.Join(root, "note.md")}, zipPath, &p); err != nil {
		t.Fatal(err)
	}
	if p.Done.Load() != 5006 || p.Total.Load() != 5006 {
		t.Errorf("progress %d of %d", p.Done.Load(), p.Total.Load())
	}
	// Two items at the top: they go into a folder named after the archive.
	got, err := Extract(context.Background(), zipPath, &Progress{})
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "out") || read(t, filepath.Join(got, "proj", "sub", "b.txt")) != strings.Repeat("b", 5000) ||
		read(t, filepath.Join(got, "note.md")) != "n" || !exists(filepath.Join(got, "proj", "empty")) {
		t.Errorf("extracted to %s", got)
	}
	// One folder at the top: it comes out by itself, under a free name beside the original.
	one := filepath.Join(root, "one.zip")
	if err := Compress(context.Background(), []string{filepath.Join(root, "proj")}, one, &Progress{}); err != nil {
		t.Fatal(err)
	}
	got, err = Extract(context.Background(), one, &Progress{})
	if err != nil || got != filepath.Join(root, "proj (2)") || read(t, filepath.Join(got, "a.txt")) != "alpha" {
		t.Errorf("extracted one folder to %s: %v", got, err)
	}
	des, _ := os.ReadDir(root)
	for _, d := range des {
		if strings.Contains(d.Name(), ".extracting-") {
			t.Errorf("%s was left behind", d.Name())
		}
	}
	if err := Compress(context.Background(), []string{filepath.Join(root, "proj")}, one, &Progress{}); err == nil {
		t.Error("an existing archive was overwritten")
	}
}

func TestExtractRefusesEntriesOutside(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.zip")
	f, _ := os.Create(bad)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../escaped.txt")
	w.Write([]byte("x"))
	zw.Close()
	f.Close()
	if _, err := Extract(context.Background(), bad, &Progress{}); !errors.Is(err, ErrUnsafeArchive) {
		t.Errorf("an entry leading out gave %v", err)
	}
	if exists(filepath.Join(root, "escaped.txt")) || exists(filepath.Join(filepath.Dir(root), "escaped.txt")) {
		t.Error("the entry was written outside")
	}
	des, _ := os.ReadDir(root)
	if len(des) != 1 {
		t.Errorf("a failed extraction left %d items", len(des))
	}
}

func TestExtractTarGz(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pack.tar.gz")
	f, _ := os.Create(path)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"x.txt": "xx", "d/y.txt": "yy"} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	f.Close()
	got, err := Extract(context.Background(), path, &Progress{})
	if err != nil || got != filepath.Join(root, "pack") || read(t, filepath.Join(got, "d", "y.txt")) != "yy" {
		t.Errorf("extracted to %s: %v", got, err)
	}
	if !IsArchive("A.TAR.GZ") || IsArchive("notes.txt") || IsArchive(".zip") {
		t.Error("archives are not told apart")
	}
}

func TestCreate(t *testing.T) {
	root := t.TempDir()
	if p, err := Create(root, "notes.txt"); err != nil || exists(p) == false {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "notes.txt")); err != nil || info.IsDir() {
		t.Error("a name with an extension did not make a file")
	}
	if p, err := Create(root, "folder"); err != nil {
		t.Fatal(err)
	} else if info, _ := os.Stat(p); !info.IsDir() {
		t.Error("a name without an extension did not make a folder")
	}
	if _, err := Create(root, ".config"); err != nil || !isDir(filepath.Join(root, ".config")) {
		t.Errorf("a dotted name without an extension: %v", err)
	}
	if _, err := Create(root, "notes.txt"); err == nil {
		t.Error("a taken name was made again")
	}
	if _, err := Create(root, `a\b`); err == nil {
		t.Error("a name with a folder in it was allowed")
	}
}

func isDir(p string) bool { info, err := os.Stat(p); return err == nil && info.IsDir() }
