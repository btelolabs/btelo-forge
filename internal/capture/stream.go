package capture

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

// A live stream works as OBS Studio's outputs do: it has a canvas of its own, a fixed size and frame rate that the
// platforms see from start to end, and an encoder that runs as long as the stream does. What is on the canvas can
// change underneath without the stream noticing: the screen being recorded, fitted in with black bars, or black
// while nothing is recorded. The picture goes to the encoder as raw frames through a local socket, one exactly
// every frame interval by the same clock as the sound, so changing it never breaks the stream's timing.

// StreamOptions say how to stream and where: the canvas, the encoding, the sound, and the destinations, which all
// get the same stream.
type StreamOptions struct {
	Size         image.Point // the canvas, as the platforms see it; both sides even
	FPS          int
	Kbps         int // the picture's bit rate
	AudioKbps    int // the sound's; 0 is 160
	Encoder      Encoder
	SystemAudio  bool
	Microphone   bool
	MicrophoneID string // "" follows the system's default microphone
	Targets      []Target
}

// ErrNoTargets means a stream was started with nowhere to send it.
var ErrNoTargets = errors.New("choose at least one place to stream to")

// StreamStatus is how a live stream is going.
type StreamStatus struct {
	Elapsed    time.Duration
	Kbps       float64 // what the encoder makes now
	System     float32 // the latest loudness of the system's sound, 0 to 1
	Mic        float32 // and of the microphone
	Screen     bool    // the screen is on the canvas, rather than black
	ScreenErr  error   // why the screen stopped showing by itself, if it did
	Targets    []TargetStatus
	FrameDrops int // frames the encoder could not take in time, sent late as copies
}

// Streamer is a live stream. Its methods may be called from any goroutine.
type Streamer struct {
	opts   StreamOptions
	relay  *relay
	frames *frameStore
	seg    *segment
	pacing sync.WaitGroup

	mu        sync.Mutex
	start     time.Time
	levels    [2]float32
	feed      *screenFeed
	screenErr error
	gpuScale  bool
	failed    error
	stopped   bool
	ended     chan struct{}
}

// StartStream starts the encoder, with a black canvas, and the connections to the destinations; it returns once
// ffmpeg and the sound run. ShowScreen puts the screen on the canvas.
func StartStream(o StreamOptions) (*Streamer, error) {
	return startStream(o, defaultPush)
}

func startStream(o StreamOptions, push pushOptions) (*Streamer, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if o.FPS <= 0 || o.Kbps <= 0 || o.Size.X <= 0 || o.Size.Y <= 0 || o.Size.X%2 != 0 || o.Size.Y%2 != 0 {
		return nil, errors.New("the stream needs a positive frame rate and bit rate, and a canvas with even sides")
	}
	if len(o.Targets) == 0 {
		return nil, ErrNoTargets
	}
	for _, t := range o.Targets {
		if err := t.Check(); err != nil {
			return nil, err
		}
	}
	if o.AudioKbps <= 0 {
		o.AudioKbps = 160
	}
	gpuScale := gpuScales(context.Background())
	s := &Streamer{opts: o, relay: newRelay(push), frames: newFrameStore(o.Size), gpuScale: gpuScale, ended: make(chan struct{})}
	video, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	for _, t := range o.Targets {
		if err := s.relay.add(t); err != nil {
			video.Close()
			s.relay.close()
			return nil, err
		}
	}
	started := make(chan *segment, 1)
	s.pacing.Add(1)
	go func() {
		defer s.pacing.Done()
		s.pace(video, started)
	}()
	sound := Options{SystemAudio: o.SystemAudio, Microphone: o.Microphone, MicrophoneID: o.MicrophoneID}
	address := "tcp://" + video.Addr().String()
	_, err = startSegment(sound, func(_ int64, audio string) []string { return streamArgs(o, address, audio) }, s.relay, segmentHooks{
		started: func(seg *segment) {
			s.mu.Lock()
			s.seg, s.start = seg, seg.started
			s.mu.Unlock()
			started <- seg
		},
		levels: func(_ *segment, levels [2]float32) {
			s.mu.Lock()
			s.levels = levels
			s.mu.Unlock()
		},
		failed: func(_ *segment, err error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.failed == nil && !s.stopped {
				s.failed = err
				close(s.ended)
			}
		},
	})
	if err != nil {
		close(started)
		video.Close()
		s.pacing.Wait()
		s.relay.close()
		return nil, err
	}
	return s, nil
}

