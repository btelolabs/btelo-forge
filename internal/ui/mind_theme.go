package ui

import (
	"fmt"
	"image"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
)

// Themes: the tool bar's theme button opens a panel of the map's themes, each shown as a small map drawn in it; a
// click puts the map in that theme at once, as one step that can be undone.

// icTheme is the theme button's icon: a brush over a frame.
const icTheme = ""

// mindThemeNames name the themes in mind.Themes' order.
var mindThemeNames = msgs("Midnight", "Paper", "Pastel", "Ocean", "Sepia", "Neon")

// Sizes of the theme panel's previews.
const (
	themeCardW = 176
	themeCardH = 112
	themeCols  = 3
)

// mindThemeSample is the small map each theme's preview draws: a root, branches with leaves, and tasks.
func (a *App) mindThemeSample() *mind.Map {
	u := &a.u
	m := mind.New("", a.mindMeasure())
	root, _ := m.Add(mind.Node{Text: u.t("Idea")}, 0, -1)
	for i, s := range []string{"Aa", "Bb", "Cc", "Dd"} {
		c, _ := m.Add(mind.Node{Text: s}, root, -1)
		for k := range 1 + i%2 {
			m.Add(mind.Node{Text: []string{"one", "two"}[k]}, c, -1)
		}
	}
	return m
}

// mindThemePreview is theme th's preview, drawn once at the window's scale and kept.
func (a *App) mindThemePreview(th *mind.Theme, w, h int) *image.RGBA {
	key := fmt.Sprintf("%s/%d/%d", th.ID, w, h)
	if img, ok := a.mindThemeShots[key]; ok {
		return img
	}
	m := a.mindThemeSample()
	m.SetTheme(th.ID)
	img := mind.RenderFit(m, w, h, &mind.Options{Fonts: a.u.fonts})
	gfx.RoundCorners(img, float32(h)/12)
	if a.mindThemeShots == nil {
		a.mindThemeShots = map[string]*image.RGBA{}
	}
	a.mindThemeShots[key] = img
	return img
}

// mindThemePanel is where the theme panel is, or nothing while it is closed: above the zoom bar of the board r,
// whose button opens it, at the right, over the overview; kept on the board when the window is small.
func (a *App) mindThemePanel(md *mindDoc, r, zoom gfx.Rect) gfx.Rect {
	if !md.themes {
		return gfx.Rect{}
	}
	u := &a.u
	rows := (len(mind.Themes) + themeCols - 1) / themeCols
	w := u.px(16) + float32(themeCols)*u.px(themeCardW+12)
	h := u.px(16) + float32(rows)*u.px(themeCardH+34)
	return gfx.Rect{X: max(zoom.Right()-w, r.X+u.px(8)), Y: max(zoom.Y-u.px(8)-h, r.Y+u.px(8)), W: w, H: h}
}

// drawMindThemes draws the theme panel in r: each theme's preview and name, the map's own ringed; a click on one
// puts the map in it, a click outside or Escape closes the panel.
func (a *App) drawMindThemes(md *mindDoc, r gfx.Rect) {
	if r.W == 0 {
		return
	}
	u := &a.u
	for _, k := range u.keys {
		if k.Code == keyEscape {
			md.themes = false
			u.keys = nil
			return
		}
	}
	if u.pressed && !r.Contains(u.pressX, u.pressY) && u.active == "" {
		md.themes = false // a press elsewhere closes it, and goes on to what it pressed
		return
	}
	floatPanel(u, r)
	cur := md.m.Theme().ID
	for i, th := range mind.Themes {
		col, row := i%themeCols, i/themeCols
		card := gfx.Rect{X: r.X + u.px(8) + float32(col)*u.px(themeCardW+12), Y: r.Y + u.px(8) + float32(row)*u.px(themeCardH+34),
			W: u.px(themeCardW), H: u.px(themeCardH)}
		id := "mind/theme/" + th.ID
		hot := u.hover(id, gfx.Rect{X: card.X, Y: card.Y, W: card.W, H: card.H + u.px(26)})
		img := a.mindThemePreview(th, int(card.W), int(card.H))
		u.c.Blit(img, int(card.X), int(card.Y), false)
		switch {
		case th.ID == cur:
			u.c.RoundOutline(card.Inset(-u.px(3)), u.px(12), max(1, u.px(2.5)), colAccent)
		case hot:
			u.c.RoundOutline(card.Inset(-u.px(3)), u.px(12), max(1, u.px(1.5)), colBorderHi)
			u.cursor = CursorHand
		}
		fg := colDim
		if th.ID == cur || hot {
			fg = colText
		}
		u.label(u.face(12.5), gfx.Rect{X: card.X, Y: card.Bottom() + u.px(4), W: card.W, H: u.px(22)}, u.t(mindThemeNames[i]), fg, 0.5)
		if u.clicked(id, gfx.Rect{X: card.X, Y: card.Y, W: card.W, H: card.H + u.px(26)}) && th.ID != cur {
			md.m.SetTheme(th.ID)
		}
	}
}
