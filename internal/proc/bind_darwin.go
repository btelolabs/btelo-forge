//go:build darwin

package proc

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// macOS has no kernel job objects or PR_SET_PDEATHSIG, so Bind runs a single idle /bin/sh watchdog whose stdin is a
// pipe held open only by this process. Whenever this process ends — even on SIGKILL or a crash — the kernel closes
// the pipe, the watchdog wakes on EOF, and SIGKILLs every still-bound helper PID and its children.
var binder struct {
	mu   sync.Mutex
	pipe *os.File
	err  error
	once sync.Once
}

const watchdogScript = `pids=" "
while IFS= read -r line; do
  case "$line" in
    +*) pids="${pids}${line#+} " ;;
    -*) p="${line#-}"; pids="${pids%% $p *} ${pids#* $p }" ;;
  esac
done
for p in $pids; do
  kill -9 "$p" 2>/dev/null
done`

func startWatchdog() {
	r, w, err := os.Pipe()
	if err != nil {
		binder.err = err
		return
	}
	cmd := exec.Command("/bin/sh", "-c", watchdogScript)
	cmd.Stdin = r
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		binder.err = err
		return
	}
	r.Close()
	binder.pipe = w
	go cmd.Wait()
}

// Bind ties a started helper program to this one so it is terminated if this process exits or crashes unexpectedly.
func Bind(p *os.Process) error {
	if p == nil || p.Pid <= 0 {
		return nil
	}
	binder.once.Do(startWatchdog)
	if binder.err != nil {
		return binder.err
	}
	binder.mu.Lock()
	defer binder.mu.Unlock()
	if binder.pipe == nil {
		return nil
	}
	_, err := fmt.Fprintf(binder.pipe, "+%d\n", p.Pid)
	return err
}

// unbind removes a helper that has exited cleanly from the watchdog so a later reused PID is never killed.
func unbind(pid int) {
	if pid <= 0 {
		return
	}
	binder.mu.Lock()
	defer binder.mu.Unlock()
	if binder.pipe != nil {
		_, _ = fmt.Fprintf(binder.pipe, "-%d\n", pid)
	}
}