// pace accepts the encoder's connection for the picture and writes it a frame for every frame interval since the
// segment's origin, the moment its sound is timed from: whatever is on the canvas then. When the encoder falls
// behind, the frames it missed follow as soon as it reads again, so the picture keeps the sound's time.
func (s *Streamer) pace(l net.Listener, started <-chan *segment) {
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		l.Close()
		if err != nil {
			conn = nil
		}
		accepted <- conn
	}()
	seg, ok := <-started
	if !ok {
		l.Close()
		if conn := <-accepted; conn != nil {
			conn.Close()
		}
		return
	}
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-seg.exited:
		l.Close()
		return
	}
	if conn == nil {
		return
	}
	defer conn.Close()
	fps, freq := int64(s.opts.FPS), counterFrequency()
	frame := make([]byte, s.frames.size)
	tick := time.NewTicker(time.Second / time.Duration(fps) / 2)
	defer tick.Stop()
	var sent int64
	for {
		due := (counter()-seg.origin)*fps/freq + 1
		// The frames written as the encoder first connects are its start, not lateness.
		if behind := due - sent; behind > 1 && sent > 0 {
			s.frames.addLate(int(behind - 1))
		}
		for ; sent < due; sent++ {
			s.frames.copyTo(frame)
			if _, err := conn.Write(frame); err != nil {
				return
			}
		}
		select {
		case <-seg.quit:
			return
		case <-seg.exited:
			return
		case <-tick.C:
		}
	}
}

// Ended is closed when the stream stopped by itself, because the encoding failed; Err says why. Stop must still be
// called. A destination that fails does not end the stream: it is tried again, and Status reports it.
func (s *Streamer) Ended() <-chan struct{} { return s.ended }

// Err is why the stream stopped by itself, or nil.
func (s *Streamer) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// Options are how the stream is made.
func (s *Streamer) Options() StreamOptions { return s.opts }

// Status reports how long the stream has run, its picture, the sound's levels and each destination's state.
func (s *Streamer) Status() StreamStatus {
	s.mu.Lock()
	st := StreamStatus{Elapsed: time.Since(s.start), Kbps: s.relay.rate.kbps(), System: s.levels[0], Mic: s.levels[1],
		Screen: s.feed != nil && s.frames.showing(s.feed), ScreenErr: s.screenErr}
	s.mu.Unlock()
	st.FrameDrops = s.frames.lateFrames()
	for _, t := range s.relay.list() {
		st.Targets = append(st.Targets, t.status())
	}
	return st
}

// ShowScreen puts a display, or a region of it, on the canvas, fitted in its middle with black bars, in place of
// what was there; the stream goes on without a break. Display, Region and Cursor of o are used.
func (s *Streamer) ShowScreen(o Options) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("the stream has ended")
	}
	f, err := startFeed(o, s.opts, s.gpuScale, s.frames, s.feedEnded)
	if err != nil {
		return err
	}
	old := s.feed
	s.feed, s.screenErr = f, nil
	if old != nil {
		go old.stop()
	}
	return nil
}

// ShowNothing makes the canvas black at once; the stream goes on, and the screen's grabber ends in the background.
func (s *Streamer) ShowNothing() {
	s.mu.Lock()
	f := s.feed
	s.feed, s.screenErr = nil, nil
	s.frames.clear()
	s.mu.Unlock()
	if f != nil {
		go f.stop()
	}
}

// feedEnded hears that a feed's ffmpeg stopped by itself. One that scaled on the graphics card and failed before its
// first frame is started again scaling on the processor, which every ffmpeg can; otherwise the canvas is black from
// then, and Status says why.
func (s *Streamer) feedEnded(f *screenFeed, err error, delivered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.feed != f {
		return
	}
	if f.gpu && !delivered {
		s.gpuScale = false
		again, retryErr := startFeed(f.opts, s.opts, false, s.frames, s.feedEnded)
		if retryErr == nil {
			s.feed = again
			return
		}
		err = retryErr
	}
	s.feed, s.screenErr = nil, err
}

