package playback

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/media"
	"github.com/btelolabs/btelo-video-cut/internal/wave"
)

// Audition plays one sound file or URL for previewing a library track.
type Audition struct {
	wake func()

	mu      sync.Mutex
	key     string
	cancel  context.CancelFunc
	out     *wave.Output
	started time.Time
	err     error
}

// NewAudition returns an idle audition that calls wake when playback ends.
func NewAudition(wake func()) *Audition { return &Audition{wake: wake} }

// Play starts source (a path or an http URL, fetched naming referer when it is set) under key, replacing whatever
// was playing.
func (a *Audition) Play(key, source, referer string) {
	a.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.key, a.cancel, a.err, a.started = key, cancel, nil, time.Now()
	a.mu.Unlock()
	go func() {
		err := a.play(ctx, key, source, referer)
		a.mu.Lock()
		if a.key == key && a.cancel != nil && ctx.Err() == nil {
			a.key, a.cancel, a.out = "", nil, nil
			if err != nil && !errors.Is(err, wave.ErrStopped) {
				a.err = err
			}
		}
		a.mu.Unlock()
		cancel()
		a.wake()
	}()
}

func (a *Audition) play(ctx context.Context, key, source, referer string) error {
	out, err := wave.Open()
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.key == key && ctx.Err() == nil {
		a.out = out
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.out == out {
			a.out = nil
		}
		a.mu.Unlock()
		out.Close()
	}()
	args := []string{"-v", "error", "-nostdin"}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1", "-user_agent", "btelo-video-cut/1.0")
		if referer != "" {
			args = append(args, "-referer", referer)
		}
	}
	args = append(args, "-i", source, "-vn", "-f", "s16le", "-ar", "48000", "-ac", "2", "pipe:1")
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
	playErr := out.Play(bufio.NewReaderSize(stdout, 1<<16), ctx.Done())
	waitErr := cmd.Wait()
	if playErr != nil {
		return playErr
	}
	if waitErr != nil && ctx.Err() == nil {
		return errors.New("this sound cannot be decoded")
	}
	return nil
}

// Playing returns the key being auditioned and seconds heard, or "" when idle.
func (a *Audition) Playing() (string, float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel == nil {
		return "", 0
	}
	if a.out == nil {
		return a.key, 0
	}
	return a.key, float64(a.out.Played()) / wave.Rate
}

// Err returns and clears the last failure.
func (a *Audition) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.err
	a.err = nil
	return err
}

// Stop ends the audition.
func (a *Audition) Stop() {
	a.mu.Lock()
	cancel := a.cancel
	a.key, a.cancel, a.out = "", nil, nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
