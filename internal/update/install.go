package update

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The names given beside a replaced file while an update puts the new one in place: the new copy before it moves
// in, and the old one after it moves out, kept until the next start removes it.
const (
	newSuffix = ".update-new"
	oldSuffix = ".update-old"
)

// The folder a release's archive holds the app in, and the program and app bundle inside it.
const (
	archiveFolder = "Btelo Forge"
	windowsExe    = "Btelo Forge.exe"
	macBundle     = "Btelo Forge.app"
	macProgram    = "btelo-forge"
)

// Target is the installed copy of the app an update replaces.
type Target struct {
	GOOS   string
	Exe    string // the running program
	Dir    string // the folder that holds what is replaced, which must be writable
	Launch string // what starts the app: the program, or on macOS its bundle
}

// TargetFor is the installed copy that the program exe, running on goos, belongs to: on Windows the folder it is
// in, as the release's archive unpacks it; on macOS the app bundle it is inside.
func TargetFor(goos, exe string) (Target, error) {
	exe = filepath.Clean(exe)
	switch goos {
	case "windows":
		return Target{GOOS: goos, Exe: exe, Dir: filepath.Dir(exe), Launch: exe}, nil
	case "darwin":
		macos := filepath.Dir(exe)
		contents := filepath.Dir(macos)
		bundle := filepath.Dir(contents)
		if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
			return Target{}, ErrUnsupported
		}
		if strings.Contains(filepath.ToSlash(bundle), "/AppTranslocation/") {
			return Target{}, ErrTranslocated
		}
		return Target{GOOS: goos, Exe: exe, Dir: filepath.Dir(bundle), Launch: bundle}, nil
	}
	return Target{}, ErrUnsupported
}

// Pair is something of an unpacked release, a file or a folder, and what it replaces.
type Pair struct {
	From, To string
}

// Pairs lists what of the release unpacked into staged replaces what of the target: on Windows every file beside
// the program, the program taking the running one's name; on macOS the app bundle.
func (t Target) Pairs(staged string) ([]Pair, error) {
	root := filepath.Join(staged, archiveFolder)
	switch t.GOOS {
	case "windows":
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, ErrNotThisApp
		}
		var pairs []Pair
		found := false
		for _, e := range entries {
			to := filepath.Join(t.Dir, e.Name())
			if e.Name() == windowsExe {
				if !e.Type().IsRegular() {
					return nil, ErrNotThisApp
				}
				to, found = t.Exe, true
			}
			pairs = append(pairs, Pair{From: filepath.Join(root, e.Name()), To: to})
		}
		if !found {
			return nil, ErrNotThisApp
		}
		return pairs, nil
	case "darwin":
		bundle := filepath.Join(root, macBundle)
		if st, err := os.Stat(filepath.Join(bundle, "Contents", "MacOS", macProgram)); err != nil || !st.Mode().IsRegular() {
			return nil, ErrNotThisApp
		}
		return []Pair{{From: bundle, To: t.Launch}}, nil
	}
	return nil, ErrUnsupported
}

// Writable reports ErrReadOnly when no file can be made in dir, as in a folder only an administrator may change.
func Writable(dir string) error {
	f, err := os.CreateTemp(dir, ".btelo-forge-write-*")
	if err != nil {
		return fmt.Errorf("%w: %s", ErrReadOnly, dir)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// Unpack writes the files of a zip archive into dir. Names that would lead out of dir are refused; the files keep
// the permissions the archive gives them, so programs stay runnable.
func Unpack(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		name := path.Clean(strings.ReplaceAll(f.Name, `\`, "/"))
		if name == "." || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || filepath.VolumeName(name) != "" {
			return fmt.Errorf("the archive holds %q, outside its folder", f.Name)
		}
		dest := filepath.Join(dir, filepath.FromSlash(name))
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			if err := unpackLink(f, name, dest); err != nil {
				return err
			}
		case mode.IsRegular():
			if err := unpackFile(f, dest, mode.Perm()|0o600); err != nil {
				return err
			}
		default:
			return fmt.Errorf("the archive holds %q, which is not a file or a folder", f.Name)
		}
	}
	return nil
}

func unpackFile(f *zip.File, dest string, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, src)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// unpackLink makes the symbolic link an archive holds, as an app bundle's frameworks have; it must point within the
// archive.
func unpackLink(f *zip.File, name, dest string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	target, err := io.ReadAll(io.LimitReader(src, 4096))
	src.Close()
	if err != nil {
		return err
	}
	link := string(target)
	if path.IsAbs(link) || strings.HasPrefix(path.Clean(path.Join(path.Dir(name), link)), "../") {
		return fmt.Errorf("the archive's link %q points outside it", f.Name)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.Symlink(filepath.FromSlash(link), dest)
}

// rename is os.Rename, which the tests replace to fail part way.
var rename = os.Rename

// Replace puts each pair's new file or folder in place of the old: all of them are copied in beside what they
// replace first, then each old one is moved aside and the new one moved in. When anything fails, what was done is
// undone, and the installed copy is as it was.
func Replace(pairs []Pair) error {
	for i, p := range pairs {
		os.RemoveAll(p.To + newSuffix)
		if err := copyTree(p.From, p.To+newSuffix); err != nil {
			for _, q := range pairs[:i+1] {
				os.RemoveAll(q.To + newSuffix)
			}
			return err
		}
	}
	moved := make([]bool, len(pairs)) // the old one is aside
	placed := make([]bool, len(pairs))
	undo := func() {
		for i := len(pairs) - 1; i >= 0; i-- {
			p := pairs[i]
			if placed[i] {
				rename(p.To, p.To+newSuffix)
			}
			if moved[i] {
				rename(p.To+oldSuffix, p.To)
			}
		}
		for _, p := range pairs {
			os.RemoveAll(p.To + newSuffix)
		}
	}
	for i, p := range pairs {
		if _, err := os.Lstat(p.To); err == nil {
			os.RemoveAll(p.To + oldSuffix) // left by an earlier update
			if err := rename(p.To, p.To+oldSuffix); err != nil {
				undo()
				return err
			}
			moved[i] = true
		}
		if err := rename(p.To+newSuffix, p.To); err != nil {
			undo()
			return err
		}
		placed[i] = true
	}
	return nil
}

// copyTree copies a file, a link or a folder with everything in it, keeping permissions.
func copyTree(from, to string) error {
	st, err := os.Lstat(from)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		link, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(link, to)
	case st.IsDir():
		if err := os.MkdirAll(to, st.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, src)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// Cleanup removes from dir what updates left there: the copies they replaced, and new ones a failed update did not
// move in. It reports whether nothing is left; a program still running cannot be removed yet.
func Cleanup(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	clean := true
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, oldSuffix) || strings.HasSuffix(n, newSuffix) {
			if os.RemoveAll(filepath.Join(dir, n)) != nil {
				clean = false
			}
		}
	}
	return clean
}

// CleanupSoon removes what updates left in dir, trying again for a while what cannot be removed yet: the program
// an update replaced may still be ending as the new one starts.
func CleanupSoon(ctx context.Context, dir string) {
	for range 60 {
		if Cleanup(dir) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Restart starts the app the target names, apart from this process, which can then end.
func Restart(t Target) error {
	switch t.GOOS {
	case "darwin":
		// A new instance of the bundle, which macOS would otherwise bring forward as the one still ending.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "open", "-n", t.Launch).CombinedOutput(); err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "windows":
		cmd := exec.Command(t.Launch)
		cmd.Dir = t.Dir
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	return ErrUnsupported
}
