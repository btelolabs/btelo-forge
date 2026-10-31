package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/psd"
)

// Photoshop's files, opened as a canvas and exported beside one.

const psdExt = ".psd"

func isPSD(path string) bool { return strings.EqualFold(filepath.Ext(path), psdExt) }

// openPSD opens a Photoshop file as a new canvas beside it, and says what did not come through.
func (a *App) openPSD(path string) {
	a.background(func(context.Context) {
		d, loss, err := readPSD(path)
		a.post(func() {
			if err != nil {
				a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err)), true)
				return
			}
			a.openArt(d, a.freePath(strings.TrimSuffix(path, filepath.Ext(path))+paint.Ext))
			if loss.Any() {
				a.toast(a.psdLossText(loss), false)
			}
		})
	})
}

func readPSD(path string) (*paint.Doc, paint.PSDLoss, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, paint.PSDLoss{}, err
	}
	defer file.Close()
	f, err := psd.Decode(file)
	if err != nil {
		return nil, paint.PSDLoss{}, err
	}
	return paint.FromPSD(f)
}

// psdLossText tells what a Photoshop file left out.
func (a *App) psdLossText(l paint.PSDLoss) string {
	u := &a.u
	var parts []string
	if l.Adjustments > 0 {
		parts = append(parts, u.tf("%d adjustment layers", l.Adjustments))
	}
	if l.Effects > 0 {
		parts = append(parts, u.tf("the effects of %d layers", l.Effects))
	}
	if l.Clipped > 0 {
		parts = append(parts, u.tf("the clipping of %d layers", l.Clipped))
	}
	return u.tf("Left out: %s", strings.Join(parts, u.t(", ")))
}

// exportPSDTarget is the file a canvas exports to: beside it, named after it, a name no file has yet.
func exportPSDTarget(ad *artDoc) string {
	stem := strings.TrimSuffix(ad.path, paint.Ext)
	out := stem + psdExt
	for n := 2; ; n++ {
		if _, err := os.Stat(out); errors.Is(err, os.ErrNotExist) {
			return out
		}
		out = fmt.Sprintf("%s (%d)%s", stem, n, psdExt)
	}
}

// writePSD writes the canvas to path.
func writePSD(f *psd.File, path string) error {
	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	err = psd.Encode(out, f)
	if closed := out.Close(); err == nil {
		err = closed
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// exportPSD writes the canvas as a Photoshop file beside it, on another goroutine, and says where and what is missing.
func (a *App) exportPSD(ad *artDoc) {
	f, loss, err := ad.d.ToPSD()
	if err != nil {
		a.toast(a.u.tf("Export failed: %s", a.describe(err)), true)
		return
	}
	path := exportPSDTarget(ad)
	a.background(func(context.Context) {
		err := writePSD(f, path)
		a.post(func() {
			switch {
			case err != nil:
				a.toast(a.u.tf("Export failed: %s", a.describe(err)), true)
			case loss.Any():
				a.toast(a.u.tf("Saved %s", path)+" · "+a.psdLossText(loss), false)
			default:
				a.toast(a.u.tf("Saved %s", path), false)
			}
		})
	})
}
