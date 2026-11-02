package ui

import (
	"context"
	"fmt"
	"image"
	"math"
	"path/filepath"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mail"
)

// WebView is the system's own browser engine inside the window, one implementation per system. It shows a message's
// page and nothing else: scripts are off, and everything but the page itself, and pictures when allowed, is refused.
type WebView interface {
	// Show loads a page, with its Content-Security-Policy; pictures lets it fetch images from outside. fit is run in it
	// by the view, when it has loaded and when the view's width changes; the page's own scripts stay off.
	Show(page, policy, fit string, pictures bool)
	// Place shows the view over r of the window, in pixels. The view hides what is drawn beneath it, so holes are cut
	// out of it for what is drawn over it.
	Place(r image.Rectangle, holes []image.Rectangle)
	Hide()
	Links() []string // the links clicked since it was last asked
	Touched() bool   // the reader went into the view since it was last asked
	Err() error      // what stopped the view, if anything did
	Close()
}

// webHost is a host that can open a view; one that cannot, as in the tests, leaves a message as text.
type webHost interface {
	OpenWebView(dir string, wake func()) (WebView, error)
}

// mailPage is the page of the message open.
type mailPage struct {
	message  int64 // the message it is of
	body     mail.Body
	known    bool // body was read
	pictures bool // its pictures may be fetched
	fetching bool // a server is being asked for a page there is none of yet
	remote   bool // it has pictures from outside, which takes a search of all of it to know
	held     bool // the page before this one stays in the view until this one is read, which is less than a frame or two
	shown    string
}

// mailPageWait is how long a message open waits for a server before its plain copy is shown.
const mailPageWait = 8 * time.Second

// mailPageOf reads the page of the message open, and fetches what is missing of it, once for each message opened.
func (a *App) mailPageOf(m mail.Message) *mailPage {
	s := &a.mail
	p := &s.page
	if p.message == m.ID {
		return p
	}
	*p = mailPage{message: m.ID, held: p.held || p.shown != ""}
	store, mailer := s.mailer.Store, s.mailer
	ac, known := s.account(m.Account)
	a.background(func(ctx context.Context) {
		body, err := store.Body(m.ID)
		remote := mail.HasRemote(body.HTML)
		trusted, terr := store.Trusted(m.Address)
		if err == nil {
			err = terr
		}
		a.post(func() {
			if err != nil {
				s.notice = a.describe(err)
			}
			if p.message == m.ID {
				p.body, p.known, p.pictures, p.remote = body, err == nil, trusted, remote
				p.fetching = err == nil && !body.Whole && known && body.HTML == ""
			}
		})
		if err != nil || body.Whole || !known {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, mailPageWait)
		defer cancel()
		whole, err := mailer.Complete(ctx, ac, m.ID)
		remote = mail.HasRemote(whole.HTML)
		a.post(func() {
			if p.message != m.ID {
				return
			}
			p.fetching = false
			// Not being able to reach the server leaves what is kept: mail is read offline too.
			if err == nil {
				p.body, p.remote = whole, remote
			}
		})
	})
	return p
}

// mailWebView is the view, opened when first needed; nil means the message is shown as text.
func (a *App) mailWebView() WebView {
	s := &a.mail
	if s.web != nil || s.webOff {
		return s.web
	}
	h, ok := a.host.(webHost)
	if !ok {
		s.webOff = true
		return nil
	}
	v, err := h.OpenWebView(filepath.Join(a.cacheDir, "webview"), a.host.Wake)
	if err != nil {
		s.webOff = true
		s.notice = a.describe(err)
		return nil
	}
	s.web = v
	return v
}

