//go:build !windows

package update

import "io/fs"

func isExecutable(st fs.FileInfo) bool {
	return st.Mode().Perm()&0o111 != 0
}
