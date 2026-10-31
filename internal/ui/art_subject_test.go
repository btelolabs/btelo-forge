package ui

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/matting"
)

func TestAMatteComesBackThePicturesSize(t *testing.T) {
	var asked int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		out := make([]byte, matting.Side*matting.Side)
		for y := 256; y < 768; y++ {
			for x := 256; x < 768; x++ {
				out[y*matting.Side+x] = 255
			}
		}
		w.Write(out)
	}))
	defer s.Close()
	img := image.NewRGBA(image.Rect(10, 20, 410, 320))
	m, err := ai.Endpoint{URL: s.URL + "/v1", Client: s.Client()}.Matte(context.Background(), img)
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 || m.Rect != img.Rect {
		t.Fatalf("%d asked, %v", asked, m.Rect)
	}
	if v := m.AlphaAt(210, 170).A; v != 255 {
		t.Errorf("the middle is %d", v)
	}
	if v := m.AlphaAt(30, 40).A; v != 0 {
		t.Errorf("the corner is %d", v)
	}
}

func TestAMatteThatFailsSaysSo(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "out of memory", http.StatusInternalServerError)
	}))
	defer s.Close()
	_, err := ai.Endpoint{URL: s.URL, Client: s.Client()}.Matte(context.Background(), image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if err == nil {
		t.Fatal("no error")
	}
}

func TestSubjectCommandsAreInTheMenusAndTheModelIsListed(t *testing.T) {
	x, ad := canvasHarness(t)
	seen := map[string]bool{}
	for _, c := range x.a.artCommands(ad, gfx.Rect{W: 800, H: 600}) {
		seen[c.id] = true
	}
	if !seen["draw.select-subject"] || !seen["draw.remove-background"] {
		t.Errorf("commands: %v %v", seen["draw.select-subject"], seen["draw.remove-background"])
	}
	cfg := ai.DefaultConfig()
	found := false
	for _, m := range cfg.Models {
		found = found || m.Kind == ai.KindMatting
	}
	if !found {
		t.Error("the default models have none that finds a subject")
	}
	if b, err := json.Marshal(cfg); err != nil || len(b) == 0 {
		t.Error(err)
	}
}

func TestRemovingABackgroundMasksTheChosenLayer(t *testing.T) {
	_, ad := canvasHarness(t)
	d := ad.d
	l := d.AddLayer("Photo")
	d.Select(image.Rect(0, 0, 400, 300))
	if err := d.Fill(color.NRGBA{R: 200, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	m := image.NewAlpha(image.Rect(0, 0, 400, 300))
	for y := 100; y < 200; y++ {
		for x := 100; x < 300; x++ {
			m.Pix[m.PixOffset(x, y)] = 255
		}
	}
	if err := d.MaskBackground(m); err != nil {
		t.Fatal(err)
	}
	if d.UndoName() != "Remove background" {
		t.Errorf("step %q", d.UndoName())
	}
	n := d.Node(l.ID)
	if n.Mask == nil || n.Mask.At(150, 150) != 255 || n.Mask.At(20, 20) != 0 {
		t.Fatalf("mask %+v", n.Mask)
	}
	if c := n.Pixels.At(20, 20); c.R == 0 {
		t.Error("the pixels were lost")
	}
}
