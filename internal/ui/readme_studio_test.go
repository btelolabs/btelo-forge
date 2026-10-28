package ui

import (
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// TestReadmeStudioScreens renders the README's pictures of drawing and recording into FORGE_README_OUT, in English
// and Chinese: a poster drawn on an artboard of the infinite canvas (pixels, vectors, text, effects and an adjustment
// layer) with the node tool on a curve, the Layer Style dialog, and the recording dialog.
func TestReadmeStudioScreens(t *testing.T) {
	out := os.Getenv("FORGE_README_OUT")
	if out == "" {
		t.Skip("set FORGE_README_OUT to a folder to render the README screenshots")
	}
	for _, lang := range []Language{English, Chinese} {
		x := newHarness(t)
		x.c.Resize(1920, 1280)
		x.scale = 1.25
		x.a.u.lang = lang
		x.a.settings.Language = lang
		x.a.settings.ExportDir = x.mediaDir()
		save := func(name string) {
			x.frame(Input{})
			x.frame(Input{})
			f, err := os.Create(filepath.Join(out, string(lang)+"-"+name+".jpg"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := jpeg.Encode(f, x.c.Img, &jpeg.Options{Quality: 86}); err != nil {
				t.Fatal(err)
			}
		}
		hills, title := readmePoster(t, x, lang)
		ad := x.a.art
		ad.d.Choose(hills)
		x.a.setArtTool(toolNode)
		ad.nodes = &nodeEdit{layer: hills, sel: []paint.NodeRef{{Node: 2}}, grab: nodeGrabNone}
		ad.fitView()
		x.settle(10 * time.Second)
		save("canvas")

		ad.d.Choose(title)
		x.a.openStyle(ad)
		x.a.style.page = effectStroke
		save("style")
		x.a.closeStyle(false)
	}
	// The recording dialog needs a screen to record; where there is none (a machine without one, or another
	// system), the README keeps the picture it has.
	for _, lang := range []Language{English, Chinese} {
		x := recordingHarness(t)
		x.c.Resize(1920, 1280)
		x.scale = 1.25
		x.a.u.lang = lang
		x.a.settings.ExportDir = filepath.Join(realHome, "Videos")
		x.click(x.recordCard())
		x.pump(time.Minute, func() bool { return x.a.rec.listed })
		x.a.settings.Record.Microphone = true
		x.frame(Input{})
		x.frame(Input{})
		f, err := os.Create(filepath.Join(out, string(lang)+"-record.jpg"))
		if err != nil {
			t.Fatal(err)
		}
		err = jpeg.Encode(f, x.c.Img, &jpeg.Options{Quality: 86})
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

// readmePoster draws a small poster on an artboard: a sky gradient painted in pixels, a sun with a glow, two
// hills drawn as curves, a title with a shadow and a stroke, and a warming adjustment layer. It returns the front
// hill's and the title's layers.
func readmePoster(t *testing.T, x *harness, lang Language) (hills, title int) {
	t.Helper()
	a := x.a
	if err := a.newCanvas(); err != nil {
		t.Fatal(err)
	}
	ad := a.art
	d := ad.d
	names := map[string]string{"board": "Poster", "sky": "Sky", "sun": "Sun", "far": "Far hills", "near": "Hills", "warm": "Warmth"}
	if lang == Chinese {
		names = map[string]string{"board": "海报", "sky": "天空", "sun": "太阳", "far": "远山", "near": "山丘", "warm": "暖色"}
	}
	const w, h = 1200, 760
	board := image.Rect(0, 0, w, h)
	b, err := d.AddBoard(board, names["board"])
	if err != nil {
		t.Fatal(err)
	}
	b.Fill = color.NRGBA{R: 19, G: 22, B: 40, A: 255}
	d.SetBoard(b, "Artboard")
	d.Rename(d.Active, names["sky"])
	d.Select(board)
	if err := d.DrawGradient(paint.Gradient{Kind: paint.GradientLinear, From: [2]float64{0, 0}, To: [2]float64{0, h * 0.8},
		Start: color.NRGBA{R: 28, G: 34, B: 78, A: 255}, End: color.NRGBA{R: 246, G: 138, B: 82, A: 255}, Opacity: 1, Dither: true}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()

	solid := func(c color.NRGBA) paint.VectorPaint { return paint.VectorPaint{Kind: paint.PaintSolid, Color: c} }
	sun := d.AddVector(paint.Vector{Kind: paint.VectorEllipse, Size: paint.Pt{X: 220, Y: 220}, M: paint.Translate(760, 210),
		Fill: solid(color.NRGBA{R: 255, G: 214, B: 102, A: 255})}, names["sun"])
	glow := paint.DefaultOuterGlow()
	glow.Color, glow.Size, glow.Opacity = color.NRGBA{R: 255, G: 190, B: 90, A: 255}, 60, 0.9
	if err := d.SetStyle(sun.ID, &paint.LayerStyle{OuterGlow: &glow}); err != nil {
		t.Fatal(err)
	}
	hill := func(name string, c color.NRGBA, pts ...[2]float64) *paint.Node {
		var ps []paint.Pt
		for _, p := range pts {
			ps = append(ps, paint.Pt{X: p[0], Y: p[1]})
		}
		path := paint.FitPath(ps, false, 0.5)
		// Closed along the bottom of the board, below the curve.
		last := ps[len(ps)-1]
		for _, p := range []paint.Pt{{X: last.X, Y: h}, {X: ps[0].X, Y: h}} {
			path.Nodes = append(path.Nodes, paint.PathNode{P: p, In: p, Out: p})
		}
		path.Closed = true
		return d.AddVector(paint.Vector{Kind: paint.VectorCurve, M: paint.Identity, Path: []paint.Subpath{path}, Fill: solid(c)}, name)
	}
	curve := func(y0, amp, phase float64) [][2]float64 {
		var pts [][2]float64
		for i := 0; i <= 40; i++ {
			px := float64(i) * w / 40
			pts = append(pts, [2]float64{px, y0 - amp*math.Sin(px/w*3.4+phase)})
		}
		return pts
	}
	hill(names["far"], color.NRGBA{R: 76, G: 52, B: 92, A: 255}, curve(500, 70, 0.6)...)
	near := hill(names["near"], color.NRGBA{R: 30, G: 44, B: 58, A: 255}, curve(600, 60, 2.2)...)

	text := paint.Text{Text: "BTELO FORGE", X: 80, Y: 90, Size: 112, Bold: true, Color: color.NRGBA{R: 255, G: 246, B: 230, A: 255}}
	ts, err := d.BeginText(0)
	if err != nil {
		t.Fatal(err)
	}
	ts.Set(text, a.renderText(text))
	ts.Commit()
	titleID := ts.Layer().ID
	shadow := paint.DefaultDropShadow()
	shadow.Distance, shadow.Size, shadow.Opacity = 8, 14, 0.6
	stroke := paint.DefaultOutline()
	stroke.Color, stroke.Size = color.NRGBA{R: 232, G: 108, B: 52, A: 255}, 4
	if err := d.SetStyle(titleID, &paint.LayerStyle{DropShadow: &shadow, Stroke: &stroke}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAdjustment(paint.Adjustment{Kind: paint.AdjustHue, Values: []float64{-4, 12, 0}}, names["warm"]); err != nil {
		t.Fatal(err)
	}
	d.EditMask = false
	ad.changedAll()
	return near.ID, titleID
}
