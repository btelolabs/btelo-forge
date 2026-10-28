package render

import (
	"context"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
)

func TestAtempoStaysInItsCleanRange(t *testing.T) {
	for speed, want := range map[float64]string{1: "", 2: "atempo=2", 4: "atempo=2,atempo=2", 3: "atempo=2,atempo=1.500000", 0.25: "atempo=0.5,atempo=0.5", 0.75: "atempo=0.750000"} {
		if got := atempo(speed); got != want {
			t.Errorf("atempo(%v) = %q, want %q", speed, got, want)
		}
	}
}

func src(path string, seconds float64, audio bool) project.Source {
	return project.Source{Path: path, Duration: seconds, Width: 640, Height: 360, FPS: 30, HasVideo: true, HasAudio: audio}
}

func TestCutTakesEveryTrackInsideTheRange(t *testing.T) {
	p := project.New(src("a.mp4", 10, true), project.Export{})
	p.SplitClip(p.Video[0].Clips[0].ID, 4)
	p.SetSpeed(p.Video[0].Clips[1].ID, 2)           // main: 0–4 at 1×, 4–7 at 2×
	p.Place(p.AddSource(src("b.mp4", 3, false)), 6) // 6–9, past the main track
	p.SetTrackVolume(false, 0, 0.5, false)
	id := p.AddMusic("m.mp3", "m", "", 100, 1)
	k, i, _ := p.FindMusic(id)
	p.Music[k].Clips[i].Length = 5 // 1–6
	w := cut(p, 3, 8)
	if len(w.main) != 2 || math.Abs(w.main[0].In-3) > 1e-9 || math.Abs(w.main[1].Out-10) > 1e-9 || math.Abs(w.mainLength-4) > 1e-9 {
		t.Fatalf("main %+v length %v", w.main, w.mainLength)
	}
	if w.main[0].Gain != 0.5 || !w.main[0].Sound {
		t.Fatalf("main gain %+v", w.main[0])
	}
	if math.Abs(w.length-5) > 1e-9 {
		t.Fatalf("length %v", w.length)
	}
	if len(w.upper) != 1 || math.Abs(w.upper[0][0].Delay-3) > 1e-9 || math.Abs(w.upper[0][0].Out-2) > 1e-9 || w.upper[0][0].Sound {
		t.Fatalf("upper %+v", w.upper)
	}
	c := w.cues[0]
	if math.Abs(c.Offset-2) > 1e-9 || c.Delay != 0 || math.Abs(c.Length-3) > 1e-9 || c.FadeIn || !c.FadeOut {
		t.Fatalf("cue %+v", c)
	}
	p.Music[0].Muted = true
	if cut(p, 3, 8).cues != nil {
		t.Fatal("muted music track still plays")
	}
}

func TestDestinationNumbersExistingCuts(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "旅行 vlog.mov")
	first := Destination(source, "", ".mp4")
	if first != filepath.Join(dir, "旅行 vlog.cut.mp4") {
		t.Fatal(first)
	}
	os.WriteFile(first, nil, 0o644)
	if second := Destination(source, "", ".mp4"); filepath.Base(second) != "旅行 vlog.cut-2.mp4" {
		t.Fatal(second)
	}
	other := t.TempDir()
	if got := Destination(source, other, ".mp4"); got != filepath.Join(other, "旅行 vlog.cut.mp4") {
		t.Fatal(got)
	}
}

func TestOutputSizeIsEvenAndNeverUpscales(t *testing.T) {
	c := project.Canvas{Width: 1921, Height: 1081}
	if w, h := OutputSize(c, 0); w != 1922 || h != 1082 {
		t.Fatalf("%d×%d", w, h)
	}
	if w, h := OutputSize(c, 720); w != 1280 || h != 720 {
		t.Fatalf("%d×%d", w, h)
	}
	if w, h := OutputSize(project.Canvas{Width: 640, Height: 360}, 1080); w != 640 || h != 360 {
		t.Fatalf("upscaled to %d×%d", w, h)
	}
}

