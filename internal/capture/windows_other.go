//go:build !windows && !darwin

package capture

func listWindows() []WindowRect { return nil }
