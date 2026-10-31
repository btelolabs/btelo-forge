package render

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// TestDump renders the PDFs of FORGE_PDF_DIR into FORGE_PDF_OUT as contact sheets, for looking at.
func TestDump(t *testing.T) {
	dir, out := os.Getenv("FORGE_PDF_DIR"), os.Getenv("FORGE_PDF_OUT")
	if dir == "" || out == "" {
		t.Skip("set FORGE_PDF_DIR and FORGE_PDF_OUT")
	}
	fonts, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skip(err)
	}
	src := &BookFonts{Book: flow.NewBook(fonts)}
	files, _ := filepath.Glob(filepath.Join(dir, "*.pdf"))
	const cols, rows, cw, ch = 6, 4, 300, 400
	per := cols * rows
	if v, _ := strconv.Atoi(os.Getenv("FORGE_PDF_SCALE")); v == 1 {
		per = 1
	}
	var sheet *image.RGBA
	flush := func(n int) {
		if sheet == nil {
			return
		}
		f, _ := os.Create(filepath.Join(out, fmt.Sprintf("sheet%02d.png", n)))
		png.Encode(f, sheet)
		f.Close()
		sheet = nil
	}
	for i, path := range files {
		if i%per == 0 {
			flush(i/per - 1)
			sheet = image.NewRGBA(image.Rect(0, 0, cols*cw, rows*ch))
			draw.Draw(sheet, sheet.Rect, image.NewUniform(color.Gray{80}), image.Point{}, draw.Src)
		}
		data, _ := os.ReadFile(path)
		start := time.Now()
		d, err := pdf.Open(data, "")
		if err != nil {
			t.Logf("%s: %v", filepath.Base(path), err)
			continue
		}
		c := NewCache(d, src)
		pages := d.Pages()
		if len(pages) == 0 {
			t.Logf("%s: no pages", filepath.Base(path))
			continue
		}
		cx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		w, h := d.ViewSize(pages[0], 0)
		res := c.Page(cx, pages[0], Options{Scale: min(cw/w, ch/h)})
		cancel()
		if res.Err != nil {
			t.Logf("%s: %v", filepath.Base(path), res.Err)
		}
		t.Logf("%-40s %d pages %v text %d", filepath.Base(path), len(pages), time.Since(start).Round(time.Millisecond), len(res.Text.Chars))
		x, y := (i%per)%cols*cw, (i%per)/cols*ch
		draw.Draw(sheet, image.Rect(x, y, x+cw-2, y+ch-2), res.Img, image.Point{}, draw.Src)
	}
	flush(len(files) / per)
}
