package errlog

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoggerWritesFormattedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "error.log")
	l := Open(path)
	defer l.Close()
	l.now = func() time.Time { return time.Date(2026, 11, 26, 6, 58, 1, 123_000_000, time.UTC) }

	l.Error("record", "Recording could not start: permission required", errors.New("SCStreamErrorDomain code=-3801"))
	l.Warn("export", "identical text", errors.New("identical text"))
	l.Error("ffmpeg", "line one\nline two\r\nline three", nil)
	l.Error("empty", "", nil) // ignored

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := []string{
		"2026-11-26 06:58:01.123 [ERROR] [record] Recording could not start: permission required | raw: SCStreamErrorDomain code=-3801",
		"2026-11-26 06:58:01.123 [WARN] [export] identical text",
		"2026-11-26 06:58:01.123 [ERROR] [ffmpeg] line one",
		"  line two",
		"  line three",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines:\n%s", len(lines), string(data))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d:\n got: %q\nwant: %q", i, lines[i], want[i])
		}
	}
}

func TestLoggerRotatesAtMaxBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "error.log")
	l := OpenWithLimit(path, 200)
	defer l.Close()

	for i := range 10 {
		l.Error("test", fmt.Sprintf("entry-%02d %s", i, strings.Repeat("x", 40)), nil)
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("expected rotated backup %s.1: %v", path, err)
	}
	if !strings.Contains(string(cur), "entry-09") {
		t.Errorf("current log missing latest entry: %s", cur)
	}
	if len(bak) == 0 {
		t.Error("rotated backup log is empty")
	}
}

func TestLoggerWriterAndEnsureFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "error.log")
	l := Open(path)
	defer l.Close()

	got, err := l.EnsureFile()
	if err != nil || got != path {
		t.Fatalf("EnsureFile = %q, %v", got, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created by EnsureFile: %v", err)
	}

	std := log.New(l.Writer(), "", 0)
	std.Printf("stderr bridge message %d", 42)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[LOG] [stdlog] stderr bridge message 42") {
		t.Errorf("Writer output missing: %s", data)
	}
}

func TestLoggerNilSafeAndForPath(t *testing.T) {
	var nilLogger *Logger
	nilLogger.Error("s", "m", errors.New("e"))
	nilLogger.Warn("s", "m", nil)
	nilLogger.Info("s", "m")
	if nilLogger.Path() != "" {
		t.Fatal("nil Path should be empty")
	}
	if err := nilLogger.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := nilLogger.Writer().Write([]byte("ignored\n")); err != nil {
		t.Fatal(err)
	}

	prev := Default()
	t.Cleanup(func() { SetDefault(prev) })
	p1 := filepath.Join(t.TempDir(), "a", "error.log")
	p2 := filepath.Join(t.TempDir(), "b", "error.log")
	l1 := Open(p1)
	defer l1.Close()
	SetDefault(l1)
	if ForPath(p1) != l1 {
		t.Error("ForPath did not reuse default logger for identical path")
	}
	l2 := ForPath(p2)
	defer l2.Close()
	if l2 == l1 || l2.Path() != p2 {
		t.Error("ForPath did not open isolated logger for different path")
	}
}

func TestLoggerConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "error.log")
	l := OpenWithLimit(path, 4096)
	defer l.Close()

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 25 {
				l.Error("conc", fmt.Sprintf("g=%d i=%d", id, i), nil)
			}
		}(g)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatalf("concurrent log read: len=%d err=%v", len(data), err)
	}
}
