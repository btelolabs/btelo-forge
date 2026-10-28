package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

// Progress is how far a copy, move or deletion has come. Total and Done count bytes for a copy and items for the
// rest; it is written by the work and read from any goroutine.
type Progress struct {
	Total, Done atomic.Int64
	Counting    atomic.Bool // the work is still finding out how much there is to do
	current     atomic.Pointer[string]
}

// Current is the file being worked on.
func (p *Progress) Current() string {
	if s := p.current.Load(); s != nil {
		return *s
	}
	return ""
}

func (p *Progress) at(path string) { p.current.Store(&path) }

// Fraction is how much is done, from 0 to 1, or -1 while that is not known.
func (p *Progress) Fraction() float64 {
	total := p.Total.Load()
	if p.Counting.Load() || total <= 0 {
		return -1
	}
	return min(float64(p.Done.Load())/float64(total), 1)
}

// Pair is a file and where it went.
type Pair struct{ From, To string }

// Failure is a file that could not be handled, and why.
type Failure struct {
	Path string
	Err  error
}

// Result is what a copy, move or deletion did: the items done, and those that failed.
type Result struct {
	Pairs  []Pair // for a copy or move: each item and where it is now; for a deletion: the items deleted, in From
	Failed []Failure
}

// Err is the first failure, or nil.
func (r Result) Err() error {
	if len(r.Failed) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %w", r.Failed[0].Path, r.Failed[0].Err)
}

// ErrIntoItself is a folder being copied or moved into itself.
var ErrIntoItself = errors.New("a folder cannot be copied or moved into itself")

// count adds up the bytes (or with items, the items) under paths, without following links.
func count(ctx context.Context, paths []string, items bool) int64 {
	var n int64
	for _, p := range paths {
		filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil
			}
			if items {
				n++
			} else if d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					n += info.Size()
				}
			}
			return nil
		})
	}
	return n
}

// Remove deletes paths for good, folders with everything in them, reading and deleting several folders at once.
// Links are deleted, never what they point to. Items that fail are reported and the rest are still deleted. When ctx
// ends it stops, leaving what was not deleted yet.
func Remove(ctx context.Context, paths []string, p *Progress) Result {
	if p.Total.Load() == 0 {
		p.Counting.Store(true)
		p.Total.Store(count(ctx, paths, true))
		p.Counting.Store(false)
	}
	r := &remover{ctx: ctx, p: p, sem: make(chan struct{}, 2*runtime.GOMAXPROCS(0))}
	var res Result
	for _, path := range paths {
		if ctx.Err() != nil {
			break
		}
		before := len(r.failed)
		r.remove(path)
		if len(r.failed) == before && ctx.Err() == nil {
			res.Pairs = append(res.Pairs, Pair{From: path})
		}
	}
	res.Failed = r.failed
	return res
}

type remover struct {
	ctx    context.Context
	p      *Progress
	sem    chan struct{}
	mu     sync.Mutex
	failed []Failure
}

func (r *remover) fail(path string, err error) {
	r.mu.Lock()
	r.failed = append(r.failed, Failure{path, err})
	r.mu.Unlock()
}

// remove deletes one item; a folder's folders are deleted on other goroutines while there are some to spare.
func (r *remover) remove(path string) {
	if r.ctx.Err() != nil {
		return
	}
	info, err := os.Lstat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			r.fail(path, err)
		}
		return
	}
	r.p.at(path)
	if realDir(info.Mode()) {
		des, err := os.ReadDir(path)
		if err != nil && len(des) == 0 {
			r.fail(path, err)
			return
		}
		var wg sync.WaitGroup
		for _, de := range des {
			child := filepath.Join(path, de.Name())
			if realDir(de.Type()) {
				select {
				case r.sem <- struct{}{}:
					wg.Go(func() {
						defer func() { <-r.sem }()
						r.remove(child)
					})
					continue
				default:
				}
			}
			r.remove(child)
		}
		wg.Wait()
		if r.ctx.Err() != nil {
			return
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		r.fail(path, err)
		return
	}
	r.p.Done.Add(1)
}

