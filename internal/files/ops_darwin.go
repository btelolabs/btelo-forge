//go:build darwin

package files

import (
	"context"
	"errors"
	"io/fs"

	"golang.org/x/sys/unix"
)

// tryCloneFile attempts an instant O(1) Copy-on-Write APFS clone via Darwin clonefile(2).
// Returns (true, nil) when cloned, (false, err) on cancellation or destination collision, or (false, nil) when
// cloning is not supported across volumes/filesystems and copyFile should fall back to streaming.
func tryCloneFile(ctx context.Context, src, dst string, info fs.FileInfo, p *Progress) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
	if err == nil {
		p.Done.Add(info.Size())
		return true, nil
	}
	if errors.Is(err, unix.EEXIST) {
		return false, fs.ErrExist
	}
	return false, nil
}
