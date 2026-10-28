package capture

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

// streamPictures samples what a server received twice a second, and reports for each sample whether it is all one
// shade, as the black canvas is, or shows something, as the screen does.
func streamPictures(t *testing.T, path string, size image.Point) []bool {
	t.Helper()
	cmd, err := media.Command(context.Background(), "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", path,
		"-vf", "fps=2", "-f", "rawvideo", "-pix_fmt", "gray", "pipe:1")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	n := size.X * size.Y
	var blank []bool
	for ; len(out) >= n; out = out[n:] {
		lo, hi := slices.Min(out[:n]), slices.Max(out[:n])
		blank = append(blank, hi-lo <= 2)
	}
	return blank
}

// videoGaps is the longest time between two frames of the picture a server received.
func videoGaps(t *testing.T, path string) (longest float64, frames int) {
	t.Helper()
	cmd, err := media.Command(context.Background(), "ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts_time", "-of", "csv=p=0", path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var times []float64
	for _, line := range strings.Fields(string(out)) {
		if v, err := strconv.ParseFloat(strings.Trim(line, ","), 64); err == nil {
			times = append(times, v)
		}
	}
	slices.Sort(times)
	for i := 1; i < len(times); i++ {
		longest = max(longest, times[i]-times[i-1])
	}
	return longest, len(times)
}

func TestTheStreamGoesOnWhileItsPictureChanges(t *testing.T) {
	d, _ := recordable(t)
	canvas := image.Pt(1280, 720)
	e, err := ProbeStreamEncoder(context.Background(), canvas, 30)
	if err != nil {
		t.Fatal(err)
	}
	servers := []*rtmpServer{startRTMPServer(t, freePort(t), "one"), startRTMPServer(t, freePort(t), "two")}
	o := StreamOptions{Size: canvas, FPS: 30, Kbps: 2500, Encoder: e, SystemAudio: true}
	for i, s := range servers {
		o.Targets = append(o.Targets, Target{ID: fmt.Sprint(i), Name: fmt.Sprint("server ", i), URL: s.url})
	}
	s, err := StartStream(o)
	if err != nil {
		t.Fatal(err)
	}
	live := func() bool {
		st := s.Status()
		for _, ts := range st.Targets {
			if ts.State != TargetLive {
				return false
			}
		}
		return st.Kbps > 0
	}
	waitFor(t, "both servers receive the black canvas", 20*time.Second, live)
	if s.Status().Screen {
		t.Error("the canvas shows the screen before it was asked to")
	}
	time.Sleep(time.Second)

	// The screen goes on the canvas, as when a recording starts…
	if err := s.ShowScreen(Options{Display: d, Cursor: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the screen shows", 10*time.Second, func() bool { return s.Status().Screen })
	time.Sleep(3 * time.Second)
	// …goes black when it stops…
	s.ShowNothing()
	if s.Status().Screen {
		t.Error("the screen still shows")
	}
	time.Sleep(3 * time.Second)
	// …and comes back, from another part of the screen, when the next one starts.
	if err := s.ShowScreen(Options{Display: d, Region: image.Rect(0, 0, min(d.Width, 600), min(d.Height, 900))}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the region shows", 10*time.Second, func() bool { return s.Status().Screen })
	time.Sleep(3 * time.Second)
	if st := s.Status(); !live() || st.ScreenErr != nil {
		t.Fatalf("status %+v", st)
	}
	elapsed := s.Status().Elapsed.Seconds()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, srv := range servers {
		got := srv.received(t)
		if got["video"].codec != "h264" || got["audio"].codec != "aac" || got["video"].duration < elapsed-4 {
			t.Errorf("%s received %+v of a %.1f s stream", srv.url, got, elapsed)
		}
		if gap, frames := videoGaps(t, srv.path); gap > 0.1 || float64(frames) < got["video"].duration*30*0.97 {
			t.Errorf("%s: %d frames in %.1f s, with a gap of %.2f s", srv.url, frames, got["video"].duration, gap)
		}
		// Black, then the screen, black, and the screen again.
		blank := streamPictures(t, srv.path, canvas)
		var runs []bool
		for _, b := range blank {
			if len(runs) == 0 || runs[len(runs)-1] != b {
				runs = append(runs, b)
			}
		}
		if !slices.Equal(runs, []bool{true, false, true, false}) {
			t.Errorf("%s: the picture went %v (true is black), sampled %v", srv.url, runs, blank)
		}
	}
}

func TestStreamArgs(t *testing.T) {
	o := StreamOptions{Size: image.Pt(1920, 1080), FPS: 60, Kbps: 6000, AudioKbps: 160, Encoder: Encoder{Codec: "h264_nvenc"}}
	hw := strings.Join(streamArgs(o, "tcp://127.0.0.1:8", ""), " ")
	for _, want := range []string{
		"-f rawvideo -pix_fmt nv12 -video_size 1920x1080 -framerate 60 -i tcp://127.0.0.1:8", "-f lavfi -i anullsrc=r=48000:cl=stereo",
		"-map 0:v -map 1:a", "-rc cbr", "-b:v 6000k -maxrate 6000k -bufsize 12000k -g 120", "-c:a aac -b:a 160k", "-f mpegts pipe:1",
	} {
		if !strings.Contains(hw, want) {
			t.Errorf("command lacks %q:\n%s", want, hw)
		}
	}
	if sound := strings.Join(streamArgs(o, "tcp://127.0.0.1:8", "tcp://127.0.0.1:9"), " "); !strings.Contains(sound, "-f f32le -ar 48000 -ac 2 -i tcp://127.0.0.1:9") ||
		strings.Contains(sound, "anullsrc") {
		t.Errorf("with sound:\n%s", sound)
	}
	o.Encoder = Encoder{Codec: "libx264"}
	if sw := strings.Join(streamArgs(o, "tcp://127.0.0.1:8", ""), " "); !strings.Contains(sw, "-c:v libx264") || !strings.Contains(sw, "nal-hrd=cbr") || !strings.Contains(sw, "-keyint_min 120") {
		t.Errorf("software:\n%s", sw)
	}
}

func TestFeedFitsTheScreenOnTheCanvas(t *testing.T) {
	d := Display{Adapter: 1, Output: 2, Width: 3440, Height: 1440}
	canvas := image.Pt(1920, 1080)
	gpu := strings.Join(feedArgs(Options{Display: d, Cursor: true}, canvas, 30, true), " ")
	for _, want := range []string{
		"-init_hw_device d3d11va=grab:1", "ddagrab=output_idx=2:framerate=30:draw_mouse=1,scale_d3d11=width=1920:height=804:format=nv12,hwdownload,format=nv12,pad=1920:1080:0:138:black[v]",
		"-f rawvideo -pix_fmt nv12 pipe:1",
	} {
		if !strings.Contains(gpu, want) {
			t.Errorf("command lacks %q:\n%s", want, gpu)
		}
	}
	// An upright region on a landscape canvas, and the processor scaling it.
	cpu := strings.Join(feedArgs(Options{Display: d, Region: image.Rect(100, 100, 700, 1100)}, canvas, 30, false), " ")
	if !strings.Contains(cpu, "draw_mouse=0:offset_x=100:offset_y=100:video_size=600x1000,hwdownload,format=bgra,scale=w=648:h=1080:flags=bicubic,format=nv12,pad=1920:1080:636:0:black[v]") {
		t.Errorf("region:\n%s", cpu)
	}
	if same := strings.Join(feedArgs(Options{Display: Display{Width: 1920, Height: 1080}}, canvas, 30, true), " "); strings.Contains(same, "pad=") {
		t.Errorf("a screen the canvas's shape is padded:\n%s", same)
	}
}

func TestFitInside(t *testing.T) {
	for _, c := range []struct{ w, h, maxW, maxH, wantW, wantH int }{
		{3840, 2160, 1920, 1080, 1920, 1080},
		{1280, 720, 1920, 1080, 1920, 1080}, // enlarged to fill the canvas
		{3440, 1440, 1920, 1080, 1920, 804},
		{1080, 1920, 1920, 1080, 608, 1080},
		{2560, 1440, 1080, 1920, 1080, 608}, // a landscape screen on an upright canvas
		{601, 401, 1280, 720, 1080, 720},
	} {
		if w, h := fitInside(c.w, c.h, c.maxW, c.maxH); w != c.wantW || h != c.wantH {
			t.Errorf("%d×%d in %d×%d: %d×%d, want %d×%d", c.w, c.h, c.maxW, c.maxH, w, h, c.wantW, c.wantH)
		}
	}
}

func TestFrameStoreShowsOnlyTheCurrentFeed(t *testing.T) {
	f := newFrameStore(image.Pt(4, 2))
	frame := make([]byte, f.size)
	f.copyTo(frame)
	if frame[0] != 16 || frame[len(frame)-1] != 128 {
		t.Fatalf("black is %v", frame)
	}
	a, b := &screenFeed{}, &screenFeed{}
	f.own(a)
	shown := make([]byte, f.size)
	shown[0] = 200
	next := f.put(a, shown)
	if &next[0] == &shown[0] {
		t.Fatal("the feed reads into the frame being shown")
	}
	f.copyTo(frame)
	if frame[0] != 200 || !f.showing(a) {
		t.Fatal("the feed's frame is not shown")
	}
	f.own(b) // a newer feed: black until its first frame, and the old one's frames are ignored
	f.put(a, shown)
	f.copyTo(frame)
	if frame[0] != 16 || f.showing(a) || f.showing(b) {
		t.Fatal("an old feed's frame is shown")
	}
	f.release(a) // the old feed ending changes nothing
	f.put(b, shown)
	if !f.showing(b) {
		t.Fatal("the new feed is not shown")
	}
	f.clear()
	f.copyTo(frame)
	if frame[0] != 16 {
		t.Fatal("cleared, the canvas is not black")
	}
}

// TestTheScreenShowsWhenScalingOnTheGraphicsCardFails makes the graphics card's scaling fail, as it does in some
// ffmpeg builds: the screen still shows, scaled by the processor.
func TestTheScreenShowsWhenScalingOnTheGraphicsCardFails(t *testing.T) {
	d, _ := recordable(t)
	canvas := image.Pt(1280, 720)
	e, err := ProbeStreamEncoder(context.Background(), canvas, 30)
	if err != nil {
		t.Fatal(err)
	}
	broken := gpuScaleFilter
	gpuScaleFilter = "scale_d3d11=width=%d:height=%d:format=no-such-format"
	defer func() { gpuScaleFilter = broken }()
	server := startRTMPServer(t, freePort(t), "fallback")
	s, err := StartStream(StreamOptions{Size: canvas, FPS: 30, Kbps: 2500, Encoder: e, Targets: []Target{{ID: "0", Name: "server", URL: server.url}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	s.mu.Lock()
	s.gpuScale = true // as where scale_d3d11 passed its check but fails on the screen
	s.mu.Unlock()
	if err := s.ShowScreen(Options{Display: d}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the screen shows, scaled by the processor", 15*time.Second, func() bool { return s.Status().Screen })
	if st := s.Status(); st.ScreenErr != nil {
		t.Errorf("screen error %v", st.ScreenErr)
	}
	s.mu.Lock()
	gpu := s.gpuScale
	s.mu.Unlock()
	if gpu {
		t.Error("the stream goes on trying the graphics card's scaling")
	}
}

func TestPreviewShowsTheCanvas(t *testing.T) {
	f := newFrameStore(image.Pt(8, 4))
	if p := f.preview(image.Pt(8, 4), 4); p.Rect.Size() != image.Pt(4, 2) || p.RGBAAt(0, 0) != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("black canvas previews as %v %v", p.Rect, p.RGBAAt(0, 0))
	}
	// White in video range, with a pure red quarter: Y 81, U 90, V 240 in BT.601.
	frame := make([]byte, f.size)
	for i := range 32 {
		frame[i] = 235
	}
	for i := 32; i < len(frame); i += 2 {
		frame[i], frame[i+1] = 128, 128
	}
	frame[0], frame[1], frame[8], frame[9] = 81, 81, 81, 81
	frame[32], frame[33] = 90, 240
	feed := &screenFeed{}
	f.own(feed)
	f.put(feed, frame)
	p := f.preview(image.Pt(8, 4), 8)
	if c := p.RGBAAt(7, 3); c.R < 250 || c.G < 250 || c.B < 250 {
		t.Errorf("white shows as %v", c)
	}
	if c := p.RGBAAt(0, 0); c.R < 240 || c.G > 15 || c.B > 15 {
		t.Errorf("red shows as %v", c)
	}
}
