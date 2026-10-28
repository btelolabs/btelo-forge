package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Every step the picture records has a name in historyNames, so the History panel can translate it: the names are
// found where the paint package and the editor give them.
func TestEveryHistoryStepIsNamed(t *testing.T) {
	named := regexp.MustCompile(`(?:Commit|paintArea|clear|remap|resample|reframe|Run|Apply)\("([A-Z][^"]*)"|` +
		`\bname :?= "([A-Z][^"]*)"|step: +"([A-Z][^"]*)"|d\.set\(\w+, "([A-Z][^"]*)"`)
	var files []string
	for _, dir := range []string{"../paint", "."} {
		m, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range named.FindAllStringSubmatch(string(src), -1) {
			for _, name := range m[1:] {
				if name == "" {
					continue
				}
				found++
				if !slices.Contains(historyNames, name) {
					t.Errorf("%s: the step %q is not in historyNames", f, name)
				}
			}
		}
	}
	if found < 40 {
		t.Errorf("only %d step names were found: the pattern no longer matches how steps are named", found)
	}
}

func TestCanvasHistoryPanelAndAdjustments(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	d.AddLayer("Ink")
	d.Fill(a.brush.Color)
	ad.changedAll()
	x.frame(Input{})
	lay := a.artLayout(a.layout().body)

	// Image › Adjustments › Hue/Saturation, through the submenu.
	x.click(x.menuTitle(lay, menuImage))
	rows := a.u.menuRows(a.menu.items, a.menuBelow(a.menu.items, a.menu.anchor))
	x.mx, x.my = rows[0].X+rows[0].W/2, rows[0].Y+rows[0].H/2 // Adjustments, the first item
	x.frame(Input{})
	x.frame(Input{})
	x.shot("canvas-menu-adjustments")
	x.clickSubmenuItem(a.u.t("Adjustments"), a.u.t("Hue/Saturation…"))
	if a.adjust == nil || a.adjust.kind != adjustHue {
		t.Fatalf("the submenu did not open Hue/Saturation: %+v", a.adjust)
	}
	before := ad.look(50, 50)
	a.adjust.values[0] = 120 // red turns green
	x.pump(5*time.Second, func() bool { return a.adjust.shown == a.adjust.key() })
	if c := ad.look(50, 50); c == before || c.G <= c.R {
		t.Errorf("the preview does not show the new hue: %v from %v", c, before)
	}
	x.shot("canvas-hue-saturation")
	// Preview off shows the picture as it was; Cancel leaves it so.
	a.adjust.preview = false
	x.pump(5*time.Second, func() bool { return a.adjust.shown == a.adjust.key() })
	if c := ad.look(50, 50); c != before {
		t.Errorf("with preview off the picture shows %v", c)
	}
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if a.adjust != nil || ad.look(50, 50) != before || d.UndoName() == "Hue/Saturation" {
		t.Fatalf("Esc did not cancel: %v from %v (%q), dialog %v", ad.look(50, 50), before, d.UndoName(), a.adjust != nil)
	}
	// Ctrl+U opens it again; Enter applies.
	x.key('U', true)
	if a.adjust == nil {
		t.Fatal("Ctrl+U did not open Hue/Saturation")
	}
	a.adjust.values[0] = 120
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	if d.UndoName() != "Hue/Saturation" {
		t.Fatalf("Enter did not apply: %q", d.UndoName())
	}
	if c := ad.look(50, 50); c.G <= c.R {
		t.Errorf("applied hue %v", c)
	}
	// Curves: a point dragged up lightens the middle.
	x.key('M', true)
	if a.adjust == nil || a.adjust.kind != adjustCurves {
		t.Fatal("Ctrl+M did not open Curves")
	}
	x.frame(Input{})
	x.shot("canvas-curves")
	a.adjust.curve = append(a.adjust.curve, paint.CurvePoint{In: 128, Out: 200})
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	if d.UndoName() != "Curves" {
		t.Errorf("curves: %q", d.UndoName())
	}
	// A filter from the Filter menu.
	x.frame(Input{})
	x.click(x.menuTitle(lay, menuFilter))
	x.clickSubmenuItem(a.u.t("Blur"), a.u.t("Gaussian blur…"))
	if a.adjust == nil || a.adjust.kind != filterBlur {
		t.Fatal("the Filter menu did not open Gaussian blur")
	}
	x.frame(Input{})
	x.shot("canvas-gaussian-blur")
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	if d.UndoName() != "Gaussian blur" {
		t.Errorf("blur: %q", d.UndoName())
	}
	// Ctrl+I inverts at once.
	x.key('I', true)
	if d.UndoName() != "Invert" {
		t.Errorf("Ctrl+I: %q", d.UndoName())
	}

	// The History panel lists the steps; a click goes back to one.
	x.click(x.sideTab(lay, sideHistory))
	if a.sidePage != sideHistory {
		t.Fatal("the History tab did not open")
	}
	x.frame(Input{})
	x.shot("canvas-history")
	steps, done := d.History()
	if done != len(steps) || done < 5 {
		t.Fatalf("history %v at %d", steps, done)
	}
	x.click(x.historyRow(lay, 1)) // the first step: the new layer
	if _, now := d.History(); now != 1 || len(d.Layers()) != 2 {
		t.Errorf("going back gave step %d with %d layers", now, len(d.Layers()))
	}
	x.frame(Input{})
	x.shot("canvas-history-back")
	a.u.lang = Chinese
	x.frame(Input{})
	x.frame(Input{})
	x.shot("canvas-history-zh")
	a.u.lang = English
	x.click(x.sideTab(lay, sideLayers))
	if a.sidePage != sideLayers {
		t.Error("the Layers tab did not come back")
	}
}

// sideTab is where drawSideTabs puts a page's title.
func (x *harness) sideTab(l artLayout, page int) gfx.Rect {
	u := &x.a.u
	in := l.side.Inset(u.px(14))
	top := in.Y + u.px(250) + u.px(14)
	f := u.bold(12.5)
	px := in.X
	for i, name := range []string{u.t("Layers"), u.t("History")} {
		w := f.Measure(name) + u.px(16)
		if i == page {
			return gfx.Rect{X: px, Y: top, W: w, H: u.px(22)}
		}
		px += w + u.px(6)
	}
	return gfx.Rect{}
}

// historyRow is where drawHistory puts row i, while the list starts at the top.
func (x *harness) historyRow(l artLayout, i int) gfx.Rect {
	u := &x.a.u
	in := l.side.Inset(u.px(14))
	top := in.Y + u.px(250) + u.px(14) + u.px(26)
	return gfx.Rect{X: in.X + u.px(4), Y: top + u.px(4) + float32(i)*u.px(28), W: in.W - u.px(8), H: u.px(26)}
}
