//go:build darwin

package keyboard

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  --new   -v ", []string{"--new", "-v"}},
		{`--dir "/Users/me/My Docs" 'two words'`, []string{"--dir", "/Users/me/My Docs", "two words"}},
	}
	for _, tc := range cases {
		if got := splitArgs(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("splitArgs(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestOpenPlan(t *testing.T) {
	// URLs open directly via `open`.
	if prog, argv, det := openPlan("https://example.com", ""); prog != "open" || !slices.Equal(argv, []string{"https://example.com"}) || det {
		t.Errorf("url: %q %v %v", prog, argv, det)
	}
	// Bare application names use `open -a`.
	if prog, argv, det := openPlan("Safari", "--new"); prog != "open" || !slices.Equal(argv, []string{"-a", "Safari", "--args", "--new"}) || det {
		t.Errorf("app name: %q %v %v", prog, argv, det)
	}
	// Existing file opens directly via `open`.
	dir := t.TempDir()
	doc := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(doc, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if prog, argv, det := openPlan(doc, ""); prog != "open" || !slices.Equal(argv, []string{doc}) || det {
		t.Errorf("doc: %q %v %v", prog, argv, det)
	}
	// Existing executable with args runs detached so CLI arguments reach it.
	exe := filepath.Join(dir, "tool.sh")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if prog, argv, det := openPlan(exe, `--flag "a b"`); prog != exe || !slices.Equal(argv, []string{"--flag", "a b"}) || !det {
		t.Errorf("cli exe: %q %v %v", prog, argv, det)
	}
}

func TestMacAdapterBasics(t *testing.T) {
	a := NewAdapter()
	if a.CanRemap(vkCapsLock) {
		t.Error("CapsLock should not be remappable via CGEventTap on macOS")
	}
	if !a.CanRemap('A') || !a.CanRemap(0x2d) || !a.CanRemap(0x90) {
		t.Error("regular keys should be remappable")
	}
	// Calling Stop on an unstarted adapter must be a safe no-op.
	a.Stop()
}

func TestSystemMapDarwinKindAndRead(t *testing.T) {
	m := NewSystemMap()
	if m.Kind() != HIDMap {
		t.Fatalf("Kind() = %v, want HIDMap", m.Kind())
	}
	// Read() must succeed even when no LaunchAgent plist exists.
	if _, err := m.Read(); err != nil {
		t.Fatalf("Read() failed: %v", err)
	}
}
