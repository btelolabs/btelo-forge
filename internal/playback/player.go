// Package playback plays the edited timeline: ffmpeg renders the preview streams, the sound card drives the clock.
package playback

import (
	"bufio"
	"context"
	"errors"
	"image"
	"io"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/render"
	"github.com/btelolabs/btelo-forge/internal/wave"
)

// FPS is the preview frame rate.
const FPS = 30

// span is how much of the timeline one pair of ffmpeg processes covers before the player starts the next pair.
const span = 90.0

// Frame is a decoded preview picture at output time T.
type Frame struct {
	Image *image.RGBA
	T     float64
}

// Player is driven from the UI goroutine; decoding and audio run on their own goroutines.
type Player struct {
	wake func()

	mu      sync.Mutex
	run     *session
	err     error
	stopped bool
	running sync.WaitGroup // every session, until its decoders and sound have finished
}

// New returns a player that calls wake whenever a new frame is ready or playback ends.
func New(wake func()) *Player { return &Player{wake: wake, stopped: true} }

type session struct {
	cancel   context.CancelFunc
	from, to float64
	end      float64
	started  time.Time
	audio    *wave.Output
	audible  bool
	frames   chan Frame
	ready    chan struct{} // closed once the first picture is decoded, so sound and picture start together
	ended    chan struct{} // closed once the clock has reached to
	done     chan struct{}
	current  Frame
	pending  *Frame
}

// Start plays p from output time from with pictures of w×h; it replaces any running playback.
func (pl *Player) Start(p *project.Project, from float64, w, h int) {
	pl.Stop()
	snapshot := p.Clone()
	end := snapshot.Duration()
	if from >= end-0.05 {
		from = 0
	}
	pl.mu.Lock()
	pl.err, pl.stopped = nil, false
	pl.mu.Unlock()
	pl.begin(snapshot, from, end, w, h)
}

func (pl *Player) begin(p *project.Project, from, end float64, w, h int) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{cancel: cancel, from: from, to: min(from+span, end), end: end, frames: make(chan Frame, 8), ready: make(chan struct{}), ended: make(chan struct{}), done: make(chan struct{}), current: Frame{T: -1}}
	s.audible = render.Audible(p, s.from, s.to)
	if s.audible {
		out, err := wave.Open()
		if err != nil {
			s.audible = false
		} else {
			s.audio = out
		}
	}
	pl.mu.Lock()
	pl.run = s
	pl.mu.Unlock()
	pl.running.Add(1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := pl.decode(ctx, p, s, w, h); err != nil && ctx.Err() == nil {
			pl.fail(err)
		}
	}()
	if s.audio != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := pl.sound(ctx, p, s)
			close(s.ended)
			if err != nil && ctx.Err() == nil && !errors.Is(err, wave.ErrStopped) {
				pl.fail(err)
			}
		}()
	} else {
		pl.running.Add(1)
		go func() {
			defer pl.running.Done()
			select {
			case <-s.ready:
			case <-ctx.Done():
				close(s.ended)
				return
			}
			select {
			case <-time.After(time.Duration((s.to - s.from) * float64(time.Second))):
			case <-ctx.Done():
			}
			close(s.ended)
		}()
	}
	go func() {
		defer pl.running.Done()
		wg.Wait()
		if s.audio != nil {
			s.audio.Close()
		}
		close(s.done)
		pl.wake()
		if ctx.Err() != nil {
			return
		}
		pl.mu.Lock()
		next := pl.run == s && !pl.stopped && s.to < end-0.01
		pl.mu.Unlock()
		if next {
			pl.begin(p, s.to, end, w, h)
		}
	}()
}

func (pl *Player) fail(err error) {
	pl.mu.Lock()
	pl.err = err
	pl.mu.Unlock()
	pl.Stop()
	pl.wake()
}

