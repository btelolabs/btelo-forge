package ui

import (
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mail"
)

type fakeWeb struct {
	page, policy string
	pictures     bool
	shows, hides int
	at           image.Rectangle
	holes        []image.Rectangle
	shown        bool
	links        []string
	touched      bool
	err          error
	closed       bool
}

func (w *fakeWeb) Show(page, policy, _ string, pictures bool) {
	w.page, w.policy, w.pictures = page, policy, pictures
	w.shows++
}
func (w *fakeWeb) Place(r image.Rectangle, holes []image.Rectangle) {
	w.at, w.holes, w.shown = r, holes, true
}
func (w *fakeWeb) Hide() {
	w.shown = false
	w.hides++
}
func (w *fakeWeb) Err() error { return w.err }
func (w *fakeWeb) Close()     { w.closed = true }
func (w *fakeWeb) Links() []string {
	out := w.links
	w.links = nil
	return out
}
func (w *fakeWeb) Touched() bool {
	t := w.touched
	w.touched = false
	return t
}

// webFakeHost is a host with a view, and the links it was told to open.
type webFakeHost struct {
	*fakeHost
	web    *fakeWeb
	opened []string
}

func (h *webFakeHost) OpenWebView(string, func()) (WebView, error) { return h.web, nil }
func (h *webFakeHost) LaunchURL(url string) error {
	h.opened = append(h.opened, url)
	return nil
}

func mailWithID(m mail.Message, id int64) mail.Message {
	m.ID = id
	return m
}

const htmlMail = "From: News <news@a.com>\r\nSubject: Letter\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
	"<h1>Letter</h1><img src=\"https://tracker.example/p.gif\">"

func openHTMLMail(t *testing.T, x *harness) (*webFakeHost, *mailState) {
	t.Helper()
	host := &webFakeHost{fakeHost: x.h, web: &fakeWeb{}}
	x.a.host = host
	home, _ := bindTwo(t, x)
	home.Add("News <news@a.com>", "Letter", "").Raw = htmlMail
	s := &x.a.mail
	x.a.mailSync()
	x.pump(10*time.Second, func() bool { return !s.syncing && len(s.list) == 4 })
	for _, m := range s.list {
		if m.Subject == "Letter" {
			s.current = m.ID
		}
	}
	x.pump(5*time.Second, func() bool { return host.web.shown })
	return host, s
}

func TestAnHTMLMessageIsShownInTheViewWithoutItsPictures(t *testing.T) {
	x := newHarness(t)
	host, s := openHTMLMail(t, x)
	web := host.web
	if !strings.Contains(web.page, "<h1>Letter</h1>") || web.pictures || strings.Contains(web.policy, "https:") {
		t.Fatalf("page %q, policy %q, pictures %t", web.page, web.policy, web.pictures)
	}
	if web.at.Empty() || web.at.Min.Y <= 0 {
		t.Errorf("the view is at %v", web.at)
	}
	shows := web.shows
	x.frame(Input{})
	if web.shows != shows {
		t.Error("the page is loaded again in every frame")
	}
	x.a.u.lang = "zh"
	x.frame(Input{})
	x.shot("mail-page-zh")
	x.a.u.lang = "en"
	// The button stands at the right of its row, where nothing else is, somewhere above the view.
	u := &x.a.u
	bx := float32(web.at.Min.X) + min(float32(web.at.Dx())-u.px(48), u.px(760)) + u.px(20) - u.face(12.5).Measure(u.t("Always for this sender")) - u.px(28+8+20)
	for y := float32(web.at.Min.Y) - u.px(15); y > 0 && !web.pictures; y -= u.px(10) {
		x.click(gfx.Rect{X: bx, Y: y, W: 1, H: 1})
		x.frame(Input{})
	}
	if !web.pictures {
		t.Fatal("no button offers the pictures")
	}
	if !strings.Contains(web.policy, "https:") {
		t.Errorf("the policy with pictures is %q", web.policy)
	}
	if s.page.pictures && func() bool { ok, _ := s.mailer.Store.Trusted("news@a.com"); return ok }() {
		t.Error("showing the pictures once trusted the sender")
	}
}

func TestGoingToTheNextPageKeepsTheViewUp(t *testing.T) {
	x := newHarness(t)
	host, s := openHTMLMail(t, x)
	web := host.web
	web.hides = 0
	s.current += 1000 // a message not read yet: nothing of it is known for this frame
	s.list = append(s.list, mailWithID(s.list[0], s.current))
	x.frame(Input{})
	if web.hides != 0 || !web.shown {
		t.Errorf("the view left between two messages: hides %d, shown %t", web.hides, web.shown)
	}
}