// gpuScales reports whether the ffmpeg in use scales frames on the graphics card: scale_d3d11 is missing from older
// builds and fails to make its textures in some newer ones, so it is tried on a small picture.
func gpuScales(ctx context.Context) bool {
	if ok, err := hasFilter(ctx, "scale_d3d11"); err != nil || !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd, err := media.Command(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-init_hw_device", "d3d11va=probe:0", "-filter_hw_device", "probe", "-f", "lavfi", "-i", "color=c=black:s=64x64,format=bgra",
		"-vf", "hwupload,scale_d3d11=width=32:height=18:format=nv12,hwdownload,format=nv12", "-frames:v", "1", "-f", "null", "-")
	return err == nil && cmd.Run() == nil
}

// AddTarget starts sending the stream to another destination as well.
func (s *Streamer) AddTarget(t Target) error {
	if err := t.Check(); err != nil {
		return err
	}
	return s.relay.add(t)
}

// RemoveTarget stops sending to one destination; the stream goes on to the others.
func (s *Streamer) RemoveTarget(id string) error { return s.relay.remove(id) }

// RetryTarget connects again at once to a destination that is waiting to retry or has been given up.
func (s *Streamer) RetryTarget(id string) error { return s.relay.retry(id) }

// Stop ends the stream: the encoder finishes, and each destination sends what it has and closes its connection.
func (s *Streamer) Stop() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return errors.New("the stream was already stopped")
	}
	s.stopped = true
	f, seg := s.feed, s.seg
	s.feed = nil
	s.mu.Unlock()
	if f != nil {
		f.stop()
	}
	seg.stop()
	s.pacing.Wait()
	s.relay.close()
	return nil
}

// streamArgs is the encoder's command line: the canvas's raw frames from the pacer, and the sound, encoded at a
// constant bit rate with a keyframe every two seconds as platforms ask, into a transport stream on standard output.
// A stream always has sound, silence when none is chosen, since platforms expect it.
func streamArgs(o StreamOptions, video, audio string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostats",
		"-thread_queue_size", "64", "-f", "rawvideo", "-pix_fmt", "nv12", "-video_size", fmt.Sprintf("%dx%d", o.Size.X, o.Size.Y),
		"-framerate", strconv.Itoa(o.FPS), "-i", video}
	if audio != "" {
		args = append(args, "-thread_queue_size", "4096", "-probesize", "32", "-analyzeduration", "0", "-f", "f32le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "-i", audio)
	} else {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("anullsrc=r=%d:cl=stereo", sampleRate))
	}
	args = append(args, "-map", "0:v", "-map", "1:a")
	args = append(args, streamEncodeArgs(o.Encoder, o.FPS, o.Kbps)...)
	args = append(args, "-c:a", "aac", "-b:a", strconv.Itoa(o.AudioKbps)+"k", "-ar", strconv.Itoa(sampleRate), "-ac", "2")
	return append(args, "-flush_packets", "1", "-f", "mpegts", "pipe:1")
}

// streamEncodeArgs are the encoder's settings for streaming: a constant bit rate with a buffer of two seconds, and a
// keyframe exactly every two seconds, so that every platform takes it and a destination can join at any keyframe.
func streamEncodeArgs(e Encoder, fps, kbps int) []string {
	gop := strconv.Itoa(fps * 2)
	rate, buffer := strconv.Itoa(kbps)+"k", strconv.Itoa(kbps*2)+"k"
	cbr := []string{"-b:v", rate, "-maxrate", rate, "-bufsize", buffer, "-g", gop}
	switch e.Codec {
	case "h264_nvenc":
		return append([]string{"-c:v", "h264_nvenc", "-preset", "p5", "-tune", "hq", "-rc", "cbr", "-profile:v", "high", "-bf", "2", "-no-scenecut", "1", "-strict_gop", "1"}, cbr...)
	case "h264_amf":
		return append([]string{"-c:v", "h264_amf", "-usage", "transcoding", "-quality", "balanced", "-rc", "cbr", "-profile:v", "high", "-header_insertion_mode", "idr"}, cbr...)
	case "h264_mf":
		return append([]string{"-c:v", "h264_mf", "-hw_encoding", "1", "-rate_control", "cbr"}, cbr...)
	default:
		return append([]string{"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-bf", "2", "-keyint_min", gop, "-sc_threshold", "0",
			"-x264-params", "nal-hrd=cbr"}, cbr...)
	}
}

