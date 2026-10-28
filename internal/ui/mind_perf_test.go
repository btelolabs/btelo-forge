package ui

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/mind"
)

// realisticMap is a map of about n nodes as people make them: several trees of uneven depth with words of every
// length in Chinese and English, free notes, flowchart boxes and connectors between them.
func realisticMap(measure mind.Measurer, n int) *mind.Map {
	rng := rand.New(rand.NewSource(7))
	words := []string{"产品", "用户", "增长", "复盘", "风险", "预算", "目标", "渠道", "Launch", "feedback", "pricing", "retention",
		"onboarding", "设计评审", "数据分析", "竞品调研", "marketing plan", "Q3 roadmap", "技术债务", "性能优化"}
	phrase := func() string {
		k := 1 + rng.Intn(6)
		s := ""
		for i := range k {
			if i > 0 {
				s += " "
			}
			s += words[rng.Intn(len(words))]
		}
		if rng.Intn(10) == 0 {
			s += "：这一条写得比较长，用来看换行之后的排版和测量是不是还快"
		}
		return s
	}
	m := mind.New("Perf", measure)
	m.Begin("build")
	var all []mind.ID
	trees := 12
	for t := range trees {
		layout := []mind.Layout{mind.LayoutMap, mind.LayoutRight, mind.LayoutDown, mind.LayoutList}[t%4]
		root, _ := m.Add(mind.Node{Text: fmt.Sprintf("主题 %d %s", t, phrase()), Layout: layout, X: float64(t%4) * 6000, Y: float64(t/4) * 9000}, 0, -1)
		all = append(all, root)
		queue := []mind.ID{root}
		for len(queue) > 0 && m.Len() < (t+1)*n*9/10/trees {
			p := queue[0]
			queue = queue[1:]
			for range 2 + rng.Intn(7) {
				id, _ := m.Add(mind.Node{Text: phrase()}, p, -1)
				all = append(all, id)
				if rng.Intn(4) != 0 {
					queue = append(queue, id)
				}
			}
		}
	}
	shapes := []mind.Shape{mind.ShapeRounded, mind.ShapeDiamond, mind.ShapeCapsule, mind.ShapeParallelogram, mind.ShapeNone}
	for m.Len() < n {
		id, _ := m.Add(mind.Node{Text: phrase(), Shape: shapes[rng.Intn(len(shapes))], X: rng.Float64()*24000 - 2000, Y: 30000 + rng.Float64()*8000}, 0, -1)
		all = append(all, id)
	}
	for range n / 40 {
		a, b := all[rng.Intn(len(all))], all[rng.Intn(len(all))]
		if a != b {
			m.AddEdge(mind.Edge{From: a, To: b, Route: mind.Route(rng.Intn(3)), Dashed: rng.Intn(3) == 0})
		}
	}
	m.Commit()
	return m
}

// frameTime draws one frame with the input and returns how long it took.
func (x *harness) frameTime(in Input) time.Duration {
	start := time.Now()
	x.frame(in)
	return time.Since(start)
}

