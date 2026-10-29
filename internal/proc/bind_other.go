//go:build !windows

package proc

import "os"

// Bind would tie a helper program to this one; elsewhere than on Windows the helper is ended when it is stopped.
func Bind(*os.Process) error { return nil }
