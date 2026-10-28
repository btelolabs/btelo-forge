package capture

import (
	"context"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/media"
)

// recordable finds a display and a working encoder, or skips: build machines often have no desktop to grab.
func recordable(t *testing.T) (Display, Encoder) {
	t.Helper()
	if !Supported() {
		t.Skip(ErrUnsupported)
	}
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	displays, err := Displays()
	if err != nil {
		t.Skip(err)
	}
	e, err := ProbeEncoder(context.Background(), displays[0], 30)
	if err != nil {
		t.Skip(err)
	}
	t.Logf("display %+v, encoder %+v", displays[0], e)
	return displays[0], e
}

type streamInfo struct {
	codec    string
	start    float64
	duration float64
}

func streams(t *testing.T, path string) map[string]streamInfo {
	t.Helper()
	cmd, err := media.Command(context.Background(), "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name,start_time,duration",
		"-of", "csv=p=0", path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	found := map[string]streamInfo{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(strings.TrimSpace(line), ",")
		if len(f) < 4 {
			continue
		}
		start, _ := strconv.ParseFloat(f[2], 64)
		duration, _ := strconv.ParseFloat(f[3], 64)
		found[f[1]] = streamInfo{codec: f[0], start: start, duration: duration}
	}
	return found
}

func TestRecordPauseAndJoin(t *testing.T) {
	d, e := recordable(t)
	out := filepath.Join(t.TempDir(), "screen.mp4")
	r, err := Start(Options{Display: d, FPS: 30, Cursor: true, SystemAudio: true, Encoder: e, Path: out})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if err := r.Pause(); err != nil {
		t.Fatal(err)
	}
	if st := r.Status(); !st.Paused || st.Bytes == 0 {
		t.Fatalf("paused status %+v", st)
	}
	time.Sleep(time.Second)
	if err := r.Resume(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	select {
	case <-r.Ended():
		t.Fatalf("the recording stopped by itself: %v", r.Err())
	default:
	}
	path, err := r.Stop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := streams(t, path)
	v, a := s["video"], s["audio"]
	if v.codec != "h264" || a.codec != "aac" {
		t.Fatalf("streams %+v", s)
	}
	// Two segments of two seconds each, less the moment ffmpeg takes to start grabbing in each; the pause is left out.
	if v.duration < 2.8 || v.duration > 4.2 {
		t.Errorf("video lasts %.2fs, want 2.8 to 4.2", v.duration)
	}
	t.Logf("video %+v, sound %+v", v, a)
	if diff := v.duration - a.duration; diff > 0.1 || diff < -0.1 {
		t.Errorf("video %.2fs and sound %.2fs differ", v.duration, a.duration)
	}
	// The picture's start time includes its B-frame delay and MP4's edit list, so it is a few frames late on paper;
	// TestSoundStaysOnThePicture checks what is actually seen and heard.
	if v.start > 0.2 || a.start > 0.05 {
		t.Errorf("the streams begin at %.3fs and %.3fs, want at the start", v.start, a.start)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.mkv"))
	if len(leftovers) > 0 {
		t.Errorf("segments left behind: %v", leftovers)
	}
}

func TestStopWithoutSoundKeepsOnlyPicture(t *testing.T) {
	d, e := recordable(t)
	out := filepath.Join(t.TempDir(), "quiet.mp4")
	r, err := Start(Options{Display: d, FPS: 30, Encoder: e, Path: out})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := streams(t, out)
	if _, ok := s["audio"]; ok || s["video"].codec != "h264" {
		t.Fatalf("streams %+v", s)
	}
	if _, err := r.Stop(context.Background()); err == nil {
		t.Error("stopping twice succeeded")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

// TestEveryWayOfRecording records with each grabber and encoder this computer's ffmpeg can run, not only the one
// ProbeEncoder picks, since other computers fall back to them: another vendor's card, or none that encodes.
func TestEveryWayOfRecording(t *testing.T) {
	d, _ := recordable(t)
	worked := 0
	for _, e := range encoders {
		t.Run(e.Codec, func(t *testing.T) {
			if err := tryEncoder(context.Background(), Options{Display: d, FPS: 30, Encoder: e}); err != nil {
				t.Skipf("not available here: %v", err)
			}
			out := filepath.Join(t.TempDir(), "way.mp4")
			r, err := Start(Options{Display: d, FPS: 30, Cursor: true, SystemAudio: true, Encoder: e, Path: out})
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Second)
			if _, err := r.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			s := streams(t, out)
			v, a := s["video"], s["audio"]
			if v.codec != "h264" || a.codec != "aac" || v.duration < 1 {
				t.Fatalf("streams %+v", s)
			}
			if diff := v.duration - a.duration; diff > 0.1 || diff < -0.1 {
				t.Errorf("video %.2fs and sound %.2fs differ", v.duration, a.duration)
			}
			worked++
		})
	}
	if worked == 0 {
		t.Error("no way of recording worked")
	}
}

func TestRecordAndScreenshotARegion(t *testing.T) {
	d, e := recordable(t)
	// An odd-sized region is rounded down to even sides, as H.264 needs.
	region := image.Rect(101, 51, 101+641, 51+361)
	out := filepath.Join(t.TempDir(), "region.mp4")
	r, err := Start(Options{Display: d, FPS: 30, Encoder: e, Region: region, Path: out})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	cmd, _ := media.Command(context.Background(), "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", out)
	size, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(size)) != "640,360" {
		t.Errorf("recorded %q (%v), want 640,360", strings.TrimSpace(string(size)), err)
	}
	png := filepath.Join(t.TempDir(), "region.png")
	if err := Screenshot(context.Background(), d, region, false, png); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(png)
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width != 640 || cfg.Height != 360 {
		t.Errorf("screenshot %dx%d (%v), want 640x360", cfg.Width, cfg.Height, err)
	}
}

func TestDisplaysAreOnTheDesktop(t *testing.T) {
	list, err := Displays()
	if err != nil {
		t.Skip(err)
	}
	for _, d := range list {
		if d.Primary && (d.X != 0 || d.Y != 0) {
			t.Errorf("the main display is at %d,%d", d.X, d.Y)
		}
	}
}
