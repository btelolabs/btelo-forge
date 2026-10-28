package ui

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
)

// TestFilesPieDemo records the README's animation of an analysed folder in both languages into FORGE_DEMO, as
// en-files-analyse.gif and zh-files-analyse.gif: the pie turning as the keys and the mouse move down the list, a
// click on a wedge, a folder opened, and a deletion asked for and called off. The folder is made of sparse files,
// which take no room on the disc, and nothing in it is deleted.
func TestFilesPieDemo(t *testing.T) {
	out := os.Getenv("FORGE_DEMO")
	if out == "" {
		t.Skip("set FORGE_DEMO to a folder to record the README's animation")
	}
	for _, lang := range []Language{English, Chinese} {
		t.Run(string(lang), func(t *testing.T) { recordPieDemo(t, lang, filepath.Join(out, string(lang)+"-files-analyse.gif")) })
	}
}

// demoFolder makes a folder of projects worth some 90 GB, every large file sparse, and a folder of pictures.
func demoFolder(t *testing.T, root string) []string {
	const gb = 1 << 30
	big := map[string]float64{
		"AI Models/flux1-dev-fp8.safetensors":       11.9,
		"AI Models/llama-3.1-8b-instruct-q8.gguf":   8.5,
		"AI Models/whisper-large-v3.bin":            3.1,
		"Unreal Game/Content/Paks/pakchunk0.pak":    11.2,
		"Unreal Game/DerivedDataCache/ddc.bin":      6.6,
		"Unreal Game/Saved/Autosaves/Harbour.umap":  2.1,
		"Unreal Game/Binaries/Win64/Harbour.exe":    0.9,
		"Recordings/2026-10-02 launch stream.mp4":   4.8,
		"Recordings/2026-10-19 tutorial.mp4":        3.1,
		"Recordings/2026-10-26 bug report.mp4":      0.6,
		"Datasets/imagenet-subset.tar":              4.2,
		"Datasets/speech-clips.tar":                 2.5,
		"Datasets/captions.parquet":                 0.4,
		"Ubuntu-24.04-desktop-amd64.iso":            5.8,
		"backup-2026-09.zip":                        3.4,
		"node_modules/.pnpm/esbuild/esbuild.exe":    0.5,
		"node_modules/.pnpm/typescript/lib/tsc.js":  0.3,
		"node_modules/.pnpm/electron/dist/app.asar": 0.6,
	}
	var all []string
	for rel, size := range big {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		sparseFile(t, p, int64(size*gb))
		all = append(all, p)
	}
	for name, body := range map[string]string{"notes.md": "# Notes\n\nShip the harbour level.\n", "todo.txt": "render the trailer\n"} {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		all = append(all, p)
	}
	renders := filepath.Join(root, "Renders")
	if err := os.MkdirAll(renders, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		p := filepath.Join(renders, fmt.Sprintf("harbour-%02d.png", i+1))
		demoPicture(t, p, i)
		all = append(all, p)
	}
	return all
}

// demoPicture paints a render of a harbour at dusk, its colours turning with i.
func demoPicture(t *testing.T, path string, i int) {
	c := gfx.NewCanvas(480, 300)
	hue := float64(i) / 12
	sky := func(f float64) [3]uint8 {
		h := hue + f*0.18
		return [3]uint8{uint8(120 + 110*math.Sin(2*math.Pi*h)), uint8(90 + 80*math.Sin(2*math.Pi*(h+0.33))), uint8(140 + 100*math.Sin(2*math.Pi*(h+0.66)))}
	}
	for y := 0; y < 300; y++ {
		c.Fill(gfx.Rect{Y: float32(y), W: 480, H: 1}, gfx.RGBA(sky(float64(y)/300)))
	}
	c.Circle(120+float32(i*22), 120, 34, gfx.Alpha(gfx.Hex(0xfff1c4), 0.9))
	c.Fill(gfx.Rect{Y: 200, W: 480, H: 100}, gfx.Hex(0x1d2a3a))
	for k := 0; k < 6; k++ {
		x := float32(30 + k*80 + (i*13)%40)
		c.Triangle(x, 200, x+28, 200, x+14, float32(150-(k*7+i*5)%40), gfx.Hex(0x0f1620))
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, c.Img); err != nil {
		t.Fatal(err)
	}
}

// demoRecorder draws the frames of an animation a thirtieth of a second apart on the pie's clock, the pointer over
// each, and writes them as numbered pictures.
type demoRecorder struct {
	x      *harness
	now    time.Time
	dir    string
	frames int
}

const demoFPS = 30

func (r *demoRecorder) frame(in Input) {
	r.now = r.now.Add(time.Second / demoFPS)
	r.x.frame(in)
	drawPointer(r.x.c, r.x.mx, r.x.my)
	f, err := os.Create(filepath.Join(r.dir, fmt.Sprintf("%05d.png", r.frames)))
	if err != nil {
		r.x.t.Fatal(err)
	}
	defer f.Close()
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(f, r.x.c.Img); err != nil {
		r.x.t.Fatal(err)
	}
	r.frames++
}

// hold records d of the window left alone.
func (r *demoRecorder) hold(d time.Duration) {
	for n := int(d.Seconds() * demoFPS); n > 0; n-- {
		r.frame(Input{})
	}
}

// key taps a key, then holds for d.
func (r *demoRecorder) key(code uint16, mods string, d time.Duration) {
	r.frame(Input{Keys: []Key{*key(code, mods)}})
	r.hold(d)
}

