// Package errlog writes timestamped error logs to a rotating file under the application's state directory.
// All methods are safe for concurrent use and treat a nil or uninitialized Logger as a no-op.
package errlog

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MaxBytes is the default log size threshold (2 MiB) before error.log is rotated to error.log.1.
const MaxBytes int64 = 2 << 20

var defaultLogger atomic.Pointer[Logger]

// SetDefault registers l as the process-wide default logger (nil clears it).
func SetDefault(l *Logger) { defaultLogger.Store(l) }

// Default returns the process-wide default logger, or nil if none has been registered.
func Default() *Logger { return defaultLogger.Load() }

// Error writes an ERROR entry to the default logger, or does nothing if no default logger is set.
func Error(scope, message string, err error) {
	if l := defaultLogger.Load(); l != nil {
		l.Error(scope, message, err)
	}
}

// Warn writes a WARN entry to the default logger, or does nothing if no default logger is set.
func Warn(scope, message string, err error) {
	if l := defaultLogger.Load(); l != nil {
		l.Warn(scope, message, err)
	}
}

// Logger appends timestamped entries to a single rotating log file.
type Logger struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
	now      func() time.Time
}

// Open returns a Logger writing to path with default size rotation (MaxBytes).
// The parent directory and file are created lazily on first write or EnsureFile.
func Open(path string) *Logger {
	return OpenWithLimit(path, MaxBytes)
}

// OpenWithLimit returns a Logger writing to path and rotating to path+".1" once size exceeds maxBytes.
func OpenWithLimit(path string, maxBytes int64) *Logger {
	if path == "" {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = MaxBytes
	}
	return &Logger{
		path:     filepath.Clean(path),
		maxBytes: maxBytes,
		now:      time.Now,
	}
}

// ForPath returns the default logger when it already writes to path (sharing one mutex and file handle across
// cmd/btelo-forge and internal/ui), or opens an independent Logger for path (keeping tests isolated).
func ForPath(path string) *Logger {
	if path == "" {
		return nil
	}
	clean := filepath.Clean(path)
	if d := defaultLogger.Load(); d != nil && d.path == clean {
		return d
	}
	return Open(clean)
}

// Path returns the cleaned log file path, or "" if l is nil.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// EnsureFile creates the log directory and an empty log file if it does not exist yet, returning its path.
func (l *Logger) EnsureFile() (string, error) {
	if l == nil || l.path == "" {
		return "", os.ErrInvalid
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.openLocked(); err != nil {
		return l.path, err
	}
	return l.path, nil
}

// Close closes the underlying log file if open.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Error appends an ERROR entry with an optional underlying raw error.
func (l *Logger) Error(scope, message string, err error) {
	l.Log("ERROR", scope, message, err)
}

// Warn appends a WARN entry with an optional underlying raw error.
func (l *Logger) Warn(scope, message string, err error) {
	l.Log("WARN", scope, message, err)
}

// Info appends an INFO entry.
func (l *Logger) Info(scope, message string) {
	l.Log("INFO", scope, message, nil)
}

// Log writes one structured entry:
//
//	2026-11-26 06:58:01.123 [ERROR] [scope] message | raw: <err>
//
// Continuation lines in message or err are indented by two spaces so every record begins with a timestamp.
func (l *Logger) Log(level, scope, message string, err error) {
	if l == nil || l.path == "" {
		return
	}
	if level == "" {
		level = "ERROR"
	}
	if scope == "" {
		scope = "app"
	}
	msg := strings.TrimSpace(message)
	raw := ""
	if err != nil {
		raw = strings.TrimSpace(err.Error())
	}
	if msg == "" && raw == "" {
		return
	}
	if msg == "" {
		msg, raw = raw, ""
	} else if raw == msg {
		raw = ""
	}

	ts := l.now().Format("2006-01-02 15:04:05.000")
	var b strings.Builder
	b.Grow(len(ts) + len(level) + len(scope) + len(msg) + len(raw) + 24)
	fmt.Fprintf(&b, "%s [%s] [%s] %s", ts, level, scope, formatBody(msg))
	if raw != "" {
		b.WriteString(" | raw: ")
		b.WriteString(formatBody(raw))
	}
	b.WriteByte('\n')

	l.writeEntry([]byte(b.String()))
}

// Writer returns an io.Writer suitable for log.SetOutput(io.MultiWriter(os.Stderr, l.Writer())).
func (l *Logger) Writer() io.Writer {
	if l == nil {
		return io.Discard
	}
	return logWriter{l: l}
}

type logWriter struct{ l *Logger }

func (w logWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(bytes.TrimRight(p, "\r\n")))
	if text != "" {
		w.l.Log("LOG", "stdlog", text, nil)
	}
	return len(p), nil
}

func formatBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\n  ")
}

func (l *Logger) writeEntry(line []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.openLocked(); err != nil {
		return
	}
	if l.size > 0 && l.size+int64(len(line)) > l.maxBytes {
		if err := l.rotateLocked(); err != nil {
			return
		}
	}
	if n, err := l.file.Write(line); err == nil {
		l.size += int64(n)
	}
}

func (l *Logger) openLocked() error {
	if l.file != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	var size int64
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}
	l.file = f
	l.size = size
	return nil
}

func (l *Logger) rotateLocked() error {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	backup := l.path + ".1"
	_ = os.Remove(backup)
	_ = os.Rename(l.path, backup)
	l.size = 0
	return l.openLocked()
}
