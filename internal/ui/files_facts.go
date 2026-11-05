package ui

import (
	"context"
	"path/filepath"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/files"
)

// Nothing the file manager draws reads the disk on the drawing goroutine. What it shows of a path it holds no
// listing for (a file a search found, the one a picture is asked for), the folder a preview lists, the drives and the
// repository a folder is in are asked for while drawing and answered from what was read last; what was asked for and
// is not known, or was read longer ago than its cache allows, is read after the frame in the background, at most
// factWorkers reads at a time, and the answer draws the window again. A drive that is slow to answer, a network share
// or a disc spinning up, so never holds a frame: what it holds only shows late.

const (
	factWorkers = 4
	factBatch   = 256 // keys one worker reads before it answers
	factFresh   = 2 * time.Second
	driveFresh  = 5 * time.Second
	// factKeep is how many frames a fact no frame asked for is kept.
	factKeep = 600
)

// fileFacts are the caches of what was read in the background.
type fileFacts struct {
	stats  lookups[pathFact]      // by path
	lists  lookups[folderListing] // by folder, for the preview of a folder
	drives lookups[[]files.Drive] // the one key ""
	repos  lookups[string]        // a folder's repository root, "" for none
	busy   int                    // reads running
	frame  uint64
	ready  bool
}

// pathFact is what reading one path found.
type pathFact struct {
	files.Entry
	Err error
}

// folderListing is a folder's entries, folders first and in natural order, as its preview lists them.
type folderListing struct {
	entries []files.Entry
	err     error
}

// lookups is one cache: what was read of each key, and what this frame asked for that is due to be read.
type lookups[T any] struct {
	read   func(ctx context.Context, key string) T // runs in the background
	fresh  time.Duration
	items  map[string]*lookup[T]
	wanted []*lookup[T]
}

type lookup[T any] struct {
	key     string
	val     T
	known   bool
	reading bool
	at      time.Time // when it was read; zero once it is known to be out of date
	used    uint64    // the last frame it was asked for
}

// facts are the caches, made on first use.
func (a *App) facts() *fileFacts {
	f := &a.fileFacts
	if !f.ready {
		f.ready = true
		f.stats = lookups[pathFact]{fresh: factFresh, read: func(_ context.Context, path string) pathFact {
			e, err := files.Stat(path)
			return pathFact{Entry: e, Err: err}
		}}
		f.lists = lookups[folderListing]{fresh: factFresh, read: func(_ context.Context, dir string) folderListing {
			entries, err := files.List(dir)
			slices.SortFunc(entries, func(x, y files.Entry) int {
				if x.Dir != y.Dir {
					if x.Dir {
						return -1
					}
					return 1
				}
				return naturalCompare(x.Name, y.Name)
			})
			return folderListing{entries, err}
		}}
		f.drives = lookups[[]files.Drive]{fresh: driveFresh, read: func(context.Context, string) []files.Drive { return files.Drives() }}
		f.repos = lookups[string]{fresh: factFresh, read: func(_ context.Context, dir string) string {
			root, _ := files.FindRepo(dir)
			return root
		}}
	}
	return f
}

// statOf is what is known of the file or folder at path, a link as what it points to; known is false until it has
// been read once.
func (a *App) statOf(path string) (pathFact, bool) {
	f := a.facts()
	return f.stats.get(path, f.frame)
}

// listingOf is the folder's entries as last read; known is false until it has been read once.
func (a *App) listingOf(dir string) (folderListing, bool) {
	f := a.facts()
	return f.lists.get(dir, f.frame)
}

// knownDrives are the drives as last read: none until they have been read, which starts with the app.
func (a *App) knownDrives() []files.Drive {
	f := a.facts()
	d, _ := f.drives.get("", f.frame)
	return d
}

// repoRoot is the root of the git repository dir is in, "" for none; known is false until it has been looked for.
func (a *App) repoRoot(dir string) (string, bool) {
	f := a.facts()
	return f.repos.get(dir, f.frame)
}

