package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

// The drawing tools: they act on the canvas in the active tab, the same picture the window shows, and each change
// is one step of its history, as if made with the mouse.

var errNoCanvas = errors.New("the active tab has no canvas; call canvas_new or canvas_open first")

// canvasNow is the canvas in the active tab, with anything under way in it (a transform, typing, a curve being
// drawn) settled first, as a menu command settles it.
func (a *App) canvasNow() (*artDoc, error) {
	ad := a.art
	if ad == nil {
		return nil, errNoCanvas
	}
	if ad.stroke != nil || ad.move != nil || ad.shape != nil || ad.props != nil || ad.nodes != nil && ad.nodes.s != nil {
		return nil, errors.New("the canvas is being drawn on with the mouse; try again in a moment")
	}
	a.applyTransform(ad)
	a.commitText(ad)
	a.finishPen(ad)
	return ad, nil
}

// drawEdit runs fn on the active canvas and shows what it changed.
func (a *App) drawEdit(fn func(ad *artDoc) (any, error)) (any, error) {
	ad, err := a.canvasNow()
	if err != nil {
		return nil, err
	}
	v, err := fn(ad)
	ad.changedAll()
	return v, err
}

// nrgba reads a #rrggbb or #rrggbbaa colour.
func nrgba(s string) (color.NRGBA, error) {
	if len(s) == 9 && s[0] == '#' {
		c, err := parseColor(s[:7])
		if err != nil {
			return color.NRGBA{}, err
		}
		var al uint8
		if _, err := fmt.Sscanf(s[7:], "%02x", &al); err != nil {
			return color.NRGBA{}, fmt.Errorf("color %q is not #rrggbbaa", s)
		}
		return color.NRGBA{R: c[0], G: c[1], B: c[2], A: al}, nil
	}
	c, err := parseColor(s)
	return color.NRGBA{R: c[0], G: c[1], B: c[2], A: 255}, err
}

func hexOf(c color.NRGBA) string {
	if c.A == 255 {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

func rectView(r image.Rectangle) []int {
	if r.Empty() {
		return nil
	}
	return []int{r.Min.X, r.Min.Y, r.Dx(), r.Dy()}
}

// layerView is a layer or group for canvas_state.
type layerView struct {
	ID        int         `json:"id"`
	Name      string      `json:"name"`
	Kind      string      `json:"kind"` // pixels, vector, text or group
	Visible   bool        `json:"visible"`
	Opacity   float64     `json:"opacity"`
	Blend     string      `json:"blend"`
	LockAlpha bool        `json:"lock_alpha,omitempty"`
	Mask      string      `json:"mask,omitempty"` // on or off
	Bounds    []int       `json:"bounds,omitempty"`
	Text      *paint.Text `json:"text,omitempty"`
	Shape     string      `json:"shape,omitempty"`
	Fill      string      `json:"fill,omitempty"`
	Stroke    string      `json:"stroke,omitempty"`
	Width     float64     `json:"stroke_width,omitempty"`
	Nodes     int         `json:"nodes,omitempty"`
	Adjust    string      `json:"adjustment,omitempty"` // an adjustment layer's kind
	Settings  any         `json:"settings,omitempty"`   // and its settings
	Style     any         `json:"style,omitempty"`      // the layer's effects
	Children  []layerView `json:"children,omitempty"`
}

var vectorKindNames = []string{"curve", "rectangle", "ellipse", "polygon", "star"}

// blendIDs name the blend modes for the tools, in paint.Blends' order.
var blendIDs = []string{"normal", "darken", "multiply", "color_burn", "linear_burn", "darker_color", "lighten", "screen",
	"color_dodge", "linear_dodge", "lighter_color", "overlay", "soft_light", "hard_light", "vivid_light", "linear_light",
	"pin_light", "hard_mix", "difference", "exclusion", "subtract", "divide", "hue", "saturation", "color", "luminosity"}

func blendID(b paint.Blend) string {
	if i := slices.Index(paint.Blends, b); i >= 0 {
		return blendIDs[i]
	}
	if b == paint.BlendPassThrough {
		return "pass_through"
	}
	return "normal"
}

func blendFrom(s string) (paint.Blend, error) {
	if s == "pass_through" {
		return paint.BlendPassThrough, nil
	}
	if i := slices.Index(blendIDs, s); i >= 0 {
		return paint.Blends[i], nil
	}
	return 0, fmt.Errorf("unknown blend mode %q; one of %v", s, blendIDs)
}

func paintView(p paint.VectorPaint) string {
	switch p.Kind {
	case paint.PaintSolid:
		return hexOf(p.Color)
	case paint.PaintLinear, paint.PaintRadial:
		return "gradient"
	}
	return ""
}

func viewOfNode(n *paint.Node) layerView {
	v := layerView{ID: n.ID, Name: n.Name, Visible: n.Visible, Opacity: n.Opacity, Blend: blendID(n.Blend), LockAlpha: n.LockAlpha}
	if n.Mask != nil {
		v.Mask = "on"
		if n.MaskOff {
			v.Mask = "off"
		}
	}
	switch {
	case n.IsGroup():
		v.Kind = "group"
		for _, c := range n.Children {
			v.Children = append(v.Children, viewOfNode(c))
		}
		return v
	case n.Vector != nil:
		vec := n.Vector
		v.Kind, v.Shape = "vector", vectorKindNames[vec.Kind]
		v.Fill, v.Stroke, v.Width = paintView(vec.Fill), paintView(vec.Stroke.Paint), vec.Stroke.Width
		for _, s := range vec.Outline() {
			v.Nodes += len(s.Nodes)
		}
	case n.Adjust != nil:
		v.Kind, v.Adjust = "adjustment", adjustKindIDs[n.Adjust.Kind]
		v.Settings = map[string]any{"values": n.Adjust.Values, "colorize": n.Adjust.On, "curve": n.Adjust.Curve}
		return v
	case n.Text != nil:
		v.Kind, v.Text = "text", n.Text
	default:
		v.Kind = "pixels"
	}
	v.Bounds = rectView(paint.ReachOf(n))
	if n.Style != nil {
		v.Style = n.Style
	}
	return v
}

// canvasView is the state of the active canvas for canvas_state.
func (a *App) canvasView(ad *artDoc) map[string]any {
	d := ad.d
	var layers []layerView
	for _, c := range d.Root.Children {
		layers = append(layers, viewOfNode(c))
	}
	var boards []map[string]any
	for _, b := range d.Boards {
		boards = append(boards, map[string]any{"id": b.ID, "name": b.Name, "rect": rectView(b.Rect), "background": hexOf(b.Fill)})
	}
	steps, done := d.History()
	return map[string]any{
		"path": ad.path, "frame": rectView(d.Frame), "content_bounds": rectView(d.Bounds()), "paper": d.Paper,
		"artboards": boards, "layers": layers, "active_layer": d.Active, "also_chosen": ad.picked,
		"selection": rectView(d.SelectionBounds()), "history": steps, "history_done": done,
		"foreground": hexOf(a.brush.Color), "background": hexOf(a.backgroundColor()), "tool": artToolNames[a.artTool],
		"view": map[string]any{"zoom": ad.zoom, "visible": rectView(d.View)},
		"note": "coordinates are canvas pixels; rectangles are [x, y, width, height]; the canvas has no edge beyond its frame or artboards",
	}
}

type ptIn [2]float64

func (p ptIn) pt() paint.Pt { return paint.Pt{X: p[0], Y: p[1]} }

// strokeIn and fillIn describe a vector object's paint.
type vectorStyleIn struct {
	Fill        *string   `json:"fill,omitempty" jsonschema:"fill colour #rrggbb or #rrggbbaa, or none; the foreground colour when left out for a shape"`
	Stroke      *string   `json:"stroke,omitempty" jsonschema:"stroke colour, or none"`
	StrokeWidth *float64  `json:"stroke_width,omitempty" jsonschema:"stroke width in canvas pixels"`
	Align       string    `json:"align,omitempty" jsonschema:"stroke alignment: centre, inside or outside"`
	Cap         string    `json:"cap,omitempty" jsonschema:"line ends: butt, round or square"`
	Join        string    `json:"join,omitempty" jsonschema:"corners: miter, round or bevel"`
	Dash        []float64 `json:"dash,omitempty" jsonschema:"dash pattern in stroke widths: on, off, on, off…; [] for solid"`
	FillRule    string    `json:"fill_rule,omitempty" jsonschema:"non_zero or even_odd"`
}

// apply sets the style given on v.
func (s vectorStyleIn) apply(v *paint.Vector) error {
	set := func(p *paint.VectorPaint, val *string) error {
		if val == nil {
			return nil
		}
		if *val == "none" || *val == "" {
			*p = paint.VectorPaint{}
			return nil
		}
		c, err := nrgba(*val)
		*p = solidPaint(c)
		return err
	}
	if err := set(&v.Fill, s.Fill); err != nil {
		return err
	}
	if err := set(&v.Stroke.Paint, s.Stroke); err != nil {
		return err
	}
	if s.StrokeWidth != nil {
		if *s.StrokeWidth < 0 || *s.StrokeWidth > 10000 {
			return errors.New("stroke_width must be from 0 to 10000")
		}
		v.Stroke.Width = *s.StrokeWidth
	}
	pick := func(val string, names []string, set func(int)) error {
		if val == "" {
			return nil
		}
		i := slices.Index(names, val)
		if i < 0 {
			return fmt.Errorf("%q is not one of %v", val, names)
		}
		set(i)
		return nil
	}
	if err := pick(s.Align, []string{"centre", "inside", "outside"}, func(i int) { v.Stroke.Align = paint.StrokeAlign(i) }); err != nil {
		return err
	}
	if err := pick(s.Cap, []string{"butt", "round", "square"}, func(i int) { v.Stroke.Cap = paint.LineCap(i) }); err != nil {
		return err
	}
	if err := pick(s.Join, []string{"miter", "round", "bevel"}, func(i int) { v.Stroke.Join = paint.LineJoin(i) }); err != nil {
		return err
	}
	if err := pick(s.FillRule, []string{"non_zero", "even_odd"}, func(i int) { v.Rule = paint.FillRule(i) }); err != nil {
		return err
	}
	if s.Dash != nil {
		if len(s.Dash) > 64 {
			return errors.New("a dash pattern has at most 64 lengths")
		}
		for _, l := range s.Dash {
			if l < 0 || l > 1000 {
				return errors.New("dash lengths are from 0 to 1000 stroke widths")
			}
		}
		v.Stroke.Dash = slices.Clone(s.Dash)
	}
	return nil
}

// renderPNG draws the picture's area r at scale (at most 1) as a PNG.
func renderPNG(d *paint.Doc, r image.Rectangle, scale float64) ([]byte, error) {
	if r.Empty() {
		return nil, paint.ErrEmpty
	}
	l := paint.LevelFor(scale)
	lr := image.Rect(r.Min.X>>l, r.Min.Y>>l, (r.Max.X+(1<<l)-1)>>l, (r.Max.Y+(1<<l)-1)>>l)
	if lr.Dx()*lr.Dy() > 4096*4096 {
		return nil, errors.New("that area is too large to show; give a smaller rect or a smaller scale")
	}
	img := image.NewRGBA(lr)
	d.Render(img, lr, l)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func imageResult(data []byte, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return errorResult(err), nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: "image/png"}}}, nil, nil
}

