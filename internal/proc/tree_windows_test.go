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

// TestBindEndsTheHelperWithItsParent starts a parent that binds a helper to itself and is then killed; the helper ends
// with it.
func TestBindEndsTheHelperWithItsParent(t *testing.T) {
	if os.Getenv("PROC_BIND_PARENT") == "1" {
		helper := exec.Command("ping", "-n", "60", "127.0.0.1")
		Hide(helper)
		if err := helper.Start(); err != nil {
			os.Exit(2)
		}
		if err := Bind(helper.Process); err != nil {
			helper.Process.Kill()
			os.Exit(3)
		}
		os.Stdout.WriteString(strconv.Itoa(helper.Process.Pid) + "\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	parent := exec.Command(os.Args[0], "-test.run=^TestBindEndsTheHelperWithItsParent$")
	parent.Env = append(os.Environ(), "PROC_BIND_PARENT=1")
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 32)
	n, _ := out.Read(line)
	pid, err := strconv.Atoi(strings.TrimSpace(string(line[:n])))
	if err != nil {
		parent.Process.Kill()
		t.Fatalf("the parent did not start its helper: %q", line[:n])
	}
	if !alive(uint32(pid)) {
		t.Fatal("the helper is not running")
	}
	parent.Process.Kill() // not KillTree: the helper must end because its parent did
	parent.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for alive(uint32(pid)) {
		if time.Now().After(deadline) {
			exec.Command("taskkill", "/f", "/pid", strconv.Itoa(pid)).Run()
			t.Fatal("the bound helper outlived its parent")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
