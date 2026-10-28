package ui

import (
	"errors"
	"fmt"
	"image/color"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

// The drawing tools for retouching, adjustment layers and layer styles.

// adjustKindIDs name the adjustment layers' kinds for the tools, in paint.AdjustKind's order.
var adjustKindIDs = []string{"brightness_contrast", "levels", "curves", "hue_saturation", "posterize", "threshold", "invert", "black_white"}

// neutralValues are each kind's settings that leave the picture as it is.
var neutralValues = map[paint.AdjustKind][]float64{paint.AdjustBrightness: {0, 0}, paint.AdjustLevels: {0, 1, 255, 0, 255},
	paint.AdjustHue: {0, 0, 0}, paint.AdjustPosterize: {4}, paint.AdjustThreshold: {128}}

func (a *App) addDrawToolsMore(s *mcp.Server) {
	type retouchIn struct {
		Tool     string   `json:"tool" jsonschema:"dodge (lighten), burn (darken), sponge (change saturation), blur, sharpen or smudge"`
		Points   []ptIn   `json:"points" jsonschema:"the stroke, [[x, y], …] in canvas pixels"`
		Size     float64  `json:"size,omitempty" jsonschema:"brush diameter in pixels (the brush's size when left out)"`
		Hardness *float64 `json:"hardness,omitempty" jsonschema:"0 soft to 1 hard"`
		Strength float64  `json:"strength,omitempty" jsonschema:"0 to 1: exposure for dodge and burn, flow for the sponge (0.5 when left out)"`
		Range    string   `json:"range,omitempty" jsonschema:"dodge and burn: shadows, midtones (default) or highlights"`
		Saturate bool     `json:"saturate,omitempty" jsonschema:"sponge: add colour instead of taking it away"`
	}
	tool(s, a, "canvas_retouch", "Retouch the chosen pixel layer along a stroke, as Photoshop's dodge, burn, sponge, blur, sharpen and smudge tools do, within the selection.", func(in retouchIn) (any, error) {
		return a.drawEdit(func(ad *artDoc) (any, error) {
			kinds := []string{"dodge", "burn", "sponge", "blur", "sharpen", "smudge"}
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

	type curveIn struct {
		In  float64 `json:"in"`
		Out float64 `json:"out"`
	}
	type adjustIn struct {
		Action   string    `json:"action" jsonschema:"add (a new adjustment layer above the chosen layer) or set (an adjustment layer's settings)"`
		ID       int       `json:"id,omitempty" jsonschema:"set: the adjustment layer"`
		Kind     string    `json:"kind,omitempty" jsonschema:"add: brightness_contrast, levels, curves, hue_saturation, posterize, threshold, invert or black_white"`
		Values   []float64 `json:"values,omitempty" jsonschema:"brightness_contrast: [brightness -150…150, contrast -50…100]; levels: [input black, midtones gamma, input white, output black, output white] in 0…255 with gamma 0.1…9.99; hue_saturation: [hue -180…180, saturation -100…100, lightness -100…100]; posterize: [levels 2…255]; threshold: [level 1…255]; left out, the picture stays as it is"`
		Colorize bool      `json:"colorize,omitempty" jsonschema:"hue_saturation: colorize"`
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
