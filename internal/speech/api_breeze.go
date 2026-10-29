package speech

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
)

// breeze is breeze-server: it loads the model given on its command line, warms itself up before it answers
// /health, and streams speech as headerless PCM.
type breeze struct{}

func (breeze) prepare(l Launch, port int) (map[string]string, error) {
	return map[string]string{"model": l.ModelPath, "port": strconv.Itoa(port), "voices": filepath.Join(l.Dir, "voices")}, nil
}

func (breeze) ready(ctx context.Context, p *process) bool {
	var h struct {
		Status     string `json:"status"`
		SampleRate int    `json:"sample_rate"`
	}
	if !p.get(ctx, "/health", &h) || h.Status != "ok" {
		return false
	}
	p.rate = h.SampleRate
	if p.rate <= 0 {
		p.rate = 24000
	}
	return true
}

func (breeze) warm(context.Context, *process) error { return nil }

func (breeze) speak(ctx context.Context, p *process, text string, v Voice, w io.Writer, made func(float64)) (int, error) {
	form := url.Values{"text": {text}, "instruction": {v.Instruction}, "seed": {strconv.Itoa(v.seed())}}
	if v.Guidance > 0 {
		form.Set("cfg_scale", strconv.FormatFloat(v.Guidance, 'f', -1, 64))
	}
	resp, err := p.post(ctx, "/v1/audio/speech", "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	rate := p.rate
	if r, err := strconv.Atoi(resp.Header.Get("X-Sample-Rate")); err == nil && r > 0 {
		rate = r
	}
	if f := resp.Header.Get("X-Sample-Format"); f != "" && f != "s16le" {
		return 0, fmt.Errorf("the speech engine sends %s samples, not s16le", f)
	}
	return rate, copySpeech(ctx, w, resp.Body, rate, made)
}