// drawMailPage keeps a place in the window for the view, whose page Frame's end puts there.
func (a *App) drawMailPage(r gfx.Rect, p *mailPage) {
	s := &a.mail
	want := fmt.Sprintf("%d/%t/%d", p.message, p.pictures, len(p.body.HTML))
	if p.known && p.shown != want {
		p.shown = want
		s.web.Show(mail.Page(p.body.HTML, p.pictures), mail.PagePolicy(p.pictures), mail.PageFit, p.pictures)
	}
	a.u.c.Fill(r, colWhite) // what a dialog over the view shows of it, and the moment before the page arrives
	s.webAt = r
}

// drawMailPictures offers the pictures of a message, which stay out until asked for: fetching one tells its sender that
// the message was opened. The offer shares the row of the message's buttons, of which used is taken, while it fits: the
// less of the reader goes on what is around a message, the more of it shows.
func (a *App) drawMailPictures(x, y, w, used float32, m mail.Message, p *mailPage) float32 {
	u := &a.u
	s := &a.mail
	end := y + u.px(44)
	if p.pictures || !p.remote {
		return end
	}
	f := u.face(12.5)
	show, always := u.t("Show pictures"), u.t("Always for this sender")
	bw, tw := f.Measure(show)+u.px(28), f.Measure(always)+u.px(28)
	row := rectAt(x+used, y, w-used, u.px(30))
	if row.W < bw+tw+u.px(8) {
		y = end - u.px(6)
		end = y + u.px(40)
		row = rectAt(x, y, w, u.px(30))
		u.c.Text(f, row.X, row.Y+(row.H-f.Height())/2, f.Ellipsize(u.t("Pictures are hidden for your privacy"), max(row.W-bw-tw-u.px(18), 0)), colDim)
	}
	if u.button("mail/pictures", rectAt(row.Right()-bw-tw-u.px(8), row.Y, bw, row.H), "", show, plain, true) {
		p.pictures = true
	}
	if u.button("mail/trust", rectAt(row.Right()-tw, row.Y, tw, row.H), "", always, plain, m.Address != "") {
		p.pictures = true
		if err := s.mailer.Store.Trust(m.Address); err != nil {
			s.notice = a.describe(err)
		}
	}
	return end
}

// mailWebCovered reports whether a dialog holds the window: it is over the whole view, dimming what is behind it.
func (a *App) mailWebCovered() bool {
	return a.filePicker != nil || a.export != nil || a.settingsOpen || a.rec.open || a.rec.ask == askInWindow || a.cam.open || a.dialog.kind != dialogNone ||
		a.adjust != nil || a.style != nil || a.print != nil || a.smartList != nil
}

// syncMailWeb brings the view in line with the frame just drawn. It comes last, when all that is drawn over the view is
// known.
func (a *App) syncMailWeb() {
	s := &a.mail
	u := &a.u
	at, covers := s.webAt, u.covers
	s.webAt, s.webLast, u.covers = gfx.Rect{}, at, u.covers[:0]
	if s.web == nil {
		return
	}
	if err := s.web.Err(); err != nil {
		s.web.Close()
		s.web, s.webOff, s.page.shown = nil, true, ""
		s.notice = a.describe(err)
		return
	}
	for _, link := range s.web.Links() {
		if err := a.host.LaunchURL(link); err != nil {
			s.notice = a.describe(err)
		}
	}
	if s.web.Touched() {
		a.menu = nil // a click in the view is a click outside the menu
	}
	if at.W <= 0 || at.H <= 0 || a.mailWebCovered() {
		s.web.Hide()
		return
	}
	box := pixels(at)
	var holes []image.Rectangle
	for _, r := range covers {
		if h := pixels(r).Intersect(box); !h.Empty() {
			holes = append(holes, h)
		}
	}
	s.web.Place(box, holes)
}

// pixels is the smallest rectangle of whole pixels around r.
func pixels(r gfx.Rect) image.Rectangle {
	return image.Rect(int(math.Floor(float64(r.X))), int(math.Floor(float64(r.Y))), int(math.Ceil(float64(r.Right()))), int(math.Ceil(float64(r.Bottom()))))
}
