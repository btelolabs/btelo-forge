package proc

import (
	"math/rand"
	"os"
	"os/exec"
	"testing"
	"time"
)

// sink keeps the helper's loop from being optimised away.
var sink int

// TestMain lets the test run itself as the program to measure: one that holds memory and keeps a processor busy.
func TestMain(m *testing.M) {
	if os.Getenv("PROC_TEST_SLEEP") == "1" { // a program that just stays, for the registry's tests
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	if os.Getenv("PROC_TEST_BUSY") == "1" {
		// The pages are touched again and again, as a model's weights are while it answers: Windows takes pages
		// that are left alone out of the working set.
		hold := make([]byte, 200<<20)
		rand.Read(hold)
		for {
			for i := 0; i < len(hold); i += 4096 {
				hold[i] += byte(sink) + 1
				sink += int(hold[i])
			}
		}
	}
	os.Exit(m.Run())
}

func TestMeterMeasuresAProgramAndForgetsOneThatEnded(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "PROC_TEST_BUSY=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := false
	defer func() {
		if !ended {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	m := NewMeter()
	defer m.Close()
	m.Sample(cmd.Process.Pid)
	var u Usage
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(700 * time.Millisecond)
		got, ok := m.Sample(cmd.Process.Pid)[cmd.Process.Pid]
		if !ok {
			t.Fatal("a running program is not measured")
		}
		if u = got; u.Memory >= 150<<20 && u.CPU > 0 {
			break
		}
	}
	if u.Memory < 150<<20 {
		t.Fatalf("memory %d MB, want at least 150", u.Memory>>20)
	}
	if u.CPU <= 0 || u.CPU > 100 {
		t.Fatalf("cpu %.2f%%, want above 0 and at most 100", u.CPU)
	}
	t.Logf("cpu %.1f%%, memory %d MB, gpu known %v", u.CPU, u.Memory>>20, u.GPUKnown)
	cmd.Process.Kill()
	cmd.Wait()
	ended = true
	if _, ok := m.Sample(cmd.Process.Pid)[cmd.Process.Pid]; ok {
		t.Fatal("a program that ended is still measured")
	}
}
