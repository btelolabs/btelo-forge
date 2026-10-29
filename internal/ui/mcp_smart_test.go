package ui

import (
	"image/color"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
)

// An AI makes a smart object of a canvas's layer, puts it over a video and on a mind map, then changes the layer:
// the video's picture of it follows. It can also show the user the stream's tab.
func TestMCPSharesASmartObjectAcrossTools(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.settings.ExportDir = x.mediaDir()
	s := connectMCP(t, x)
	s.ok("open_video", map[string]any{"path": x.fixture(), "placement": "new_tab"}, nil)
	video := a.current()

	s.ok("canvas_new", map[string]any{"width": 200, "height": 120, "background": "none"}, nil)
	var shape struct {
		ID int `json:"id"`
	}
	s.ok("canvas_add_shape", map[string]any{"kind": "rectangle", "rect": []int{0, 0, 200, 120}, "fill": "#e02020", "stroke": "none"}, &shape)
	var made struct {
		Link string `json:"link_id"`
	}
	s.ok("canvas_layer", map[string]any{"action": "convert_to_smart", "id": shape.ID}, &made)
	if made.Link == "" {
		t.Fatal("convert_to_smart gave no link id")
	}

	s.ok("switch_tab", map[string]any{"index": video}, nil)
	var placed struct {
		ID int `json:"id"`
	}
	s.ok("add_smart_object", map[string]any{"link_id": made.Link, "start": 0, "end": 4, "size": 0.4}, &placed)
	o, ok := a.p.Overlay(placed.ID)
	if !ok || o.Smart != made.Link {
		t.Fatalf("overlay %d: %+v", placed.ID, o)
	}
	if res := s.call("add_smart_object", map[string]any{"link_id": "NOSUCHID9"}); !res.IsError {
		t.Error("a smart object that does not exist was put on the video")
	}
	// The picture is replaced away from the window as the layers change; while Windows replaces it, it cannot be
	// opened, so a picture that cannot be read is one not there yet.
	var colour color.RGBA
	read := func() bool {
		img, err := loadImage(o.Image)
		if err != nil {
			return false
		}
		b := img.Bounds()
		r, g, bl, _ := img.At(b.Dx()/2, b.Dy()/2).RGBA()
		colour = color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 255}
		return true
	}
	x.pump(10*time.Second, read)
	if colour.R < 200 || colour.B > 60 {
		t.Fatalf("the video's picture of the smart object is %v, want red", colour)
	}

	s.ok("mind_new", map[string]any{"title": "Smart"}, nil)
	s.ok("mind_add", map[string]any{"nodes": []map[string]any{{"kind": "smart", "ref": made.Link, "x": 300, "y": 0}}}, nil)

	// The layer inside the smart object turns blue on its canvas, and the video's picture follows.
	s.ok("switch_tab", map[string]any{"index": video + 1}, nil)
	s.ok("canvas_set_vector", map[string]any{"id": shape.ID, "fill": "#2040e0"}, nil)
	x.pump(10*time.Second, func() bool { return read() && colour.B > 200 && colour.R < 60 })

	if capture.Supported() {
		s.ok("show_stream", nil, nil)
		if !a.liveTab {
			t.Error("show_stream did not bring the stream's tab forward")
		}
	}
}