// forgetFacts marks as out of date what was read of dirs and of what is in them, or of everything when dirs is nil,
// as an operation or another program changed them. What was read stays shown until it is read again.
func (a *App) forgetFacts(dirs []string) {
	f := a.facts()
	in := func(path string) bool {
		return dirs == nil || slices.ContainsFunc(dirs, func(d string) bool {
			return files.SamePath(d, path) || files.SamePath(d, filepath.Dir(path))
		})
	}
	f.stats.stale(in)
	f.lists.stale(in)
	if dirs == nil {
		f.drives.stale(func(string) bool { return true })
		f.repos.stale(func(string) bool { return true })
	}
}

// pumpFacts starts reading what this frame asked for and does not know, and lets go of what no frame asked for in a
// long while; it runs once a frame, after everything is drawn.
func (a *App) pumpFacts() {
	f := a.facts()
	pumpLookups(a, f, &f.drives)
	pumpLookups(a, f, &f.stats)
	pumpLookups(a, f, &f.lists)
	pumpLookups(a, f, &f.repos)
	f.frame++
}

// factsBusy reports whether reads run, whose answers are still to be drawn.
func (a *App) factsBusy() bool { return a.fileFacts.busy > 0 }

// get is what is known of key, noting it as asked for in this frame and wanted when it is not known or out of date.
func (c *lookups[T]) get(key string, frame uint64) (T, bool) {
	if c.items == nil {
		c.items = map[string]*lookup[T]{}
	}
	l := c.items[key]
	if l == nil {
		l = &lookup[T]{key: key, used: frame}
		c.items[key] = l
		c.wanted = append(c.wanted, l)
		return l.val, false
	}
	if l.used != frame {
		l.used = frame
		if !l.reading && (!l.known || time.Since(l.at) > c.fresh) {
			c.wanted = append(c.wanted, l)
		}
	}
	return l.val, l.known
}

// stale marks the keys that match as out of date.
func (c *lookups[T]) stale(match func(key string) bool) {
	for k, l := range c.items {
		if match(k) {
			l.at = time.Time{}
		}
	}
}

// pumpLookups reads, in batches, what c was asked for in this frame, as far as the workers allow; what is left is
// asked for again by the next frame that draws it, which the answers bring.
func pumpLookups[T any](a *App, f *fileFacts, c *lookups[T]) {
	for len(c.wanted) > 0 && f.busy < factWorkers {
		n := min(len(c.wanted), factBatch)
		var batch []*lookup[T]
		for _, l := range c.wanted[:n] {
			if !l.reading && c.items[l.key] == l {
				l.reading = true
				batch = append(batch, l)
			}
		}
		c.wanted = c.wanted[n:]
		if len(batch) == 0 {
			continue
		}
		keys := make([]string, len(batch))
		for i, l := range batch {
			keys[i] = l.key
		}
		f.busy++
		read := c.read
		a.background(func(ctx context.Context) {
			vals := make([]T, len(keys))
			for i, k := range keys {
				if ctx.Err() != nil {
					return
				}
				vals[i] = read(ctx, k)
			}
			a.post(func() {
				f.busy--
				now := time.Now()
				for i, l := range batch {
					l.reading = false
					l.val, l.known, l.at = vals[i], true, now
				}
			})
		})
	}
	c.wanted = c.wanted[:0]
	if f.frame%factKeep == 0 {
		for k, l := range c.items {
			if !l.reading && l.used+factKeep < f.frame {
				delete(c.items, k)
			}
		}
	}
}

// loadAhead is something the app keeps on disk and reads once, such as the folders visited or the tags: it is read in
// the background as the app starts, so the first frame that needs it finds it read. A frame that needs it before
// the read is done waits for that read rather than reading again; an App made without New reads it there and then.
type loadAhead[T any] struct {
	done chan struct{}
	val  T
}

// start reads it in the background.
func (l *loadAhead[T]) start(a *App, load func() T) {
	l.done = make(chan struct{})
	a.background(func(context.Context) {
		defer close(l.done)
		l.val = load()
	})
}

// get is what was read; load reads it when it was never started.
func (l *loadAhead[T]) get(load func() T) T {
	if l.done == nil {
		l.val, l.done = load(), make(chan struct{})
		close(l.done)
	}
	<-l.done
	return l.val
}