// Copy copies srcs into the folder dst. An item whose name is taken there gets a free one, as Explorer names
// copies. When ctx ends, the item being copied is taken away again and the rest are not copied.
func Copy(ctx context.Context, srcs []string, dst string, p *Progress) Result {
	p.Counting.Store(true)
	p.Total.Store(count(ctx, srcs, false))
	p.Counting.Store(false)
	var res Result
	for _, src := range srcs {
		if ctx.Err() != nil {
			break
		}
		if Within(dst, src) {
			res.Failed = append(res.Failed, Failure{src, ErrIntoItself})
			continue
		}
		to := filepath.Join(dst, UniqueName(dst, filepath.Base(src)))
		if err := copyItem(ctx, src, to, p); err != nil {
			if !errors.Is(err, fs.ErrExist) { // what was made of the copy, never a file that came there meanwhile
				os.RemoveAll(to)
			}
			if ctx.Err() == nil {
				res.Failed = append(res.Failed, Failure{src, err})
			}
			continue
		}
		res.Pairs = append(res.Pairs, Pair{src, to})
	}
	return res
}

// Move moves srcs into the folder dst: renamed when they are on the same drive, otherwise copied and then deleted.
// An item already in dst is left where it is; one whose name is taken there gets a free one.
func Move(ctx context.Context, srcs []string, dst string, p *Progress) Result {
	var res Result
	var across []string
	p.Total.Store(int64(len(srcs)))
	for _, src := range srcs {
		if ctx.Err() != nil {
			return res
		}
		if SamePath(filepath.Dir(src), dst) {
			p.Done.Add(1)
			continue
		}
		if Within(dst, src) {
			res.Failed = append(res.Failed, Failure{src, ErrIntoItself})
			continue
		}
		to := filepath.Join(dst, UniqueName(dst, filepath.Base(src)))
		p.at(src)
		if err := os.Rename(src, to); err == nil {
			res.Pairs = append(res.Pairs, Pair{src, to})
			p.Done.Add(1)
			continue
		}
		across = append(across, src)
	}
	if len(across) == 0 {
		return res
	}
	// The rest are on another drive: copied, and deleted once each copy is whole.
	p.Done.Store(0)
	copied := Copy(ctx, across, dst, p)
	res.Failed = append(res.Failed, copied.Failed...)
	for _, pair := range copied.Pairs {
		gone := Remove(context.WithoutCancel(ctx), []string{pair.From}, &Progress{})
		if err := gone.Err(); err != nil {
			res.Failed = append(res.Failed, Failure{pair.From, fmt.Errorf("copied to %s, but the original could not be deleted: %w", pair.To, err)})
		}
		res.Pairs = append(res.Pairs, pair)
	}
	return res
}

// Rename gives a file a new name in its folder; it refuses a name that is already taken.
func Rename(path, name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return "", fmt.Errorf("%q is not a file name", name)
	}
	to := filepath.Join(filepath.Dir(path), name)
	if SamePath(to, path) && filepath.Base(path) == name {
		return path, nil
	}
	if _, err := os.Lstat(to); err == nil && !SamePath(to, path) {
		return "", fmt.Errorf("%s already exists", name)
	}
	return to, os.Rename(path, to)
}

// copyItem copies a file, link or folder with everything in it.
func copyItem(ctx context.Context, src, dst string, p *Progress) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	p.at(src)
	switch {
	case isLink(info.Mode()):
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()|0o700); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, de := range des {
			if err := copyItem(ctx, filepath.Join(src, de.Name()), filepath.Join(dst, de.Name()), p); err != nil {
				return err
			}
		}
		return os.Chtimes(dst, info.ModTime(), info.ModTime())
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", src)
	}
	return copyFile(ctx, src, dst, info, p)
}

// copyBuffer is the size of the pieces a file is copied in; progress moves, and a cancel is noticed, between them.
const copyBuffer = 1 << 20

func copyFile(ctx context.Context, src, dst string, info fs.FileInfo, p *Progress) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	buf := make([]byte, copyBuffer)
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return err
			}
			p.Done.Add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}
