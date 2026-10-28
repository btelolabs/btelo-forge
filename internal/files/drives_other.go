//go:build !windows

package files

// Drives is empty where every disk is a folder under /, which is the top.
func Drives() []Drive { return nil }
