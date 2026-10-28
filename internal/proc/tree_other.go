//go:build !windows

package proc

import "os"

// KillTree ends a helper program. Elsewhere than on Windows the helpers are the programs themselves.
func KillTree(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	return p.Kill()
}
