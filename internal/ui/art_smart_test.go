package ui

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// smartCanvas is the canvas harness with a red square on a layer of its own made a smart object; it returns the
// smart object.
func smartCanvas(t *testing.T) (*harness, *artDoc, *paint.Node) {
	t.Helper()
	x, ad := canvasHarness(t)
	d := ad.d
	d.AddLayer("Square")
	d.Select(image.Rect(20, 20, 80, 80))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	x.a.artRun(ad, func() error { _, err := d.ConvertToSmart("Badge"); return err })
	n := d.Node(d.Active)
	if !n.IsSmart() || len(n.Children) != 1 {
		t.Fatalf("the layer did not become a smart object: %+v", n)
	}
	x.frame(Input{})
	return x, ad, n
}

// keptFile waits for the smart objects folder to hold link id's file and picture.
func (x *harness) keptFile(id string) {
	x.t.Helper()
	x.pump(10*time.Second, func() bool {
		_, e1 := os.Stat(x.a.smartPath(id))
		_, e2 := os.Stat(x.a.smartImage(id))
		return e1 == nil && e2 == nil
	})
}

func TestSmartObjectsAcrossCanvases(t *testing.T) {
	x, ad, n := smartCanvas(t)
	a := x.a
	id := n.Smart.ID
	if len(id) != 9 || id[0] < 'A' || id[0] > 'Z' {
		t.Fatalf("the link id %q", id)
	}
	x.keptFile(id)
	// Copied whole, it pastes into another canvas with its link.
	x.key('C', true)
	if a.nodeClip == nil || !a.nodeClip.n.IsSmart() {
		t.Fatal("copying a smart object keeps it whole")
	}
	other := paint.New("Layer 1")
	a.openArt(other, filepath.Join(x.mediaDir(), "other"+paint.Ext))
	od := a.art
	x.frame(Input{})
	x.key('V', true)
	copies := other.SmartCopies(id)
	if len(copies) != 1 || len(copies[0].Children) != 1 {
		t.Fatalf("the pasted canvas holds %d copies", len(copies))
	}
	// A change in the first canvas reaches the second.
	a.switchTab(0)
	ad.d.Choose(ad.d.Node(n.ID).Children[0].ID)
	x.a.artRun(ad, func() error { return ad.d.Fill(color.NRGBA{G: 255, A: 255}) })
	x.frame(Input{})
	if c := other.Composite(image.Rect(50, 50, 51, 51)).RGBAAt(50, 50); c != (color.RGBA{0, 255, 0, 255}) {
		t.Fatalf("the other canvas shows %v", c)
	}
	if !od.dirty || other.UndoName() != "Update smart object" {
		t.Errorf("the other canvas should save the update: dirty %v, %q", od.dirty, other.UndoName())
	}
	// The kept file follows.
	x.pump(10*time.Second, func() bool {
		c, err := a.readSmart(id)
		return err == nil && c.Version == ad.d.Node(n.ID).Smart.Version
	})
	// Breaking the link: the first canvas's copy changes alone.
	ad.d.Choose(n.ID)
	a.artRun(ad, func() error { _, err := ad.d.UnlinkSmart(); return err })
	nid := ad.d.Node(n.ID).Smart.ID
	if nid == id {
		t.Fatal("breaking the link keeps the id")
	}
	ad.d.Choose(ad.d.Node(n.ID).Children[0].ID)
	a.artRun(ad, func() error { return ad.d.Fill(color.NRGBA{B: 255, A: 255}) })
	x.frame(Input{})
	if c := other.Composite(image.Rect(50, 50, 51, 51)).RGBAAt(50, 50); c != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("after breaking the link the other canvas changed: %v", c)
	}
}

