package capture

import (
	"bufio"
	"context"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/media"
)

// TestSoundStaysOnThePicture plays a clip whose screen flashes white exactly as it beeps, records it with a pause in
// the middle, and checks that every flash in the recording comes with its beep. It needs speakers (or any output
// device) and ffplay, so it runs only when BVC_SYNC_CLIP names such a clip.
func TestSoundStaysOnThePicture(t *testing.T) {
	clip := os.Getenv("BVC_SYNC_CLIP")
	if clip == "" {
		t.Skip("set BVC_SYNC_CLIP to a clip that flashes and beeps together")
	}
	d, e := recordable(t)
	r, err := Start(Options{Display: d, FPS: 60, SystemAudio: true, Encoder: e, Path: t.TempDir() + "/sync.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	play := exec.Command("ffplay", "-v", "error", "-autoexit", "-left", "200", "-top", "200", clip)
	if err := play.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	r.Pause()
	time.Sleep(time.Second)
	r.Resume()
	play.Wait()
	time.Sleep(500 * time.Millisecond)
	out, err := r.Stop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flashes := onsets(t, out, "-vf", "signalstats,metadata=print:key=lavfi.signalstats.YAVG", "-an")
	beeps := onsets(t, out, "-af", "asetnsamples=240,astats=metadata=1:reset=1,ametadata=print:key=lavfi.astats.Overall.RMS_level", "-vn")
	t.Logf("flashes %v", flashes)
	t.Logf("beeps   %v", beeps)
	if len(flashes) < 6 || len(beeps) < 6 {
		t.Fatalf("found %d flashes and %d beeps", len(flashes), len(beeps))
	}
	// Each flash is matched with the nearest beep; the screen shows a frame a little after the sound card plays its
	// sound, so the picture may trail by up to a few frames, never lead.
	for _, f := range flashes {
		nearest := math.Inf(1)
		for _, b := range beeps {
			if math.Abs(f-b) < math.Abs(nearest) {
				nearest = f - b
			}
		}
		if nearest < -0.02 || nearest > 0.07 {
			t.Errorf("the flash at %.3fs is %+.0f ms from its beep", f, nearest*1000)
		}
	}
}

// onsets lists the times where the measured value jumps up.
func onsets(t *testing.T, path, flag, filter, drop string) []float64 {
	t.Helper()
	cmd, err := media.Command(context.Background(), "ffmpeg", "-v", "info", "-i", path, drop, flag, filter, "-f", "null", "-")
	if err != nil {
		t.Fatal(err)
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	at := regexp.MustCompile(`pts_time:([0-9.]+)`)
	value := regexp.MustCompile(`=(-?[0-9.]+|-inf)$`)
	var times []float64
	var now, last float64
	seen := false
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		if m := at.FindStringSubmatch(line); m != nil {
			now, _ = strconv.ParseFloat(m[1], 64)
			continue
		}
		if m := value.FindStringSubmatch(line); m != nil {
			v, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				v = -200
			}
			if seen && v-last > 4 && (len(times) == 0 || now-times[len(times)-1] > 0.5) {
				times = append(times, now)
			}
			last, seen = v, true
		}
	}
	cmd.Wait()
	return times
}
