package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

// Status is how a recording is going.
type Status struct {
	Elapsed time.Duration // recorded so far, pauses left out
	Bytes   int64         // written so far
	Paused  bool
	System  float32 // the latest loudness of the system's sound, 0 to 1
	Mic     float32 // and of the microphone
}

// Recorder is one recording. Its methods may be called from any goroutine.
type Recorder struct {
	opts Options

	mu       sync.Mutex
	parts    []string
	cur      *segment
	finished time.Duration // length of the segments before cur
	levels   [2]float32
	failed   error
	ended    chan struct{} // closed when the recording stopped by itself
	stopped  bool
}

// segment is one ffmpeg run, from a start or resume to the next pause or stop.
type segment struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stderr    *tailBuffer
	started   time.Time
	exited    chan struct{}
	exitErr   error
	quit      chan struct{} // closed by end to stop the audio sources and the mixer
	drain     chan struct{} // closed by stop: bring the sound up to just behind real time
	ending    sync.Once
	audio     sync.WaitGroup
	conn      net.Conn
	connMu    sync.Mutex
	closing   bool
	listening bool // the sound goes to ffmpeg through a connection
}

// Start begins recording; it returns once ffmpeg and the audio devices are running.
func Start(o Options) (*Recorder, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if o.FPS <= 0 {
		return nil, errors.New("the frame rate must be positive")
	}
	if err := os.MkdirAll(filepath.Dir(o.Path), 0o755); err != nil {
		return nil, err
	}
	r := &Recorder{opts: o, ended: make(chan struct{})}
	if err := r.begin(); err != nil {
		return nil, err
	}
	return r, nil
}

// Ended is closed when the recording stopped by itself, such as when ffmpeg failed; Err says why. Stop still has
// to be called to save what was recorded.
func (r *Recorder) Ended() <-chan struct{} { return r.ended }

// Err is why the recording stopped by itself, or nil.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}

// Path is the finished file's path.
func (r *Recorder) Path() string { return r.opts.Path }

// Options are what is being recorded.
func (r *Recorder) Options() Options { return r.opts }

// Status reports the length, size and sound levels so far.
func (r *Recorder) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Status{Elapsed: r.finished, Paused: r.cur == nil, System: r.levels[0], Mic: r.levels[1]}
	if r.cur != nil {
		s.Elapsed += time.Since(r.cur.started)
	}
	for _, p := range r.parts {
		if st, err := os.Stat(p); err == nil {
			s.Bytes += st.Size()
		}
	}
	return s
}

// Pause ends the current segment; Resume starts the next.
func (r *Recorder) Pause() error {
	r.mu.Lock()
	cur := r.cur
	if cur == nil || r.stopped {
		r.mu.Unlock()
		return nil
	}
	r.cur = nil
	r.finished += time.Since(cur.started)
	r.levels = [2]float32{}
	r.mu.Unlock()
	return cur.stop()
}

// Resume continues a paused recording.
func (r *Recorder) Resume() error {
	r.mu.Lock()
	if r.cur != nil || r.stopped || r.failed != nil {
		r.mu.Unlock()
		return r.Err()
	}
	r.mu.Unlock()
	return r.begin()
}

// Stop ends the recording and joins its segments into the MP4; it takes a few seconds for a long recording, since
// nothing is encoded again. When joining fails the segments are kept and the error names them.
func (r *Recorder) Stop(ctx context.Context) (string, error) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return "", errors.New("the recording was already stopped")
	}
	r.stopped = true
	cur := r.cur
	r.cur = nil
	r.mu.Unlock()
	if cur != nil {
		cur.stop()
	}
	return r.opts.Path, join(ctx, r.parts, r.opts.Path)
}

