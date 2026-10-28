package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// tree writes files under a new folder: each key is a slash-separated path, a trailing slash making an empty folder.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestScanAddsUpEveryFolder(t *testing.T) {
	root := tree(t, map[string]string{
		"a.txt": "12345", "big/x.bin": strings.Repeat("x", 1000), "big/deep/y.bin": strings.Repeat("y", 500),
		"small/z": "zz", "empty/": "",
	})
	var p ScanProgress
	n, err := Scan(context.Background(), root, &p)
	if err != nil {
		t.Fatal(err)
	}
	if n.Size != 5+1000+500+2 || n.Items != 8 {
		t.Errorf("the root holds %d bytes in %d items, want 1507 in 8", n.Size, n.Items)
	}
	big := n.Child("big")
	if big == nil || big.Size != 1500 || big.Items != 3 {
		t.Fatalf("big is %+v", big)
	}
	if got := n.Find(filepath.Join(root, "big", "deep")); got == nil || got.Size != 500 || got.Path() != filepath.Join(root, "big", "deep") {
		t.Errorf("found %+v for big/deep", got)
	}
	if n.Find(filepath.Dir(root)) != nil {
		t.Error("a folder above the root was found in it")
	}
	if p.Files.Load() != 4 || p.Dirs.Load() != 5 || p.Bytes.Load() != 1507 {
		t.Errorf("progress counted %d files, %d folders, %d bytes", p.Files.Load(), p.Dirs.Load(), p.Bytes.Load())
	}

	deep := big.Child("deep")
	deep.Detach()
	if big.Size != 1000 || big.Items != 1 || n.Size != 1007 || n.Items != 6 || big.Child("deep") != nil {
		t.Errorf("after detaching deep: big %d/%d, root %d/%d", big.Size, big.Items, n.Size, n.Items)
	}
	n.Child("small").Attach(deep)
	if n.Size != 1507 || n.Child("small").Size != 502 || deep.Path() != filepath.Join(root, "small", "deep") {
		t.Errorf("after attaching deep to small: root %d, small %d at %s", n.Size, n.Child("small").Size, deep.Path())
	}
}

func TestScanStopsWhenCancelled(t *testing.T) {
	files := map[string]string{}
	for i := range 200 {
		files[fmt.Sprintf("d%d/f%d", i%26, i)] = "x"
	}
	root := tree(t, files)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, root, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled analysis returned %v", err)
	}
}

func TestListMarksHiddenFilesAndFolders(t *testing.T) {
	root := tree(t, map[string]string{".env": "x", "dir/f": "y", "plain.txt": "abc"})
	es, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Entry{}
	for _, e := range es {
		byName[e.Name] = e
	}
	if !byName[".env"].Hidden || byName["plain.txt"].Hidden || !byName["dir"].Dir || byName["plain.txt"].Size != 3 {
		t.Errorf("listed %+v", es)
	}
}

func TestRemoveDeletesEverythingAndCounts(t *testing.T) {
	files := map[string]string{"keep.txt": "k"}
	for i := range 30 {
		files[fmt.Sprintf("gone/%c/f%d", 'a'+i%5, i)] = "x"
	}
	root := tree(t, files)
	var p Progress
	res := Remove(context.Background(), []string{filepath.Join(root, "gone")}, &p)
	if err := res.Err(); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(root, "gone")) || !exists(filepath.Join(root, "keep.txt")) {
		t.Error("the wrong things were deleted")
	}
	if p.Total.Load() != 36 || p.Done.Load() != 36 || p.Fraction() != 1 {
		t.Errorf("progress %d of %d", p.Done.Load(), p.Total.Load())
	}
	if len(res.Pairs) != 1 {
		t.Errorf("reported %v as deleted", res.Pairs)
	}
}

func TestRemoveLeavesWhatALinkPointsTo(t *testing.T) {
	root := tree(t, map[string]string{"target/precious": "p", "victim/": ""})
	link := filepath.Join(root, "victim", "link")
	if err := os.Symlink(filepath.Join(root, "target"), link); err != nil {
		t.Skipf("links cannot be made here: %v", err)
	}
	if res := Remove(context.Background(), []string{filepath.Join(root, "victim")}, &Progress{}); res.Err() != nil {
		t.Fatal(res.Err())
	}
	if !exists(filepath.Join(root, "target", "precious")) {
		t.Error("deleting a folder with a link in it deleted what the link points to")
	}
}

