package proc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tail keeps the end of what a helper program prints, to explain why it stopped.
type Tail struct {
	mu  sync.Mutex
	buf []byte
}

// Write keeps the last few kilobytes.
func (t *Tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 8<<10 {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-4<<10:]...)
	}
	return len(b), nil
}

// Last is the last few lines printed, without the progress bars.
func (t *Tail) Last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, l := range strings.FieldsFunc(string(t.buf), func(r rune) bool { return r == '\n' || r == '\r' }) {
		if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, "|") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "no output"
	}
	return strings.Join(lines[max(len(lines)-4, 0):], " | ")
}

// FreePort is a TCP port on the loopback address that nothing listens on.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Service is a helper program that answers HTTP on a port of its own: an AI model server. It is started with its
// whole process tree bound to this program, so it never outlives it.
type Service struct {
	Cmd    *exec.Cmd
	Port   int
	Base   string // http://127.0.0.1:port
	Log    *Tail
	Exited chan struct{} // closed when the program ends
}

// Start runs program in its own folder on a free port, with the arguments that args gives for that port.
func Start(program string, args func(port int) ([]string, error)) (*Service, error) {
	return StartAs("", program, args)
}

// StartAs is Start for a program with a label, which says what it runs and is kept with it in the registry (see
// SetRegistry), so that a later run can tell what an earlier one left running.
func StartAs(label, program string, args func(port int) ([]string, error)) (*Service, error) {
	port, err := FreePort()
	if err != nil {
		return nil, err
	}
	argv, err := args(port)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(program, argv...)
	cmd.Dir = filepath.Dir(program)
	Hide(cmd)
	log := &Tail{}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &Service{Cmd: cmd, Port: port, Base: "http://127.0.0.1:" + strconv.Itoa(port), Log: log, Exited: make(chan struct{})}
	if err := Bind(cmd.Process); err != nil {
		s.Stop()
		return nil, err
	}
	pid := cmd.Process.Pid
	register(Entry{Pid: pid, Program: program, Label: label, Started: time.Now()})
	go func() {
		cmd.Wait()
		unregister(pid)
		close(s.Exited)
	}()
	return s, nil
}

// Pid is the program's process id.
func (s *Service) Pid() int {
	if s == nil || s.Cmd == nil || s.Cmd.Process == nil {
		return 0
	}
	return s.Cmd.Process.Pid
}

// Running reports whether the program has not ended.
func (s *Service) Running() bool {
	if s == nil {
		return false
	}
	select {
	case <-s.Exited:
		return false
	default:
		return true
	}
}

// Stop ends the program and everything it started, and waits a few seconds for it to go.
func (s *Service) Stop() {
	if s == nil || s.Cmd == nil || s.Cmd.Process == nil {
		return
	}
	if s.Exited != nil {
		select {
		case <-s.Exited:
			return
		default:
		}
	}
	KillTree(s.Cmd.Process)
	if s.Exited != nil {
		select {
		case <-s.Exited:
		case <-time.After(5 * time.Second):
		}
	}
}

// WaitReady polls ready every quarter second until it reports true, the program ends, ctx is done or limit has
// passed. what names the program in the errors.
func (s *Service) WaitReady(ctx context.Context, limit time.Duration, what string, ready func(context.Context) bool) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for !ready(ctx) {
		select {
		case <-ctx.Done():
			s.Stop()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%s did not load the model within %s", what, limit)
			}
			return ctx.Err()
		case <-s.Exited:
			return fmt.Errorf("%s stopped while loading the model: %s", what, s.Log.Last())
		case <-tick.C:
		}
	}
	return nil
}
