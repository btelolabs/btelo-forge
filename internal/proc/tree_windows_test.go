package proc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestKillTreeEndsWhatTheHelperStarted starts a launcher, as a package manager's shim is, that starts a program and
// waits; killing the tree ends both.
func TestKillTreeEndsWhatTheHelperStarted(t *testing.T) {
	// cmd starts a second cmd, which pings for a minute.
	launcher := exec.Command("cmd", "/c", "cmd /c ping -n 60 127.0.0.1 >nul")
	Hide(launcher)
	if err := launcher.Start(); err != nil {
		t.Skip(err)
	}
	var tree []uint32
	deadline := time.Now().Add(10 * time.Second)
	for len(tree) < 2 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		tree = descendants(uint32(launcher.Process.Pid))
	}
	if len(tree) < 2 {
		launcher.Process.Kill()
		t.Fatalf("the launcher's programs were not found: %v", tree)
	}
	if err := KillTree(launcher.Process); err != nil {
		t.Fatal(err)
	}
	launcher.Wait()
	deadline = time.Now().Add(5 * time.Second)
	for _, pid := range tree {
		for alive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("process %d started by the launcher is still running", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if err := KillTree(nil); err != os.ErrProcessDone {
		t.Errorf("KillTree(nil) = %v", err)
	}
}

func alive(pid uint32) bool {
	out, err := exec.Command("tasklist", "/fi", "PID eq "+strconv.Itoa(int(pid)), "/nh").Output()
	return err == nil && strings.Contains(string(out), " "+strconv.Itoa(int(pid))+" ")
}
