package ui

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

func TestAPhotoshopFileIsExportedAndOpened(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	ad.path = filepath.Join(t.TempDir(), "poster"+".btcanvas")
	l := d.AddLayer("Ink")
	d.Select(image.Rect(40, 40, 80, 80))
	if err := d.Fill(color.NRGBA{B: 255, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	var run func()
	for _, c := range a.artCommands(ad, gfx.Rect{W: 800, H: 600}) {
		if c.id == "draw.export-as-psd" {
			run = c.run
		}
	}
	if run == nil {
		t.Fatal("no such command")
	}
	run()
	a.running.Wait()
	x.frame(Input{})
	path := strings.TrimSuffix(ad.path, ".btcanvas") + ".psd"
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		t.Fatalf("the file: %v", err)
	}
	// A second export keeps the first.
	run()
	a.running.Wait()
	x.frame(Input{})
	if _, err := os.Stat(strings.TrimSuffix(ad.path, ".btcanvas") + " (2).psd"); err != nil {
		t.Error(err)
	}
	opened, loss, err := readPSD(path)
	if err != nil {
		t.Fatal(err)
	}
	if loss.Any() {
		t.Errorf("loss %+v", loss)
	}
	if c := opened.Composite(image.Rect(60, 60, 61, 61)).RGBAAt(60, 60); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("came back %v", c)
	}
	if opened.Node(opened.Active) == nil || opened.Node(opened.Active).Name != l.Name {
		t.Errorf("chosen %+v", opened.Node(opened.Active))
	}
	if !isPSD(`C:\a\b.PSD`) || isPSD("b.png") {
		t.Error("isPSD")
	}
}

func TestOpeningAFileThatIsNotPhotoshopSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.psd")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPSD(path); err == nil {
		t.Error("it opened")
	}
}
