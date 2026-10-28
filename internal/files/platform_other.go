//go:build !windows

package files

import (
	"io/fs"
	"runtime"
	"strings"
)

// hidden reports whether a file is hidden: its name starts with a dot.
func hidden(name string, _ fs.FileInfo) bool { return strings.HasPrefix(name, ".") }

// samePath compares two cleaned paths as the file system usually does: without regard to case on macOS.
func samePath(a, b string) bool {
	if runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