// begin starts a segment: the audio sources, the mixer feeding ffmpeg through a local socket, and ffmpeg.
func (r *Recorder) begin() error {
	o := r.opts
	part := strings.TrimSuffix(o.Path, filepath.Ext(o.Path)) + fmt.Sprintf(".part%d.mkv", len(r.parts)+1)
	origin, t0 := counter(), wallMicros()
	s := &segment{exited: make(chan struct{}), quit: make(chan struct{}), drain: make(chan struct{}), stderr: &tailBuffer{}}
	// ffmpeg grabs no picture until its sound input delivers, so the mixer feeds it from the start, with silence
	// until the audio devices have opened; they open meanwhile, timed from origin like the picture.
	var address string
	var listener net.Listener
	var opened func() error
	if o.audio() {
		var err error
		if listener, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return err
		}
		address = "tcp://" + listener.Addr().String()
		var tracks []*track
		tracks, opened = s.startAudio(o, origin)
		s.audio.Add(1)
		go func() {
			defer s.audio.Done()
			r.feed(s, listener, tracks, origin)
		}()
	}
	cmd, err := media.Command(context.Background(), "ffmpeg", recordArgs(o, t0, address, part)...)
	if err == nil {
		cmd.Stderr = s.stderr
		s.stdin, err = cmd.StdinPipe()
	}
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		if listener != nil {
			listener.Close()
			s.end()
		}
		return err
	}
	if opened != nil {
		if err := opened(); err != nil {
			listener.Close()
			s.end()
			cmd.Process.Kill()
			cmd.Wait()
			os.Remove(part)
			return err
		}
	}
	s.cmd, s.started, s.listening = cmd, time.Now(), listener != nil
	r.mu.Lock()
	r.parts = append(r.parts, part)
	r.cur = s
	r.mu.Unlock()
	go func() {
		s.exitErr = cmd.Wait()
		s.connMu.Lock()
		closing := s.closing
		if s.conn != nil {
			s.conn.Close()
		}
		s.connMu.Unlock()
		if listener != nil {
			listener.Close()
		}
		close(s.exited)
		if !closing {
			r.fail(s, fmt.Errorf("recording stopped: %v: %s", s.exitErr, s.stderr.String()))
		}
	}()
	return nil
}

// end stops the segment's audio and waits for it.
func (s *segment) end() {
	s.ending.Do(func() { close(s.quit) })
	s.audio.Wait()
}

// fail ends a recording whose segment stopped by itself.
func (r *Recorder) fail(s *segment, err error) {
	s.end()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur != s {
		return
	}
	r.finished += time.Since(s.started)
	r.cur, r.failed = nil, err
	close(r.ended)
}

// startAudio starts the chosen sources; opened waits until each has opened its device, or failed to.
func (s *segment) startAudio(o Options, origin int64) (tracks []*track, opened func() error) {
	type source struct {
		loopback bool
		id       string
	}
	var sources []source
	if o.SystemAudio {
		sources = append(sources, source{loopback: true})
	}
	if o.Microphone {
		sources = append(sources, source{id: o.MicrophoneID})
	}
	results := make(chan error, len(sources))
	for _, src := range sources {
		t := newTrack()
		tracks = append(tracks, t)
		s.audio.Add(1)
		go func() {
			defer s.audio.Done()
			captureAudio(src.loopback, src.id, origin, t.add, func(err error) {
				if err != nil {
					kind := ErrSystemSound
					if !src.loopback {
						kind = ErrMicrophone
					}
					err = fmt.Errorf("%w: %w", kind, err)
				}
				results <- err
			}, s.quit)
		}()
	}
	// The level meters show system sound first and the microphone second, whichever of them is recorded.
	if !o.SystemAudio {
		tracks = append([]*track{nil}, tracks...)
	}
	return tracks, func() error {
		var errs []error
		deadline := time.After(10 * time.Second)
		for range sources {
			select {
			case err := <-results:
				errs = append(errs, err)
			case <-deadline:
				return errors.New("the audio devices did not answer")
			}
		}
		return errors.Join(errs...)
	}
}

