//go:build darwin

package proc

import (
	"os"
	"syscall"
)

// KillTree ends a helper program and every descendant process it started.
func KillTree(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	tree := descendants(p.Pid)
	pgid, pgErr := syscall.Getpgid(p.Pid)
	err := p.Kill()
	if pgErr == nil && pgid == p.Pid && pgid != syscall.Getpgrp() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	for i := len(tree) - 1; i >= 0; i-- {
		_ = syscall.Kill(tree[i], syscall.SIGKILL)
	}
	return err
}