// glide moves the pointer to (x, y) over d, easing in and out as a hand does.
func (r *demoRecorder) glide(x, y float32, d time.Duration) {
	x0, y0 := r.x.mx, r.x.my
	n := max(int(d.Seconds()*demoFPS), 1)
	for i := 1; i <= n; i++ {
		f := float32(i) / float32(n)
		f = f * f * (3 - 2*f)
		r.x.mx, r.x.my = x0+(x-x0)*f, y0+(y-y0)*f
		r.frame(Input{})
	}
}

// click presses and lets go where the pointer is.
func (r *demoRecorder) click() {
	r.frame(Input{Down: true, Pointer: []Pointer{{X: r.x.mx, Y: r.x.my, Down: true}}})
	r.frame(Input{Pointer: []Pointer{{X: r.x.mx, Y: r.x.my}}})
}

// drawPointer draws an arrow pointer with its tip at (x, y), white with a dark edge, as the system draws it.
func drawPointer(c *gfx.Canvas, x, y float32) {
	pts := [][2]float32{{0, 0}, {0, 17}, {4, 13}, {7, 20}, {9.5, 19}, {6.5, 12}, {12, 12}}
	fan := func(dx, dy float32, col [3]uint8) {
		for i := 1; i+1 < len(pts); i++ {
			c.Triangle(x+dx, y+dy, x+pts[i][0]+dx, y+pts[i][1]+dy, x+pts[i+1][0]+dx, y+pts[i+1][1]+dy, gfx.RGBA(col))
		}
	}
	for _, d := range [][2]float32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-1, -1}, {1, 1}, {-1, 1}, {1, -1}} {
		fan(d[0], d[1], [3]uint8{12, 13, 16})
	}
	fan(0, 0, [3]uint8{255, 255, 255})
}

func recordPieDemo(t *testing.T, lang Language, gif string) {
	x := newHarness(t)
	x.a.u.lang = lang
	x.c = gfx.NewCanvas(1200, 760)
	// A folder of a fixed name, so the path bar reads as a demo rather than as a test's random folders.
	demo := filepath.Join(os.TempDir(), "Btelo Forge demo")
	if err := os.RemoveAll(demo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(demo) })
	root := filepath.Join(demo, "Projects")
	made := demoFolder(t, root)
	r := &demoRecorder{x: x, now: time.Unix(1_800_000_000, 0), dir: t.TempDir()}
	x.a.fm.clock = func() time.Time { return r.now }
	x.a.openFilesTab([]savedPane{{Dir: root}, {Dir: filepath.Join(root, "Renders"), Grid: true}})
	x.waitFiles()
	x.settle(20 * time.Second) // the pictures' thumbnails
	fd := x.a.files
	p := fd.panes[0]
	u := &x.a.u
	rowY := func(row int) float32 {
		l := x.a.layout()
		return l.body.Y + u.px(38) + (float32(row)-p.top+0.5)*u.fileRowH()
	}
	x.mx, x.my = u.px(360), rowY(1)
	x.frame(Input{})

	r.hold(900 * time.Millisecond)
	// Analyse: the list is sorted by size and the pie grows under it.
	r.frame(Input{Keys: []Key{*key('I', "")}})
	x.waitFiles()
	r.hold(1300 * time.Millisecond)
	// Down the list with the keys, the pie turning each item to twelve o'clock.
	for i := 0; i < 4; i++ {
		r.key('J', "", 650*time.Millisecond)
	}
	// Up the list with the mouse.
	for _, row := range []int{3, 2, 1, 0} {
		r.glide(u.px(380), rowY(row), 280*time.Millisecond)
		r.hold(420 * time.Millisecond)
	}
	// A click on a wedge goes to its item.
	w := x.a.pieWedges(p, x.a.paneAnalysis(p))
	m := &p.pie
	for _, k := range []int{4, 2} {
		a := (w[k].from+w[k].to)/2 + m.turn.pos
		rr := float64(m.radius) * 0.62
		r.glide(m.cx+float32(rr*math.Sin(a)), m.cy-float32(rr*math.Cos(a)), 550*time.Millisecond)
		r.hold(350 * time.Millisecond)
		r.click()
		r.hold(900 * time.Millisecond)
	}
	// Into the largest folder, which has a pie of its own, and back.
	r.glide(u.px(380), rowY(0), 500*time.Millisecond)
	r.hold(300 * time.Millisecond)
	r.frame(Input{Keys: []Key{*key('L', "")}})
	x.waitFiles()
	r.hold(900 * time.Millisecond)
	r.key('J', "", 700*time.Millisecond)
	r.key('J', "", 800*time.Millisecond)
	r.frame(Input{Keys: []Key{*key('H', "")}})
	x.waitFiles()
	r.hold(800 * time.Millisecond)
	// Delete asks first; Esc calls it off.
	r.key('D', "", 1600*time.Millisecond)
	if fd.confirm == nil {
		t.Fatal("d did not ask before deleting")
	}
	r.key(keyEscape, "", 1100*time.Millisecond)
	if fd.confirm != nil {
		t.Fatal("Esc did not call the deletion off")
	}
	for _, path := range made {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the demo lost %s: %v", path, err)
		}
	}

	cmd, err := media.Command(t.Context(), "ffmpeg", "-v", "error", "-y", "-framerate", fmt.Sprint(demoFPS),
		"-i", filepath.Join(r.dir, "%05d.png"),
		"-vf", "split[a][b];[a]palettegen=max_colors=192:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle",
		"-loop", "0", gif)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
}
