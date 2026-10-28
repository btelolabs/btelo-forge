package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// A click on the tab strip — another tab, a new one, or a tab's close button — changes the tab in the middle of a
// frame: the body drawn after the strip finds the tab it was drawing gone, and draws nothing this once instead of
// failing, whatever kind of tab it was.
func TestTabStripChangesTheTabMidFrame(t *testing.T) {
	kinds := []struct {
		name string
		open func(x *harness)
	}{
		{"canvas", func(x *harness) {
			if err := x.a.newCanvas(); err != nil {
				x.t.Fatal(err)
			}
		}},
		{"mind map", func(x *harness) {
			if err := x.a.newMindMap(); err != nil {
				x.t.Fatal(err)
			}
		}},
		{"file manager", func(x *harness) { x.a.newFilesTab(x.mediaDir(), true) }},
	}
	clicks := []struct {
		name string
		at   func(x *harness, kind, w float32, tabs gfx.Rect) (float32, float32)
	}{
		{"another tab", func(x *harness, kind, w float32, tabs gfx.Rect) (float32, float32) {
			return tabs.X + x.a.u.px(8) + (1-kind)*w + x.a.u.px(30), tabs.Y + tabs.H/2
		}},
		{"new tab", func(x *harness, kind, w float32, tabs gfx.Rect) (float32, float32) {
			return tabs.X + x.a.u.px(8) + 2*w + x.a.u.px(20), tabs.Y + tabs.H/2
		}},
		{"close", func(x *harness, kind, w float32, tabs gfx.Rect) (float32, float32) {
			u := &x.a.u
			right := tabs.X + u.px(8) + (kind+1)*w - u.px(2)
			return right - u.px(16), tabs.Y + u.px(6) + (tabs.H-u.px(6))/2
		}},
	}
	for _, k := range kinds {
		for _, c := range clicks {
			t.Run(k.name+"/"+c.name, func(t *testing.T) {
				x := newHarness(t)
				x.a.settings.ExportDir = x.mediaDir()
				k.open(x)
				x.a.newTab()
				x.a.switchTab(0)
				x.frame(Input{})
				if len(x.a.docs) != 2 || x.a.current() != 0 {
					t.Fatalf("want the %s in the first of two tabs, have %d tabs, tab %d active", k.name, len(x.a.docs), x.a.current())
				}
				tabs := x.a.layout().tabs
				u := &x.a.u
				w := min(u.px(220), (tabs.W-u.px(300))/2) // the strip's two tabs are as wide as they can be, up to 220
				x.mx, x.my = c.at(x, 0, w, tabs)
				x.frame(Input{})
				x.frame(Input{Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
				x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
				x.frame(Input{})
				if x.a.current() == 0 && len(x.a.docs) == 2 {
					t.Fatalf("the click at %.0f,%.0f changed no tab", x.mx, x.my)
				}
				if crashes, _ := os.ReadDir(x.a.crashDir()); len(crashes) > 0 {
					report, _ := os.ReadFile(filepath.Join(x.a.crashDir(), crashes[0].Name()))
					t.Fatalf("the app failed:\n%s", report)
				}
			})
		}
	}
}