func TestCopyNamesCopiesAndRefusesItself(t *testing.T) {
	root := tree(t, map[string]string{"src/a.txt": "hello", "src/sub/b.txt": "world", "dst/a.txt": "taken", "dst/src/": ""})
	var p Progress
	res := Copy(context.Background(), []string{filepath.Join(root, "src", "a.txt"), filepath.Join(root, "src")}, filepath.Join(root, "dst"), &p)
	if err := res.Err(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "dst", "a (2).txt")); got != "hello" {
		t.Errorf("the copy over a taken name holds %q", got)
	}
	if got := read(t, filepath.Join(root, "dst", "src (2)", "sub", "b.txt")); got != "world" {
		t.Errorf("the folder copy holds %q", got)
	}
	if read(t, filepath.Join(root, "dst", "a.txt")) != "taken" {
		t.Error("a file was overwritten")
	}
	if p.Total.Load() != 15 || p.Done.Load() != 15 {
		t.Errorf("progress %d of %d bytes", p.Done.Load(), p.Total.Load())
	}
	res = Copy(context.Background(), []string{filepath.Join(root, "src")}, filepath.Join(root, "src", "sub"), &Progress{})
	if len(res.Failed) != 1 || !errors.Is(res.Failed[0].Err, ErrIntoItself) || exists(filepath.Join(root, "src", "sub", "src")) {
		t.Errorf("copying a folder into itself gave %+v", res)
	}
}

func TestCancelledCopyLeavesNoHalfCopy(t *testing.T) {
	root := tree(t, map[string]string{"big.bin": strings.Repeat("b", 3*copyBuffer), "dst/": ""})
	ctx, cancel := context.WithCancel(context.Background())
	p := &Progress{}
	var once sync.Once
	go func() {
		for p.Done.Load() == 0 {
			runtime.Gosched()
		}
		once.Do(cancel)
	}()
	Copy(ctx, []string{filepath.Join(root, "big.bin")}, filepath.Join(root, "dst"), p)
	once.Do(cancel)
	if p.Done.Load() < 3*copyBuffer && exists(filepath.Join(root, "dst", "big.bin")) {
		t.Error("a cancelled copy left part of the file")
	}
}

func TestMoveAndUndo(t *testing.T) {
	root := tree(t, map[string]string{"a/one.txt": "1", "a/dir/two.txt": "2", "b/one.txt": "other"})
	srcs := []string{filepath.Join(root, "a", "one.txt"), filepath.Join(root, "a", "dir")}
	res := Move(context.Background(), srcs, filepath.Join(root, "b"), &Progress{})
	if err := res.Err(); err != nil {
		t.Fatal(err)
	}
	if exists(srcs[0]) || read(t, filepath.Join(root, "b", "one (2).txt")) != "1" || read(t, filepath.Join(root, "b", "dir", "two.txt")) != "2" {
		t.Fatal("the items were not moved")
	}
	undo := Undo(context.Background(), Action{Kind: Moved, Pairs: res.Pairs}, &Progress{})
	if err := undo.Err(); err != nil {
		t.Fatal(err)
	}
	if read(t, srcs[0]) != "1" || read(t, filepath.Join(srcs[1], "two.txt")) != "2" || read(t, filepath.Join(root, "b", "one.txt")) != "other" {
		t.Error("undo did not put the items back")
	}
	// Moving an item into the folder it is in does nothing.
	if res := Move(context.Background(), srcs[:1], filepath.Join(root, "a"), &Progress{}); len(res.Pairs) != 0 || !exists(srcs[0]) {
		t.Errorf("moving into its own folder gave %+v", res)
	}
}

func TestUndoCopyAndRename(t *testing.T) {
	root := tree(t, map[string]string{"f.txt": "x", "dst/": ""})
	res := Copy(context.Background(), []string{filepath.Join(root, "f.txt")}, filepath.Join(root, "dst"), &Progress{})
	if r := Undo(context.Background(), Action{Kind: Copied, Pairs: res.Pairs}, &Progress{}); r.Err() != nil || exists(filepath.Join(root, "dst", "f.txt")) {
		t.Errorf("undoing a copy: %v", r.Err())
	}
	if !exists(filepath.Join(root, "f.txt")) {
		t.Fatal("undoing a copy deleted the original")
	}
	to, err := Rename(filepath.Join(root, "f.txt"), "g.txt")
	if err != nil || read(t, to) != "x" {
		t.Fatalf("rename: %v", err)
	}
	if _, err := Rename(to, "dst"); err == nil {
		t.Error("renaming over a folder was allowed")
	}
	if _, err := Rename(to, `..\x`); err == nil {
		t.Error("a name with a folder in it was allowed")
	}
	if r := Undo(context.Background(), Action{Kind: Renamed, Pairs: []Pair{{filepath.Join(root, "f.txt"), to}}}, &Progress{}); r.Err() != nil || read(t, filepath.Join(root, "f.txt")) != "x" {
		t.Errorf("undoing a rename: %v", r.Err())
	}
	// Undo never overwrites what came there since.
	to, _ = Rename(filepath.Join(root, "f.txt"), "g.txt")
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("new"), 0o644)
	if r := Undo(context.Background(), Action{Kind: Renamed, Pairs: []Pair{{filepath.Join(root, "f.txt"), to}}}, &Progress{}); !errors.Is(r.Err(), ErrOccupied) {
		t.Errorf("undo over a new file: %v", r.Err())
	}
}

