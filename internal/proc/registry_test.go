package proc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// sleeper starts the test program as one that stays, outside the registry, as a program an earlier run left behind.
func sleeper(t *testing.T) (*exec.Cmd, Entry) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "PROC_TEST_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	exe, _ := os.Executable()
	return cmd, Entry{Pid: cmd.Process.Pid, Program: exe, Label: "model:test", Started: time.Now()}
}

func writeRegistry(t *testing.T, path string, entries ...Entry) {
	t.Helper()
	data, _ := json.Marshal(entries)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRegistry(t *testing.T, path string) []Entry {
	t.Helper()
	var out []Entry
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestAStartedProgramIsKeptInTheRegistryWhileItRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	SetRegistry(path)
	t.Cleanup(func() { SetRegistry("") })
	exe, _ := os.Executable()
	s, err := StartAs("model:x", exe, func(port int) ([]string, error) { return []string{"-test.run=NONE"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	// The program ends by itself at once (it only runs no test), and is forgotten.
	select {
	case <-s.Exited:
	case <-time.After(20 * time.Second):
		t.Fatal("the program did not end")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(readRegistry(t, path)) != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := readRegistry(t, path); len(got) != 0 {
		t.Fatalf("an ended program is still registered: %+v", got)
	}
}

func TestARunningProgramIsRegisteredAndStopForgetsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	SetRegistry(path)
	t.Cleanup(func() { SetRegistry("") })
	exe, _ := os.Executable()
	t.Setenv("PROC_TEST_SLEEP", "1")
	s, err := StartAs("model:y", exe, func(port int) ([]string, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	got := readRegistry(t, path)
	if len(got) != 1 || got[0].Pid != s.Pid() || got[0].Label != "model:y" || got[0].Program != exe {
		t.Fatalf("registry %+v, pid %d", got, s.Pid())
	}
	s.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for len(readRegistry(t, path)) != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := readRegistry(t, path); len(got) != 0 {
		t.Fatalf("a stopped program is still registered: %+v", got)
	}
}

func TestAProgramAnEarlierRunLeftIsFoundAndCanBeEnded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	cmd, left := sleeper(t)
	_, gone := sleeper(t)
	gone.Pid = 0x7ffffff0 // a process that does not exist
	exe, _ := os.Executable()
	reused := Entry{Pid: cmd.Process.Pid, Program: filepath.Join(filepath.Dir(exe), "other.exe"), Started: time.Now()}
	stale := left
	stale.Started = time.Now().Add(-time.Hour) // the id now belongs to a process begun long after
	writeRegistry(t, path, left, gone, reused, stale)

	found := SetRegistry(path)
	t.Cleanup(func() { SetRegistry("") })
	if len(found) != 1 || found[0].Pid != left.Pid || found[0].Label != "model:test" {
		t.Fatalf("found %+v, want only pid %d", found, left.Pid)
	}
	if got := readRegistry(t, path); len(got) != 1 {
		t.Fatalf("the file still names what is gone: %+v", got)
	}
	if got := Orphans(); len(got) != 1 {
		t.Fatalf("orphans %+v", got)
	}
	if err := KillOrphan(left.Pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the program left running was not ended")
	}
	if len(Orphans()) != 0 || len(readRegistry(t, path)) != 0 {
		t.Fatalf("an ended program is still named: %+v / %+v", Orphans(), readRegistry(t, path))
	}
	if err := KillOrphan(left.Pid); err == nil {
		t.Fatal("a program was ended twice")
	}
	if err := KillOrphan(os.Getpid()); err == nil {
		t.Fatal("a program that is no orphan was ended")
	}
}

func TestAnOrphanThatEndsOnItsOwnDisappears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	cmd, left := sleeper(t)
	writeRegistry(t, path, left)
	if found := SetRegistry(path); len(found) != 1 {
		t.Fatalf("found %+v", found)
	}
	t.Cleanup(func() { SetRegistry("") })
	cmd.Process.Kill()
	cmd.Wait()
	if got := Orphans(); len(got) != 0 {
		t.Fatalf("orphans %+v", got)
	}
}
