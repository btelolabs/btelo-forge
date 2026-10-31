package ui

import (
	"errors"
	"fmt"
	"github.com/btelolabs/btelo-forge/internal/ai"
	"image"
	"image/color"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The drawing tools for retouching, adjustment layers and layer styles.

// adjustKindIDs name the adjustment layers' kinds for the tools, in paint.AdjustKind's order.
var adjustKindIDs = []string{"brightness_contrast", "levels", "curves", "hue_saturation", "posterize", "threshold", "invert", "black_white",
	"color_balance", "vibrance", "selective_color", "photo_filter", "gradient_map", "exposure"}

// neutralValues are each kind's settings that leave the picture as it is.
var neutralValues = map[paint.AdjustKind][]float64{paint.AdjustBrightness: {0, 0}, paint.AdjustLevels: {0, 1, 255, 0, 255},
	paint.AdjustHue: {0, 0, 0}, paint.AdjustPosterize: {4}, paint.AdjustThreshold: {128},
	paint.AdjustColorBalance: make([]float64, 9), paint.AdjustVibrance: {0, 0}, paint.AdjustSelectiveColor: make([]float64, 36),
	paint.AdjustPhotoFilter: {0, 236, 138, 0}, paint.AdjustGradientMap: {50, 0, 0, 0, 255, 255, 255}, paint.AdjustExposure: {0, 0, 1}}

func (a *App) addDrawToolsMore(s *mcp.Server) {
	type retouchIn struct {
		Tool     string   `json:"tool" jsonschema:"dodge (lighten), burn (darken), sponge (change saturation), blur, sharpen, smudge or liquify (reshape)"`
		Points   []ptIn   `json:"points" jsonschema:"the stroke, [[x, y], …] in canvas pixels"`
		Size     float64  `json:"size,omitempty" jsonschema:"brush diameter in pixels (the brush's size when left out)"`
		Hardness *float64 `json:"hardness,omitempty" jsonschema:"0 soft to 1 hard"`
		Strength float64  `json:"strength,omitempty" jsonschema:"0 to 1: exposure for dodge and burn, flow for the sponge (0.5 when left out)"`
		Range    string   `json:"range,omitempty" jsonschema:"dodge and burn: shadows, midtones (default) or highlights"`
		Saturate bool     `json:"saturate,omitempty" jsonschema:"sponge: add colour instead of taking it away"`
		Mode     string   `json:"mode,omitempty" jsonschema:"liquify: forward (push along the stroke, the default), pucker (draw in to the brush) or bloat (push out from it)"`
	}
	tool(s, a, "canvas_retouch", "Retouch the chosen pixel layer along a stroke, as Photoshop's dodge, burn, sponge, blur, sharpen and smudge tools and Liquify do, within the selection.", func(in retouchIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			kinds := []string{"dodge", "burn", "sponge", "blur", "sharpen", "smudge", "liquify"}
			k := slices.Index(kinds, in.Tool)
			if k < 0 {
				return nil, fmt.Errorf("tool is one of %v", kinds)
			}
			if len(in.Points) == 0 {
				return nil, errors.New("give the stroke's points")
			}
			rt := paint.Retouch{Kind: paint.RetouchKind(k), Range: paint.RangeMidtones, Desaturate: !in.Saturate, Strength: in.Strength}
			if rt.Strength <= 0 {
				rt.Strength = 0.5
			}
			if in.Mode != "" {
				modes := []string{"forward", "pucker", "bloat"}
				i := slices.Index(modes, in.Mode)
				if i < 0 {
					return nil, fmt.Errorf("mode is one of %v", modes)
				}
				rt.Liquify = paint.LiquifyMode(i)
			}
			if in.Range != "" {
				ranges := []string{"shadows", "midtones", "highlights"}
				i := slices.Index(ranges, in.Range)
				if i < 0 {
					return nil, fmt.Errorf("range is one of %v", ranges)
				}
				rt.Range = paint.ToneRange(i)
			}
			b := a.brush
			b.Smooth = 0 // the points are exact: no trailing
			if in.Size > 0 {
				b.Size = min(in.Size, 2000)
			}
			if in.Hardness != nil {
				b.Hardness = min(max(*in.Hardness, 0), 1)
			}
			st, _, err := ad.d.BeginRetouch(b, rt, in.Points[0][0], in.Points[0][1])
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

	type healIn struct {
		Tool      string  `json:"tool" jsonschema:"spot (a stroke, filled from around it), heal (a stroke cloned from offset, taking the tones around it), patch (the selection replaced from offset, likewise) or fill (Content-Aware Fill of the selection)"`
		Points    []ptIn  `json:"points,omitempty" jsonschema:"spot and heal: the stroke, [[x, y], …] in canvas pixels"`
		Offset    []int   `json:"offset,omitempty" jsonschema:"heal and patch: [dx, dy] from where it lands to where it is taken from"`
		Size      float64 `json:"size,omitempty" jsonschema:"spot and heal: brush diameter in pixels (the brush's size when left out)"`
		AllLayers bool    `json:"all_layers,omitempty" jsonschema:"read the picture as it looks, not only the chosen layer; the repair lands on the chosen layer, which may be empty"`
	}
	tool(s, a, "canvas_heal", "Take something out of the picture on the chosen pixel layer, as Photoshop's Spot Healing Brush, Healing Brush, Patch tool and Content-Aware Fill do, within the selection.", func(in healIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			offset := func() (int, int, error) {
				if len(in.Offset) != 2 || in.Offset[0] == 0 && in.Offset[1] == 0 {
					return 0, 0, errors.New("offset is [dx, dy], not both zero")
				}
				return in.Offset[0], in.Offset[1], nil
			}
			switch in.Tool {
			case "fill":
				f, err := d.PrepareContentFill(in.AllLayers)
				if err != nil {
					return nil, err
				}
				if err := f.Compute(); err != nil {
					return nil, err
				}
				if _, err := f.Apply(); err != nil {
					return nil, err
				}
			case "patch":
				dx, dy, err := offset()
				if err != nil {
					return nil, err
				}
				if err := d.PatchSelection(dx, dy, in.AllLayers); err != nil {
					return nil, err
				}
			case "spot", "heal":
				if len(in.Points) == 0 {
					return nil, errors.New("give the stroke's points")
				}
				b := a.brush
				b.Smooth = 0 // the points are exact: no trailing
				if in.Size > 0 {
					b.Size = min(in.Size, 2000)
				}
				x, y := in.Points[0][0], in.Points[0][1]
				var st *paint.Stroke
				var err error
				if in.Tool == "spot" {
					st, _, err = d.BeginSpotHeal(b, in.AllLayers, x, y)
				} else {
					var dx, dy int
					if dx, dy, err = offset(); err != nil {
						return nil, err
					}
					st, _, err = d.BeginHeal(b, dx, dy, in.AllLayers, x, y)
				}
				if err != nil {
					return nil, err
				}
				for _, p := range in.Points[1:] {
					st.To(p[0], p[1])
				}
				st.End()
			default:
				return nil, fmt.Errorf("unknown tool %q", in.Tool)
			}
			return map[string]any{"history": d.UndoName()}, nil
		})
	})

	type subjectIn struct {
		Action string `json:"action" jsonschema:"select (select what the picture is of) or remove_background (hide everything else with the chosen layer's mask, keeping its pixels)"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "canvas_subject", Description: "Find what the picture is of with a local model, which is downloaded the first time (250 MB), and select it or remove what is around it."},
		func(ctx contextT, req *mcp.CallToolRequest, in subjectIn) (*mcp.CallToolResult, any, error) {
			if in.Action != "select" && in.Action != "remove_background" {
				return errorResult(fmt.Errorf("unknown action %q", in.Action)), nil, nil
			}
			type job struct {
				ad  *artDoc
				img *image.RGBA
				cfg *ai.Config
			}
			j, err := call(a, func() (job, error) {
				ad, err := a.canvasNow()
				if err != nil {
					return job{}, err
				}
				img, err := subjectPicture(ad.d)
				return job{ad, img, a.ai.config}, err
			})
			if err != nil {
				return errorResult(err), nil, nil
			}
			mask, err := a.matte(ctx, j.cfg, j.img)
			if err != nil {
				return errorResult(err), nil, nil
			}
			return result(call(a, func() (any, error) {
				if in.Action == "select" {
					j.ad.d.SelectShape(paint.MaskFrom(mask, 0), paint.SelectNew)
				} else if err := j.ad.d.MaskBackground(mask); err != nil {
					return nil, err
				}
				j.ad.changedAll()
				return map[string]any{"history": j.ad.d.UndoName()}, nil
			}))
		})

	type smartFilterIn struct {
		Action string    `json:"action" jsonschema:"add, set (its settings), remove, toggle (off or on), up or down"`
		ID     int       `json:"id,omitempty" jsonschema:"the smart object; the chosen layer when left out"`
		Index  int       `json:"index,omitempty" jsonschema:"set, remove, toggle, up, down: the filter's place, from 0, first applied first"`
		Kind   string    `json:"kind,omitempty" jsonschema:"add: gaussian_blur [radius], unsharp_mask [amount, radius, threshold], add_noise [amount], mosaic [cell size], shadows_highlights [shadows, highlights, radius], develop [as canvas_filter] or any kind of canvas_adjustment"`
		Values []float64 `json:"values,omitempty" jsonschema:"the filter's settings"`
		On     bool      `json:"on,omitempty" jsonschema:"add_noise: grey; as colorize of canvas_adjustment for adjustments"`
	}
	tool(s, a, "canvas_smart_filter", "Give a smart object filters that stay editable, as Photoshop's smart filters: the layers in it do not change, and each filter can be changed, turned off, reordered or taken away. canvas_layer convert_to_smart makes one.", func(in smartFilterIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			id := in.ID
			if id == 0 {
				id = d.Active
			}
			var err error
			switch in.Action {
			case "add", "set":
				sf := paint.SmartFilter{Kind: in.Kind, Values: slices.Clone(in.Values), On: in.On, Seed: 1}
				if in.Action == "set" {
					n := d.Node(id)
					if n == nil || n.Filters == nil || in.Index < 0 || in.Index >= len(n.Filters.List) {
						return nil, paint.ErrNoSmartFilter
					}
					old := n.Filters.List[in.Index]
					sf.Kind, sf.Seed, sf.Off = old.Kind, old.Seed, old.Off
					if old.Adjust != nil {
						adj := *old.Adjust
						adj.Values, adj.On = sf.Values, in.On
						sf.Adjust, sf.Values, sf.On = &adj, nil, false
					}
				} else if k := slices.Index(adjustKindIDs, in.Kind); k >= 0 {
					adj := paint.Adjustment{Kind: paint.AdjustKind(k), Values: sf.Values, On: in.On}
					if adj.Values == nil {
						adj.Values = slices.Clone(neutralValues[adj.Kind])
					}
					if adj.Kind == paint.AdjustCurves {
						adj.Curve = []paint.CurvePoint{{In: 0, Out: 0}, {In: 255, Out: 255}}
					}
					sf = paint.SmartFilter{Kind: paint.SmartAdjust, Adjust: &adj}
				}
				var st *paint.SmartFilterSession
				if in.Action == "add" {
					st, err = d.BeginSmartFilter(id, sf)
				} else if st, err = d.EditSmartFilter(id, in.Index); err == nil {
					if err = st.Set(sf); err != nil {
						st.Cancel()
						return nil, err
					}
				}
				if err == nil {
					st.End()
				}
			case "remove":
				err = d.RemoveSmartFilter(id, in.Index)
			case "toggle":
				err = d.ToggleSmartFilter(id, in.Index)
			case "up":
				err = d.MoveSmartFilter(id, in.Index, -1)
			case "down":
				err = d.MoveSmartFilter(id, in.Index, 1)
			default:
				err = fmt.Errorf("unknown action %q", in.Action)
			}
			if err != nil {
				return nil, err
			}
			return map[string]any{"history": d.UndoName()}, nil
		})
	})

	type curveIn struct {
		In  float64 `json:"in"`
		Out float64 `json:"out"`
	}
	type adjustIn struct {
		Action   string    `json:"action" jsonschema:"add (a new adjustment layer above the chosen layer) or set (an adjustment layer's settings)"`
		ID       int       `json:"id,omitempty" jsonschema:"set: the adjustment layer"`
		Kind     string    `json:"kind,omitempty" jsonschema:"add: brightness_contrast, levels, curves, hue_saturation, posterize, threshold, invert, black_white, color_balance, vibrance, selective_color, photo_filter, gradient_map or exposure"`
		Values   []float64 `json:"values,omitempty" jsonschema:"brightness_contrast: [brightness -150…150, contrast -50…100]; levels: [input black, midtones gamma, input white, output black, output white] in 0…255 with gamma 0.1…9.99; hue_saturation: [hue -180…180, saturation -100…100, lightness -100…100]; posterize: [levels 2…255]; threshold: [level 1…255]; color_balance: nine values -100…100, cyan–red, magenta–green and yellow–blue for shadows, midtones, highlights; vibrance: [vibrance, saturation] -100…100; selective_color: 36 values -100…100, cyan, magenta, yellow and black for reds, yellows, greens, cyans, blues, magentas, whites, neutrals, blacks; photo_filter: [density 0…100, red, green, blue]; gradient_map: [midpoint 1…99, then red, green, blue of the shadows' colour and of the highlights' colour]; exposure: [stops -10…10, offset -0.5…0.5, gamma 0.01…9.99]; left out, the picture stays as it is (a gradient map, black to white)"`
		Colorize bool      `json:"colorize,omitempty" jsonschema:"hue_saturation: colorize; color_balance and photo_filter: keep each pixel's lightness; selective_color: change in proportion to what is there"`
		Curve    []curveIn `json:"curve,omitempty" jsonschema:"curves: points {in, out} from 0 to 255, two or more"`
		Name     string    `json:"name,omitempty"`
	}
	tool(s, a, "canvas_adjustment", "Add an adjustment layer, which changes everything under it without changing its pixels (paint or fill its mask to choose where), or change one's settings.", func(in adjustIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			d := ad.d
			adj := paint.Adjustment{Values: slices.Clone(in.Values), On: in.Colorize}
			for _, p := range in.Curve {
				adj.Curve = append(adj.Curve, paint.CurvePoint{In: p.In, Out: p.Out})
			}
			switch in.Action {
			case "add":
				k := slices.Index(adjustKindIDs, in.Kind)
				if k < 0 {
					return nil, fmt.Errorf("kind is one of %v", adjustKindIDs)
				}
				adj.Kind = paint.AdjustKind(k)
				if adj.Values == nil {
					adj.Values = slices.Clone(neutralValues[adj.Kind])
				}
				if adj.Kind == paint.AdjustCurves && adj.Curve == nil {
					adj.Curve = []paint.CurvePoint{{In: 0, Out: 0}, {In: 255, Out: 255}}
				}
				name := in.Name
				if name == "" {
					name = fmt.Sprintf("%s %d", in.Kind, len(d.Layers())+1)
				}
				n, err := d.AddAdjustment(adj, name)
				if err != nil {
					return nil, err
				}
				return viewOfNode(n), nil
			case "set":
				n := d.Node(in.ID)
				if n == nil || n.Adjust == nil {
					return nil, errors.New("id is not an adjustment layer")
				}
				adj.Kind = n.Adjust.Kind
				if err := d.SetAdjustment(n.ID, adj); err != nil {
					return nil, err
				}
				return viewOfNode(d.Node(n.ID)), nil
			}
			return nil, fmt.Errorf("unknown action %q", in.Action)
		})
	})

	type styleIn struct {
		ID           int       `json:"id" jsonschema:"the pixel, text or vector layer"`
		DropShadow   *effectIn `json:"drop_shadow,omitempty"`
		InnerShadow  *effectIn `json:"inner_shadow,omitempty"`
		OuterGlow    *effectIn `json:"outer_glow,omitempty"`
		InnerGlow    *effectIn `json:"inner_glow,omitempty"`
		Stroke       *effectIn `json:"stroke,omitempty"`
		ColorOverlay *effectIn `json:"color_overlay,omitempty"`
	}
	tool(s, a, "canvas_style", "Set a layer's effects, as Photoshop's Layer Style: the effects given are on (settings left out take Photoshop's defaults), the others off; none at all clears them.", func(in styleIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			st, err := styleFrom(in.DropShadow, in.InnerShadow, in.OuterGlow, in.InnerGlow, in.Stroke, in.ColorOverlay)
			if err != nil {
				return nil, err
			}
			if err := ad.d.SetStyle(in.ID, &st); err != nil {
				return nil, err
			}
			return map[string]any{"history": ad.d.UndoName(), "style": ad.d.Node(in.ID).Style}, nil
		})
	})
}

// effectIn is an effect's settings as canvas_style takes them; those left out keep the effect's defaults.
type effectIn struct {
	Color    string   `json:"color,omitempty" jsonschema:"#rrggbb"`
	Blend    string   `json:"blend,omitempty" jsonschema:"a blend mode by name, as canvas_set_layer takes"`
	Opacity  *float64 `json:"opacity,omitempty" jsonschema:"0 to 1"`
	Angle    *float64 `json:"angle,omitempty" jsonschema:"shadows: the light's direction in degrees (120 lights from the top left)"`
	Distance *float64 `json:"distance,omitempty" jsonschema:"shadows: pixels"`
	Spread   *float64 `json:"spread,omitempty" jsonschema:"shadows and glows: 0 to 1"`
	Size     *float64 `json:"size,omitempty" jsonschema:"pixels: softness, or a stroke's width"`
	Position string   `json:"position,omitempty" jsonschema:"stroke: outside, inside or center"`
}

// styleFrom makes a layer style of the effects given, each from Photoshop's defaults with the settings given.
func styleFrom(drop, inner, outer, innerGlow, stroke, overlay *effectIn) (paint.LayerStyle, error) {
	var st paint.LayerStyle
	var errs []error
	col := func(f *effectIn, c *color.NRGBA) {
		if f.Color != "" {
			v, err := nrgba(f.Color)
			*c, errs = v, append(errs, err)
		}
	}
	blend := func(f *effectIn, b *paint.Blend) {
		if f.Blend != "" {
			v, err := blendFrom(f.Blend)
			*b, errs = v, append(errs, err)
		}
	}
	set := func(dst *float64, v *float64) {
		if v != nil {
			*dst = *v
		}
	}
	shadow := func(f *effectIn, def paint.Shadow) *paint.Shadow {
		if f == nil {
			return nil
		}
		sh := def
		col(f, &sh.Color)
		blend(f, &sh.Blend)
		set(&sh.Opacity, f.Opacity)
		set(&sh.Angle, f.Angle)
		set(&sh.Distance, f.Distance)
		set(&sh.Spread, f.Spread)
		set(&sh.Size, f.Size)
		return &sh
	}
	glow := func(f *effectIn, def paint.Glow) *paint.Glow {
		if f == nil {
			return nil
		}
		g := def
		col(f, &g.Color)
		blend(f, &g.Blend)
		set(&g.Opacity, f.Opacity)
		set(&g.Spread, f.Spread)
		set(&g.Size, f.Size)
		return &g
	}
	st.DropShadow = shadow(drop, paint.DefaultDropShadow())
	st.InnerShadow = shadow(inner, paint.DefaultInnerShadow())
	st.OuterGlow = glow(outer, paint.DefaultOuterGlow())
	st.InnerGlow = glow(innerGlow, paint.DefaultInnerGlow())
	if f := stroke; f != nil {
		o := paint.DefaultOutline()
		col(f, &o.Color)
		set(&o.Opacity, f.Opacity)
		set(&o.Size, f.Size)
		if f.Position != "" {
			i := slices.Index([]string{"outside", "inside", "center"}, f.Position)
			if i < 0 {
				errs = append(errs, errors.New("position is outside, inside or center"))
			}
			o.Position = paint.StrokePosition(max(i, 0))
		}
		st.Stroke = &o
	}
	if f := overlay; f != nil {
		o := paint.DefaultOverlay()
		col(f, &o.Color)
		blend(f, &o.Blend)
		set(&o.Opacity, f.Opacity)
		st.ColorOverlay = &o
	}
	return st, errors.Join(errs...)
}
