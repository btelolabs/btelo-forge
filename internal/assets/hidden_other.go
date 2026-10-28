//go:build !windows

package assets

import "os"

// hiddenFile reports whether the system hides the file; elsewhere than Windows that is its leading dot, already
// looked at by name.
func hiddenFile(string, os.FileInfo) bool { return false }