// ProbeStreamEncoder finds the best encoder that encodes a canvas of this size and rate on this computer, trying
// each for half a second of black.
func ProbeStreamEncoder(ctx context.Context, size image.Point, fps int) (Encoder, error) {
	var errs []error
	for _, e := range encoders {
		err := tryStreamEncoder(ctx, e, size, fps)
		if err == nil {
			return e, nil
		}
		if ctx.Err() != nil {
			return Encoder{}, ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s: %w", e.Codec, err))
	}
	return Encoder{}, fmt.Errorf("%w: %w", ErrNoEncoder, errors.Join(errs...))
}

func tryStreamEncoder(ctx context.Context, e Encoder, size image.Point, fps int) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i",
		fmt.Sprintf("color=c=black:s=%dx%d:r=%d,format=nv12", size.X, size.Y, fps), "-t", "0.5"}
	args = append(args, streamEncodeArgs(e, fps, 2500)...)
	cmd, err := media.Command(ctx, "ffmpeg", append(args, "-f", "null", "-")...)
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(stderr.String()))
	}
	return nil
}

// frameStore holds the canvas's current picture as an NV12 frame: the latest from the screen feed, or black.
type frameStore struct {
	size  int
	black []byte

	mu    sync.Mutex
	cur   []byte
	owner *screenFeed // the feed whose frames are shown
	lates int
}

func newFrameStore(canvas image.Point) *frameStore {
	luma := canvas.X * canvas.Y
	black := make([]byte, luma*3/2)
	for i := range black {
		if i < luma {
			black[i] = 16 // video-range black
		} else {
			black[i] = 128
		}
	}
	return &frameStore{size: len(black), black: black}
}

// copyTo copies the current picture into frame.
func (f *frameStore) copyTo(frame []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur != nil {
		copy(frame, f.cur)
	} else {
		copy(frame, f.black)
	}
}

// own makes a feed's frames the ones shown, once they come; until then the canvas is black.
func (f *frameStore) own(feed *screenFeed) {
	f.mu.Lock()
	f.owner, f.cur = feed, nil
	f.mu.Unlock()
}

// put shows a feed's new frame, when that feed is the one shown; it returns the buffer the feed reads into next.
func (f *frameStore) put(feed *screenFeed, frame []byte) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owner != feed {
		return frame
	}
	frame, f.cur = f.cur, frame
	if frame == nil {
		frame = make([]byte, f.size)
	}
	return frame
}

// release goes black if a feed's frames are the ones shown.
func (f *frameStore) release(feed *screenFeed) {
	f.mu.Lock()
	if f.owner == feed {
		f.owner, f.cur = nil, nil
	}
	f.mu.Unlock()
}

func (f *frameStore) clear() {
	f.mu.Lock()
	f.owner, f.cur = nil, nil
	f.mu.Unlock()
}

func (f *frameStore) showing(feed *screenFeed) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owner == feed && f.cur != nil
}

func (f *frameStore) addLate(n int) {
	f.mu.Lock()
	f.lates += n
	f.mu.Unlock()
}

func (f *frameStore) lateFrames() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lates
}

// screenFeed is an ffmpeg that grabs the screen as raw frames already fitted to the canvas.
type screenFeed struct {
	opts   Options
	gpu    bool // it scales on the graphics card
	cancel context.CancelFunc
	done   chan struct{}
}

// startFeed starts grabbing the screen for the canvas, whose frames show once they come; ended hears when the feed
// stops by itself.
func startFeed(o Options, so StreamOptions, gpuScale bool, frames *frameStore, ended func(f *screenFeed, err error, delivered bool)) (*screenFeed, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd, err := media.Command(ctx, "ffmpeg", feedArgs(o, so.Size, so.FPS, gpuScale)...)
	var out io.ReadCloser
	if err == nil {
		out, err = cmd.StdoutPipe()
	}
	stderr := &tailBuffer{}
	if err == nil {
		cmd.Stderr = stderr
		err = cmd.Start()
	}
	if err != nil {
		cancel()
		return nil, err
	}
	f := &screenFeed{opts: o, gpu: gpuScale, cancel: cancel, done: make(chan struct{})}
	frames.own(f)
	go func() {
		defer close(f.done)
		defer cancel()
		buf := make([]byte, frames.size)
		var readErr error
		delivered := false
		for {
			if _, readErr = io.ReadFull(out, buf); readErr != nil {
				break
			}
			buf = frames.put(f, buf)
			delivered = true
		}
		err := cmd.Wait()
		frames.release(f)
		if ctx.Err() != nil {
			return // stopped
		}
		if why := ffmpegFailure(stderr.lines()); why != "" {
			err = errors.New(why)
		} else if err == nil {
			err = readErr
		}
		ended(f, err, delivered)
	}()
	return f, nil
}

