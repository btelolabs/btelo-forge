//go:build !darwin

package files

import (
	"os"
	"time"
)

func skipScanDir(_, _, _ string) bool { return false }

func readDirForScan(path string, fn func(name string, isDir, isLink bool, size int64, mod time.Time)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	des, err := f.ReadDir(-1)
	for _, de := range des {
		info, ierr := de.Info()
		if ierr != nil {
			continue
		}
		mode := info.Mode()
		fn(de.Name(), realDir(mode), isLink(mode), info.Size(), info.ModTime())
	}
	return err
}
