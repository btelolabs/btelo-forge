package render

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/anim"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
)

func TestEncoderPrefersTheCardOnlyWhenAsked(t *testing.T) {
	all := media.Encoders{"libx264": true, "libx265": true, "libaom-av1": true, "h264_nvenc": true}
	for _, c := range []struct {
		codec    project.Codec
		hardware bool
		enc      media.Encoders
		want     string
	}{
		{project.H264, false, all, "libx264"},
		{project.H264, true, all, "h264_nvenc"},
		{project.HEVC, true, all, "libx265"},
		{project.AV1, false, all, "libaom-av1"},
		{project.AV1, false, media.Encoders{"libsvtav1": true, "libaom-av1": true}, "libsvtav1"},
		{project.AV1, false, media.Encoders{"libx264": true}, ""},
	} {
		if got := Encoder(c.codec, c.hardware, c.enc); got != c.want {
			t.Errorf("Encoder(%v, %v) = %q, want %q", c.codec, c.hardware, got, c.want)
		}
	}
}

func TestOutputSizeCapsTheShortSide(t *testing.T) {
	if w, h := OutputSize(project.Canvas{Width: 1080, Height: 1920}, 720); w != 720 || h != 1280 {
		t.Fatalf("portrait %d×%d", w, h)
	}
	if w, h := OutputSize(project.Canvas{Width: 1920, Height: 1080}, 2160); w != 1920 || h != 1080 {
		t.Fatalf("upscaled to %d×%d", w, h)
	}
	if r := OutputRate(project.Canvas{FPS: 25}, project.Export{FPS: 30}); r != 25 {
		t.Fatalf("raised the rate to %v", r)
	}
}

func TestTargetSizeLeavesRoomForSoundAndRefusesTooLittle(t *testing.T) {
	kbps, err := videoBitrate(10, 60, 128, true)
	if err != nil {
		t.Fatal(err)
	}
	if total := float64(kbps+128) * 1000 * 60 / 8; total > 10*(1<<20) || total < 9*(1<<20) {
		t.Fatalf("%d kbit/s fills %.1f MB of 10", kbps, total/(1<<20))
	}
	_, err = videoBitrate(1, 600, 128, true)
	var small *TargetTooSmall
	if !errors.As(err, &small) || !errors.Is(err, ErrTargetTooSmall) || small.MinimumMB <= 1 {
		t.Fatalf("%v", err)
	}
	if again, err := videoBitrate(small.MinimumMB, 600, 128, true); err != nil || again < minVideoKbps {
		t.Fatalf("the suggested minimum %.1f MB is refused too: %v", small.MinimumMB, err)
	}
}

