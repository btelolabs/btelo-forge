package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
)

// newMindHarness opens a new mind map in the harness and draws until its pictures and tiles are in.
func newMindHarness(t *testing.T) (*harness, *mindDoc) {
	x := newHarness(t)
	if err := x.a.newMindMap(); err != nil {
		t.Fatal(err)
	}
	md := x.a.board
	if md == nil {
		t.Fatal("no map tab")
	}
	x.settleMind()
	return x, md
}

// settleMind draws until the map's tiles are whole and no picture is being read.
func (x *harness) settleMind() {
	x.t.Helper()
	quiet := 0
	x.pump(20*time.Second, func() bool {
		md := x.a.board
		if md == nil || md.whole && !x.a.mindPics.pictureBusy() && !md.saving {
			quiet++
		} else {
			quiet = 0
		}
		return quiet > 3 // a frame may ask for pictures as it ends
	})
}

// boardPoint is where the board point p is in the window.
func (x *harness) boardPoint(md *mindDoc, p mind.Vec) (float32, float32) {
	return md.screenView(x.a.layout().body).Screen(p)
}

// nodeCentre is the middle of node id in the window.
func (x *harness) nodeCentre(md *mindDoc, id mind.ID) (float32, float32) {
	b := md.m.Box(md.m.Node(id))
	return x.boardPoint(md, mind.Vec{X: b.CX(), Y: b.CY()})
}

func TestMindKeysBuildATree(t *testing.T) {
	x, md := newMindHarness(t)
	m := md.m
	root := md.primary
	if m.Node(root) == nil || m.Node(root).Text == "" {
		t.Fatal("a new map has no central topic chosen")
	}
	// Tab adds a child and types into it; Enter ends; Enter again adds a sibling.
	x.press(keyTab, "")
	x.typeText("Goals")
	x.press(keyEnter, "")
	x.press(keyEnter, "")
	x.typeText("Risks")
	x.press(keyEnter, "")
	// Typing with a topic chosen replaces its words.
	x.typeText("Risks and costs")
	x.press(keyEnter, "")
	kids := slices.Clone(m.Node(root).Children)
	if len(kids) != 2 || m.Node(kids[0]).Text != "Goals" || m.Node(kids[1]).Text != "Risks and costs" {
		t.Fatalf("children %v", outline(m))
	}
	// Each topic typed is one step: undo takes the last words back, then the topic.
	x.press('Z', "c")
	if m.Node(kids[1]).Text != "Risks" {
		t.Fatalf("undo gave %q", m.Node(kids[1]).Text)
	}
	x.press('Y', "c")
	// Delete removes the chosen topic and chooses its parent.
	md.selectOnly(kids[0])
	x.press(keyDelete, "")
	if m.Node(kids[0]) != nil || md.primary != root {
		t.Fatal("delete did not remove the topic or choose its parent")
	}
	x.press('Z', "c")
	// Arrows go from topic to topic.
	md.selectOnly(root)
	x.press(keyRight, "")
	if md.primary == root || m.Node(md.primary).Parent != root {
		t.Fatalf("right from the root chose %v", md.primary)
	}
	// Ctrl+/ folds.
	md.selectOnly(root)
	x.press(keySlash, "c")
	if !m.Node(root).Folded {
		t.Fatal("not folded")
	}
	x.press(keySlash, "c")
	x.settleMind()
	x.shot("mind-keys")
}

func outline(m *mind.Map) string {
	var ids []mind.ID
	for _, r := range m.Roots() {
		ids = append(ids, r.ID)
	}
	return m.Outline(ids...)
}

