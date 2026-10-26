package playback

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/media"
	"github.com/btelolabs/btelo-video-cut/internal/project"
	"github.com/btelolabs/btelo-video-cut/internal/wave"
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