func TestUniqueName(t *testing.T) {
	root := tree(t, map[string]string{"a.txt": "", "a (2).txt": "", ".gitignore": "", "dir/": ""})
	for name, want := range map[string]string{"a.txt": "a (3).txt", "b.txt": "b.txt", ".gitignore": ".gitignore (2)", "dir": "dir (2)"} {
		if got := UniqueName(root, name); got != want {
			t.Errorf("UniqueName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestWithin(t *testing.T) {
	sep := string(filepath.Separator)
	root := filepath.Join(os.TempDir(), "x")
	cases := []struct {
		path string
		want bool
	}{{root, true}, {root + sep + "y", true}, {root + "y", false}, {filepath.Dir(root), false}}
	for _, c := range cases {
		if got := Within(c.path, root); got != c.want {
			t.Errorf("Within(%q, %q) = %v", c.path, root, got)
		}
	}
}

func TestHistoryRanksByFrequencyAndRecency(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	var h History
	proj := filepath.Join("E:", "projects", "btelo-video-cut")
	other := filepath.Join("E:", "projects", "video-old")
	docs := filepath.Join("C:", "Users", "me", "Documents")
	for range 3 {
		h.Add(proj, now)
	}
	h.Add(other, now.Add(-30*24*time.Hour))
	h.Add(docs, now)
	if got := h.Match("video", now); !slices.Equal(got, []string{proj, other}) {
		t.Errorf("video matched %v", got)
	}
	if got := h.Match("proj cut", now); !slices.Equal(got, []string{proj}) {
		t.Errorf("words in order matched %v", got)
	}
	if got := h.Match("cut proj", now); len(got) != 0 {
		t.Errorf("words out of order matched %v", got)
	}
	if got := h.Match("projects", now); len(got) != 0 {
		t.Errorf("the last word matched a parent folder only: %v", got)
	}
	if got := h.Match("", now); len(got) != 3 || got[0] != proj {
		t.Errorf("nothing typed listed %v", got)
	}
	h.Forget(other)
	path := filepath.Join(t.TempDir(), "h", "history.json")
	if err := h.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := LoadHistory(path).Match("video", now); !slices.Equal(got, []string{proj}) {
		t.Errorf("after saving and forgetting, video matched %v", got)
	}
	if LoadHistory(filepath.Join(t.TempDir(), "none.json")).Dirs == nil {
		t.Error("a missing history has no map to add to")
	}
}

func TestHistoryAges(t *testing.T) {
	var h History
	now := time.Now()
	for i := range historyMax + 10 {
		h.Add(filepath.Join("C:", "d"+strings.Repeat("x", i%3), string(rune('a'+i%20))), now)
	}
	var sum float64
	for _, v := range h.Dirs {
		sum += v.Rank
	}
	if sum > historyMax {
		t.Errorf("the ranks add up to %v", sum)
	}
}

func TestIndexFilter(t *testing.T) {
	root := tree(t, map[string]string{"main.go": "", "internal/ui/files.go": "", "internal/files/scan.go": "", "README.md": "",
		".git/config": "", "node_modules/x/files.js": "", ".hidden/files.txt": ""})
	ix := NewIndex(root)
	ix.Build(context.Background(), true)
	if !ix.Done() {
		t.Fatal("the index is not done")
	}
	hits, n := ix.Filter(context.Background(), "files", 10)
	var rels []string
	for _, h := range hits {
		rels = append(rels, filepath.ToSlash(h.Rel))
	}
	// The nearest first; .git and node_modules are not searched; the folder "files" matches as a name.
	want := []string{".hidden/files.txt", "internal/files", "internal/ui/files.go"}
	if n != 3 || !slices.Equal(rels, want) {
		t.Errorf("files matched %v (%d)", rels, n)
	}
	if hits, _ := ix.Filter(context.Background(), "ui go", 10); len(hits) != 1 || filepath.ToSlash(hits[0].Rel) != "internal/ui/files.go" {
		t.Errorf("ui go matched %v", hits)
	}
	if hits, _ := ix.Filter(context.Background(), "readme", 10); len(hits) != 1 {
		t.Errorf("lower case did not match a capital: %v", hits)
	}
	if hits, _ := ix.Filter(context.Background(), "Readme", 10); len(hits) != 0 {
		t.Errorf("a capital matched without regard to case: %v", hits)
	}
	plain := NewIndex(root)
	plain.Build(context.Background(), false)
	if hits, _ := plain.Filter(context.Background(), "files", 10); len(hits) != 2 {
		t.Errorf("without hidden files, files matched %v", hits)
	}
}

func TestGrep(t *testing.T) {
	root := tree(t, map[string]string{
		"a.go":       "package a\n\tfunc Hello() {}\nhello again\r\n",
		"b/c.txt":    "nothing\nsay HELLO\n",
		"bin.dat":    "hello\x00binary",
		".git/HEAD":  "hello",
		"long.txt":   strings.Repeat("é", 300) + "hello" + strings.Repeat("z", 300),
		"empty.txt":  "",
		"other.text": "goodbye",
	})
	var mu sync.Mutex
	var got []string
	var stats GrepStats
	Grep(context.Background(), root, "hello", false, &stats, func(l Line) {
		mu.Lock()
		defer mu.Unlock()
		if l.Text[l.At:l.At+l.Len] != "hello" && !strings.EqualFold(l.Text[l.At:l.At+l.Len], "hello") {
			t.Errorf("%s:%d marks %q", l.Rel, l.No, l.Text[l.At:l.At+l.Len])
		}
		if len(l.Text) > lineMax+8 {
			t.Errorf("%s:%d is %d bytes", l.Rel, l.No, len(l.Text))
		}
		got = append(got, filepath.ToSlash(l.Rel)+":"+string(rune('0'+l.No)))
	})
	slices.Sort(got)
	want := []string{"a.go:2", "a.go:3", "b/c.txt:2", "long.txt:1"}
	if !slices.Equal(got, want) {
		t.Errorf("hello found %v", got)
	}
	got = nil
	Grep(context.Background(), root, "Hello", false, &stats, func(l Line) {
		mu.Lock()
		got = append(got, filepath.ToSlash(l.Rel)+":"+string(rune('0'+l.No)))
		mu.Unlock()
	})
	if !slices.Equal(got, []string{"a.go:2"}) {
		t.Errorf("with a capital, Hello found %v", got)
	}
}

func TestParseGitStatus(t *testing.T) {
	out := "# branch.oid 0123456789abcdef\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +2 -1\n" +
		"1 .M N... 100644 100644 100644 a a x.go\n1 M. N... 100644 100644 100644 a a y.go\n1 MM N... 1 1 1 a a z.go\n" +
		"2 R. N... 100644 100644 100644 a a R100 new.go\told.go\nu UU N... 1 1 1 1 a a a c.go\n? new.txt\n? dir/\n"
	var s GitStatus
	parseStatus(&s, []byte(out))
	if s.Branch != "main" || s.Ahead != 2 || s.Behind != 1 || s.Staged != 3 || s.Unstaged != 2 || s.Untracked != 2 || s.Conflicts != 1 || s.Changed() != 7 {
		t.Errorf("parsed %+v (%d changed)", s, s.Changed())
	}
	var d GitStatus
	parseStatus(&d, []byte("# branch.oid 0123456789abcdef\n# branch.head (detached)\n"))
	if !d.Detached || d.Branch != "0123456" {
		t.Errorf("detached parsed as %+v", d)
	}
}

func TestReadHeadAndFindRepo(t *testing.T) {
	root := tree(t, map[string]string{".git/HEAD": "ref: refs/heads/feature/x\n", "sub/deep/": ""})
	if got, ok := FindRepo(filepath.Join(root, "sub", "deep")); !ok || got != root {
		t.Errorf("found the repository at %q", got)
	}
	if b, detached := readHead(root); b != "feature/x" || detached {
		t.Errorf("HEAD read as %q", b)
	}
	wt := tree(t, map[string]string{".git": "gitdir: " + filepath.Join(root, ".git") + "\n"})
	if b, _ := readHead(wt); b != "feature/x" {
		t.Errorf("a worktree's HEAD read as %q", b)
	}
}

func TestDrivesHoldTheSystemDrive(t *testing.T) {
	ds := Drives()
	if runtime.GOOS != "windows" {
		if len(ds) != 0 {
			t.Errorf("drives listed off Windows: %v", ds)
		}
		return
	}
	sys := filepath.VolumeName(os.Getenv("SystemRoot"))
	for _, d := range ds {
		if d.Name == sys {
			if d.Total <= 0 || d.Free <= 0 || d.Free > d.Total || d.Path != sys+`\` {
				t.Errorf("the system drive reads as %+v", d)
			}
			return
		}
	}
	t.Errorf("%s is not among %v", sys, ds)
}
