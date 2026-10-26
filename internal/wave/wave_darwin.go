package wave

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/oto/v3"
)

var (
	contextOnce sync.Once
	context     *oto.Context
	contextErr  error
)

func sharedContext() (*oto.Context, error) {
	contextOnce.Do(func() {
		var ready chan struct{}
		context, ready, contextErr = oto.NewContext(&oto.NewContextOptions{SampleRate: Rate, ChannelCount: 2, Format: oto.FormatSignedInt16LE, ApplicationName: "btelo Cut"})
		if contextErr == nil {
			<-ready
		}
	})
	return context, contextErr
}

// Output is one stream on the shared audio device. Play runs on one goroutine; Played and Close may be called from any.
type Output struct {
	ctx    *oto.Context
	mu     sync.Mutex
	player *oto.Player
	read   atomic.Int64
	closed bool
}

// Open prepares playback on the default output device.
func Open() (*Output, error) {
	ctx, err := sharedContext()
	if err != nil {
		return nil, err
	}
	return &Output{ctx: ctx}, nil
}

type counting struct {
	r io.Reader
	n *atomic.Int64
}

func (c counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// Play streams little-endian s16 stereo PCM from r until it ends or stop closes, then waits for the tail to be heard.
func (o *Output) Play(r io.Reader, stop <-chan struct{}) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ErrStopped
	}
	o.player = o.ctx.NewPlayer(counting{r, &o.read})
	p := o.player
	o.mu.Unlock()
	p.Play()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for p.IsPlaying() {
		select {
		case <-stop:
			p.Pause()
			return ErrStopped
		case <-tick.C:
		}
	}
	if err := p.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Played is the number of frames heard so far: what the player has taken from the stream minus what it still holds.
func (o *Output) Played() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.player == nil {
		return 0
	}
	return max(o.read.Load()-int64(o.player.BufferedSize()), 0) / 4
}

// Close stops playback.
func (o *Output) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	if o.player != nil {
		return o.player.Close()
	}
	return nil
}
