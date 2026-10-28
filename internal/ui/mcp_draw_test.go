package ui

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

// toolNames lists the tools a session's server offers.
func (s *mcpSession) toolNames() []string {
	var names []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		if res, err := s.cs.ListTools(context.Background(), nil); err == nil {
			for _, tool := range res.Tools {
				names = append(names, tool.Name)
			}
		}
	}()
	s.x.pump(20*time.Second, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	sort.Strings(names)
	return names
}

func TestEachDomainHasItsOwnMCPServer(t *testing.T) {
	x := newHarness(t)
	for _, domain := range mcpDomains {
		s := connectMCPAt(t, x, mcpDomainPaths[domain])
		want := mcpToolsOf(domain)
		sort.Strings(want)
		if got := s.toolNames(); !slices.Equal(got, want) {
			t.Errorf("domain %d serves %v\nwant %v", domain, got, want)
		}
		text := x.a.mcpConnection(domain)
		if !strings.Contains(text, x.a.services.mcpURL+mcpDomainPaths[domain]) || !strings.Contains(text, mcpServerName(domain)) {
			t.Errorf("domain %d's setup text:\n%s", domain, text)
		}
		// Each setup tells of its own tools and not the others'.
		for d, tools := range mcpDomainTools {
			mentioned := strings.Contains(text, tools[0]+",") || strings.HasSuffix(strings.TrimSpace(text), tools[0])
			if wanted := domain == mcpDomainAll || domain == d; mentioned != wanted {
				t.Errorf("domain %d's setup mentions %s: %v", domain, tools[0], mentioned)
			}
		}
	}
}

func TestDrawingOverMCP(t *testing.T) {
	x := newHarness(t)
	x.a.settings.ExportDir = x.mediaDir()
	s := connectMCPAt(t, x, mcpDomainPaths[domainDrawing])
	var state struct {
		Path   string `json:"path"`
		Frame  []int  `json:"frame"`
		Layers []struct {
			ID   int    `json:"id"`
			Kind string `json:"kind"`
		} `json:"layers"`
	}
	// An infinite canvas: no frame.
	s.ok("canvas_new", map[string]any{}, &state)
	if state.Frame != nil || len(state.Layers) != 1 || !strings.HasSuffix(state.Path, ".btcanvas") {
		t.Fatalf("new canvas %+v", state)
	}
	var shape struct {
		ID   int    `json:"id"`
		Kind string `json:"kind"`
	}
	s.ok("canvas_add_shape", map[string]any{"kind": "rectangle", "rect": []float64{0, 0, 200, 120}, "fill": "#2050ff", "stroke": "#000000", "stroke_width": 4}, &shape)
	var circle struct {
		ID int `json:"id"`
	}
	s.ok("canvas_add_shape", map[string]any{"kind": "ellipse", "rect": []float64{140, 60, 100, 100}, "fill": "#ff0000"}, &circle)
	s.ok("canvas_boolean", map[string]any{"op": "subtract", "ids": []int{shape.ID, circle.ID}}, nil)
	// A brush stroke on a pixel layer above.
	s.ok("canvas_layer", map[string]any{"action": "add", "name": "Ink"}, nil)
	s.ok("canvas_paint", map[string]any{"points": [][]float64{{10, 200}, {190, 200}}, "color": "#00aa00", "size": 12, "hardness": 1}, nil)
	s.ok("canvas_add_text", map[string]any{"text": "Hi", "x": 20, "y": 20, "size": 40, "color": "#ffffff"}, nil)
	s.ok("canvas_artboard", map[string]any{"action": "add", "name": "Card", "rect": []int{-10, -10, 260, 240}}, nil)
	s.ok("canvas_state", nil, &state)
	if len(state.Layers) != 4 || x.a.art.d.Node(shape.ID).Name != "Rectangle 2" {
		t.Fatalf("layers %+v", state.Layers)
	}
	d := x.a.art.d
	if c := d.Composite(d.Bounds()).RGBAAt(100, 200); c.G < 150 || c.R > 10 {
		t.Errorf("the stroke is %v", c)
	}
	if c := d.Composite(d.Bounds()).RGBAAt(190, 100); c.A != 0 {
		t.Errorf("the boolean left %v in the bite", c)
	}
	// A picture of the artboard, and its export.
	res := s.call("canvas_render", map[string]any{"artboard": d.Boards[0].ID})
	img, isImage := res.Content[0].(*mcp.ImageContent)
	if res.IsError || !isImage {
		t.Fatalf("render: %+v", res.Content)
	}
	pic, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil || pic.Bounds().Dx() != 260 || pic.Bounds().Dy() != 240 {
		t.Fatalf("rendered %v, %v", pic.Bounds(), err)
	}
	var out struct {
		Files []string `json:"files"`
	}
	s.ok("canvas_export", map[string]any{"what": "artboards"}, &out)
	if len(out.Files) != 1 || !strings.HasSuffix(out.Files[0], " - Card.png") {
		t.Fatalf("exported %v", out.Files)
	}
	if _, err := os.Stat(out.Files[0]); err != nil {
		t.Error(err)
	}
	// Mistakes come back as errors, not changes.
	if res := s.call("canvas_filter", map[string]any{"name": "sparkle"}); !res.IsError {
		t.Error("an unknown filter was accepted")
	}
	steps, _ := d.History()
	s.ok("canvas_history", map[string]any{"action": "undo"}, nil)
	if after, done := d.History(); len(after) != len(steps) || done != len(steps)-1 {
		t.Errorf("undo: %d steps, %d done", len(after), done)
	}
	x.frame(Input{})
	x.shot("canvas-mcp")
}

