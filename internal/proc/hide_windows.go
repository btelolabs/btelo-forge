// Package proc starts helper programs without flashing a console window.
package proc

import (
	"os/exec"
	"syscall"
)

// Hide keeps the command's console window from appearing.
func Hide(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