// stop ends the feed and waits for it.
func (f *screenFeed) stop() {
	f.cancel()
	<-f.done
}

// feedArgs grab a display, or a region of it, and fit it in the middle of the canvas: scaled up or down to fill it
// one way, with black bars the other. The graphics card scales and converts to NV12 when ffmpeg has scale_d3d11,
// so only the canvas's frames are copied to memory.
func feedArgs(o Options, canvas image.Point, fps int, gpuScale bool) []string {
	d := o.Display
	cursor := "0"
	if o.Cursor {
		cursor = "1"
	}
	area := evenArea(d, o.Region)
	w, h := fitInside(area.Dx(), area.Dy(), canvas.X, canvas.Y)
	chain := []string{fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d:draw_mouse=%s%s", d.Output, fps, cursor, crop(d, o.Region))}
	if gpuScale {
		chain = append(chain, fmt.Sprintf(gpuScaleFilter, w, h), "hwdownload", "format=nv12")
	} else {
		chain = append(chain, "hwdownload", "format=bgra", fmt.Sprintf("scale=w=%d:h=%d:flags=bicubic", w, h), "format=nv12")
	}
	if w != canvas.X || h != canvas.Y {
		chain = append(chain, fmt.Sprintf("pad=%d:%d:%d:%d:black", canvas.X, canvas.Y, (canvas.X-w)/2&^1, (canvas.Y-h)/2&^1))
	}
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-init_hw_device", fmt.Sprintf("d3d11va=grab:%d", d.Adapter), "-filter_hw_device", "grab",
		"-filter_complex", strings.Join(chain, ",") + "[v]", "-map", "[v]", "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "nv12", "pipe:1"}
}

// gpuScaleFilter scales on the graphics card and converts to NV12 there; tests replace it to make it fail.
var gpuScaleFilter = "scale_d3d11=width=%d:height=%d:format=nv12"

// fitInside scales w×h, up or down, to the largest size with its shape that lies within maxW×maxH; sides are even.
func fitInside(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return maxW, maxH
	}
	even := func(v float64) int { return max(2, int(v/2+0.5)*2) }
	if w*maxH > h*maxW { // wider than the canvas
		return maxW, min(even(float64(h)*float64(maxW)/float64(w)), maxH)
	}
	return min(even(float64(w)*float64(maxH)/float64(h)), maxW), maxH
}

// Preview is the canvas as it is now, shrunk to at most width pixels across, to show in the app.
func (s *Streamer) Preview(width int) *image.RGBA {
	return s.frames.preview(s.opts.Size, width)
}

// preview converts the current picture to RGBA, taking every step-th pixel, with the BT.601 video-range matrix the
// screen's frames are converted with.
func (f *frameStore) preview(size image.Point, width int) *image.RGBA {
	step := max(1, (size.X+width-1)/max(width, 1))
	w, h := size.X/step, size.Y/step
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	f.mu.Lock()
	defer f.mu.Unlock()
	src := f.cur
	if src == nil {
		src = f.black
	}
	luma := size.X * size.Y
	clamp := func(v int) uint8 { return uint8(min(max(v, 0), 255)) }
	for y := range h {
		sy := y * step
		row := src[sy*size.X:]
		chroma := src[luma+(sy/2)*size.X:]
		out := img.Pix[y*img.Stride:]
		for x := range w {
			sx := x * step
			c := 298 * (int(row[sx]) - 16)
			u, v := int(chroma[sx&^1])-128, int(chroma[sx|1])-128
			o := out[x*4:]
			o[0] = clamp((c + 409*v + 128) >> 8)
			o[1] = clamp((c - 100*u - 208*v + 128) >> 8)
			o[2] = clamp((c + 516*u + 128) >> 8)
			o[3] = 255
		}
	}
	return img
}
