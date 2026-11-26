//go:build darwin

package proc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestKillTreeEndsWhatTheHelperStarted starts a shell launcher that spawns a grandchild sleeping process;
// KillTree must terminate both the launcher and its descendants.
func TestKillTreeEndsWhatTheHelperStarted(t *testing.T) {
	launcher := exec.Command("/bin/sh", "-c", "/bin/sh -c 'sleep 60 & wait' & wait")
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	var tree []int
	deadline := time.Now().Add(10 * time.Second)
	for len(tree) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		tree = descendants(launcher.Process.Pid)
	}
	if len(tree) < 2 {
		launcher.Process.Kill()
		launcher.Wait()
		t.Fatalf("the launcher's descendants were not found: %v", tree)
	}
	if err := KillTree(launcher.Process); err != nil {
		t.Fatal(err)
	}
	launcher.Wait()
	deadline = time.Now().Add(5 * time.Second)
	for _, pid := range tree {
		for Alive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("descendant process %d is still running", pid)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	if err := KillTree(nil); err != os.ErrProcessDone {
		t.Errorf("KillTree(nil) = %v", err)
	}
}

// TestZombieProcessIsNotAlive verifies that an exited, unreaped child (zombie / SZOMB) is reported as not alive.
// This directly guards against baseline failure TestAModelAnEarlierRunLeftRunningIsShownAndCanBeStopped.
func TestZombieProcessIsNotAlive(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	pid := cmd.Process.Pid
	if !Alive(pid) {
		t.Fatal("started process is not alive")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// Do NOT call cmd.Wait() yet — process is now an unreaped zombie (SZOMB) in our child table.
	deadline := time.Now().Add(5 * time.Second)
	for Alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("unreaped zombie process %d is still reported as Alive", pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestBindEndsTheHelperWithItsParent starts a parent that binds a helper and is then killed with SIGKILL;
// the bound helper must be terminated automatically.
func TestBindEndsTheHelperWithItsParent(t *testing.T) {
	if os.Getenv("PROC_BIND_PARENT") == "1" {
		helper := exec.Command("/bin/sleep", "60")
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
		parent.Wait()
		t.Fatalf("the parent did not start its helper: %q", line[:n])
	}
	if !Alive(pid) {
		parent.Process.Kill()
		parent.Wait()
		t.Fatal("the helper is not running")
	}
	parent.Process.Kill() // SIGKILL: watchdog must terminate the bound helper
	parent.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for Alive(pid) {
		if time.Now().After(deadline) {
			_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
			t.Fatal("the bound helper outlived its killed parent")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func BenchmarkMeterSample(b *testing.B) {
	m := NewMeter()
	defer m.Close()
	pid := os.Getpid()
	m.Sample(pid)
	b.ResetTimer()
	for range b.N {
		m.Sample(pid)
	}
}
