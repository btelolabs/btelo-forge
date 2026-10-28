package ui

import (
	"math"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

func TestPlayerModeHidesTheToolsAndOnlyPlays(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	clips := len(x.a.p.Video[0].Clips)
	x.key(keyTab, false)
	if !x.a.watching() {
		t.Fatal("Tab should turn player mode on")
	}
	// Editing keys do nothing while the tools are hidden.
	x.key('S', false)
	x.key(keyDelete, false)
	if len(x.a.p.Video[0].Clips) != clips {
		t.Fatal("a key edited the video in player mode")
	}
	// The seek bar runs along the bottom: a click three quarters along seeks there.
	bar := x.a.screen.Bottom() - x.a.u.px(58)
	x.click(gfx.Rect{X: x.a.screen.W * 0.75, Y: bar + x.a.u.px(12)})
	if want := x.a.p.Duration() * 0.75; math.Abs(x.a.playhead-want) > 0.2 {
		t.Fatalf("seeked to %.2f, want about %.2f", x.a.playhead, want)
	}
	x.key(keySpace, false)
	if !x.a.player.Playing() {
		t.Fatal("Space should still play")
	}
	x.key(keySpace, false)
	x.mx, x.my = x.a.screen.W/2, x.a.screen.H/2
	x.settle(10 * time.Second)
	if x.a.player.Playing() || x.a.shown.key.at < 5 {
		t.Fatalf("paused at %.2f, showing the frame at %.2f", x.a.playhead, x.a.shown.key.at)
	}
	x.shot("player-mode")
	x.key(keyEscape, false)
	if x.a.watching() {
		t.Fatal("Esc should leave player mode")
	}
	// The button in the tab bar turns it on again; closing the edit turns it off.
	x.a.setWatching(true)
	x.a.closeTab(x.a.current())
	x.frame(Input{})
	if x.a.watch.on {
		t.Fatal("player mode outlived its edit")
	}
}