// TestMindTwentyThousandNodes measures a realistic map of 20 000 nodes in the editor: opening it, drawing it
// whole, moving and zooming the view, typing and adding topics, saving and reading it again. Set MIND_PERF_NODES
// for another size.
func TestMindTwentyThousandNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("large map")
	}
	n := 20_000
	if s := os.Getenv("MIND_PERF_NODES"); s != "" {
		fmt.Sscan(s, &n)
	}
	x := newHarness(t)
	measure := x.a.mindMeasure()

	start := time.Now()
	m := realisticMap(measure, n)
	build := time.Since(start)
	path := filepath.Join(x.mediaDir(), "perf"+mind.Ext)
	start = time.Now()
	data, err := mind.Encode(m, filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	encode := time.Since(start)
	os.WriteFile(path, data, 0o644)

	// Opening: reading, laying out and the first frame with every tile of the whole board.
	start = time.Now()
	x.a.openMindFile(path)
	x.pump(time.Minute, func() bool { return x.a.board != nil })
	read := time.Since(start)
	md := x.a.board
	start = time.Now()
	x.frame(Input{})
	first := time.Since(start)
	x.settleMind()
	whole := time.Since(start)

	// Moving the view: whole frames, the view always complete.
	body := x.a.layout().body
	var pans []time.Duration
	x.mx, x.my = body.X+body.W/2, body.Y+body.H/2
	md.zoomAt(body.W/2, body.H/2, 0.6)
	x.settleMind()
	for i := range 40 {
		md.view.OX -= 37
		md.view.OY += float64(13 * (i % 3))
		pans = append(pans, x.frameTime(Input{}))
		if !md.whole {
			t.Errorf("a pan left the view incomplete")
		}
	}
	// Zooming with the wheel: each step's first frame, and how long until the new zoom is drawn whole.
	var zooms, zoomWhole []time.Duration
	for i := range 12 {
		w := float32(1)
		if i >= 6 {
			w = -1
		}
		zs := time.Now()
		zooms = append(zooms, x.frameTime(Input{Wheel: w}))
		for !md.whole {
			x.frame(Input{})
		}
		zoomWhole = append(zoomWhole, time.Since(zs))
	}
	// Typing into a topic in the middle of a big tree, and adding children to it.
	md.fit = true
	x.settleMind()
	var target *mind.Node
	md.m.Each(func(nd *mind.Node) bool {
		if nd.Parent != 0 && len(nd.Children) > 3 && md.m.Depth(nd) == 2 {
			target = nd
			return false
		}
		return true
	})
	md.selectOnly(target.ID)
	a := x.a
	a.mindZoomToSelection(md, body)
	md.view.Zoom = 1
	a.mindKeepInView(md, target)
	x.settleMind()
	x.press(keyF2, "")
	var typing []time.Duration
	for _, r := range "更新的文字内容 more words" {
		typing = append(typing, x.frameTime(Input{Typed: []rune{r}}))
	}
	x.press(keyEnter, "")
	var adds []time.Duration
	for range 10 {
		md.selectOnly(target.ID)
		adds = append(adds, x.frameTime(Input{Keys: []Key{{Code: keyTab}}, Up: []uint16{keyTab}}))
		x.press(keyEscape, "")
	}
	// Where an edit's frame goes: the layout (with connectors routed round the nodes), then the tiles drawn again.
	{
		start := time.Now()
		md.m.Add(mind.Node{Text: "measured"}, target.ID, -1)
		regions, all := md.m.Changed()
		lay := time.Since(start)
		md.tiles.Invalidate(regions, all)
		start = time.Now()
		x.frame(Input{})
		t.Logf("an added child: layout %v (%d areas changed, all %v), the frame after %v", lay, len(regions), all, time.Since(start))
	}
	// Saving: what the window does (reading the map) and what goes on beside it (compressing, writing).
	start = time.Now()
	snap, err := mind.Snapshot(md.m, filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	snapTime := time.Since(start)
	start = time.Now()
	var buf bytes.Buffer
	snap.Write(&buf)
	compress := time.Since(start)

	pct := func(list []time.Duration, p float64) time.Duration {
		s := append([]time.Duration(nil), list...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		return s[min(len(s)-1, int(float64(len(s))*p))]
	}
	t.Logf("%d nodes, %d connectors; file %d KB (%.1f bytes a node)", md.m.Len(), md.m.EdgeCount(), len(data)/1024, float64(len(data))/float64(md.m.Len()))
	t.Logf("built in %v, encoded in %v", build, encode)
	t.Logf("open: read and laid out %v, first frame %v, the whole board drawn %v", read, first, whole)
	t.Logf("pan: median %v, 95%% %v, worst %v (%d frames)", pct(pans, 0.5), pct(pans, 0.95), pct(pans, 1), len(pans))
	t.Logf("zoom step: first frame median %v, worst %v; whole again median %v, worst %v", pct(zooms, 0.5), pct(zooms, 1), pct(zoomWhole, 0.5), pct(zoomWhole, 1))
	t.Logf("typing a character: median %v, worst %v", pct(typing, 0.5), pct(typing, 1))
	t.Logf("adding a child: median %v, worst %v", pct(adds, 0.5), pct(adds, 1))
	t.Logf("saving: %v in the window, %v beside it (%d KB)", snapTime, compress, buf.Len()/1024)
	if !raceBuild && (pct(pans, 0.95) > 50*time.Millisecond || pct(typing, 0.95) > 50*time.Millisecond || pct(adds, 0.95) > 80*time.Millisecond) {
		t.Errorf("frames are too slow for a map of %d nodes", n)
	}
}
