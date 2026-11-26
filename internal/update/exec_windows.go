//go:build windows

package update

import "io/fs"

// Windows does not represent Unix execute bits on extensionless files such as macOS bundle binaries during cross-OS tests.
func isExecutable(fs.FileInfo) bool { return true }
