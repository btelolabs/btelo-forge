package keyboard

import (
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestRegArgumentsWriteTheValue has reg.exe carry out the very arguments, under a key of the current user: the real
// value is left alone, as only the user may change it.
func TestRegArgumentsWriteTheValue(t *testing.T) {
	const sub = `Software\BteloForgeTest`
	defer registry.DeleteKey(registry.CURRENT_USER, sub)
	table := []Mapping{{0x3a, 0x01}, {0xe038, 0x1d}, {0x46, 0}}
	run := func(m []Mapping) {
		t.Helper()
		line := "reg.exe " + regArguments(`HKCU\`+sub, m)
		cmd := exec.Command("reg.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line} // as ShellExecute passes them, unquoted again
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", line, err, strings.TrimSpace(string(out)))
		}
	}
	run(table)
	key, err := registry.OpenKey(registry.CURRENT_USER, sub, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	data, _, err := key.GetBinaryValue(scancodeName)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeScancodeMap(data)
	if err != nil || !slices.Equal(back, table) {
		t.Fatalf("%v %v", back, err)
	}
	run(nil)
	if _, _, err := key.GetBinaryValue(scancodeName); err == nil {
		t.Fatal("removing left the value")
	}
}

func TestReadTheSystemTable(t *testing.T) {
	if _, err := NewSystemMap().Read(); err != nil {
		t.Fatal(err)
	}
}