// The smart object's own file opens in a canvas with its layers at the top level; a change there reaches every copy.
func TestOpeningTheSmartObjectsFile(t *testing.T) {
	x, ad, n := smartCanvas(t)
	a := x.a
	id := n.Smart.ID
	x.keptFile(id)
	ad.d.Choose(n.ID)
	a.openSmartFile(ad)
	x.pump(10*time.Second, func() bool { return a.art != ad && a.art != nil })
	src := a.art
	if src.d.SmartSource() != id || len(src.d.Root.Children) != 1 || src.d.Root.Children[0].Name != "Square" {
		t.Fatalf("the smart object's file: %q, %+v", src.d.SmartSource(), src.d.Root.Children)
	}
	if title := a.artTitle(src); !strings.Contains(title, "#"+id) || !strings.Contains(title, "Badge") {
		t.Errorf("its tab is called %q", title)
	}
	x.frame(Input{})
	x.shot("canvas-smart-file")
	// An adjustment layer added in it: the canvas's copy takes it.
	a.artRun(src, func() error {
		_, err := src.d.AddAdjustment(paint.Adjustment{Kind: paint.AdjustInvert}, "Invert")
		return err
	})
	x.frame(Input{})
	if got := ad.d.Node(n.ID); len(got.Children) != 2 || got.Children[0].Adjust == nil {
		t.Fatalf("the canvas's copy holds %d layers", len(got.Children))
	}
	if c := ad.d.Composite(image.Rect(50, 50, 51, 51)).RGBAAt(50, 50); c != (color.RGBA{0, 255, 255, 255}) {
		t.Errorf("the inverted copy shows %v", c)
	}
	// Opening it again shows the tab it is open in.
	a.switchTab(0)
	ad.d.Choose(n.ID)
	tabs := len(a.docs)
	a.openSmartFile(ad)
	if len(a.docs) != tabs || a.art != src {
		t.Error("opening the file again should show its tab")
	}
}

// A missing kept file is made from the canvas's spare; a damaged one is set aside and made again; both say so.
func TestMissingAndDamagedSmartFiles(t *testing.T) {
	x, ad, n := smartCanvas(t)
	a := x.a
	id := n.Smart.ID
	x.keptFile(id)
	if err := a.saveArt(ad, true); err != nil {
		t.Fatal(err)
	}
	reopen := func() {
		a.closeTab(a.current())
		a.openCanvasFileWith(ad.path, nil)
		x.pump(10*time.Second, func() bool { return a.art != nil && a.art.path == ad.path })
	}
	os.Remove(a.smartPath(id))
	a.toasts = nil
	reopen()
	x.keptFile(id)
	if len(a.toasts) == 0 || !strings.Contains(a.toasts[len(a.toasts)-1].text, id) {
		t.Errorf("a missing file should be told: %+v", a.toasts)
	}
	if c, err := a.readSmart(id); err != nil || len(c.Layers) != 1 {
		t.Errorf("the file made again: %v", err)
	}
	// Damaged.
	os.WriteFile(a.smartPath(id), []byte("not a canvas"), 0o644)
	a.toasts = nil
	reopen()
	x.pump(10*time.Second, func() bool {
		_, err := a.readSmart(id)
		return err == nil
	})
	if _, err := os.Stat(a.smartPath(id) + ".damaged"); err != nil {
		t.Errorf("the damaged file is not set aside: %v", err)
	}
	if len(a.toasts) == 0 || !a.toasts[len(a.toasts)-1].err {
		t.Errorf("a damaged file should be told as an error: %+v", a.toasts)
	}
	// The kept file comes first: one changed elsewhere is what an opened canvas shows.
	c, _ := a.readSmart(id)
	f := paint.SmartFile(c)
	f.Choose(f.Root.Children[0].ID)
	f.Fill(color.NRGBA{B: 255, A: 255})
	changed := f.SmartChanges()[0]
	a.keepSmart(changed, true)
	x.pump(10*time.Second, func() bool {
		k, err := a.readSmart(id)
		return err == nil && k.Version == changed.Version
	})
	reopen()
	x.pump(10*time.Second, func() bool {
		return a.art.d.Composite(image.Rect(50, 50, 51, 51)).RGBAAt(50, 50) == color.RGBA{0, 0, 255, 255}
	})
}

