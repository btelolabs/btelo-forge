package ui

import (
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
)

// The stream's tab at the size of a small window: destinations' names in full, the add button whole under the tab
// strip, and the last choice's words wrapped rather than cut. With the tab in front, the stream going live or ending
// is shown by its badge, not told again in a note over it.
func TestLiveTabFitsAndDoesNotRepeatItself(t *testing.T) {
	if !capture.Supported() {
		t.Skip(capture.ErrUnsupported)
	}
	x := newHarness(t)
	x.c.Resize(1422, 893)
	x.scale = 1.25
	a := x.a
	a.openLive()
	for _, d := range []liveDestination{
		{Platform: "custom", Name: "Studio server", Server: "rtmp://127.0.0.1:19350/live", On: true},
		{Platform: "custom", Name: "Backup server", Server: "rtmp://127.0.0.1:19351/live", On: true},
	} {
		if err := a.saveDestination(-1, d); err != nil {
			t.Fatal(err)
		}
	}
	x.settle(10 * time.Second)
	x.shot("live-tab-layout")

	u := &a.u
	f := u.bold(13)
	for _, d := range a.settings.Live.Destinations {
		name := a.destinationName(d)
		row := u.liveSideWidth(x.a.layout().body.W) - 2*u.px(liveSidePad) - u.px(8)
		if got := f.Ellipsize(name, u.liveNameWidth(row)); got != name {
			t.Errorf("%q shows as %q", name, got)
		}
	}

	a.liveToast("Live on 2 destinations")
	if len(a.toasts) != 0 {
		t.Errorf("with the stream's tab in front, a note said %q", a.toasts[0].text)
	}
	a.newTab()
	a.liveToast("The stream has ended")
	if len(a.toasts) != 1 {
		t.Fatal("away from the stream's tab, the stream's end was not told")
	}
}