// feed accepts ffmpeg's connection and writes the mixed sound to it every 10 ms until the segment ends.
func (r *Recorder) feed(s *segment, l net.Listener, tracks []*track, origin int64) {
	conn, err := l.Accept()
	l.Close()
	if err != nil {
		return
	}
	s.connMu.Lock()
	if s.closing {
		s.connMu.Unlock()
		conn.Close()
		return
	}
	s.conn = conn
	s.connMu.Unlock()
	defer conn.Close()
	var live []*track
	for _, t := range tracks {
		if t != nil {
			live = append(live, t)
		}
	}
	m := newMixer(live)
	freq := counterFrequency()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var levels [2]float32
	drain := s.drain
	for {
		select {
		case <-s.quit:
			return
		case <-drain:
			m.latency, drain = drainLatency, nil
		case <-tick.C:
		}
		now := (counter() - origin) * sampleRate / freq
		if err := m.advance(conn, now); err != nil {
			return
		}
		for i, t := range tracks {
			if t != nil && i < len(levels) {
				levels[i] = max(t.takePeak(), levels[i]*0.85)
			}
		}
		r.mu.Lock()
		if r.cur == s {
			r.levels = levels
		}
		r.mu.Unlock()
	}
}

// stop asks ffmpeg to finish the segment with q on its standard input, and ends it after a while if it does not.
// ffmpeg stops reading its sound the moment it is asked, so the mixer first catches up to just behind real time;
// the picture runs on a little longer, which join cuts off.
func (s *segment) stop() error {
	s.connMu.Lock()
	s.closing = true
	s.connMu.Unlock()
	if s.listening {
		close(s.drain)
		time.Sleep(mixLatency * time.Second / sampleRate)
	}
	io.WriteString(s.stdin, "q")
	s.stdin.Close()
	select {
	case <-s.exited:
	case <-time.After(15 * time.Second):
		s.cmd.Process.Kill()
		<-s.exited
	}
	s.end()
	return nil
}

// join makes the segments into one MP4 and removes them. Each segment's sound begins a little before its picture
// (ffmpeg starts grabbing once its inputs are open) and its picture ends a little after its sound (ffmpeg grabs for a
// moment after it is asked to finish), so each is cut to where both run: the picture is copied from its first frame,
// a keyframe, and the lossless sound is cut to the sample and encoded once.
func join(ctx context.Context, parts []string, out string) error {
	var spans []span
	var used []string
	for _, p := range parts {
		if sp, ok := segmentSpan(ctx, p); ok {
			spans, used = append(spans, sp), append(used, p)
		}
	}
	if len(used) == 0 {
		for _, p := range parts {
			os.Remove(p)
		}
		return ErrNothingRecorded
	}
	base := strings.TrimSuffix(out, filepath.Ext(out))
	listFile, tmp := base+".parts.txt", base+".joining.mp4"
	if err := os.WriteFile(listFile, []byte(concatList(used, spans)), 0o644); err != nil {
		return err
	}
	defer os.Remove(listFile)
	cmd, err := media.Command(ctx, "ffmpeg", joinArgs(listFile, used, spans, tmp)...)
	if err != nil {
		return err
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("the recording is kept in %s; joining it failed: %v: %s", strings.Join(used, ", "), err, lastLine(string(output)))
	}
	if err := os.Rename(tmp, out); err != nil {
		os.Remove(tmp)
		return err
	}
	for _, p := range parts {
		os.Remove(p)
	}
	return nil
}

// concatList is the concat demuxer's list of the segments' pictures, each from its first frame to its end.
func concatList(paths []string, spans []span) string {
	var b strings.Builder
	for i, p := range paths {
		fmt.Fprintf(&b, "file '%s'\ninpoint %s\n", strings.ReplaceAll(filepath.ToSlash(p), "'", `'\''`), seconds(spans[i].start))
		if end := spans[i].end(); end > spans[i].start {
			fmt.Fprintf(&b, "outpoint %s\n", seconds(end))
		}
	}
	return b.String()
}

