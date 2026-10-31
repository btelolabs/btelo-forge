package ui

import (
	"context"
	"image"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
)

// The slideshow fills the window, tabs and all: the app's frame sends it here while one runs, as it does player mode.
// Each slide is drawn once at the window's size and kept, with the next one drawn ahead away from the window, so that
// going on is immediate. A slide with a transition fades in; hidden slides are passed over.

// slideShow is a slideshow under way.
type slideShow struct {
	e       *slidesEditor
	at      int // the slide in the file, len(slides) for the end
	r       gfx.Rect
	pics    map[slideShowKey]*image.RGBA
	making  map[slideShowKey]bool
	from    *image.RGBA // the picture being faded from
	started time.Time
	back    int // where the editor returns to
}

type slideShowKey struct {
	part string
	rev  uint64
	w, h int
}

// fadeTime is how long a transition lasts.
const fadeTime = 450 * time.Millisecond

func (s *slideShow) fading() bool { return s.from != nil && time.Since(s.started) < fadeTime }

// slideshowOn reports that a slideshow fills the window now.
func (a *App) slideshowOn() bool {
	if a.office == nil {
		return false
	}
	e, ok := a.office.ed.(*slidesEditor)
	return ok && e.show != nil
}

// drawSlideshow draws the slideshow over the whole window and takes all input.
func (a *App) drawSlideshow(r gfx.Rect) {
	a.office.ed.(*slidesEditor).show.draw(r)
}

// startShow starts from the first slide, or from the one shown.
func (e *slidesEditor) startShow(fromHere bool) {
	if len(e.d.Slides) == 0 {
		return
	}
	e.endText()
	at := 0
	if fromHere {
		at = e.cur
	}
	s := &slideShow{e: e, pics: map[slideShowKey]*image.RGBA{}, making: map[slideShowKey]bool{}, back: e.cur, at: -1}
	if at = s.nextShown(at, 1); at >= len(e.d.Slides) {
		// Every slide from here on is hidden: the show has nothing to show.
		e.a.toast(e.a.u.t("Every slide from here on is hidden."), false)
		return
	}
	s.at = at
	e.show = s
	e.a.menu = nil
	e.a.u.active = ""
	e.a.requestFrame()
}

// nextShown is the first slide not hidden from i on, in a direction; the ends are -1 and the number of slides.
func (s *slideShow) nextShown(i, dir int) int {
	list := s.e.d.Slides
	for i >= 0 && i < len(list) {
		if !list[i].Hidden() {
			return i
		}
		i += dir
	}
	return i
}

func (s *slideShow) leave() {
	e := s.e
	e.show = nil
	e.a.u.focus = ""
	if s.at >= 0 && s.at < len(e.d.Slides) {
		e.goTo(s.at)
	} else {
		e.goTo(s.back)
	}
	e.a.requestFrame()
}

func (s *slideShow) go_(i int) {
	if i < 0 {
		return
	}
	list := s.e.d.Slides
	i = min(i, len(list))
	if i == s.at {
		return
	}
	s.from = nil
	if i < len(list) && s.at >= 0 && s.at < len(list) && list[i].HasTransition() {
		s.from = s.picture(list[s.at])
		s.started = time.Now()
	}
	s.at = i
	s.e.a.requestFrame()
}

func (s *slideShow) next() { s.go_(s.nextShown(s.at+1, 1)) }

func (s *slideShow) previous() {
	if at := s.nextShown(s.at-1, -1); at >= 0 {
		s.go_(at)
	}
}

// picture is a slide at the window's size: from the store, or drawn now.
func (s *slideShow) picture(sl *pptx.Slide) *image.RGBA {
	key := slideShowKey{sl.Part, sl.Rev, int(s.r.W), int(s.r.H)}
	if img, ok := s.pics[key]; ok {
		return img
	}
	img := s.render(s.e.r, sl, key)
	s.pics[key] = img
	return img
}

func (s *slideShow) render(r *pptx.Renderer, sl *pptx.Slide, key slideShowKey) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, key.w, key.h))
	c := gfx.Wrap(img)
	c.Clear(colBlack)
	d := s.e.d
	k := min(float64(key.w)/d.W, float64(key.h)/d.H)
	x, y := (float64(key.w)-d.W*k)/2, (float64(key.h)-d.H*k)/2
	r.Slide(c, sl, pptx.View{Scale: k, X: float64(int(x)), Y: float64(int(y))}, pptx.Options{})
	return img
}

// prepare draws the next slide ahead, away from the window.
func (s *slideShow) prepare() {
	e := s.e
	i := s.nextShown(s.at+1, 1)
	if i >= len(e.d.Slides) {
		return
	}
	sl := e.d.Slides[i]
	key := slideShowKey{sl.Part, sl.Rev, int(s.r.W), int(s.r.H)}
	if s.pics[key] != nil || s.making[key] {
		return
	}
	s.making[key] = true
	frozen := e.d.Freeze(sl)
	rend := *e.r
	e.a.background(func(context.Context) {
		img := s.render(&rend, frozen, key)
		e.a.post(func() {
			delete(s.making, key)
			s.pics[key] = img
		})
	})
}

func (s *slideShow) draw(r gfx.Rect) {
	e := s.e
	u := &e.a.u
	s.r = r
	u.focus, u.focusDrawn = "office/slides/show", true
	const f5 = 0x74
	keys := u.keys
	u.keys = nil
	for _, k := range keys {
		switch k.Code {
		case keyEscape:
			s.leave()
			return
		case keyRight, keyDown, keyPageDn, keySpace, keyEnter, 'N':
			if s.at >= len(e.d.Slides) {
				s.leave()
				return
			}
			s.next()
		case keyLeft, keyUp, keyPageUp, keyBack, 'P':
			s.previous()
		case keyHome:
			s.go_(s.nextShown(0, 1))
		case keyEnd:
			s.go_(s.nextShown(len(e.d.Slides)-1, -1))
		}
	}
	u.typed = nil
	if u.wheel < 0 {
		s.next()
	} else if u.wheel > 0 {
		s.previous()
	}
	if u.press("office/slides/show/click", r) {
		if s.at >= len(e.d.Slides) {
			u.letGo("office/slides/show/click")
			s.leave()
			return
		}
		s.next()
	}
	u.letGo("office/slides/show/click")
	if u.rightAt != nil {
		s.previous()
	}
	if s.at >= len(e.d.Slides) {
		u.c.Fill(r, colBlack)
		u.label(u.face(15), r, u.t("End of the slideshow. Click to leave."), colDim, 0.5)
		return
	}
	img := s.picture(e.d.Slides[s.at])
	if s.fading() {
		t := float64(time.Since(s.started)) / float64(fadeTime)
		u.c.Blit(s.from, int(r.X), int(r.Y), true)
		u.c.Blit(gfx.Fade(img, t), int(r.X), int(r.Y), false)
		e.a.requestFrame()
		return
	}
	s.from = nil
	u.c.Blit(img, int(r.X), int(r.Y), true)
	s.prepare()
}
