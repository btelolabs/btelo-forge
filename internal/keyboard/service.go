package keyboard

import (
	"context"
	"sync"
)

// Service runs the rules on an adapter. It starts switched off, and nothing watches the keyboard until SetActive
// says so: a keyboard hook is what security software looks for, so only the user's own choice may begin one. Even
// then the watch is on only while there is something to do for it.
type Service struct {
	adapter Adapter
	report  func(error) // an action failed; called on the service's own goroutine

	life     sync.Mutex // Apply, SetActive and Close, one at a time
	engine   *Engine    // guarded by mu, which handle takes and the rest of the service never holds across the adapter
	mu       sync.Mutex
	rules    []Rule
	active   bool
	watching bool
	err      error

	work   chan func() error
	cancel context.CancelFunc
	done   chan struct{}
}

// NewService prepares a service. report hears of actions that failed, from another goroutine.
func NewService(a Adapter, report func(error)) *Service {
	s := &Service{adapter: a, report: report, engine: NewEngine(nil), work: make(chan func() error, 32), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.worker(ctx)
	return s
}

// worker runs the actions that take a while, in order, away from the hook.
func (s *Service) worker(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.work:
			if err := job(); err != nil {
				s.report(err)
			}
		}
	}
}

// Check returns the error for the first rule the system cannot carry out.
func (s *Service) Check(rules []Rule) error {
	for _, r := range rules {
		if r.Abbr == "" && !s.adapter.CanRemap(r.From.Key) {
			return &RuleError{Line: r.Line, Problem: Unavailable, Text: KeyName(r.From.Key)}
		}
	}
	return nil
}

// Apply puts a configuration in effect at once, with no need to start the watch again; what the rules held down is
// released first. Rules with a mistake return it, and none of them work until it is corrected.
func (s *Service) Apply(c *Config) error {
	rules, err := Parse(c.Rules)
	if err == nil {
		err = s.Check(rules)
	}
	if err != nil {
		rules = nil
	}
	s.life.Lock()
	defer s.life.Unlock()
	s.rules = rules
	s.swap(NewEngine(rules))
	s.sync()
	return err
}

// SetActive switches the rules on or off. The app calls it with the user's choice and nothing else.
func (s *Service) SetActive(on bool) {
	s.life.Lock()
	defer s.life.Unlock()
	s.active = on
	s.sync()
}

// swap replaces the engine, and lets go of the keys the old one held.
func (s *Service) swap(e *Engine) {
	s.mu.Lock()
	old := s.engine
	keys := old.Release()
	s.engine = e
	s.mu.Unlock()
	if len(keys) > 0 {
		s.adapter.Send(keys)
	}
}

// Err is why the watch could not begin, or nil.
func (s *Service) Err() error {
	s.life.Lock()
	defer s.life.Unlock()
	return s.err
}

// Retry tries again to begin a watch that failed, as after the user gave the permission.
func (s *Service) Retry() {
	s.life.Lock()
	defer s.life.Unlock()
	s.sync()
}

// Watching reports whether the watch is on.
func (s *Service) Watching() bool {
	s.life.Lock()
	defer s.life.Unlock()
	return s.watching
}

// sync turns the watch on or off to suit; the caller holds life.
func (s *Service) sync() {
	wanted := s.active && len(s.rules) > 0
	switch {
	case wanted && !s.watching:
		s.err = s.adapter.Start(s.handle)
		s.watching = s.err == nil
	case !wanted && s.watching:
		s.stop()
	case !wanted:
		s.err = nil
	}
}

func (s *Service) stop() {
	s.adapter.Stop()
	s.watching = false
	s.mu.Lock()
	keys := s.engine.Release()
	s.mu.Unlock()
	if len(keys) > 0 {
		s.adapter.Send(keys)
	}
}

// Close ends the watch, letting go of whatever is held, and the worker.
func (s *Service) Close() {
	s.life.Lock()
	defer s.life.Unlock()
	if s.watching {
		s.stop()
	}
	s.cancel()
	<-s.done
}

// handle takes a key from the adapter. The engine's lock is let go before anything is sent, as a sent key may come
// back through the hook while Send runs.
func (s *Service) handle(vk uint16, down bool) bool {
	s.mu.Lock()
	out := s.engine.Key(vk, down, s.adapter)
	s.mu.Unlock()
	if len(out.Keys) > 0 {
		if err := s.adapter.Send(out.Keys); err != nil {
			s.queue(func() error { return err })
		}
	}
	if out.Type != "" {
		s.queue(func() error { return s.adapter.Type(out.Type) })
	}
	if out.Open != "" {
		s.queue(func() error { return s.adapter.Open(out.Open, out.Args) })
	}
	return out.Swallow
}

// queue hands a job to the worker without making the keyboard wait; with the queue full the job is dropped, as
// keys pressed that fast have nothing to wait for.
func (s *Service) queue(job func() error) {
	select {
	case s.work <- job:
	default:
	}
}
