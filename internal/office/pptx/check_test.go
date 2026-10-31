package pptx

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// TestOfficeCheckFiles writes the files tools/officecheck/slides_check.py looks at: FORGE_OFFICECHECK names a folder
// with rich.pptx in it, which python-pptx made.
func TestOfficeCheckFiles(t *testing.T) {
	dir := os.Getenv("FORGE_OFFICECHECK")
	if dir == "" {
		t.Skip("set FORGE_OFFICECHECK to a folder")
	}
	data, err := Blank()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blank.pptx"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile(filepath.Join(dir, "rich.pptx"))
	if err != nil {
		t.Skip(err)
	}
	d, err := Open(orig)
	if err != nil {
		t.Fatal(err)
	}
	r := renderer(t, d)
	for i, s := range d.Slides {
		shot(t, r, s, fmt.Sprintf("rich-%d", i+1), Options{})
	}
	s := d.Slides[0]
	title := d.Shapes(s)[0]
	if err := d.Edit("", []*Slide{s}, false, func() error {
		body, _ := d.Body(s, Target{ID: title.ID})
		_, end := Whole(body)
		TypeText(body, end, ", edited")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetNotes(d.Slides[1], "New notes", ""); err != nil {
		t.Fatal(err)
	}
	s3 := d.Slides[2]
	id, err := d.AddShape(s3, "ellipse", Box{X: 700, Y: 400, W: 100, H: 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetFill(s3, []int{id}, color.RGBA{10, 120, 90, 255}); err != nil {
		t.Fatal(err)
	}
	if err := d.Edit("", []*Slide{s3}, false, func() error {
		body, _ := d.Body(s3, Target{ID: id})
		TypeText(body, Pos{}, "Added")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, sh := range d.Shapes(s3) {
		if sh.body() != nil && PlainText(sh.body()) == "Arrow" {
			b := sh.Box
			b.X = 72
			if err := d.SetBox(s3, "", []int{sh.ID}, []Box{b}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := d.AddSlide(d.Masters[0].Layouts[1], 5); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rich-edited.pptx"), out, 0o644); err != nil {
		t.Fatal(err)
	}
}
