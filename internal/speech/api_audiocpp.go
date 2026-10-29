package speech

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// audioCPP is audiocpp_server of audio.cpp: it is started with a config file naming one model, loads it before it
// lists it as loaded, and answers each piece of speech at once as a WAV file. Its first piece sets up the graphics
// card and takes several seconds longer, so a short one is made while loading.
type audioCPP struct{}

// audioCPPModel is the id of the model in the config file.
const audioCPPModel = "voice"

func (audioCPP) prepare(l Launch, port int) (map[string]string, error) {
	config := map[string]any{
		"host": "127.0.0.1", "port": port, "backend": l.Engine.Backend, "lazy_load": false, "max_loaded_models": 1, "ui": false,
		"models": []any{map[string]any{"id": audioCPPModel, "family": l.Model.Family, "task": l.Model.Task, "mode": "offline",
			"path": filepath.ToSlash(l.ModelPath)}},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(l.Dir, "server.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return map[string]string{"config": path}, nil
}

func (audioCPP) ready(ctx context.Context, p *process) bool {
	var h struct {
		Status string `json:"status"`
	}
	var models struct {
		Data []struct {
			ID     string `json:"id"`
			Loaded bool   `json:"loaded"`
		} `json:"data"`
	}
	if !p.get(ctx, "/health", &h) || h.Status != "ok" || !p.get(ctx, "/v1/models", &models) {
		return false
	}
	for _, m := range models.Data {
		if m.ID == audioCPPModel && m.Loaded {
			return true
		}
	}
	return false
}

func (a audioCPP) warm(ctx context.Context, p *process) error {
	p.pace = 1
	_, err := a.speak(ctx, p, "Hello.", Voice{}, io.Discard, func(float64) {})
	return err
}

func (audioCPP) speak(ctx context.Context, p *process, text string, v Voice, w io.Writer, made func(float64)) (int, error) {
	options := map[string]any{"instruct": v.Instruction}
	if lang := Language(text); lang != "" {
		options["language"] = lang
	}
	body, err := json.Marshal(map[string]any{"model": audioCPPModel, "input": text, "seed": v.seed(), "options": options})
	if err != nil {
		return 0, err
	}
	// The speech comes all at once: until then, what is made is estimated from how fast the last piece was.
	began, done := time.Now(), make(chan struct{})
	var ticking sync.WaitGroup
	ticking.Add(1)
	go func() {
		defer ticking.Done()
		tick := time.NewTicker(150 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				made(time.Since(began).Seconds() / p.pace)
			}
		}
	}()
	resp, err := p.post(ctx, "/v1/audio/speech", "application/json", body)
	close(done)
	ticking.Wait()
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	rate, err := readWAVHeader(resp.Body)
	if err != nil {
		return 0, err
	}
	counted := &countingWriter{w: w}
	if err := copySpeech(ctx, counted, resp.Body, rate, made); err != nil {
		return 0, err
	}
	if seconds := float64(counted.n) / float64(rate*2); seconds > 0.2 {
		p.pace = max(time.Since(began).Seconds()/seconds, 0.05)
	}
	return rate, nil
}

// readWAVHeader reads a WAV file's chunks up to its samples, which must be 16-bit mono PCM, and returns their rate.
func readWAVHeader(r io.Reader) (int, error) {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return 0, fmt.Errorf("the speech engine sent no sound: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return 0, errors.New("the speech engine did not send a WAV file")
	}
	rate := 0
	for {
		var head [8]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return 0, errors.New("the speech engine sent a WAV file without sound")
		}
		id, size := string(head[0:4]), int64(binary.LittleEndian.Uint32(head[4:]))
		switch id {
		case "fmt ":
			if size < 16 || size > 1<<10 {
				return 0, errors.New("the speech engine sent a broken WAV file")
			}
			f := make([]byte, size+size%2)
			if _, err := io.ReadFull(r, f); err != nil {
				return 0, err
			}
			format, channels, bits := binary.LittleEndian.Uint16(f[0:]), binary.LittleEndian.Uint16(f[2:]), binary.LittleEndian.Uint16(f[14:])
			if format != 1 || channels != 1 || bits != 16 {
				return 0, fmt.Errorf("the speech engine sent %d-bit sound in %d channels (format %d), not 16-bit mono PCM", bits, channels, format)
			}
			rate = int(binary.LittleEndian.Uint32(f[4:]))
		case "data":
			if rate <= 0 {
				return 0, errors.New("the speech engine sent a WAV file without its format")
			}
			return rate, nil
		default:
			if _, err := io.CopyN(io.Discard, r, size+size%2); err != nil {
				return 0, err
			}
		}
	}
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}