func TestMCPSettingsCopyEachDomain(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.settingsOpen, a.settingsPage, a.mcpDomain = true, settingsMCPPage, domainDrawing
	x.frame(Input{})
	x.shot("settings-mcp-drawing")
	for _, domain := range mcpDomains {
		a.mcpDomain = domain
		x.frame(Input{})
		text := a.mcpConnection(domain)
		if !strings.Contains(text, mcpTasks[domain]) {
			t.Errorf("domain %d: %s", domain, text)
		}
	}
}

func TestRetouchAdjustStyleAndWarpOverMCP(t *testing.T) {
	x := newHarness(t)
	x.a.settings.ExportDir = x.mediaDir()
	s := connectMCPAt(t, x, mcpDomainPaths[domainDrawing])
	s.ok("canvas_new", map[string]any{"width": 200, "height": 120, "background": "#808080"}, nil)
	d := x.a.art.d
	look := func(px, py int) [4]uint8 {
		c := d.Composite(d.Frame).RGBAAt(px, py)
		return [4]uint8{c.R, c.G, c.B, c.A}
	}
	s.ok("canvas_retouch", map[string]any{"tool": "dodge", "points": [][]float64{{20, 60}, {60, 60}}, "size": 30, "strength": 1}, nil)
	if c := look(40, 60); c[0] <= 140 || d.UndoName() != "Dodge" {
		t.Errorf("dodged %v (%q)", c, d.UndoName())
	}
	var layer struct {
		ID   int    `json:"id"`
		Kind string `json:"kind"`
	}
	s.ok("canvas_adjustment", map[string]any{"action": "add", "kind": "invert"}, &layer)
	if layer.Kind != "adjustment" || look(150, 60)[0] != 127 {
		t.Fatalf("invert layer %+v, %v", layer, look(150, 60))
	}
	s.ok("canvas_adjustment", map[string]any{"action": "add", "kind": "levels", "values": []float64{0, 1, 255, 0, 255}}, &layer)
	s.ok("canvas_adjustment", map[string]any{"action": "set", "id": layer.ID, "values": []float64{0, 1, 255, 0, 64}}, nil)
	if c := look(150, 60); c[0] > 40 {
		t.Errorf("levels over the invert %v", c)
	}
	if res := s.call("canvas_adjustment", map[string]any{"action": "set", "id": layer.ID, "values": []float64{1}}); !res.IsError {
		t.Error("levels took one value")
	}
	// A shape with a stroke and a shadow from canvas_style, then warped.
	var shape struct {
		ID int `json:"id"`
	}
	s.ok("canvas_add_shape", map[string]any{"kind": "rectangle", "rect": []float64{60, 30, 80, 60}, "fill": "#ff0000"}, &shape)
	s.ok("canvas_style", map[string]any{"id": shape.ID, "stroke": map[string]any{"color": "#00ff00", "size": 4}, "drop_shadow": map[string]any{}}, nil)
	if n := d.Node(shape.ID); n.Style == nil || n.Style.Stroke == nil || n.Style.DropShadow == nil || n.Style.Stroke.Size != 4 {
		t.Fatalf("the style %+v", n.Style)
	}
	if res := s.call("canvas_style", map[string]any{"id": shape.ID, "stroke": map[string]any{"position": "sideways"}}); !res.IsError {
		t.Error("a sideways stroke was taken")
	}
	s.ok("canvas_layer", map[string]any{"action": "choose", "id": shape.ID}, nil)
	s.ok("canvas_transform", map[string]any{"corners": [][]float64{{80, 30}, {120, 30}, {140, 90}, {60, 90}}}, nil)
	if d.UndoName() != "Distort" || d.Node(shape.ID).Vector.Kind != paint.VectorCurve {
		t.Errorf("distort: %q", d.UndoName())
	}
	var state struct {
		Layers []map[string]any `json:"layers"`
	}
	s.ok("canvas_state", nil, &state)
	kinds := map[string]bool{}
	for _, l := range state.Layers {
		kinds[l["kind"].(string)] = true
	}
	if !kinds["adjustment"] || !kinds["vector"] {
		t.Errorf("canvas_state kinds %v", kinds)
	}
}
