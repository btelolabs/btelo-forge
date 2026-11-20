package playback

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
	"github.com/btelolabs/btelo-forge/internal/wave"
)

func fixture(t *testing.T) project.Source {
	t.Helper()
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	out, err := wave.Open()
	if err != nil {
		t.Skip("no audio device: ", err)
	}
	out.Close()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	cmd, _ := media.Command(context.Background(), "ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000:duration=4,volume=0.02",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	info, err := media.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return project.Source{Path: path, Duration: info.Duration, Width: info.Width, Height: info.Height, FPS: info.FPS, HasVideo: true, HasAudio: info.HasAudio}
}

func TestPlayerFollowsTheSoundClockAndDeliversFrames(t *testing.T) {
	p := project.New(fixture(t), project.Export{})
	p.SplitClip(p.Video[0].Clips[0].ID, 1)
	p.SetSpeed(p.Video[0].Clips[1].ID, 2) // 1 s at 1×, then 3 s of source in 1.5 s
	pl := New(func() {})
	pl.Start(p, 0.5, 160, 90)
	defer pl.Stop()
	var f Frame
	begun, first := time.Time{}, 0.0
	for wait := time.Now(); time.Since(wait) < 5*time.Second; time.Sleep(16 * time.Millisecond) {
		f, _ = pl.Frame()
		if at, _ := pl.Position(); at > 0.5 {
			begun, first = time.Now(), at
			break
		}
	}
	if begun.IsZero() {
		t.Fatal("playback never started")
	}
	for time.Since(begun) < time.Second {
		f, _ = pl.Frame()
		time.Sleep(16 * time.Millisecond)
	}
	// The sound card drives the clock, so it never runs ahead of real time (beyond one buffer of granularity); on a
	// loaded machine it may lag.
	at, playing := pl.Position()
	elapsed := time.Since(begun).Seconds()
	if !playing || at-first > elapsed+0.1 || at-first < 0.3 {
		t.Fatalf("%.2f s after the clock first read %.3f it reads %.3f (playing %v)", elapsed, first, at, playing)
	}
	f, ok := pl.Frame()
	if !ok || f.Image.Rect.Dx() != 160 || f.Image.Rect.Dy() != 90 || f.T > at+1.0/FPS || f.T < at-0.5 {
		t.Fatalf("frame %+v ok=%v at clock %v", f.T, ok, at)
	}
	deadline := time.Now().Add(4 * time.Second)
	for pl.Playing() && time.Now().Before(deadline) {
		pl.Position()
		time.Sleep(50 * time.Millisecond)
	}
	if pl.Playing() {
		t.Fatal("playback did not stop at the end of the timeline")
	}
	if end, _ := pl.Position(); end != 0 {
		t.Fatalf("stopped player still reports %v", end)
	}
	if err := pl.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditionPlaysAndStops(t *testing.T) {
	src := fixture(t)
	a := NewAudition(func() {})
	a.Play("k", src.Path, "")
	time.Sleep(700 * time.Millisecond)
	key, heard := a.Playing()
	if key != "k" || heard < 0.2 {
		t.Fatalf("audition %q heard %v", key, heard)
	}
	a.Stop()
	if key, _ := a.Playing(); key != "" {
		t.Fatal("still playing after Stop")
	}
	if err := a.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitReturnsOnceStoppedPlaybackHasLetGo(t *testing.T) {
	p := project.New(fixture(t), project.Export{})
	pl := New(func() {})
	pl.Start(p, 0, 160, 90)
	a := NewAudition(func() {})
	a.Play("k", p.Sources[0].Path, "")
	time.Sleep(300 * time.Millisecond)
	pl.Stop()
	a.Stop()
	done := make(chan struct{})
	go func() {
		pl.Wait()
		a.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stopped playback still running after 5 s")
	}
}

// clip makes a test file: pictures at fps for seconds, with a sound of sound seconds when it is above zero.
func clip(t *testing.T, fps int, seconds, sound float64) File {
	t.Helper()
	fixture(t) // skips without ffmpeg or a sound device
	path := filepath.Join(t.TempDir(), "file.mkv")
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=320x180:rate=%d:duration=%v", fps, seconds)}
	if sound > 0 {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=330:sample_rate=48000:duration=%v,volume=0.02", sound))
	}
	args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	cmd, _ := media.Command(context.Background(), "ffmpeg", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	info, err := media.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return File{Path: path, Duration: info.Duration, FPS: info.FPS, HasVideo: info.HasVideo, HasAudio: info.HasAudio}
}

// played runs pl until it stops or limit passes, and returns the distinct picture times it showed and how long the
// playing took.
func played(t *testing.T, pl *Player, limit time.Duration) ([]float64, time.Duration) {
	t.Helper()
	var times []float64
	begun := time.Now()
	for pl.Playing() && time.Since(begun) < limit {
		if f, ok := pl.Frame(); ok && (len(times) == 0 || f.T != times[len(times)-1]) {
			times = append(times, f.T)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if pl.Playing() {
		t.Fatalf("still playing after %v", limit)
	}
	if err := pl.Err(); err != nil {
		t.Fatal(err)
	}
	return times, time.Since(begun)
}

func TestAFilePlaysAtItsOwnFrameRateToItsEnd(t *testing.T) {
	f := clip(t, 60, 2, 2)
	if f.rate() != 60 {
		t.Fatalf("rate %v", f.rate())
	}
	pl := New(func() {})
	pl.StartFile(f, 0.5, 160, 90)
	defer pl.Stop()
	times, took := played(t, pl, 6*time.Second)
	// 1.5 s at 60 pictures a second: the pictures shown are 1/60 s apart, apart from the few a busy machine skips,
	// and far more than the 30 a second the edited timeline is previewed at.
	if len(times) < 60 {
		t.Fatalf("%d pictures shown in 1.5 s: %v", len(times), times)
	}
	for i := 1; i < len(times); i++ {
		if step := times[i] - times[i-1]; math.Abs(step*60-math.Round(step*60)) > 1e-6 || step <= 0 {
			t.Fatalf("pictures at %v then %v", times[i-1], times[i])
		}
	}
	if times[0] != 0.5 || times[len(times)-1] < 1.9 {
		t.Fatalf("pictures from %v to %v", times[0], times[len(times)-1])
	}
	if took < 1300*time.Millisecond || took > 4*time.Second {
		t.Fatalf("1.5 s played in %v", took)
	}
}

func TestAFasterFilePlaysInLessTime(t *testing.T) {
	f := clip(t, 30, 3, 3)
	f.Speed = 2
	pl := New(func() {})
	pl.StartFile(f, 0, 160, 90)
	defer pl.Stop()
	times, took := played(t, pl, 6*time.Second)
	// 3 s of the file at twice its speed: 1.5 s, the pictures 2/30 s of the file apart.
	if took < 1300*time.Millisecond || took > 2500*time.Millisecond {
		t.Fatalf("3 s at 2× played in %v", took)
	}
	if len(times) < 15 || times[len(times)-1] < 2.8 {
		t.Fatalf("pictures at %v", times)
	}
	for i := 1; i < len(times); i++ {
		if steps := (times[i] - times[i-1]) / (2.0 / 30); math.Abs(steps-math.Round(steps)) > 1e-6 || steps < 0.5 {
			t.Fatalf("pictures at %v then %v", times[i-1], times[i])
		}
	}
}

func TestASoundShorterThanThePicturesDoesNotEndTheFileEarly(t *testing.T) {
	f := clip(t, 30, 2, 0.5)
	pl := New(func() {})
	pl.StartFile(f, 0, 160, 90)
	defer pl.Stop()
	times, took := played(t, pl, 6*time.Second)
	if took < 1700*time.Millisecond || times[len(times)-1] < 1.9 {
		t.Fatalf("2 s of pictures, 0.5 s of sound: played %v, the last picture at %v", took, times[len(times)-1])
	}
}

func TestAFileWithoutSoundKeepsTimeByTheClock(t *testing.T) {
	f := clip(t, 30, 1.5, 0)
	if f.HasAudio {
		t.Fatal("the clip has sound")
	}
	pl := New(func() {})
	pl.StartFile(f, 0, 160, 90)
	defer pl.Stop()
	times, took := played(t, pl, 5*time.Second)
	if took < 1300*time.Millisecond || len(times) < 30 {
		t.Fatalf("1.5 s without sound played in %v with %d pictures", took, len(times))
	}
}

func TestTheVolumeScalesTheSoundAndClips(t *testing.T) {
	pcm := []byte{0x00, 0x10, 0x00, 0xf0, 0xff, 0x7f, 0x01, 0x80} // 4096, -4096, 32767, -32767
	var v atomic.Uint64
	v.Store(math.Float64bits(2))
	g := &gain{r: &oneByteAtATime{data: pcm}, volume: &v}
	out, err := io.ReadAll(g)
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{8192, -8192, 32767, -32768}
	if len(out) != 8 {
		t.Fatalf("%d bytes", len(out))
	}
	for i, w := range want {
		if got := int16(binary.LittleEndian.Uint16(out[2*i:])); got != w {
			t.Fatalf("sample %d is %d, want %d", i, got, w)
		}
	}
	pl := New(func() {})
	if pl.Volume() != 1 {
		t.Fatalf("a new player's volume is %v", pl.Volume())
	}
	pl.SetVolume(-1)
	if pl.Volume() != 0 {
		t.Fatalf("volume %v", pl.Volume())
	}
}

// oneByteAtATime gives what it reads one byte per read, as a pipe may, splitting every sample.
type oneByteAtATime struct{ data []byte }

func (r *oneByteAtATime) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}