func TestProgressLines(t *testing.T) {
	if f, ok := progressLine("out_time_us=5000000", 10); !ok || f != 0.5 {
		t.Fatal(f, ok)
	}
	if _, ok := progressLine("out_time_us=N/A", 10); ok {
		t.Fatal("accepted N/A")
	}
}

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	cmd, err := media.Command(context.Background(), "ffmpeg", append([]string{"-v", "error", "-y"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v %s", args, err, out)
	}
}

// solid writes a clip of one colour, with a tone when audio is set.
func solid(t *testing.T, dir, name, colour, size, seconds string, audio bool) project.Source {
	path := filepath.Join(dir, name)
	args := []string{"-f", "lavfi", "-i", "color=c=" + colour + ":s=" + size + ":r=30:d=" + seconds}
	if audio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration="+seconds, "-c:a", "aac")
	}
	ffmpeg(t, append(args, "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", path)...)
	info, err := media.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return project.Source{Path: path, Duration: info.Duration, Width: info.Width, Height: info.Height, FPS: info.FPS, HasVideo: true, HasAudio: info.HasAudio}
}

// colourAt names the colour of one pixel of path at time t, on a 160×90 picture.
func colourAt(t *testing.T, path string, at float64, x, y int) string {
	t.Helper()
	img, err := media.Frame(context.Background(), path, at, 160, 90)
	if err != nil {
		t.Fatal(err)
	}
	c := img.RGBAAt(x, y)
	r, g, b := int(c.R), int(c.G), int(c.B)
	switch {
	case r > 150 && g < 80 && b < 80:
		return "red"
	case g > 100 && r < 80 && b < 80:
		return "green"
	case b > 150 && r < 80 && g < 80:
		return "blue"
	case r < 40 && g < 40 && b < 40:
		return "black"
	case r > 200 && g > 200 && b > 200:
		return "white"
	}
	return "mixed"
}

func TestEncodeStacksTracksAppendsSourcesAndMixes(t *testing.T) {
	dir := t.TempDir()
	red := solid(t, dir, "red.mp4", "red", "320x180", "4", true)
	green := solid(t, dir, "green.mp4", "green", "180x180", "2", false)
	blue := solid(t, dir, "blue.mp4", "blue", "320x180", "1", true)
	tone := filepath.Join(dir, "tone.mp3")
	ffmpeg(t, "-f", "lavfi", "-i", "sine=frequency=660:duration=10", tone)

	p := project.New(red, project.Export{Height: 90})
	p.SplitClip(p.Video[0].Clips[0].ID, 2)
	p.SetSpeed(p.Video[0].Clips[1].ID, 2) // red 0–2, red at 2× 2–3
	p.Append(p.AddSource(green))          // green 3–5, square, so black bars at the sides
	p.Place(p.AddSource(blue), 0.5)       // blue covers 0.5–1.5
	p.AddMusic(tone, "tone", "Tone by test", 10, 0)

	caption := filepath.Join(dir, "caption.png")
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	f, _ := os.Create(caption)
	png.Encode(f, img)
	f.Close()

	out := Destination(red.Path, "", ".mp4")
	var last float64
	err := Encode(context.Background(), p, []Overlay{{Path: caption, X: 0, Y: 0, Start: 3.2, End: 4}}, out, "Music:", func(f float64) { last = f })
	if err != nil {
		t.Fatal(err)
	}
	if last != 1 {
		t.Fatalf("progress ended at %v", last)
	}
	info, err := media.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(info.Duration-5) > 0.15 || !info.HasAudio || info.Height != 90 || info.Width != 160 {
		t.Fatalf("output %+v, want 5 s 160×90 with audio", info)
	}
	for _, c := range []struct {
		at   float64
		x, y int
		want string
	}{{0.2, 80, 45, "red"}, {1, 80, 45, "blue"}, {2.5, 80, 45, "red"}, {4.5, 80, 45, "green"}, {4.5, 5, 45, "black"}, {3.5, 3, 3, "white"}, {4.5, 3, 3, "black"}} {
		if got := colourAt(t, out, c.at, c.x, c.y); got != c.want {
			t.Errorf("at %v s pixel (%d,%d) is %s, want %s", c.at, c.x, c.y, got, c.want)
		}
	}
	credits, err := os.ReadFile(strings.TrimSuffix(out, ".mp4") + ".credits.txt")
	if err != nil || !strings.HasPrefix(string(credits), "Music:") || !strings.Contains(string(credits), "Tone by test") {
		t.Fatalf("credits %q %v", credits, err)
	}
	if _, err := os.Stat(out + ".part"); !os.IsNotExist(err) {
		t.Fatal("partial file left behind")
	}
}