func (a *App) addDrawTools(s *mcp.Server) {
	type newIn struct {
		Width      int    `json:"width,omitempty" jsonschema:"a frame's width; leave out width and height for an infinite canvas with no size, as Affinity Designer's"`
		Height     int    `json:"height,omitempty"`
		Background string `json:"background,omitempty" jsonschema:"the framed canvas's background colour, or none for transparent; white when left out"`
	}
	tool(s, a, "canvas_new", "Make a new drawing canvas in its own tab and return its state. With no size it is infinite: draw anywhere, export the content or artboards.", func(in newIn) (any, error) {
		if in.Width == 0 && in.Height == 0 {
			if err := a.newCanvas(); err != nil {
				return nil, err
			}
			return a.canvasView(a.art), nil
		}
		bg := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		switch in.Background {
		case "":
		case "none":
			bg = color.NRGBA{}
		default:
			c, err := nrgba(in.Background)
			if err != nil {
				return nil, err
			}
			bg = c
		}
		d, err := paint.NewFramed(in.Width, in.Height, a.u.tc("layer", "Background"), bg)
		if err != nil {
			return nil, err
		}
		path, err := a.capturePath(a.u.t("Canvas %s"), paint.Ext, time.Now())
		if err != nil {
			return nil, err
		}
		a.openArt(d, path)
		a.art.dirty = true
		if err := a.saveArt(a.art, true); err != nil {
			return nil, err
		}
		return a.canvasView(a.art), nil
	})

	type openIn struct {
		Path string `json:"path" jsonschema:"absolute path of a .btcanvas file, or a picture (PNG, JPEG…) to open as a new canvas"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "canvas_open", Description: "Open a saved canvas (.btcanvas) or a picture as a canvas in a tab, and return its state."},
		func(ctx contextT, req *mcp.CallToolRequest, in openIn) (*mcp.CallToolResult, any, error) {
			if _, err := os.Stat(in.Path); err != nil {
				return errorResult(err), nil, nil
			}
			before, _ := call(a, func() (*artDoc, error) {
				if strings.EqualFold(filepath.Ext(in.Path), paint.Ext) {
					a.openCanvasFile(in.Path)
				} else {
					a.openImageAsArt(in.Path)
				}
				return a.art, nil
			})
			err := waitFor(a, time.Minute, func() (bool, error) { return a.art != nil && a.art != before, nil })
			if err != nil {
				return errorResult(fmt.Errorf("the canvas did not open: %w", err)), nil, nil
			}
			return result(call(a, func() (any, error) { return a.canvasView(a.art), nil }))
		})

	tool(s, a, "canvas_state", "The active canvas: its layers (ids, kinds, bounds, styles), artboards, frame, selection, colours and history. Call it first; ids come from it.", func(none) (any, error) {
		ad, err := a.canvasNow()
		if err != nil {
			return nil, err
		}
		return a.canvasView(ad), nil
	})

	type renderIn struct {
		Rect     []int   `json:"rect,omitempty" jsonschema:"[x, y, width, height] in canvas pixels; the frame, or else all that is drawn, when left out"`
		Artboard int     `json:"artboard,omitempty" jsonschema:"an artboard id: show that artboard as it exports"`
		Scale    float64 `json:"scale,omitempty" jsonschema:"1 for full size (the default), 0.5 for half…"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "canvas_render", Description: "A PNG of the canvas as it looks (or one artboard), to check the drawing."},
		func(ctx contextT, req *mcp.CallToolRequest, in renderIn) (*mcp.CallToolResult, any, error) {
			return imageResult(call(a, func() ([]byte, error) {
				ad, err := a.canvasNow()
				if err != nil {
					return nil, err
				}
				d := ad.d
				r := d.Extent()
				for _, b := range d.Boards {
					if d.Frame.Empty() {
						r = r.Union(b.Rect)
					}
				}
				switch {
				case in.Artboard != 0:
					b, ok := d.Board(in.Artboard)
					if !ok {
						return nil, paint.ErrNoBoard
					}
					r = b.Rect
				case len(in.Rect) == 4:
					r = image.Rect(in.Rect[0], in.Rect[1], in.Rect[0]+in.Rect[2], in.Rect[1]+in.Rect[3])
				case len(in.Rect) != 0:
					return nil, errors.New("rect is [x, y, width, height]")
				}
				scale := in.Scale
				if scale <= 0 || scale > 1 {
					scale = 1
				}
				return renderPNG(d, r, scale)
			}))
		})

	tool(s, a, "canvas_save", "Save the active canvas to its .btcanvas file (it is also saved by itself a moment after each change).", func(none) (any, error) {
		ad, err := a.canvasNow()
		if err != nil {
			return nil, err
		}
		ad.dirty = true
		if err := a.saveArt(ad, true); err != nil {
			return nil, err
		}
		return map[string]any{"path": ad.path}, nil
	})

	type exportIn struct {
		What     string `json:"what,omitempty" jsonschema:"picture (the frame, or all that is drawn; the default), selection, artboard or artboards (each its own PNG)"`
		Artboard int    `json:"artboard,omitempty" jsonschema:"the artboard id, for what artboard"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "canvas_export", Description: "Export PNGs next to the canvas file and return their paths."},
		func(ctx contextT, req *mcp.CallToolRequest, in exportIn) (*mcp.CallToolResult, any, error) {
			type job struct {
				paths []string
				pics  []namedPicture
			}
			j, err := call(a, func() (job, error) {
				ad, err := a.canvasNow()
				if err != nil {
					return job{}, err
				}
				d := ad.d
				var pics []namedPicture
				switch in.What {
				case "", "picture":
					img, err := d.Picture()
					if err != nil {
						return job{}, err
					}
					pics = append(pics, namedPicture{img: img})
				case "selection":
					if d.Selection == nil {
						return job{}, errors.New("nothing is selected")
					}
					img, err := d.Copy(true)
					if err != nil {
						return job{}, err
					}
					pics = append(pics, namedPicture{name: a.u.t("Selection"), img: img})
				case "artboard", "artboards":
					for _, b := range d.Boards {
						if in.What == "artboard" && b.ID != in.Artboard {
							continue
						}
						img, err := d.BoardPicture(b.ID)
						if err != nil {
							return job{}, err
						}
						pics = append(pics, namedPicture{name: b.Name, img: img})
					}
					if len(pics) == 0 {
						return job{}, paint.ErrNoBoard
					}
				default:
					return job{}, fmt.Errorf("unknown export %q", in.What)
				}
				return job{paths: exportTargets(ad, pics), pics: pics}, nil
			})
			if err != nil {
				return errorResult(err), nil, nil
			}
			for i, p := range j.pics {
				if err := writePNG(j.paths[i], p.img); err != nil {
					return errorResult(err), nil, nil
				}
			}
			return result(map[string]any{"files": j.paths}, nil)
		})

	type layerIn struct {
		Action string `json:"action" jsonschema:"add (a pixel layer), add_group, choose, delete, duplicate, move_up, move_down, merge_down, merge_group, flatten, rasterize, convert_to_curves, group, ungroup, add_mask, apply_mask, delete_mask"`
		ID     int    `json:"id,omitempty" jsonschema:"the layer to act on; the chosen layer when left out"`
		Name   string `json:"name,omitempty" jsonschema:"a new layer's or group's name"`
		Also   []int  `json:"also,omitempty" jsonschema:"for choose: more layers chosen besides id, as for boolean"`
	}
	tool(s, a, "canvas_layer", "Add, choose, delete, duplicate, reorder, merge, group, mask or rasterize layers. New layers go above the chosen one.", func(in layerIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			if in.ID != 0 {
				if d.Node(in.ID) == nil {
					return nil, paint.ErrNoLayer
				}
				d.Choose(in.ID)
			}
			name := in.Name
			var err error
			switch in.Action {
			case "add":
				if name == "" {
					name = a.layerName()
				}
				d.AddLayer(name)
			case "add_group":
				if name == "" {
					name = a.groupName()
				}
				d.AddGroup(name)
			case "choose":
				ad.picked = nil
				for _, id := range in.Also {
					if d.Node(id) == nil {
						return nil, fmt.Errorf("there is no layer %d", id)
					}
					ad.togglePicked(id)
				}
			case "delete":
				err = d.Delete()
			case "duplicate":
				_, err = d.Duplicate(a.u.t(" copy"))
			case "move_up", "move_down":
				err = d.Move(in.Action == "move_up")
			case "merge_down":
				err = d.MergeDown()
			case "merge_group":
				err = d.MergeGroup()
			case "flatten":
				d.Flatten(a.u.tc("layer", "Background"))
			case "rasterize":
				err = d.Rasterize()
			case "convert_to_curves":
				n, ok := ad.editedVector()
				if !ok {
					return nil, paint.ErrNotVector
				}
				d.SetVector(n.ID, n.Vector.ToCurve(), "Convert to curves")
			case "group":
				if name == "" {
					name = a.groupName()
				}
				_, err = d.GroupChosen(name)
			case "ungroup":
				err = d.Ungroup()
			case "add_mask":
				err = d.AddMask()
			case "apply_mask":
				err = d.ApplyMask()
			case "delete_mask":
				err = d.DeleteMask()
			default:
				return nil, fmt.Errorf("unknown action %q", in.Action)
			}
			if err != nil {
				return nil, err
			}
			return map[string]any{"active_layer": d.Active, "layers": len(d.Layers())}, nil
		})
	})

	type setLayerIn struct {
		ID        int      `json:"id" jsonschema:"the layer or group"`
		Name      *string  `json:"name,omitempty"`
		Visible   *bool    `json:"visible,omitempty"`
		Opacity   *float64 `json:"opacity,omitempty" jsonschema:"0 to 1"`
		Blend     string   `json:"blend,omitempty" jsonschema:"normal, multiply, screen, overlay, soft_light, darken, lighten, difference, color, luminosity… (canvas_state lists them by name)"`
		LockAlpha *bool    `json:"lock_alpha,omitempty" jsonschema:"lock the transparent pixels"`
		MaskOff   *bool    `json:"mask_off,omitempty" jsonschema:"turn the layer mask off or on"`
		EditMask  *bool    `json:"edit_mask,omitempty" jsonschema:"paint on the layer mask instead of the pixels"`
	}
	tool(s, a, "canvas_set_layer", "Rename a layer, show or hide it, set its opacity, blend mode, locks and mask.", func(in setLayerIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			n := d.Node(in.ID)
			if n == nil {
				return nil, paint.ErrNoLayer
			}
			if in.Opacity != nil && (*in.Opacity < 0 || *in.Opacity > 1) {
				return nil, errors.New("opacity is from 0 to 1")
			}
			if in.Blend != "" {
				b, err := blendFrom(in.Blend)
				if err != nil {
					return nil, err
				}
				d.SetBlend(n.ID, b)
			}
			if in.Name != nil {
				d.Rename(n.ID, *in.Name)
			}
			if in.Visible != nil {
				d.SetVisible(n.ID, *in.Visible)
			}
			if in.Opacity != nil {
				d.SetOpacity(n.ID, *in.Opacity)
			}
			if in.LockAlpha != nil {
				d.SetLockAlpha(n.ID, *in.LockAlpha)
			}
			if (in.MaskOff != nil || in.EditMask != nil) && n.Mask == nil {
				return nil, errors.New("the layer has no mask; add one with canvas_layer add_mask")
			}
			if in.MaskOff != nil {
				d.SetMaskOff(n.ID, *in.MaskOff)
			}
			if in.EditMask != nil {
				d.Choose(n.ID)
				d.EditMask = *in.EditMask
			}
			return viewOfNode(d.Node(n.ID)), nil
		})
	})

	type selectIn struct {
		Shape     string  `json:"shape" jsonschema:"rect, ellipse, polygon, wand (by colour from a point), all, none, invert or reselect"`
		Rect      []int   `json:"rect,omitempty" jsonschema:"[x, y, width, height] for rect and ellipse"`
		Points    []ptIn  `json:"points,omitempty" jsonschema:"[[x, y], …] for polygon; [[x, y]] for wand"`
		Mode      string  `json:"mode,omitempty" jsonschema:"new (default), add, subtract or intersect"`
		Feather   float64 `json:"feather,omitempty" jsonschema:"softens the edge by this many pixels"`
		Tolerance *int    `json:"tolerance,omitempty" jsonschema:"wand: 0 to 255, how far colours may differ (32 when left out)"`
		Merged    bool    `json:"merged,omitempty" jsonschema:"wand: look at all layers, not only the chosen one"`
		Scattered bool    `json:"scattered,omitempty" jsonschema:"wand: take matching colours anywhere, not only touching"`
	}
	tool(s, a, "canvas_select", "Make or change the selection, which painting, filling, filters and transforms then keep to.", func(in selectIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			modes := []string{"new", "add", "subtract", "intersect"}
			mode := paint.SelectNew
			if in.Mode != "" {
				i := slices.Index(modes, in.Mode)
				if i < 0 {
					return nil, fmt.Errorf("mode is one of %v", modes)
				}
				mode = paint.SelectMode(i)
			}
			rect := func() (image.Rectangle, error) {
				if len(in.Rect) != 4 || in.Rect[2] <= 0 || in.Rect[3] <= 0 {
					return image.Rectangle{}, errors.New("rect is [x, y, width, height] with a size")
				}
				return image.Rect(in.Rect[0], in.Rect[1], in.Rect[0]+in.Rect[2], in.Rect[1]+in.Rect[3]), nil
			}
			var shape *paint.Mask
			switch in.Shape {
			case "rect", "ellipse":
				r, err := rect()
				if err != nil {
					return nil, err
				}
				shape = d.RectMask(r)
				if in.Shape == "ellipse" {
					shape = d.EllipseMask(r)
				}
			case "polygon":
				if len(in.Points) < 3 {
					return nil, errors.New("a polygon needs three points or more")
				}
				pts := make([][2]float64, len(in.Points))
				for i, p := range in.Points {
					pts[i] = p
				}
				shape = d.PolygonMask(pts)
			case "wand":
				if len(in.Points) != 1 {
					return nil, errors.New("wand takes one point")
				}
				tol := 32
				if in.Tolerance != nil {
					tol = min(max(*in.Tolerance, 0), 255)
				}
				m, err := d.WandMask(int(math.Floor(in.Points[0][0])), int(math.Floor(in.Points[0][1])), tol, !in.Scattered, in.Merged)
				if err != nil {
					return nil, err
				}
				shape = m
			case "all":
				d.SelectAll()
			case "none":
				d.Deselect()
			case "invert":
				d.InvertSelection()
			case "reselect":
				d.Reselect()
			default:
				return nil, fmt.Errorf("unknown shape %q", in.Shape)
			}
			if shape != nil {
				d.SelectShape(d.Feathered(shape, max(in.Feather, 0)), mode)
			}
			return map[string]any{"selection": rectView(d.SelectionBounds())}, nil
		})
	})

	type strokeIn struct {
		Points   []ptIn   `json:"points" jsonschema:"the line, [[x, y], …] in canvas pixels; one point dabs once"`
		Color    string   `json:"color,omitempty" jsonschema:"#rrggbb; the foreground colour when left out"`
		Size     float64  `json:"size,omitempty" jsonschema:"brush diameter in pixels (the brush's size when left out)"`
		Hardness *float64 `json:"hardness,omitempty" jsonschema:"0 soft to 1 hard"`
		Opacity  *float64 `json:"opacity,omitempty" jsonschema:"0 to 1"`
		Flow     *float64 `json:"flow,omitempty" jsonschema:"0 to 1"`
		Eraser   bool     `json:"eraser,omitempty" jsonschema:"erase instead of paint"`
	}
	tool(s, a, "canvas_paint", "Paint a brush stroke (or erase) along points on the chosen pixel layer, within the selection.", func(in strokeIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			if len(in.Points) == 0 {
				return nil, errors.New("give the stroke's points")
			}
			b := a.brush
			b.Smooth = 0 // the points are exact: no trailing
			if in.Color != "" {
				c, err := nrgba(in.Color)
				if err != nil {
					return nil, err
				}
				b.Color = c
			}
			if in.Size > 0 {
				b.Size = min(in.Size, 5000)
			}
			for _, f := range []struct {
				v   *float64
				dst *float64
			}{{in.Hardness, &b.Hardness}, {in.Opacity, &b.Opacity}, {in.Flow, &b.Flow}} {
				if f.v != nil {
					*f.dst = min(max(*f.v, 0), 1)
				}
			}
			st, _, err := ad.d.BeginStroke(b, in.Eraser, in.Points[0][0], in.Points[0][1])
			if err != nil {
				return nil, err
			}
			for _, p := range in.Points[1:] {
				st.To(p[0], p[1])
			}
			st.End()
			return map[string]any{"history": ad.d.UndoName()}, nil
		})
	})

	type fillIn struct {
		Kind      string  `json:"kind,omitempty" jsonschema:"fill (the selection, or the whole layer; the default), bucket (the area of like colour at at) or gradient (from at to to)"`
		Color     string  `json:"color,omitempty" jsonschema:"#rrggbb; the foreground colour when left out; a gradient's start"`
		Color2    string  `json:"color2,omitempty" jsonschema:"a gradient's end; the background colour when left out"`
		At        ptIn    `json:"at,omitempty"`
		To        ptIn    `json:"to,omitempty"`
		Gradient  string  `json:"gradient,omitempty" jsonschema:"linear (default), radial, angle, reflected or diamond"`
		Opacity   float64 `json:"opacity,omitempty" jsonschema:"0 to 1; 1 when left out"`
		Tolerance *int    `json:"tolerance,omitempty" jsonschema:"bucket: 0 to 255 (32 when left out)"`
		AllLayers bool    `json:"all_layers,omitempty" jsonschema:"bucket: judge colours on all layers"`
		Scattered bool    `json:"scattered,omitempty" jsonschema:"bucket: fill matching colours anywhere, not only touching"`
	}
	tool(s, a, "canvas_fill", "Fill with a colour, a paint bucket or a gradient on the chosen pixel layer, within the selection.", func(in fillIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			c, c2 := a.brush.Color, a.backgroundColor()
			var err error
			if in.Color != "" {
				if c, err = nrgba(in.Color); err != nil {
					return nil, err
				}
			}
			if in.Color2 != "" {
				if c2, err = nrgba(in.Color2); err != nil {
					return nil, err
				}
			}
			opacity := in.Opacity
			if opacity <= 0 || opacity > 1 {
				opacity = 1
			}
			switch in.Kind {
			case "", "fill":
				c.A = uint8(math.Round(float64(c.A) * opacity))
				err = d.Fill(c)
			case "bucket":
				tol := 32
				if in.Tolerance != nil {
					tol = min(max(*in.Tolerance, 0), 255)
				}
				err = d.Bucket(int(math.Floor(in.At[0])), int(math.Floor(in.At[1])), c, opacity, tol, !in.Scattered, in.AllLayers)
			case "gradient":
				kinds := []string{"linear", "radial", "angle", "reflected", "diamond"}
				k := 0
				if in.Gradient != "" {
					if k = slices.Index(kinds, in.Gradient); k < 0 {
						return nil, fmt.Errorf("gradient is one of %v", kinds)
					}
				}
				if in.At == in.To {
					return nil, errors.New("a gradient runs from at to to, which must differ")
				}
				err = d.DrawGradient(paint.Gradient{Kind: paint.GradientKinds[k], From: in.At, To: in.To, Start: c, End: c2, Opacity: opacity, Dither: true})
			default:
				return nil, fmt.Errorf("unknown kind %q", in.Kind)
			}
			if err != nil {
				return nil, err
			}
			return map[string]any{"history": d.UndoName()}, nil
		})
	})

	type shapeIn struct {
		Kind   string    `json:"kind" jsonschema:"rectangle, ellipse, polygon, star or line"`
		Rect   []float64 `json:"rect,omitempty" jsonschema:"[x, y, width, height] of the shape's box"`
		From   ptIn      `json:"from,omitempty" jsonschema:"a line's start"`
		To     ptIn      `json:"to,omitempty" jsonschema:"a line's end"`
		Radius float64   `json:"radius,omitempty" jsonschema:"a rectangle's corner radius"`
		Points int       `json:"points,omitempty" jsonschema:"a polygon's sides or a star's points (5 when left out)"`
		Inner  float64   `json:"inner,omitempty" jsonschema:"a star's inner radius as a share of its outer (0.5 when left out)"`
		Name   string    `json:"name,omitempty"`
		vectorStyleIn
	}
	tool(s, a, "canvas_add_shape", "Add a vector shape on a layer of its own, sharp at any zoom and editable node by node. Filled with the foreground colour unless fill says otherwise.", func(in shapeIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			var v paint.Vector
			switch in.Kind {
			case "line":
				if in.From == in.To {
					return nil, errors.New("a line runs from from to to, which must differ")
				}
				v = paint.Vector{Kind: paint.VectorCurve, M: paint.Identity, Path: []paint.Subpath{{Nodes: []paint.PathNode{sharpNode(in.From.pt()), sharpNode(in.To.pt())}}},
					Stroke: paint.VectorStroke{Paint: solidPaint(a.brush.Color), Width: 3, Cap: paint.CapRound}}
			case "rectangle", "ellipse", "polygon", "star":
				if len(in.Rect) != 4 || in.Rect[2] <= 0 || in.Rect[3] <= 0 {
					return nil, errors.New("rect is [x, y, width, height] with a size")
				}
				kinds := map[string]paint.VectorKind{"rectangle": paint.VectorRect, "ellipse": paint.VectorEllipse, "polygon": paint.VectorPolygon, "star": paint.VectorStar}
				sides, inner := in.Points, in.Inner
				if sides == 0 {
					sides = 5
				}
				if inner == 0 {
					inner = 0.5
				}
				if sides < 3 || sides > 1000 || inner < 0.01 || inner > 1 || in.Radius < 0 {
					return nil, errors.New("points is from 3 to 1000, inner from 0.01 to 1, radius not negative")
				}
				v = paint.Vector{Kind: kinds[in.Kind], Size: paint.Pt{X: in.Rect[2], Y: in.Rect[3]}, M: paint.Translate(in.Rect[0], in.Rect[1]),
					Radius: in.Radius, Sides: sides, Inner: inner, Fill: solidPaint(a.brush.Color)}
			default:
				return nil, fmt.Errorf("unknown kind %q", in.Kind)
			}
			if err := in.vectorStyleIn.apply(&v); err != nil {
				return nil, err
			}
			if v.Stroke.Paint.Kind != paint.PaintNone && v.Stroke.Width == 0 {
				v.Stroke.Width = 2
			}
			name := in.Name
			if name == "" {
				// Named as the shape tool names its layers.
				kind := map[string]int{"rectangle": shapeRect, "ellipse": shapeEllipse, "line": shapeLine, "polygon": shapePolygon, "star": shapeStar}[in.Kind]
				name = fmt.Sprintf("%s %d", a.u.t(shapeNames[kind]), len(ad.d.Layers())+1)
			}
			n := ad.d.AddVector(v, name)
			return viewOfNode(n), nil
		})
	})

	type nodeIn struct {
		P   ptIn  `json:"p" jsonschema:"the node, [x, y]"`
		In  *ptIn `json:"in,omitempty" jsonschema:"the handle the curve arrives along; none for a sharp, straight arrival"`
		Out *ptIn `json:"out,omitempty" jsonschema:"the handle the curve leaves along"`
	}
	type subpathIn struct {
		Nodes  []nodeIn `json:"nodes"`
		Closed bool     `json:"closed,omitempty"`
	}
	type pathIn struct {
		Subpaths []subpathIn `json:"subpaths" jsonschema:"one or more runs of Bézier nodes, in canvas pixels"`
		Smooth   []ptIn      `json:"smooth,omitempty" jsonschema:"instead of subpaths: points to fit a smooth curve through, as the pencil does"`
		Closed   bool        `json:"closed,omitempty" jsonschema:"with smooth: close the curve"`
		Name     string      `json:"name,omitempty"`
		vectorStyleIn
	}
	tool(s, a, "canvas_add_path", "Add a vector curve, from Bézier nodes or fitted smoothly through points, on a layer of its own. Stroked with the foreground colour unless stroke says otherwise.", func(in pathIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			var path []paint.Subpath
			switch {
			case len(in.Smooth) >= 2:
				pts := make([]paint.Pt, len(in.Smooth))
				for i, p := range in.Smooth {
					pts[i] = p.pt()
				}
				path = []paint.Subpath{paint.FitPath(pts, in.Closed, 0.5)}
			case len(in.Subpaths) > 0:
				total := 0
				for _, sp := range in.Subpaths {
					if len(sp.Nodes) < 2 {
						return nil, errors.New("each subpath needs two nodes or more")
					}
					total += len(sp.Nodes)
					s := paint.Subpath{Closed: sp.Closed}
					for _, n := range sp.Nodes {
						pn := sharpNode(n.P.pt())
						if n.In != nil {
							pn.In = n.In.pt()
						}
						if n.Out != nil {
							pn.Out = n.Out.pt()
						}
						s.Nodes = append(s.Nodes, pn)
					}
					path = append(path, s)
				}
				if total > 100000 {
					return nil, errors.New("too many nodes")
				}
			default:
				return nil, errors.New("give subpaths, or two smooth points or more")
			}
			v := paint.Vector{Kind: paint.VectorCurve, M: paint.Identity, Path: path,
				Stroke: paint.VectorStroke{Paint: solidPaint(a.brush.Color), Width: 3, Cap: paint.CapRound, Join: paint.JoinRound}}
			if err := in.vectorStyleIn.apply(&v); err != nil {
				return nil, err
			}
			name := in.Name
			if name == "" {
				name = a.u.tf("Curve %d", len(ad.d.Layers())+1)
			}
			return viewOfNode(ad.d.AddVector(v, name)), nil
		})
	})

	type styleIn struct {
		ID int `json:"id" jsonschema:"the vector layer"`
		vectorStyleIn
	}
	tool(s, a, "canvas_set_vector", "Change a vector layer's fill, stroke (colour, width, alignment, ends, corners, dashes) and fill rule.", func(in styleIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			n := ad.d.Node(in.ID)
			if n == nil || n.Vector == nil {
				return nil, paint.ErrNotVector
			}
			v := *n.Vector
			if err := in.vectorStyleIn.apply(&v); err != nil {
				return nil, err
			}
			ad.d.SetVector(n.ID, v, "Stroke")
			return viewOfNode(ad.d.Node(n.ID)), nil
		})
	})

	type boolIn struct {
		Op  string `json:"op" jsonschema:"add, subtract (the bottom layer less the ones above), intersect or exclude"`
		IDs []int  `json:"ids" jsonschema:"two or more vector layers"`
	}
	tool(s, a, "canvas_boolean", "Combine vector layers into one, as Affinity Designer's Add, Subtract, Intersect and Exclude; the result keeps the bottom one's place and style.", func(in boolIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			ops := []string{"add", "subtract", "intersect", "exclude"}
			op := slices.Index(ops, in.Op)
			if op < 0 {
				return nil, fmt.Errorf("op is one of %v", ops)
			}
			n, err := ad.d.Boolean(paint.BoolOp(op), in.IDs)
			if err != nil {
				return nil, err
			}
			ad.picked = nil
			return viewOfNode(n), nil
		})
	})

	type textIn struct {
		Text  string  `json:"text" jsonschema:"the words; new lines with \n"`
		X     float64 `json:"x"`
		Y     float64 `json:"y" jsonschema:"the first line's top"`
		Size  float64 `json:"size,omitempty" jsonschema:"in canvas pixels (48 when left out)"`
		Color string  `json:"color,omitempty" jsonschema:"#rrggbb; the foreground colour when left out"`
		Bold  bool    `json:"bold,omitempty"`
		Align string  `json:"align,omitempty" jsonschema:"left (default; x is the left), center or right"`
	}
	tool(s, a, "canvas_add_text", "Add a text layer.", func(in textIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			if in.Text == "" {
				return nil, errors.New("give the text")
			}
			t := paint.Text{Text: in.Text, X: in.X, Y: in.Y, Size: in.Size, Bold: in.Bold, Color: a.brush.Color}
			if t.Size <= 0 {
				t.Size = 48
			}
			t.Size = min(t.Size, 2000)
			if in.Color != "" {
				c, err := nrgba(in.Color)
				if err != nil {
					return nil, err
				}
				t.Color = c
			}
			aligns := []string{"left", "center", "right"}
			if in.Align != "" {
				i := slices.Index(aligns, in.Align)
				if i < 0 {
					return nil, fmt.Errorf("align is one of %v", aligns)
				}
				t.Align = paint.TextAlign(i)
			}
			ts, err := ad.d.BeginText(0)
			if err != nil {
				return nil, err
			}
			ts.Set(t, a.renderText(t))
			ts.Commit()
			return viewOfNode(ts.Layer()), nil
		})
	})

	type filterIn struct {
		Name       string    `json:"name" jsonschema:"gaussian_blur, unsharp_mask, add_noise, mosaic, invert, desaturate, brightness_contrast, levels, hue_saturation, posterize or threshold"`
		Radius     float64   `json:"radius,omitempty" jsonschema:"gaussian_blur (2 when left out), unsharp_mask (1): pixels"`
		Amount     float64   `json:"amount,omitempty" jsonschema:"percent: unsharp_mask 1 to 500 (50 when left out), add_noise 0.1 to 400 (12.5 when left out)"`
		Threshold  float64   `json:"threshold,omitempty" jsonschema:"unsharp_mask: 0 to 255"`
		Size       int       `json:"size,omitempty" jsonschema:"mosaic: cell size in pixels (8 when left out)"`
		Brightness float64   `json:"brightness,omitempty" jsonschema:"brightness_contrast: -150 to 150"`
		Contrast   float64   `json:"contrast,omitempty" jsonschema:"brightness_contrast: -50 to 100"`
		Hue        float64   `json:"hue,omitempty" jsonschema:"hue_saturation: -180 to 180"`
		Saturation float64   `json:"saturation,omitempty" jsonschema:"hue_saturation: -100 to 100"`
		Lightness  float64   `json:"lightness,omitempty" jsonschema:"hue_saturation: -100 to 100"`
		Levels     []float64 `json:"levels,omitempty" jsonschema:"levels: [in black, in white, gamma, out black, out white]; blacks and whites 0 to 255, gamma 0.1 to 9.99 (1 leaves the midtones)"`
		Count      int       `json:"count,omitempty" jsonschema:"posterize: levels, 2 to 255 (4 when left out)"`
		Level      float64   `json:"level,omitempty" jsonschema:"threshold: 1 to 255; brighter pixels turn white, the rest black (128 when left out)"`
		Mono       bool      `json:"mono,omitempty" jsonschema:"add_noise: grey noise"`
	}
	tool(s, a, "canvas_filter", "Apply a filter or adjustment to the chosen pixel layer, within the selection.", func(in filterIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			var f paint.Filter
			name := ""
			switch in.Name {
			case "gaussian_blur":
				radius := in.Radius
				if radius == 0 {
					radius = 2
				}
				f, name = paint.GaussianBlur(min(max(radius, 0.1), 250)), "Gaussian blur"
			case "unsharp_mask":
				amount := in.Amount
				if amount == 0 {
					amount = 50
				}
				radius := in.Radius
				if radius == 0 {
					radius = 1
				}
				f, name = paint.UnsharpMask(min(max(amount, 1), 500), min(max(radius, 0.1), 250), min(max(in.Threshold, 0), 255)), "Unsharp mask"
			case "add_noise":
				amount := in.Amount
				if amount == 0 {
					amount = 12.5
				}
				f, name = paint.AddNoise(min(max(amount, 0.1), 400), in.Mono, uint64(time.Now().UnixNano())), "Add noise"
			case "mosaic":
				size := in.Size
				if size == 0 {
					size = 8
				}
				f, name = paint.Mosaic(min(max(size, 2), 500)), "Mosaic"
			case "invert":
				f, name = paint.Invert(), "Invert"
			case "desaturate":
				f, name = paint.Desaturate(), "Desaturate"
			case "brightness_contrast":
				f, name = paint.BrightnessContrast(min(max(in.Brightness, -150), 150), min(max(in.Contrast, -50), 100)), "Brightness/Contrast"
			case "hue_saturation":
				f, name = paint.HueSaturation(min(max(in.Hue, -180), 180), min(max(in.Saturation, -100), 100), min(max(in.Lightness, -100), 100), false), "Hue/Saturation"
			case "levels":
				if len(in.Levels) != 5 {
					return nil, errors.New("levels is [in black, in white, gamma, out black, out white]")
				}
				l := in.Levels
				clamp := func(v float64) float64 { return min(max(v, 0), 255) }
				f, name = paint.Levels(clamp(l[0]), clamp(l[1]), min(max(l[2], 0.1), 9.99), clamp(l[3]), clamp(l[4])), "Levels"
			case "posterize":
				count := in.Count
				if count == 0 {
					count = 4
				}
				f, name = paint.Posterize(min(max(count, 2), 255)), "Posterize"
			case "threshold":
				level := in.Level
				if level == 0 {
					level = 128
				}
				f, name = paint.Threshold(min(max(level, 1), 255)), "Threshold"
			default:
				return nil, fmt.Errorf("unknown filter %q", in.Name)
			}
			if err := ad.d.Run(name, f); err != nil {
				return nil, err
			}
			return map[string]any{"history": ad.d.UndoName()}, nil
		})
	})

	type transformIn struct {
		Translate []float64 `json:"translate,omitempty" jsonschema:"[dx, dy] in pixels"`
		Scale     []float64 `json:"scale,omitempty" jsonschema:"[sx, sy]; negative mirrors"`
		Rotate    float64   `json:"rotate,omitempty" jsonschema:"degrees clockwise"`
		About     []float64 `json:"about,omitempty" jsonschema:"[x, y] to scale and turn about; the content's centre when left out"`
		Corners   []ptIn    `json:"corners,omitempty" jsonschema:"instead: where the content's top left, top right, bottom right and bottom left corners go, as Distort and Perspective do"`
		Warp      []ptIn    `json:"warp,omitempty" jsonschema:"instead: the 16 points of a 4 × 4 Bézier warp grid, row by row from the top left; points at the thirds of the content's bounds (canvas_state) leave it as it is"`
	}
	tool(s, a, "canvas_transform", "Move, scale, turn or mirror the chosen layer, or the selected pixels of it, as Free Transform does; or distort it by its corners, or warp it by a 4 × 4 grid. Vector layers stay vectors.", func(in transformIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			ts, err := ad.d.BeginTransform()
			if err != nil {
				return nil, err
			}
			switch {
			case len(in.Corners) == 4:
				q := [4]paint.Pt{in.Corners[0].pt(), in.Corners[1].pt(), in.Corners[2].pt(), in.Corners[3].pt()}
				if _, err := ts.SetMesh(paint.QuadMesh(q), "Distort"); err != nil {
					ts.Cancel()
					return nil, err
				}
				ts.Apply()
				return map[string]any{"history": ad.d.UndoName()}, nil
			case len(in.Warp) == 16:
				var g [16]paint.Pt
				for i, p := range in.Warp {
					g[i] = p.pt()
				}
				if _, err := ts.SetMesh(paint.WarpMesh(g, warpCells), "Warp"); err != nil {
					ts.Cancel()
					return nil, err
				}
				ts.Apply()
				return map[string]any{"history": ad.d.UndoName()}, nil
			case len(in.Corners) != 0 || len(in.Warp) != 0:
				ts.Cancel()
				return nil, errors.New("corners takes 4 points, warp 16")
			}
			b := ts.Bounds()
			cx, cy := float64(b.Min.X+b.Max.X)/2, float64(b.Min.Y+b.Max.Y)/2
			if len(in.About) == 2 {
				cx, cy = in.About[0], in.About[1]
			}
			m := paint.Identity
			if len(in.Scale) == 2 {
				if in.Scale[0] == 0 || in.Scale[1] == 0 || math.Abs(in.Scale[0]) > 100 || math.Abs(in.Scale[1]) > 100 {
					ts.Cancel()
					return nil, errors.New("scale is from -100 to 100, not 0")
				}
				m = m.Then(paint.Scale(in.Scale[0], in.Scale[1], cx, cy))
			}
			if in.Rotate != 0 {
				m = m.Then(paint.Rotate(in.Rotate, cx, cy))
			}
			if len(in.Translate) == 2 {
				m = m.Then(paint.Translate(in.Translate[0], in.Translate[1]))
			}
			ts.Set(m, false)
			ts.Apply()
			return map[string]any{"history": ad.d.UndoName()}, nil
		})
	})

	type canvasIn struct {
		Action  string    `json:"action" jsonschema:"image_size (scale everything to width × height), canvas_size (change the frame), crop (to rect), rotate (degrees), flip_horizontal, flip_vertical, reveal_all"`
		Width   int       `json:"width,omitempty"`
		Height  int       `json:"height,omitempty"`
		Anchor  []float64 `json:"anchor,omitempty" jsonschema:"canvas_size: where the old frame stays, [0, 0] top left to [1, 1] bottom right; the middle when left out"`
		Rect    []int     `json:"rect,omitempty" jsonschema:"crop: [x, y, width, height]"`
		Degrees float64   `json:"degrees,omitempty" jsonschema:"rotate: clockwise"`
		Keep    bool      `json:"keep,omitempty" jsonschema:"crop: keep the cropped pixels, only hidden"`
	}
	tool(s, a, "canvas_resize", "Change the whole picture: image size, canvas size, crop, turn or mirror it.", func(in canvasIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			var err error
			switch in.Action {
			case "image_size":
				err = d.ResizeImage(in.Width, in.Height)
			case "canvas_size":
				ax, ay := 0.5, 0.5
				if len(in.Anchor) == 2 {
					ax, ay = min(max(in.Anchor[0], 0), 1), min(max(in.Anchor[1], 0), 1)
				}
				err = d.ResizeCanvas(in.Width, in.Height, ax, ay)
			case "crop":
				if len(in.Rect) != 4 {
					return nil, errors.New("rect is [x, y, width, height]")
				}
				err = d.Crop(image.Rect(in.Rect[0], in.Rect[1], in.Rect[0]+in.Rect[2], in.Rect[1]+in.Rect[3]), !in.Keep)
			case "rotate":
				err = d.RotateCanvasBy(in.Degrees)
			case "flip_horizontal", "flip_vertical":
				d.FlipCanvas(in.Action == "flip_horizontal")
			case "reveal_all":
				err = d.RevealAll()
			default:
				return nil, fmt.Errorf("unknown action %q", in.Action)
			}
			if err != nil {
				return nil, err
			}
			ad.fitView()
			return map[string]any{"frame": rectView(d.Frame), "content_bounds": rectView(d.Bounds())}, nil
		})
	})

	type boardIn struct {
		Action     string `json:"action" jsonschema:"add, set, move (with what lies wholly on it) or delete"`
		ID         int    `json:"id,omitempty" jsonschema:"the artboard, for set, move and delete"`
		Name       string `json:"name,omitempty"`
		Rect       []int  `json:"rect,omitempty" jsonschema:"[x, y, width, height]; for move, only x and y count"`
		Background string `json:"background,omitempty" jsonschema:"#rrggbb, or none for transparent"`
	}
	tool(s, a, "canvas_artboard", "Add, change, move or delete artboards: fixed-size areas of the infinite canvas, each exported as a picture of its own.", func(in boardIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			var r image.Rectangle
			if len(in.Rect) == 4 {
				r = image.Rect(in.Rect[0], in.Rect[1], in.Rect[0]+in.Rect[2], in.Rect[1]+in.Rect[3])
			} else if len(in.Rect) != 0 {
				return nil, errors.New("rect is [x, y, width, height]")
			}
			fill := func(b *paint.Board) error {
				switch in.Background {
				case "":
				case "none":
					b.Fill = color.NRGBA{}
				default:
					c, err := nrgba(in.Background)
					if err != nil {
						return err
					}
					b.Fill = c
				}
				return nil
			}
			var b paint.Board
			var err error
			switch in.Action {
			case "add":
				name := in.Name
				if name == "" {
					name = a.u.tf("Artboard %d", len(d.Boards)+1)
				}
				if b, err = d.AddBoard(r, name); err == nil && in.Background != "" {
					if err = fill(&b); err == nil {
						err = d.SetBoard(b, "Artboard")
					}
				}
			case "set":
				var ok bool
				if b, ok = d.Board(in.ID); !ok {
					return nil, paint.ErrNoBoard
				}
				if in.Name != "" {
					b.Name = in.Name
				}
				if !r.Empty() {
					b.Rect = r
				}
				if err = fill(&b); err == nil {
					err = d.SetBoard(b, "Artboard")
				}
			case "move":
				var ok bool
				if b, ok = d.Board(in.ID); !ok {
					return nil, paint.ErrNoBoard
				}
				if len(in.Rect) < 2 {
					return nil, errors.New("give the new [x, y] in rect")
				}
				err = d.MoveBoard(b.ID, in.Rect[0]-b.Rect.Min.X, in.Rect[1]-b.Rect.Min.Y)
			case "delete":
				err = d.DeleteBoard(in.ID)
			default:
				return nil, fmt.Errorf("unknown action %q", in.Action)
			}
			if err != nil {
				return nil, err
			}
			return a.canvasView(ad)["artboards"], nil
		})
	})

	type historyIn struct {
		Action string `json:"action" jsonschema:"undo, redo or go_to"`
		Step   int    `json:"step,omitempty" jsonschema:"go_to: how many of canvas_state's history steps stay done"`
	}
	tool(s, a, "canvas_history", "Undo, redo, or go to a step of the canvas's history, as the History panel does.", func(in historyIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			switch in.Action {
			case "undo":
				d.Undo()
			case "redo":
				d.Redo()
			case "go_to":
				steps, _ := d.History()
				if in.Step < 0 || in.Step > len(steps) {
					return nil, fmt.Errorf("step is from 0 to %d", len(steps))
				}
				d.GoTo(in.Step)
			default:
				return nil, fmt.Errorf("unknown action %q", in.Action)
			}
			steps, done := d.History()
			return map[string]any{"history": steps, "history_done": done}, nil
		})
	})
}
