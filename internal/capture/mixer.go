package capture

import (
	"encoding/binary"
	"io"
	"math"
	"sync"
	"time"
)

// Audio is mixed and sent to ffmpeg as 48 kHz interleaved stereo float.
const (
	sampleRate = 48000
	channels   = 2
)

// captureInterval is how often the audio sources collect what their devices captured.
const captureInterval = 5 * time.Millisecond

// The mixer writes a span once every source has had time to deliver it: normally 100 ms behind real time, and
// while ffmpeg finishes, just a few collection rounds behind, so the sound runs to the picture's last frame.
const (
	mixLatency   = sampleRate / 10
	drainLatency = int64(4*captureInterval) * sampleRate / int64(time.Second)
)

// snapFrames is how far a packet's timestamp may stray from where the previous one ended and still be taken as
// continuing it (20 ms). Timestamps jitter by a few frames; snapping keeps the sound gapless, and a larger jump is a
// real gap (silence) or overlap (dropped), which also absorbs the slow drift between a sound card's clock and the
// system's.
const snapFrames = sampleRate / 50

// track is one source's sound placed on the recording's timeline, in frames since its start.
type track struct {
	mu     sync.Mutex
	chunks []chunk
	end    int64 // where the last packet ended, -1 before the first
	peak   float32
}

type chunk struct {
	at      int64
	samples []float32 // nil for silence
	frames  int
}

func newTrack() *track { return &track{end: -1} }

// add places a packet captured at frame at; samples is nil for silence. It copies the samples, so the caller's
// buffer may be reused.
func (t *track) add(samples []float32, frames int, at int64) {
	if frames <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.end >= 0 && at > t.end-snapFrames && at < t.end+snapFrames {
		at = t.end
	}
	if at < t.end {
		skip := int(t.end - at)
		if skip >= frames {
			return
		}
		if samples != nil {
			samples = samples[skip*channels:]
		}
		frames -= skip
		at = t.end
	}
	c := chunk{at: at, frames: frames}
	if samples != nil {
		c.samples = append([]float32(nil), samples[:frames*channels]...)
		for _, s := range c.samples {
			t.peak = max(t.peak, float32(math.Abs(float64(s))))
		}
	}
	t.chunks = append(t.chunks, c)
	t.end = at + int64(frames)
}

// mixInto adds the track's sound for frames [from, from+len(out)/channels) to out and forgets everything before
// the end of that span.
func (t *track) mixInto(out []float32, from int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	to := from + int64(len(out)/channels)
	keep := t.chunks[:0]
	for _, c := range t.chunks {
		cEnd := c.at + int64(c.frames)
		if c.samples != nil && cEnd > from && c.at < to {
			lo, hi := max(c.at, from), min(cEnd, to)
			src := c.samples[(lo-c.at)*channels : (hi-c.at)*channels]
			dst := out[(lo-from)*channels:]
			for i, s := range src {
				dst[i] += s
			}
		}
		if cEnd > to {
			keep = append(keep, c)
		}
	}
	clear(t.chunks[len(keep):])
	t.chunks = keep
}

// takePeak returns the loudest sample since the last call, 0 to 1.
func (t *track) takePeak() float32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := min(t.peak, 1)
	t.peak = 0
	return p
}

// mixer writes the sum of its tracks to w, keeping latency frames behind now so that every source's packets for a
// span have arrived before it is written; a source that has nothing for a span is silent there.
type mixer struct {
	tracks  []*track
	latency int64
	written int64
	buf     []float32
	bytes   []byte
}

func newMixer(tracks []*track) *mixer {
	return &mixer{tracks: tracks, latency: mixLatency}
}

// advance writes everything up to now (in frames since the start) less the latency.
func (m *mixer) advance(w io.Writer, now int64) error {
	target := now - m.latency
	for m.written < target {
		n := int(min(target-m.written, sampleRate/20))
		out := m.buf[:0]
		if cap(out) < n*channels {
			out = make([]float32, n*channels)
		}
		out = out[:n*channels]
		clear(out)
		for _, t := range m.tracks {
			t.mixInto(out, m.written)
		}
		m.buf = out
		if len(m.bytes) < len(out)*4 {
			m.bytes = make([]byte, len(out)*4)
		}
		b := m.bytes[:len(out)*4]
		for i, s := range out {
			binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(max(-1, min(1, s))))
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		m.written += int64(n)
	}
	return nil
}