// joinArgs copies the pictures through the concat list and, when the recording has sound, trims each segment's
// sound to the same span and joins and encodes it.
func joinArgs(list string, paths []string, spans []span, out string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "concat", "-safe", "0", "-i", list}
	sound := slices.ContainsFunc(spans, func(sp span) bool { return sp.sound })
	if !sound {
		return append(args, "-map", "0:v", "-c", "copy", "-avoid_negative_ts", "make_zero", out)
	}
	var graph, labels strings.Builder
	input := 1 // the concat list is input 0; each segment's sound is read from it again
	for i, p := range paths {
		sp := spans[i]
		end := sp.end()
		if sp.sound {
			args = append(args, "-i", p)
			fmt.Fprintf(&graph, "[%d:a]atrim=start=%s", input, seconds(sp.start))
			input++
			if end > sp.start {
				fmt.Fprintf(&graph, ":end=%s", seconds(end))
			}
		} else {
			// A segment that lost its sound gets silence as long as its picture.
			fmt.Fprintf(&graph, "anullsrc=r=%d:cl=stereo,atrim=duration=%s", sampleRate, seconds(max(end-sp.start, 0)))
		}
		fmt.Fprintf(&graph, ",asetpts=PTS-STARTPTS[a%d];", i)
		fmt.Fprintf(&labels, "[a%d]", i)
	}
	fmt.Fprintf(&graph, "%sconcat=n=%d:v=0:a=1[a]", labels.String(), len(paths))
	// The picture's B-frames start it with a negative decoding time; shifting every stream together keeps the sound
	// on it (MP4 would otherwise move the picture alone).
	return append(args, "-filter_complex", graph.String(), "-map", "0:v", "-map", "[a]", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k",
		"-avoid_negative_ts", "make_zero", out)
}

func seconds(t float64) string { return strconv.FormatFloat(t, 'f', 6, 64) }

// span is the part of a segment to keep: from its first frame of picture to where its picture or its sound ends,
// whichever is first.
type span struct {
	start              float64
	videoEnd, soundEnd float64 // 0 when not known
	sound              bool
}

// end is where the span ends, or 0 when the segment does not say.
func (sp span) end() float64 {
	switch {
	case sp.videoEnd > 0 && sp.soundEnd > 0:
		return min(sp.videoEnd, sp.soundEnd)
	case sp.soundEnd > 0:
		return sp.soundEnd
	}
	return sp.videoEnd
}

// segmentSpan reads a segment's span. It is false for a segment without picture (ffmpeg failed before the first
// frame).
func segmentSpan(ctx context.Context, path string) (span, bool) {
	cmd, err := media.Command(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,start_time:stream_tags=DURATION",
		"-of", "json", path)
	if err != nil {
		return span{}, false
	}
	out, err := cmd.Output()
	if err != nil {
		return span{}, false
	}
	return parseSpan(out)
}

func parseSpan(probe []byte) (span, bool) {
	var info struct {
		Streams []struct {
			Type  string `json:"codec_type"`
			Start string `json:"start_time"`
			Tags  struct {
				Duration string `json:"DURATION"`
			} `json:"tags"`
		} `json:"streams"`
	}
	if json.Unmarshal(probe, &info) != nil {
		return span{}, false
	}
	var sp span
	video := false
	for _, st := range info.Streams {
		// Matroska keeps each track's end time in its DURATION tag, as h:mm:ss.nnnnnnnnn.
		end, _ := clockSeconds(st.Tags.Duration)
		switch st.Type {
		case "video":
			start, err := strconv.ParseFloat(st.Start, 64)
			if err != nil {
				return span{}, false
			}
			sp.start, sp.videoEnd, video = max(start, 0), end, true
		case "audio":
			sp.soundEnd, sp.sound = end, true
		}
	}
	if end := sp.end(); end > 0 && end <= sp.start {
		return span{}, false // nothing has both picture and sound
	}
	return sp, video
}

// clockSeconds parses h:mm:ss.fraction.
func clockSeconds(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return float64(h*3600+m*60) + sec, true
}

// tailBuffer keeps the end of ffmpeg's error output for messages.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return lastLine(string(t.buf))
}
