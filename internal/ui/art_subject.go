package ui

import (
	"context"
	"errors"
	"image"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Select › Subject and Layer › Remove background: a model finds what the picture is of, in a program of its own that
// ai.Runtime downloads the first time and keeps loaded for the next.

var errNoMatting = errors.New("no model that finds a subject runs on this computer")

// subjectTimeout is how long downloading, loading and asking the model may take in all.
const subjectTimeout = 30 * time.Minute

// matte asks the model how much of each pixel of img is subject, from any goroutine but the window's.
func (a *App) matte(ctx context.Context, cfg *ai.Config, img *image.RGBA) (*image.Alpha, error) {
	m, ok := cfg.MattingModel()
	if !ok {
		return nil, errNoMatting
	}
	ep, err := a.ai.rt.Ensure(ctx, cfg, m.ID, "")
	if err != nil {
		return nil, err
	}
	return ep.Matte(ctx, img)
}

// subjectPicture is what the model is shown: the picture as it looks over the frame or all that is drawn.
func subjectPicture(d *paint.Doc) (*image.RGBA, error) {
	r := d.Extent()
	if r.Empty() {
		return nil, errors.New("the canvas is empty")
	}
	return d.Composite(r), nil
}

// findSubject finds the subject of the picture and hands it to use, on the window's goroutine.
func (a *App) findSubject(ad *artDoc, use func(*image.Alpha) error) {
	if ad.matting {
		return
	}
	cfg := a.ai.config
	if _, ok := cfg.MattingModel(); !ok {
		a.artFailed(ad, errNoMatting)
		return
	}
	img, err := subjectPicture(ad.d)
	if err != nil {
		a.artFailed(ad, err)
		return
	}
	ad.matting = true
	u := &a.u
	a.toast(u.t("Finding the subject… The first time, the model is downloaded (250 MB)."), false)
	a.background(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, subjectTimeout)
		defer cancel()
		mask, err := a.matte(ctx, cfg, img)
		a.post(func() {
			ad.matting = false
			if err == nil {
				err = use(mask)
			}
			if err != nil {
				a.artFailed(ad, err)
			}
			ad.changedAll()
		})
	})
}

// selectSubject selects what the picture is of, with the selection tools' mode.
func (a *App) selectSubject(ad *artDoc) {
	a.findSubject(ad, func(m *image.Alpha) error {
		ad.d.SelectShape(paint.MaskFrom(m, 0), paint.SelectNew)
		return nil
	})
}

// removeBackground hides what is not the subject with the chosen layer's mask, so nothing is lost.
func (a *App) removeBackground(ad *artDoc) {
	a.findSubject(ad, ad.d.MaskBackground)
}