func TestATextMessageNeedsNoView(t *testing.T) {
	x := newHarness(t)
	host := &webFakeHost{fakeHost: x.h, web: &fakeWeb{}}
	x.a.host = host
	bindTwo(t, x)
	s := &x.a.mail
	s.current = s.list[0].ID
	x.pump(2*time.Second, func() bool { return s.page.known })
	x.frame(Input{})
	if host.web.shown || host.web.shows != 0 {
		t.Errorf("a message of text went to the view: %+v", host.web)
	}
}

func TestAHostWithoutAViewShowsTheText(t *testing.T) {
	x := newHarness(t)
	home, _ := bindTwo(t, x)
	home.Add("News <news@a.com>", "Letter", "").Raw = htmlMail
	s := &x.a.mail
	x.a.mailSync()
	x.pump(10*time.Second, func() bool { return !s.syncing && len(s.list) == 4 })
	for _, m := range s.list {
		if m.Subject == "Letter" {
			s.current = m.ID
		}
	}
	x.pump(2*time.Second, func() bool { return s.page.known })
	x.frame(Input{})
	if s.web != nil || !s.webOff {
		t.Errorf("web %v, off %t", s.web, s.webOff)
	}
}

func TestWhatIsDrawnOverTheViewShowsThroughIt(t *testing.T) {
	x := newHarness(t)
	host, _ := openHTMLMail(t, x)
	web := host.web
	x.a.toast("Over the view", false)
	x.frame(Input{})
	box := web.at
	boxes, _ := x.a.toastLayout()
	want := pixels(boxes[0]).Intersect(box)
	if want.Empty() {
		t.Skip("the toast does not reach the view at this size")
	}
	if len(web.holes) != 1 || web.holes[0] != want {
		t.Errorf("holes %v, want %v", web.holes, want)
	}
}

func TestADialogTakesTheViewAwayAndItComesBack(t *testing.T) {
	x := newHarness(t)
	host, _ := openHTMLMail(t, x)
	x.a.settingsOpen = true
	x.frame(Input{})
	if host.web.shown {
		t.Error("the view stays over a dialog")
	}
	x.a.settingsOpen = false
	x.frame(Input{})
	if !host.web.shown {
		t.Error("the view did not come back")
	}
}

func TestLeavingTheTabHidesTheView(t *testing.T) {
	x := newHarness(t)
	host, _ := openHTMLMail(t, x)
	x.a.newTab()
	x.frame(Input{})
	if host.web.shown {
		t.Error("the view stays over another tab")
	}
}

func TestALinkClickedOpensOutside(t *testing.T) {
	x := newHarness(t)
	host, _ := openHTMLMail(t, x)
	host.web.links = []string{"https://example.com/a"}
	x.frame(Input{})
	if len(host.opened) != 1 || host.opened[0] != "https://example.com/a" {
		t.Errorf("opened %v", host.opened)
	}
}

func TestAClickInTheViewClosesAMenu(t *testing.T) {
	x := newHarness(t)
	host, _ := openHTMLMail(t, x)
	x.a.menu = &popupMenu{id: "test"}
	host.web.touched = true
	x.frame(Input{})
	if x.a.menu != nil {
		t.Error("the menu stayed open")
	}
}

func TestAViewThatFailsFallsBackToText(t *testing.T) {
	x := newHarness(t)
	host, s := openHTMLMail(t, x)
	host.web.err = errors.New("gone")
	x.frame(Input{})
	x.frame(Input{})
	if s.web != nil || !s.webOff || !host.web.closed {
		t.Errorf("web %v, off %t, closed %t", s.web, s.webOff, host.web.closed)
	}
}

// The view cannot show anything through it but a hole, so the round orb stays off it, with its list, and the corner stays
// for other work.
func TestTheOrbStaysOffTheView(t *testing.T) {
	x := newHarness(t)
	host, s := openHTMLMail(t, x)
	x.frame(Input{})
	view := s.webLast
	if view != (gfx.Rect{}) && view.W == 0 {
		t.Fatal("no view")
	}
	x.a.orb.open, x.a.orb.until = true, time.Now().Add(time.Minute)
	l := x.a.placeTasks([]task{{id: "model", running: true}, {id: "work", fraction: 0.5}})
	if l.orb.Right() > view.X || l.orb.W == 0 {
		t.Fatalf("the orb at %+v is on the view at %+v", l.orb, view)
	}
	if !l.zone.Contains(l.orb.X, l.orb.Y) || l.zone.Right() != l.orb.Right() {
		t.Errorf("its list %+v is not beside it %+v", l.zone, l.orb)
	}
	for i, c := range l.tasks {
		if c.id == "work" && l.cards[i].Right() != x.a.screen.W-x.a.u.px(16) {
			t.Errorf("work left the corner: %+v", l.cards[i])
		}
	}
	x.a.newTab()
	x.frame(Input{})
	x.frame(Input{})
	if l := x.a.placeTasks([]task{{id: "model", running: true}}); l.orb.Right() != x.a.screen.W-x.a.u.px(16) {
		t.Errorf("with no view the orb is at %+v", l.orb)
	}
	_ = host
}
