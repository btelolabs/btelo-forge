//go:build !darwin

package files

import (
	"context"
	"io/fs"
)

func tryCloneFile(context.Context, string, string, fs.FileInfo, *Progress) (bool, error) {
	return false, nil
}