// A smart object pasted into a video is its picture, which follows its changes.
func TestSmartObjectsInTheVideo(t *testing.T) {
	x, ad, n := smartCanvas(t)
	a := x.a
	id := n.Smart.ID
	x.keptFile(id)
	x.key('C', true)
	x.open(x.fixture())
	a.paste()
	ti, oi, ok := a.p.FindOverlay(a.sel.id)
	if !ok {
		t.Fatal("no overlay was pasted")
	}
	o := a.p.Overlays[ti].Items[oi]
	if o.Smart != id || o.Kind != project.ImageOverlay || o.Image != a.smartImage(id) || o.Aspect != 1 {
		t.Fatalf("the overlay %+v", o)
	}
	if glyph, label := overlayLook(o); glyph != icSmart || label != "#"+id {
		t.Errorf("it is listed as %q", label)
	}
	// Made wider in its canvas, its picture and proportions follow.
	for i, d := range a.docs {
		if d.art == ad {
			a.switchTab(i)
		}
	}
	ad.d.Choose(ad.d.Node(n.ID).Children[0].ID)
	ad.d.Select(image.Rect(80, 20, 140, 80))
	a.artRun(ad, func() error { return ad.d.Fill(color.NRGBA{R: 255, A: 255}) })
	x.frame(Input{})
	x.pump(10*time.Second, func() bool {
		for _, d := range a.docs {
			if d.p != nil {
				ti, oi, ok := d.p.FindOverlay(o.ID)
				return ok && d.p.Overlays[ti].Items[oi].Aspect == 2
			}
		}
		return false
	})
	img, err := loadImage(a.smartImage(id))
	if err != nil || img.Bounds().Dx() != 120 || img.Bounds().Dy() != 60 {
		t.Errorf("the picture is %v (%v)", img.Bounds(), err)
	}
}

// The layers panel can show only the smart objects, each with its link id, which a click copies.
func TestLayersPanelSmartObjects(t *testing.T) {
	x, ad, n := smartCanvas(t)
	a := x.a
	ad.d.AddLayer("Plain")
	a.artRun(ad, func() error { _, err := ad.d.Duplicate(" copy"); return err })
	x.frame(Input{})
	x.shot("canvas-smart-layers")
	if rows := smartRows(ad.d.Root, nil); len(rows) != 1 || rows[0].n.ID != n.ID {
		t.Fatalf("smart rows %d", len(rows))
	}
	a.settings.Paint.SmartOnly = true
	x.frame(Input{})
	x.shot("canvas-smart-only")
	ad.d.Choose(n.ID)
	a.copyLinkID(ad)
	if x.h.clip != n.Smart.ID {
		t.Errorf("the clipboard holds %q", x.h.clip)
	}
}

// Changes to a smart object made faster than they are kept are kept once they end: the write under way finishes,
// the versions made meanwhile wait as one, the latest, and an older one given after a newer is left out.
func TestSmartChangesKeptLatestOnly(t *testing.T) {
	x, _, n := smartCanvas(t)
	a := x.a
	id := n.Smart.ID
	x.keptFile(id)
	c, err := a.readSmart(id)
	if err != nil {
		t.Fatal(err)
	}
	f := paint.SmartFile(c)
	var versions []paint.SmartContent
	for i := range 5 {
		f.Choose(f.Root.Children[0].ID)
		f.Select(image.Rect(20+i*5, 20, 25+i*5, 25))
		f.Fill(color.NRGBA{G: 255, A: 255})
		versions = append(versions, f.SmartChanges()[0])
	}
	for _, v := range versions {
		a.keepSmart(v, true)
	}
	k := &a.smartKeep
	if len(k.waiting) != 1 || k.waiting[id].c.Version != versions[4].Version {
		t.Fatalf("waiting %+v, not the latest alone", k.waiting)
	}
	a.keepSmart(versions[1], true) // older than the one waiting: left out
	if k.waiting[id].c.Version != versions[4].Version {
		t.Fatal("an older version took the latest's place")
	}
	x.pump(10*time.Second, func() bool { return !k.busy[id] && len(k.waiting) == 0 })
	if kept, err := a.readSmart(id); err != nil || kept.Version != versions[4].Version {
		t.Fatalf("kept version %v (%v), want the latest", kept.Version, err)
	}
	a.keepSmart(versions[2], true)
	if k.busy[id] || len(k.waiting) != 0 {
		t.Error("an older version than the one kept was written again")
	}
}