func (pl *Player) decode(ctx context.Context, p *project.Project, s *session, w, h int) error {
	g, err := render.Build(render.Spec{Project: p, From: s.from, To: s.to, Width: w, Height: h, FPS: FPS, Video: true, PixFmt: "rgba"})
	if err != nil {
		return err
	}
	args, err := g.Args("-f", "rawvideo", "pipe:1")
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd, err := media.Command(ctx, "ffmpeg", args...)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Wait()
	defer stop() // runs before Wait: an early return must not leave ffmpeg blocked on a full pipe
	r := bufio.NewReaderSize(stdout, w*h*4)
	var once sync.Once
	start := func() {
		once.Do(func() {
			pl.mu.Lock()
			s.started = time.Now()
			pl.mu.Unlock()
			close(s.ready)
		})
	}
	defer start()
	for k := 0; ; k++ {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		if _, err := io.ReadFull(r, img.Pix); err != nil {
			return nil
		}
		if k == 0 {
			select {
			case s.frames <- Frame{Image: img, T: s.from}:
			case <-ctx.Done():
				return nil
			}
			start()
			pl.wake()
			continue
		}
		t := s.from + float64(k)/FPS
		if now, ok := pl.Position(); ok && t < now-2.0/FPS {
			continue
		}
		select {
		case s.frames <- Frame{Image: img, T: t}:
			pl.wake()
		case <-s.ended:
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

func (pl *Player) sound(ctx context.Context, p *project.Project, s *session) error {
	g, err := render.Build(render.Spec{Project: p, From: s.from, To: s.to, Audio: true})
	if err != nil {
		return err
	}
	args, err := g.Args("-f", "s16le", "-ar", "48000", "-ac", "2", "pipe:1")
	if err != nil {
		return err
	}
	cmd, err := media.Command(ctx, "ffmpeg", args...)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Wait()
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil
	}
	return s.audio.Play(bufio.NewReaderSize(stdout, 1<<16), ctx.Done())
}

// Position is the output time being heard, and false once playback has stopped.
func (pl *Player) Position() (float64, bool) {
	pl.mu.Lock()
	s, stopped := pl.run, pl.stopped
	pl.mu.Unlock()
	if s == nil || stopped {
		return 0, false
	}
	var t float64
	select {
	case <-s.ready:
	default:
		return s.from, true
	}
	if s.audio != nil {
		t = s.from + float64(s.audio.Played())/wave.Rate
	} else {
		pl.mu.Lock()
		started := s.started
		pl.mu.Unlock()
		t = s.from + time.Since(started).Seconds()
	}
	select {
	case <-s.done:
		if s.to >= s.end-0.01 {
			pl.Stop()
			return s.end, false
		}
		return s.to, true
	default:
	}
	return min(t, s.to), true
}

// Frame returns the newest decoded picture not later than the clock, dropping late ones.
func (pl *Player) Frame() (Frame, bool) {
	pl.mu.Lock()
	s := pl.run
	pl.mu.Unlock()
	if s == nil {
		return Frame{}, false
	}
	t, playing := pl.Position()
	if !playing {
		return s.current, s.current.Image != nil
	}
	for {
		if s.pending == nil {
			select {
			case f := <-s.frames:
				s.pending = &f
			default:
				return s.current, s.current.Image != nil
			}
		}
		if s.pending.T > t && s.current.Image != nil {
			return s.current, true
		}
		s.current, s.pending = *s.pending, nil
	}
}

// Playing reports whether playback is running.
func (pl *Player) Playing() bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.run != nil && !pl.stopped
}

// Err returns and clears the error that stopped playback.
func (pl *Player) Err() error {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	err := pl.err
	pl.err = nil
	return err
}

// Stop ends playback; it returns at once and the decoders wind down in the background (see Wait).
func (pl *Player) Stop() {
	pl.mu.Lock()
	s := pl.run
	pl.stopped = true
	pl.mu.Unlock()
	if s != nil {
		s.cancel()
	}
}

// Wait returns once every stopped session has let go of its decoders and sound device.
func (pl *Player) Wait() { pl.running.Wait() }
