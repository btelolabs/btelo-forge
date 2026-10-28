package mind

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// fakePictures answers by kind: pictures are a gradient, nested maps a cycle, videos missing.
type fakePictures struct{ img *image.RGBA }

func (f fakePictures) Picture(n *Node, px int) (*image.RGBA, PictureState, bool) {
	switch n.Kind {
	case Nested:
		return nil, PicCycle, false
	case Video:
		return nil, PicMissing, false
	}
	return f.img, PicReady, false
}

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 180, 255})
		}
	}
	return img
}

// sampleBoard is a mind map, a flowchart, a table and things shown by reference, side by side.
func sampleBoard(t testing.TB, ms Measurer) *Map {
	m := New("Sample", ms)
	root, _ := m.Add(Node{Text: "Product launch 产品发布"}, 0, -1)
	for i, s := range []string{"Goals", "Audience", "Channels", "Timeline", "Budget", "Risks"} {
		c, _ := m.Add(Node{Text: s}, root, -1)
		for k := range i%3 + 1 {
			m.Add(Node{Text: []string{"First idea", "Second thought with more words", "三"}[k]}, c, -1)
		}
	}
	start, _ := m.Add(Node{Text: "Start", Shape: ShapeCapsule, X: 900, Y: -260}, 0, -1)
	step, _ := m.Add(Node{Text: "Collect feedback", Shape: ShapeRounded, X: 880, Y: -140}, 0, -1)
	ask, _ := m.Add(Node{Text: "Good enough?", Shape: ShapeDiamond, X: 880, Y: -20}, 0, -1)
	ship, _ := m.Add(Node{Text: "Ship it", Shape: ShapeParallelogram, X: 1180, Y: 10}, 0, -1)
	m.AddEdge(Edge{From: start, To: step})
	m.AddEdge(Edge{From: step, To: ask})
	m.AddEdge(Edge{From: ask, To: ship, Label: "yes", Route: RouteElbow})
	m.AddEdge(Edge{From: ask, To: step, Label: "no", Dashed: true, FromSide: SideLeft, ToSide: SideLeft})
	tb, _ := m.Add(Node{Kind: Table, X: 880, Y: 200, Cells: &Grid{Rows: 4, Cols: 3, Cells: []string{
		"Item", "Q1", "Q2", "Ads", "1200", "1500", "Events", "800", "950", "Total", "=SUM(B2:B3)", "=SUM(C2:C3)"}}}, 0, -1)
	m.AddEdge(Edge{From: root, To: tb, Route: RouteCurve, Arrow: ArrowBoth})
	m.Add(Node{Kind: Picture, Ref: "photo.png", Text: "A picture", X: -700, Y: 260}, 0, -1)
	m.Add(Node{Kind: Nested, Ref: "self.btmind", X: -400, Y: 260}, 0, -1)
	m.Add(Node{Kind: Video, Ref: "clip.mp4", X: -60, Y: 300}, 0, -1)
	org, _ := m.Add(Node{Text: "Team", Layout: LayoutDown, X: -700, Y: -420}, 0, -1)
	for _, s := range []string{"Design", "Code", "Sales"} {
		c, _ := m.Add(Node{Text: s}, org, -1)
		m.Add(Node{Text: "Lead"}, c, -1)
	}
	return m
}

func systemFonts(t testing.TB) gfx.Fonts {
	f, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skipf("no system fonts: %v", err)
	}
	return f
}

// TestRenderSample draws the sample board; set FORGE_SHOTS to a folder to see it.
func TestRenderSample(t *testing.T) {
	fonts := systemFonts(t)
	m := sampleBoard(t, FontMeasurer{fonts})
	o := &Options{Fonts: fonts, Pictures: fakePictures{gradient(320, 240)}, Labels: Labels{Missing: "File not found", Cycle: "Holds the map it is in", TooDeep: "Too deep"}, Paper: true}
	for _, z := range []float64{1, 0.35, 0.05} {
		m.Layout()
		b := m.Bounds()
		c := gfx.NewCanvas(1600, 1000)
		v := View{Zoom: z, OX: 800 - b.CX()*z, OY: 500 - b.CY()*z}
		start := time.Now()
		Render(c, m, v, o)
		t.Logf("zoom %v drawn in %v", z, time.Since(start))
		if dir := os.Getenv("FORGE_SHOTS"); dir != "" {
			f, err := os.Create(filepath.Join(dir, "mind-sample-"+string(rune('a'+int(z*10)))+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, c.Img)
			f.Close()
		}
	}
}

// TestRenderHugeBoardIsBounded draws 300 000 nodes zoomed all the way out: it costs what the screen shows.
func TestRenderHugeBoardIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("large map")
	}
	fonts := systemFonts(t)
	m, _ := bigMap(t, 300_000)
	m.measure = FontMeasurer{fonts}
	m.reindex()
	start := time.Now()
	m.Layout()
	t.Logf("laid out in %v", time.Since(start))
	b := m.Bounds()
	c := gfx.NewCanvas(1920, 1080)
	z := math.Min(1920/b.W(), 1080/b.H())
	start = time.Now()
	Render(c, m, View{Zoom: z, OX: -b.X0 * z, OY: -b.Y0 * z}, &Options{Fonts: fonts, Paper: true})
	whole := time.Since(start)
	t.Logf("the whole board (%v) drawn in %v", b, whole)
	if whole > 400*time.Millisecond {
		t.Fatalf("drawing the whole board took %v", whole)
	}
}
