package ui

import (
	"image"
	"image/color"
	"math"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// orbSize is the side of the orb that stands for the models running, in points.
const orbSize = 34

// orbLinger is how long the list stays open after the pointer leaves it, so that a slip does not close it.
const orbLinger = 350 * time.Millisecond

// The orb's frame rates: a model that is working moves it fully, and one only held in memory slowly, as the whole window
// is drawn again for each frame. A window in the background keeps it still.
const (
	orbBusyFrame = time.Second / 24
	orbIdleFrame = time.Second / 10
)

// orbState is the orb in the window's corner.
type orbState struct {
	open  bool // the list of models is showing
	until time.Time
	img   *image.RGBA
}

// orbColors are the colours that drift through the orb.
var orbColors = [4]color.RGBA{gfx.Hex(0x4f8cff), gfx.Hex(0xa45cff), gfx.Hex(0x35d6c8), gfx.Hex(0xff6fae)}

func smooth(from, to, x float64) float64 {
	t := min(max((x-from)/(to-from), 0), 1)
	return t * t * (3 - 2*t)
}

// paintOrb draws a frame of the orb into img: a drop of liquid whose edge swells and whose colours flow, with a soft glow
// around it. t is in seconds.
func paintOrb(img *image.RGBA, t float64) {
	size := img.Bounds().Dx()
	half := float64(size) / 2
	aa := 1.6 / half
	type lobe struct{ x, y float64 }
	var lobes [len(orbColors)]lobe
	for i := range lobes {
		f := float64(i)
		lobes[i] = lobe{0.5 * math.Sin(t*(0.7+0.13*f)+f*2.1), 0.5 * math.Cos(t*(0.9-0.11*f)+f*1.3)}
	}
	for py := range size {
		y := (float64(py) + 0.5 - half) / half
		row := img.Pix[py*img.Stride:]
		for px := range size {
			x := (float64(px) + 0.5 - half) / half
			r := math.Hypot(x, y)
			a := math.Atan2(y, x)
			edge := 0.74 + 0.028*math.Sin(3*a+t*1.3) + 0.018*math.Sin(5*a-t*1.9) + 0.014*math.Sin(2*a+t*0.8)
			body := 1 - smooth(edge-aa, edge+aa, r)
			glow := 0.32 * (1 - smooth(edge, 1, r)) * (1 - body)
			alpha := body + glow
			out := row[px*4 : px*4+4 : px*4+4]
			if alpha <= 0 {
				out[0], out[1], out[2], out[3] = 0, 0, 0, 0
				continue
			}
			// Bent coordinates make the colours run like liquid rather than slide like spots.
			wx := x + 0.3*math.Sin(y*2.4+t*1.1)
			wy := y + 0.3*math.Sin(x*2.8-t*0.9)
			var red, green, blue, weights float64
			for i, l := range lobes {
				dx, dy := wx-l.x, wy-l.y
				w := 1 / (0.16 + dx*dx + dy*dy)
				w *= w
				c := orbColors[i]
				red += w * float64(c.R)
				green += w * float64(c.G)
				blue += w * float64(c.B)
				weights += w
			}
			red, green, blue = red/weights, green/weights, blue/weights
			// Lit from the upper left, and dimmer towards the edge, so that it reads as round.
			depth := min(r/edge, 1)
			shade := 1.08 - 0.42*depth*depth
			hx, hy := x+0.28, y+0.32
			shine := 150 * math.Exp(-(hx*hx+hy*hy)/0.07) * body
			put := func(v float64) uint8 { return uint8(min(max((v*shade+shine)*alpha, 0), 255*alpha)) }
			out[0], out[1], out[2], out[3] = put(red), put(green), put(blue), uint8(alpha*255)
		}
	}
}

// taskLayout is where the corner's cards go. Models that stay in memory until stopped are behind the orb, in a list that
// opens over it; work that ends by itself keeps its card above them.
type taskLayout struct {
	tasks   []task // those showing, the first at the bottom
	cards   []gfx.Rect
	cancels []gfx.Rect
	orb     gfx.Rect // empty when no model runs
	zone    gfx.Rect // the orb and its list: the pointer keeps it open here
}

// taskCard is the height of a card: a model has no progress to show.
func (a *App) taskCard(t task) float32 {
	if t.running {
		return a.u.px(46)
	}
	return a.u.px(64)
}

// layoutTasks places the work running in the background.
func (a *App) layoutTasks() taskLayout { return a.placeTasks(a.tasks()) }

func (a *App) placeTasks(tasks []task) taskLayout {
	u := &a.u
	var l taskLayout
	var running, others []task
	for _, t := range tasks {
		if t.running {
			running = append(running, t)
		} else {
			others = append(others, t)
		}
	}
	margin := u.px(16)
	w := min(u.px(280), a.screen.W-2*margin)
	bottom := a.screen.H - margin
	place := func(list []task) {
		for _, t := range list {
			h := a.taskCard(t)
			bottom -= h
			r := gfx.Rect{X: a.screen.W - margin - w, Y: bottom, W: w, H: h}
			l.tasks = append(l.tasks, t)
			l.cards = append(l.cards, r)
			l.cancels = append(l.cancels, gfx.Rect{X: r.Right() - u.px(34), Y: r.Y + u.px(6), W: u.px(28), H: u.px(28)})
			bottom -= u.px(8)
		}
	}
	if len(running) > 0 {
		side := u.px(orbSize)
		l.orb = gfx.Rect{X: a.screen.W - margin - side, Y: bottom - side, W: side, H: side}
		bottom = l.orb.Y - u.px(8)
		top := bottom - float32(len(running))*(u.px(46)+u.px(8))
		l.zone = gfx.Rect{X: a.screen.W - margin - w, Y: top, W: w, H: l.orb.Bottom() - top}
		a.followOrb(l)
		if a.orb.open {
			place(running)
		}
	} else {
		a.orb.open = false
	}
	place(others)
	return l
}

// followOrb opens the list while the pointer is on the orb, and keeps it while the pointer is on the list. A press on a
// card holds it open too, as the card must be there when the mouse is let go.
func (a *App) followOrb(l taskLayout) {
	u := &a.u
	now := time.Now()
	zone := l.orb
	if a.orb.open {
		zone = l.zone
	}
	if !u.blocked && zone.Contains(u.mx, u.my) || u.active != "" && a.orb.open {
		a.orb.open, a.orb.until = true, now.Add(orbLinger)
		return
	}
	if a.orb.open && now.After(a.orb.until) {
		a.orb.open = false
	}
}

// drawOrb draws the orb, a frame for the time now, or the last frame when the window is in the background.
func (a *App) drawOrb(r gfx.Rect) {
	u := &a.u
	// The picture is as sharp as the window's pixels; below that it would look blurred at the edge of the liquid.
	size := int(r.W)
	if a.orb.img == nil || a.orb.img.Bounds().Dx() != size {
		a.orb.img = image.NewRGBA(image.Rect(0, 0, size, size))
		paintOrb(a.orb.img, 0)
	}
	if !u.inactive {
		paintOrb(a.orb.img, float64(time.Now().UnixMilli())/1000)
	}
	u.c.Image(a.orb.img, gfx.Rect{X: float32(int(r.X)), Y: float32(int(r.Y)), W: float32(size), H: float32(size)})
}

// orbFrame is how soon the orb wants its next frame, or 0 when it wants none.
func (a *App) orbFrame() time.Duration {
	if a.u.inactive {
		return 0
	}
	for _, t := range a.tasks() {
		if t.running {
			if a.ai.answer != nil || a.mail.organised {
				return orbBusyFrame
			}
			return orbIdleFrame
		}
	}
	return 0
}