func TestUpperClipsPastTheMainTrackPlayOverBlack(t *testing.T) {
	dir := t.TempDir()
	red := solid(t, dir, "red.mp4", "red", "320x180", "2", false)
	blue := solid(t, dir, "blue.mp4", "blue", "320x180", "2", false)
	p := project.New(red, project.Export{})
	p.Place(p.AddSource(blue), 3) // 3–5, with a black gap at 2–3
	out := Destination(red.Path, "", ".mp4")
	if err := Encode(context.Background(), p, nil, out, "", nil); err != nil {
		t.Fatal(err)
	}
	info, err := media.Probe(context.Background(), out)
	if err != nil || info.HasAudio || math.Abs(info.Duration-5) > 0.15 {
		t.Fatalf("export %+v %v", info, err)
	}
	if got := colourAt(t, out, 2.5, 80, 45); got != "black" {
		t.Fatalf("gap is %s", got)
	}
	if got := colourAt(t, out, 4, 80, 45); got != "blue" {
		t.Fatalf("upper clip past the main track is %s", got)
	}
}

func TestPreviewStreamsHaveTheRequestedSize(t *testing.T) {
	dir := t.TempDir()
	red := solid(t, dir, "red.mp4", "red", "320x180", "2", true)
	p := project.New(red, project.Export{})
	p.Place(p.AddSource(red), 1.5)
	g, err := Build(Spec{Project: p, From: 1, To: 3, Width: 64, Height: 36, FPS: 10, Video: true, PixFmt: "rgba"})
	if err != nil {
		t.Fatal(err)
	}
	args, err := g.Args("-f", "rawvideo", "pipe:1")
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := media.Command(context.Background(), "ffmpeg", args...)
	video, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if frames := len(video) / (64 * 36 * 4); frames < 19 || frames > 21 || len(video)%(64*36*4) != 0 {
		t.Fatalf("%d bytes = %d frames, want 20", len(video), frames)
	}
	g, err = Build(Spec{Project: p, From: 1, To: 3, Audio: true})
	if err != nil {
		t.Fatal(err)
	}
	args, _ = g.Args("-f", "s16le", "-ar", "48000", "-ac", "2", "pipe:1")
	cmd, _ = media.Command(context.Background(), "ffmpeg", args...)
	pcm, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if seconds := float64(len(pcm)) / 4 / 48000; math.Abs(seconds-2) > 0.03 {
		t.Fatalf("audio preview %v s, want 2", seconds)
	}
}

func TestOpacityDimsTheMainTrackAndShowsWhatIsBelow(t *testing.T) {
	dir := t.TempDir()
	red := solid(t, dir, "red.mp4", "red", "320x180", "2", false)
	blue := solid(t, dir, "blue.mp4", "blue", "320x180", "2", false)
	p := project.New(red, project.Export{})
	upper := p.Place(p.AddSource(blue), 0)
	p.SetOpacity([]int{upper}, 0.5)
	out := filepath.Join(dir, "mixed.mp4")
	if err := Encode(context.Background(), p, nil, out, "", nil); err != nil {
		t.Fatal(err)
	}
	img, err := media.Frame(context.Background(), out, 1, 160, 90)
	if err != nil {
		t.Fatal(err)
	}
	if c := img.RGBAAt(80, 45); c.R < 90 || c.R > 170 || c.B < 90 || c.B > 170 {
		t.Fatalf("half-transparent blue over red gave %v", c)
	}
	p.SetOpacity([]int{upper}, 0)
	p.SetOpacity([]int{p.Video[0].Clips[0].ID}, 0.5)
	out = filepath.Join(dir, "dim.mp4")
	if err := Encode(context.Background(), p, nil, out, "", nil); err != nil {
		t.Fatal(err)
	}
	if img, err = media.Frame(context.Background(), out, 1, 160, 90); err != nil {
		t.Fatal(err)
	}
	if c := img.RGBAAt(80, 45); c.R < 90 || c.R > 160 || c.B > 40 {
		t.Fatalf("half-faded red over black under an invisible clip gave %v", c)
	}
}
