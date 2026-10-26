//go:build !windows

// Package proc starts helper programs without flashing a console window.
package proc

import "os/exec"

// Hide does nothing where helper programs never open a window.
func Hide(*exec.Cmd) {}