func TestMindPointer(t *testing.T) {
	x, md := newMindHarness(t)
	m := md.m
	body := x.a.layout().body
	// A double-click on the board adds a topic there and types into it.
	px, py := body.X+body.W*0.75, body.Y+body.H*0.3
	x.mx, x.my = px, py
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: px, Y: py, Down: true, Double: true}}})
	x.frame(Input{Pointer: []Pointer{{X: px, Y: py}}})
	if md.text == nil {
		t.Fatal("a double-click on the board does not type into a new topic")
	}
	x.typeText("Free idea")
	x.press(keyEnter, "")
	free := md.primary
	if n := m.Node(free); n == nil || n.Parent != 0 || n.Text != "Free idea" {
		t.Fatalf("the new topic: %+v", n)
	}
	// Dragging a free topic moves it, as one step.
	before := m.Node(free).X
	cx, cy := x.nodeCentre(md, free)
	x.drag(cx, cy, cx+120, cy+40)
	if got := m.Node(free).X; got-before < 100 || got-before > 140 {
		t.Fatalf("moved from %v to %v", before, got)
	}
	x.press('Z', "c")
	if m.Node(free).X != before {
		t.Fatal("undo did not put it back")
	}
	// The dot at its left draws a connector to the central topic.
	md.selectOnly(free)
	x.frame(Input{})
	v := md.screenView(body)
	hs := x.a.mindHandles(md, m.Node(free), v)
	var left gfx.Rect
	for _, h := range hs {
		if h.side == mind.SideLeft {
			left = h.r
		}
	}
	rx, ry := x.nodeCentre(md, m.Roots()[0].ID)
	x.drag(left.X+left.W/2, left.Y+left.H/2, rx, ry)
	if m.EdgeCount() != 1 {
		t.Fatalf("%d connectors after drawing one", m.EdgeCount())
	}
	// A click on a dot adds a connected box beside the node and types into it.
	md.selectOnly(free)
	x.frame(Input{})
	for _, h := range x.a.mindHandles(md, m.Node(free), md.screenView(body)) {
		if h.side == mind.SideBottom {
			x.click(h.r)
		}
	}
	if md.text == nil || m.EdgeCount() != 2 {
		t.Fatalf("the dot added no connected box: %d connectors", m.EdgeCount())
	}
	x.typeText("Next step")
	x.press(keyEnter, "")
	// Dragging a tree's topic onto another puts it under that one.
	root := m.Roots()[0].ID
	md.selectOnly(root)
	x.press(keyTab, "")
	x.typeText("A")
	x.press(keyEnter, "")
	a := md.primary
	x.press(keyEnter, "")
	x.typeText("B")
	x.press(keyEnter, "")
	b := md.primary
	x.frame(Input{})
	ax, ay := x.nodeCentre(md, a)
	bx, by := x.nodeCentre(md, b)
	x.drag(bx, by, ax, ay)
	if m.Node(b).Parent != a {
		t.Fatalf("B's parent is %d, want A (%d)", m.Node(b).Parent, a)
	}
	x.settleMind()
	x.shot("mind-pointer")
}

func TestMindTableAndPaste(t *testing.T) {
	x, md := newMindHarness(t)
	m := md.m
	at := x.a.mindViewCentre(md)
	x.a.mindPlace(md, mtTable, mind.Vec{X: at.X + 400, Y: at.Y})
	if md.cell == nil {
		t.Fatal("a new table does not type into its first cell")
	}
	tid := md.cell.id
	x.typeText("Q1")
	x.press(keyTab, "")
	x.typeText("Q2")
	x.press(keyEnter, "")
	x.typeText("100")
	x.press(keyEnter, "")
	x.typeText("=B2*2")
	x.press(keyEnter, "")
	x.press(keyEscape, "")
	g := m.Node(tid).Cells
	if g.Cell(0, 0) != "Q1" || g.Cell(0, 1) != "Q2" || g.Cell(1, 1) != "100" || mind.CellValue(g, 2, 1) != "200" {
		t.Fatalf("cells %q", g.Cells)
	}
	// Pasting an outline under the chosen topic makes a subtree; tab-separated cells make a table.
	root := m.Roots()[0].ID
	md.selectOnly(root)
	x.h.pasteText = "- one\n  - one a\n- two\n"
	x.a.mindPaste(md)
	if len(m.Node(root).Children) != 2 {
		t.Fatalf("the outline pasted: %s", outline(m))
	}
	md.selectOnly(0)
	x.h.pasteText = "a\tb\n1\t2\n"
	x.a.mindPaste(md)
	if n := m.Node(md.primary); n == nil || n.Kind != mind.Table || n.Cells.Cell(1, 1) != "2" {
		t.Fatal("tab-separated cells did not paste as a table")
	}
	// Nodes copied and pasted come back as copies.
	md.selectOnly(root)
	x.a.mindCopy(md, false)
	x.h.pasteText = x.h.clip
	before := m.Len()
	md.selectOnly(0)
	x.a.mindPaste(md)
	if m.Len() != before+4 {
		t.Fatalf("%d nodes after pasting a copy of 4, from %d", m.Len(), before)
	}
	x.h.pasteText = ""
	x.settleMind()
	x.shot("mind-table")
}