func planFor(t *testing.T, e project.Export, enc media.Encoders) *Job {
	t.Helper()
	p := project.New(project.Source{Path: "in.mp4", Duration: 10, Width: 1920, Height: 1080, FPS: 30, HasVideo: true, HasAudio: true}, e)
	job, err := PlanExport(p, nil, "out.mp4", enc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(job.Close)
	return job
}

func hasArgs(args []string, want ...string) bool {
	for i := range args {
		if i+len(want) <= len(args) && slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func TestPlansFollowEverySetting(t *testing.T) {
	all := media.Encoders{"libx264": true, "libx265": true, "libaom-av1": true, "hevc_nvenc": true}
	job := planFor(t, project.Export{Height: 720, FPS: 24, Quality: project.Smallest, Codec: project.HEVC, Speed: project.Slowest, Audio: project.AudioVoice}, all)
	last := job.Passes[0]
	if len(job.Passes) != 1 || !hasArgs(last, "-c:v", "libx265") || !hasArgs(last, "-crf", "39") || !hasArgs(last, "-preset", "veryslow") ||
		!hasArgs(last, "-b:a", "64k", "-ac", "1") || !hasArgs(last, "-tag:v", "hvc1") || !strings.Contains(strings.Join(last, " "), "scale=1280:720") ||
		!strings.Contains(strings.Join(last, " "), "fps=24") {
		t.Fatalf("%q", last)
	}
	job = planFor(t, project.Export{Audio: project.AudioNone, Codec: project.HEVC, Hardware: true}, all)
	if last := job.Passes[0]; !hasArgs(last, "-c:v", "hevc_nvenc") || !hasArgs(last, "-cq", "21") || hasArgs(last, "-c:a", "aac") {
		t.Fatalf("%q", last)
	}
	job = planFor(t, project.Export{TargetMB: 5}, all)
	if len(job.Passes) != 2 || !hasArgs(job.Passes[0], "-pass", "1") || !hasArgs(job.Passes[1], "-pass", "2") ||
		!hasArgs(job.Passes[0], "-an") || job.Dir == "" || hasArgs(job.Passes[1], "-crf", "23") {
		t.Fatalf("%q", job.Passes)
	}
	job = planFor(t, project.Export{TargetMB: 5, Codec: project.HEVC}, all)
	if !strings.Contains(strings.Join(job.Passes[0], " "), "pass=1:stats=pass.x265") {
		t.Fatalf("%q", job.Passes[0])
	}
	p := project.New(project.Source{Path: "in.mp4", Duration: 10, Width: 640, Height: 360, FPS: 30, HasVideo: true}, project.Export{Codec: project.AV1})
	if _, err := PlanExport(p, nil, "out.mp4", media.Encoders{"libx264": true}); !errors.Is(err, ErrEncoderMissing) {
		t.Fatalf("%v", err)
	}
}

// detailed writes a clip with fine moving detail, so the encoders have something to compress.
func detailed(t *testing.T, dir, seconds string) project.Source {
	path := filepath.Join(dir, "detail.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=s=640x360:r=30:d="+seconds, "-f", "lavfi", "-i", "anoisesrc=d="+seconds+":a=0.1",
		"-c:v", "libx264", "-preset", "ultrafast", "-qp", "10", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
	info, err := media.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return project.Source{Path: path, Duration: info.Duration, Width: info.Width, Height: info.Height, FPS: info.FPS, HasVideo: true, HasAudio: info.HasAudio}
}

func TestExportsEncodeEveryAvailableFormat(t *testing.T) {
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	enc, err := media.AvailableEncoders()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := detailed(t, dir, "2")
	cases := []struct {
		name  string
		e     project.Export
		codec string
	}{
		{"h264", project.Export{Speed: project.Fast}, "h264"},
		{"h265-small", project.Export{Codec: project.HEVC, Quality: project.Smallest, Height: 240, FPS: 15, Audio: project.AudioVoice, Speed: project.Fast}, "hevc"},
		{"av1", project.Export{Codec: project.AV1, Quality: project.Smallest, Speed: project.Fast, Height: 240}, "av1"},
		{"h264-gpu", project.Export{Hardware: true}, "h264"},
		{"h265-gpu", project.Export{Codec: project.HEVC, Hardware: true, Quality: project.Smallest}, "hevc"},
		{"av1-gpu", project.Export{Codec: project.AV1, Hardware: true}, "av1"},
		{"h264-gpu-size", project.Export{Hardware: true, TargetMB: 0.3}, "h264"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name := Encoder(c.e.Codec, c.e.Hardware, enc)
			if name == "" || c.e.Hardware && !strings.HasSuffix(name, "_nvenc") {
				t.Skipf("no encoder for %s on this computer", c.name)
			}
			p := project.New(src, c.e)
			out := filepath.Join(dir, c.name+".mp4")
			if err := Encode(context.Background(), p, nil, out, "", nil); err != nil {
				t.Fatal(err)
			}
			info, err := media.Probe(context.Background(), out)
			if err != nil {
				t.Fatal(err)
			}
			wantH := 360
			if c.e.Height > 0 {
				wantH = c.e.Height
			}
			if info.Height != wantH || math.Abs(info.Duration-2) > 0.2 || (c.e.FPS > 0 && math.Abs(info.FPS-float64(c.e.FPS)) > 0.01) {
				t.Fatalf("%+v", info)
			}
			if got := codecOf(t, out); got != c.codec {
				t.Fatalf("codec %s, want %s", got, c.codec)
			}
		})
	}
}

func TestTargetSizeExportsLandNearTheTarget(t *testing.T) {
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	enc, err := media.AvailableEncoders()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := detailed(t, dir, "6")
	for _, codec := range []project.Codec{project.H264, project.HEVC} {
		if Encoder(codec, false, enc) == "" {
			continue
		}
		e := project.Export{Codec: codec, TargetMB: 0.6, Speed: project.Fast, Audio: project.AudioLow}
		out := filepath.Join(dir, codecIDsForTest[codec]+".mp4")
		var progress []float64
		if err := Encode(context.Background(), project.New(src, e), nil, out, "", func(f float64) { progress = append(progress, f) }); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if mb := float64(st.Size()) / (1 << 20); mb > 0.6*1.05 || mb < 0.6*0.7 {
			t.Fatalf("%s: %.2f MB for a 0.6 MB target", codecIDsForTest[codec], mb)
		}
		if !slices.IsSorted(progress) || progress[len(progress)-1] != 1 {
			t.Fatalf("progress %v", progress)
		}
	}
}

var codecIDsForTest = map[project.Codec]string{project.H264: "h264", project.HEVC: "h265"}

func codecOf(t *testing.T, path string) string {
	t.Helper()
	cmd, err := media.Command(context.Background(), "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name", "-of", "csv=p=0", path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestAnimationPlans(t *testing.T) {
	enc := media.Encoders{"libx264": true, "gif": true, "libwebp_anim": true}
	// Settings only a video uses are ignored: the animation is silent and aims for no size.
	gifJob := planFor(t, project.Export{Codec: project.GIF, Quality: project.High, FPS: 60, Height: 480, TargetMB: 25, Audio: project.AudioHigh}, enc)
	if len(gifJob.Passes) != 2 || gifJob.Blind != 1 || gifJob.Dir == "" {
		t.Fatalf("%d passes, %d blind", len(gifJob.Passes), gifJob.Blind)
	}
	measure, final := strings.Join(gifJob.Passes[0], " "), strings.Join(gifJob.Passes[1], " ")
	if !strings.Contains(measure, "palettegen=max_colors=256:stats_mode=full[pal]") || !hasArgs(gifJob.Passes[0], "-frames:v", "1", paletteFile) {
		t.Fatalf("%s", measure)
	}
	if !strings.Contains(final, "[1:v]paletteuse=dither=floyd_steinberg:diff_mode=rectangle[gif]") || !hasArgs(gifJob.Passes[1], "-i", paletteFile) ||
		!hasArgs(gifJob.Passes[1], "-f", "gif", "out.mp4") || strings.Contains(final, "aac") || !strings.Contains(final, "scale=854:480") {
		t.Fatalf("%s", final)
	}
	webpJob := planFor(t, project.Export{Codec: project.WebP, Quality: project.Smallest, Speed: project.Slowest, Hardware: true}, enc)
	if len(webpJob.Passes) != 1 || webpJob.Blind != 0 || !hasArgs(webpJob.Passes[0], "-c:v", "libwebp_anim") ||
		!hasArgs(webpJob.Passes[0], "-quality", "50") || !hasArgs(webpJob.Passes[0], "-compression_level", "6") ||
		!hasArgs(webpJob.Passes[0], "-loop", "0") || strings.Contains(strings.Join(webpJob.Passes[0], " "), "aac") {
		t.Fatalf("%q", webpJob.Passes[0])
	}
	if got := Extension(project.Export{Codec: project.WebP}); got != ".webp" {
		t.Fatal(got)
	}
	if r := OutputRate(project.Canvas{FPS: 60}, project.Export{Codec: project.WebP}); r != 60 {
		t.Fatalf("a WebP keeps 60 fps, got %v", r)
	}
	if r := OutputRate(project.Canvas{FPS: 60}, project.Export{Codec: project.GIF}); r != maxGIFRate {
		t.Fatalf("a GIF plays at most %d fps, got %v", maxGIFRate, r)
	}
}

func TestAnimationsEncode(t *testing.T) {
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	enc, err := media.AvailableEncoders()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := detailed(t, dir, "2")
	for _, codec := range []project.Codec{project.GIF, project.WebP} {
		if Encoder(codec, false, enc) == "" {
			t.Logf("no encoder for %v on this computer", codec)
			continue
		}
		e := project.Export{Codec: codec, Height: 180, FPS: 10, Quality: project.Balanced}
		out := filepath.Join(dir, "anim"+Extension(e))
		var progress []float64
		if err := Encode(context.Background(), project.New(src, e), nil, out, "", func(f float64) { progress = append(progress, f) }); err != nil {
			t.Fatal(err)
		}
		if codec == project.GIF && (progress[0] != Analysing || !slices.IsSorted(progress[1:])) || progress[len(progress)-1] != 1 {
			t.Fatalf("progress %v", progress)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		a, err := anim.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		if a.W != 320 || a.H != 180 || a.Len() != 20 || a.Duration() != 2*time.Second {
			t.Fatalf("%v: %d×%d, %d frames, %v", codec, a.W, a.H, a.Len(), a.Duration())
		}
	}
}
