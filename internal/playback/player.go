// Package playback plays the edited timeline, or a video file as it is: ffmpeg decodes the pictures and the sound, the
// sound card drives the clock.
package playback

import (
	"bufio"
	"context"
	"errors"
	"image"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/wave"
)

// FPS is the frame rate the edited timeline is previewed at.
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
	volume  atomic.Uint64  // the gain the sound is played at, as float64 bits
}

// New returns a player that calls wake whenever a new frame is ready or playback ends.
func New(wake func()) *Player {
	pl := &Player{wake: wake, stopped: true}
	pl.volume.Store(math.Float64bits(1))
	return pl
}

type session struct {
	cancel   context.CancelFunc
	from, to float64
	end      float64
	rate     float64 // pictures a second
	speed    float64 // seconds of the source played in a second
	picture  bool    // there are pictures to decode; without them the clock starts at once
	started  time.Time
	audio    *wave.Output
	audible  bool
	frames   chan Frame
	ready    chan struct{} // closed once the first picture is decoded, so sound and picture start together
	ended    chan struct{} // closed once the clock has reached to
	done     chan struct{}
	current  Frame
	pending  *Frame

	// Smooth interpolation between coarse audio-buffer position updates so high-FPS video (e.g. 60 fps)
	// does not jump by multiple frames per audio quantum.
	clockMu    sync.Mutex
	lastPlayed int64
	lastTick   time.Time
	lastPos    float64
}

// source is what a player plays: the edited timeline, or a file as it is.
type source interface {
	// length is how long it plays, in its own seconds.
	length() float64
	// span is how much of it one pair of ffmpeg processes covers before the next pair is started.
	span() float64
	// rate is how many pictures a second the decoder gives, and speed how many of its seconds play in one.
	rate() float64
	speed() float64
	// picture says whether it has pictures; one that has none plays its sound alone.
	picture() bool
	audible(from, to float64) bool
	// video and audio are the arguments of the ffmpeg processes that decode from to to: w×h RGBA pictures at rate,
	// and s16 stereo sound at wave.Rate, each to standard output and already at speed.
	video(from, to float64, w, h int) ([]string, error)
	audio(from, to float64) ([]string, error)
}

// Start plays p from output time from with pictures of w×h; it replaces any running playback.
func (pl *Player) Start(p *project.Project, from float64, w, h int) {
	pl.start(timeline{p.Clone()}, from, w, h)
}

// StartFile plays f from its time from with pictures of w×h; it replaces any running playback.
func (pl *Player) StartFile(f File, from float64, w, h int) {
	pl.start(f, from, w, h)
}

func (pl *Player) start(src source, from float64, w, h int) {
	pl.Stop()
	end := src.length()
	if from >= end-0.05 {
		from = 0
	}
	pl.mu.Lock()
	pl.err, pl.stopped = nil, false
	pl.mu.Unlock()
	pl.begin(src, from, end, w, h)
}

func (pl *Player) begin(src source, from, end float64, w, h int) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{cancel: cancel, from: from, to: min(from+src.span(), end), end: end, rate: src.rate(), speed: src.speed(), picture: src.picture(),
		frames: make(chan Frame, 8), ready: make(chan struct{}), ended: make(chan struct{}), done: make(chan struct{}), current: Frame{T: -1}}
	s.audible = src.audible(s.from, s.to)
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
	if s.picture {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pl.decode(ctx, src, s, w, h); err != nil && ctx.Err() == nil {
				pl.fail(err)
			}
		}()
	} else {
		s.started = time.Now()
		close(s.ready)
	}
	if s.audio != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := pl.sound(ctx, src, s)
			close(s.ended)
			if err != nil && ctx.Err() == nil && !errors.Is(err, wave.ErrStopped) {
				pl.fail(err)
			}
		}()
	} else {
		// Without sound the clock alone says when the span is over: the decoder reaching the end of its pictures,
		// up to a channel's worth ahead of the clock, must not end the span early.
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-s.ready:
			case <-ctx.Done():
				close(s.ended)
				return
			}
			select {
			case <-time.After(time.Duration((s.to - s.from) / s.speed * float64(time.Second))):
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
			pl.begin(src, s.to, end, w, h)
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

func (pl *Player) decode(ctx context.Context, src source, s *session, w, h int) error {
	args, err := src.video(s.from, s.to, w, h)
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
	var reuse *image.RGBA
	for k := 0; ; k++ {
		img := reuse
		if img == nil {
			img = image.NewRGBA(image.Rect(0, 0, w, h))
		}
		if _, err := io.ReadFull(r, img.Pix); err != nil {
			return nil
		}
		if k == 0 {
			reuse = nil
			select {
			case s.frames <- Frame{Image: img, T: s.from}:
			case <-ctx.Done():
				return nil
			}
			start()
			pl.wake()
			continue
		}
		t := s.from + float64(k)/s.rate*s.speed
		if now, ok := pl.Position(); ok && t < now-2*s.speed/s.rate {
			reuse = img // dropped before leaving decode: reuse buffer without reallocating
			continue
		}
		reuse = nil
		select {
		case s.frames <- Frame{Image: img, T: t}:
			// The UI is told only when this picture may be the one it waits for; ones decoded ahead it finds by Due.
			if len(s.frames) <= 1 {
				pl.wake()
			}
		case <-s.ended:
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

func (pl *Player) sound(ctx context.Context, src source, s *session) error {
	args, err := src.audio(s.from, s.to)
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
	return s.audio.Play(&gain{r: bufio.NewReaderSize(stdout, 1<<16), volume: &pl.volume}, ctx.Done())
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
		played := s.audio.Played()
		now := time.Now()
		s.clockMu.Lock()
		if played <= 0 {
			t = s.from
		} else {
			if played > s.lastPlayed {
				s.lastPlayed = played
				s.lastTick = now
			}
			dt := min(max(now.Sub(s.lastTick).Seconds(), 0), 0.06)
			t = s.from + (float64(s.lastPlayed)/wave.Rate+dt)*s.speed
			if t < s.lastPos {
				t = s.lastPos
			} else {
				s.lastPos = t
			}
		}
		s.clockMu.Unlock()
	} else {
		pl.mu.Lock()
		started := s.started
		pl.mu.Unlock()
		t = s.from + time.Since(started).Seconds()*s.speed
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

// Due is how long until the next decoded picture is due on the clock, so the UI can draw it then and no later; it is
// false while none is decoded yet, and the player wakes the UI when one comes. Like Frame, it is for the UI goroutine.
func (pl *Player) Due() (time.Duration, bool) {
	pl.mu.Lock()
	s := pl.run
	pl.mu.Unlock()
	if s == nil {
		return 0, false
	}
	t, playing := pl.Position()
	if !playing {
		return 0, false
	}
	if s.pending == nil {
		select {
		case f := <-s.frames:
			s.pending = &f
		default:
			return 0, false
		}
	}
	return max(time.Duration((s.pending.T-t)/s.speed*float64(time.Second)), 0), true
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

// SetVolume sets the gain the sound plays at: 1 as it is, 0 silent. It applies at once, also while playing.
func (pl *Player) SetVolume(v float64) { pl.volume.Store(math.Float64bits(max(v, 0))) }

// Volume is the gain the sound plays at.
func (pl *Player) Volume() float64 { return math.Float64frombits(pl.volume.Load()) }

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