// writeCheckPNG writes a w × h picture of two colours.
func writeCheckPNG(t *testing.T, path string, w, h int, a, b color.RGBA) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := a
			if (x/16+y/16)%2 == 0 {
				c = b
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCheckGIF(t *testing.T, path string) {
	pal := color.Palette{color.RGBA{30, 30, 30, 255}, color.RGBA{240, 120, 40, 255}, color.RGBA{60, 160, 255, 255}}
	g := &gif.GIF{}
	for f := range 4 {
		img := image.NewPaletted(image.Rect(0, 0, 120, 80), pal)
		for y := range 80 {
			for x := range 120 {
				if (x+f*30)%120 < 40 {
					img.SetColorIndex(x, y, uint8(1+f%2))
				}
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	gif.EncodeAll(&buf, g)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMindFilesNestingAndSmartObjects(t *testing.T) {
	x, md := newMindHarness(t)
	m := md.m
	dir := x.mediaDir()
	pic := filepath.Join(dir, "photo.png")
	writeCheckPNG(t, pic, 320, 200, color.RGBA{80, 140, 220, 255}, color.RGBA{230, 230, 240, 255})
	anim := filepath.Join(dir, "moving.gif")
	writeCheckGIF(t, anim)
	video := clipFile(t, dir, "clip.mp4", "testsrc2=size=640x360", "2")
	// A second map that holds this one: shown inside this one, it holds this map again, a cycle.
	other := filepath.Join(dir, "other"+mind.Ext)
	om := mind.New("other", x.a.mindMeasure())
	om.Add(mind.Node{Text: "Other map"}, 0, -1)
	om.Add(mind.Node{Kind: mind.Nested, Ref: md.path, X: 300, Y: 0}, 0, -1)
	data, err := mind.Encode(om, dir)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(other, data, 0o644)
	// A smart object's picture, as a canvas keeps it.
	id := "ABCDEFGH2"
	os.MkdirAll(x.a.smartDir(), 0o755)
	writeCheckPNG(t, x.a.smartImage(id), 200, 200, color.RGBA{240, 80, 80, 255}, color.RGBA{255, 220, 220, 255})

	at := x.a.mindViewCentre(md)
	x.frame(Input{Dropped: []string{pic, anim, video, other}})
	if m.Len() != 5 {
		t.Fatalf("%d nodes after dropping four files", m.Len())
	}
	sid, err := x.a.mindAddCentred(md, mind.Node{Kind: mind.Smart, Ref: id}, mind.Vec{X: at.X, Y: at.Y + 420})
	if err != nil {
		t.Fatal(err)
	}
	selfID, _ := x.a.mindAddCentred(md, mind.Node{Kind: mind.Nested, Ref: md.path}, mind.Vec{X: at.X + 420, Y: at.Y + 420})
	md.fit = true
	x.settleMind()
	// Every picture is in, each with its own proportions.
	for _, n := range m.Visible() {
		if n.Kind == mind.Topic {
			continue
		}
		img, state, _ := x.a.mindPictures().get(n, 300, md.path)
		want := mind.PicReady
		if n.ID == selfID {
			want = mind.PicCycle
		}
		if state != want || want == mind.PicReady && img == nil {
			t.Errorf("%s %q: state %v", mind.KindNames[n.Kind], filepath.Base(n.Ref), state)
		}
		if n.Kind == mind.Picture && filepath.Base(n.Ref) == "photo.png" && (n.Aspect < 1.59 || n.Aspect > 1.61) {
			t.Errorf("the picture's proportions %v, want 1.6", n.Aspect)
		}
	}
	// The other map shows this one inside it as a cycle, drawn as a warning rather than forever.
	th := mind.DrawNested(other, []string{md.path}, 300, x.a.u.fonts, x.a.mindLabels(), mindSource{x.a})
	if th.State != mind.PicReady {
		t.Fatalf("the nested map: %v", th.State)
	}
	if got := mind.DrawNested(md.path, []string{other, md.path}, 300, x.a.u.fonts, x.a.mindLabels(), nil); got.State != mind.PicCycle {
		t.Fatalf("this map inside the other inside this one: %v", got.State)
	}
	x.shot("mind-files")

	// A change to the smart object reaches the map: its picture is read again.
	before, _, _ := x.a.mindPictures().get(m.Node(sid), 300, md.path)
	writeCheckPNG(t, x.a.smartImage(id), 300, 150, color.RGBA{40, 200, 120, 255}, color.RGBA{20, 60, 40, 255})
	x.a.smartPictureChanged(id, 2)
	x.settleMind()
	after, _, _ := x.a.mindPictures().get(m.Node(sid), 300, md.path)
	if after == nil || after == before || m.Node(sid).Aspect < 1.9 {
		t.Fatalf("the smart object did not follow its change (aspect %v)", m.Node(sid).Aspect)
	}
	// Saved, closed and opened again, the map is the same.
	path := md.path
	nodes, edges := m.Len(), m.EdgeCount()
	x.a.closeTab(x.a.current())
	x.a.openMindFile(path)
	x.pump(10*time.Second, func() bool { return x.a.board != nil })
	if got := x.a.board.m; got.Len() != nodes || got.EdgeCount() != edges {
		t.Fatalf("reopened with %d nodes and %d connectors, want %d and %d", got.Len(), got.EdgeCount(), nodes, edges)
	}
	// Only looking at it does not save it again.
	st, _ := os.Stat(path)
	x.settleMind()
	time.Sleep(mindSaveDelay + 200*time.Millisecond)
	x.settleMind()
	if now, _ := os.Stat(path); x.a.board.dirty || !now.ModTime().Equal(st.ModTime()) {
		t.Fatal("a map only opened was saved again")
	}
	if assets.KindOf(path) != assets.MindMap {
		t.Fatal("the asset manager does not know mind maps")
	}
	img, err := x.a.mindFileDrawer()(path, 256)
	if err != nil || img == nil {
		t.Fatalf("the asset manager's picture of the map: %v", err)
	}
	// The asset manager previews it, and its grid has a picture of it.
	x.a.openPreview("test", path)
	x.pump(20*time.Second, func() bool { return !x.a.preview.loading })
	if p := x.a.preview; p.img == nil || p.problem != "" {
		t.Fatalf("the preview of the map: %q", p.problem)
	}
	x.a.fileThumbs.init(x.a.cacheDir)
	x.pump(20*time.Second, func() bool {
		_, _, ok := x.a.fileThumb(path)
		x.a.pumpThumbs()
		return ok
	})
	if thumb, info, _ := x.a.fileThumb(path); thumb == nil || info.Problem != "" {
		t.Fatalf("the asset manager's small picture of the map: %+v", info)
	}
}

// TestMindLargeBoardStaysResponsive: a board of 200 000 topics is drawn whole, and an edit in it is laid out and
// drawn again within a frame's time.
func TestMindLargeBoardStaysResponsive(t *testing.T) {
	if testing.Short() {
		t.Skip("large board")
	}
	x, md := newMindHarness(t)
	m := md.m
	root := m.Roots()[0].ID
	m.Begin("build")
	queue := []mind.ID{root}
	for k := 0; k < 200_000; {
		p := queue[0]
		queue = queue[1:]
		for range 6 {
			id, _ := m.Add(mind.Node{Text: "topic"}, p, -1)
			queue = append(queue, id)
			k++
		}
	}
	m.Commit()
	start := time.Now()
	m.Layout()
	t.Logf("200 000 topics laid out in %v", time.Since(start))
	md.fit = true
	x.frame(Input{})
	x.settleMind()
	// An edit at the root: one frame lays it out and draws the tiles it changed.
	md.selectOnly(root)
	start = time.Now()
	x.press(keyTab, "")
	x.typeText("new")
	x.press(keyEnter, "")
	frame := time.Since(start) / 3
	t.Logf("an edit took %v a frame", frame)
	if frame > 250*time.Millisecond {
		t.Fatalf("a frame with an edit took %v", frame)
	}
	x.shot("mind-large")
}

func TestMindViewNavigation(t *testing.T) {
	x, md := newMindHarness(t)
	body := x.a.layout().body
	// The wheel zooms around the pointer: the board point under it stays under it.
	px, py := body.X+body.W*0.3, body.Y+body.H*0.6
	x.mx, x.my = px, py
	x.frame(Input{})
	before := md.screenView(body).Board(float64(px), float64(py))
	z := md.view.Zoom
	x.frame(Input{Wheel: 2})
	if md.view.Zoom <= z*1.3 {
		t.Fatalf("zoom %v after wheeling in from %v", md.view.Zoom, z)
	}
	after := md.screenView(body).Board(float64(px), float64(py))
	if math.Abs(after.X-before.X)*md.view.Zoom > 1 || math.Abs(after.Y-before.Y)*md.view.Zoom > 1 {
		t.Fatalf("the point under the pointer moved from %v to %v", before, after)
	}
	// A drag on the empty board moves the view by as much.
	ox, oy := md.view.OX, md.view.OY
	x.drag(body.X+40, body.Bottom()-120, body.X+140, body.Bottom()-60)
	if md.view.OX-ox != 100 || md.view.OY-oy != 60 {
		t.Fatalf("the view moved by %v,%v for a drag of 100,60", md.view.OX-ox, md.view.OY-oy)
	}
	// Shift+drag draws a marquee that chooses what it holds.
	md.selectOnly(0)
	rx, ry := x.nodeCentre(md, md.m.Roots()[0].ID)
	x.mx, x.my = rx-200, ry-100
	x.frame(Input{Shift: true})
	x.frame(Input{Shift: true, Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
	for i := 1; i <= 6; i++ {
		x.mx, x.my = rx-200+float32(i)*70, ry-100+float32(i)*35
		x.frame(Input{Shift: true, Down: true})
	}
	x.frame(Input{Shift: true, Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if len(md.sel) != 1 {
		t.Fatalf("the marquee chose %d nodes, want the central topic", len(md.sel))
	}
}

func TestMindPlaysVideoAndAnimations(t *testing.T) {
	x, md := newMindHarness(t)
	dir := x.mediaDir()
	anim := filepath.Join(dir, "moving.gif")
	writeCheckGIF(t, anim)
	video := clipFile(t, dir, "clip.mp4", "testsrc2=size=640x360", "3")
	at := x.a.mindViewCentre(md)
	x.a.mindAddFiles(md, []string{anim, video}, mind.Vec{X: at.X - 300, Y: at.Y + 120})
	x.settleMind()
	var vidID mind.ID
	for _, n := range md.m.Visible() {
		switch n.Kind {
		case mind.Video:
			vidID = n.ID
		}
	}
	// The animation's frames follow one another as they fall due.
	first := x.a.animFrame(anim)
	deadline := time.Now().Add(5 * time.Second)
	for x.a.animFrame(anim) == first && time.Now().Before(deadline) {
		x.frame(Input{})
		time.Sleep(20 * time.Millisecond)
	}
	if x.a.animFrame(anim) == first {
		t.Fatal("the animation does not play")
	}
	if wait := x.a.mindWait(); wait <= 0 || wait > 200*time.Millisecond {
		t.Fatalf("the window does not wake for the next frame: %v", wait)
	}
	// A double-click on the video plays it in its place.
	vx, vy := x.nodeCentre(md, vidID)
	x.mx, x.my = vx, vy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: vx, Y: vy, Down: true, Double: true}}})
	x.frame(Input{Pointer: []Pointer{{X: vx, Y: vy}}})
	if md.video != vidID {
		t.Fatal("a double-click did not play the video")
	}
	x.pump(10*time.Second, func() bool { return x.a.preview.path == video && !x.a.preview.loading })
	x.shot("mind-playing")
	x.press(keyEscape, "")
	if md.video != 0 {
		t.Fatal("Escape did not stop the video")
	}
}
