package speech

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/btelolabs/btelo-forge/internal/proc"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// Launch is how to start a server: the engine and its program, and the model it loads.
type Launch struct {
	Engine    Engine
	Program   string
	Model     Model
	ModelPath string
	Dir       string // a folder of the engine's own, for the files it is started with and its saved voices
}

func (l Launch) key() string {
	parts := []string{l.Engine.API, l.Engine.Backend, l.Program, l.Model.Family, l.Model.Task, l.ModelPath, l.Dir}
	return strings.Join(append(parts, l.Engine.Arguments...), "\x00")
}

// Server runs one speech server at a time, for the engine and model last asked for. It loads the model once for
// many pieces of speech, and stops after it has been idle for a while so the graphics memory it holds is given back.
type Server struct {
	idle    time.Duration
	loading time.Duration
	client  *http.Client
	turn    chan struct{} // held while a piece of speech is made, so the server gets one request at a time
	mu      sync.Mutex    // guards run and closed
	run     *process
	closed  bool
}

type process struct {
	*proc.Service
	key    string
	model  string // the id of the model it holds
	api    api
	client *http.Client
	base   string  // http://127.0.0.1:port
	rate   int     // samples per second, for a server that says so before speaking
	pace   float64 // seconds it takes to make a second of speech, for a server that does not stream
	timer  *time.Timer
}

// NewServer stops the server when idle for that long after its last piece of speech.
func NewServer(idle time.Duration) *Server {
	return &Server{idle: idle, loading: 10 * time.Minute, client: &http.Client{}, turn: make(chan struct{}, 1)}
}

// Running reports whether a server is started, loading or holding its model.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run != nil
}

// Info says which model the running server holds and its process id, and false when none runs.
func (s *Server) Info() (model string, pid int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil || !s.run.Running() {
		return "", 0, false
	}
	return s.run.model, s.run.Pid(), true
}

// Stop ends the server now, giving back its memory; the next piece of speech starts it again. A piece being made
// fails.
func (s *Server) Stop() {
	s.mu.Lock()
	p := s.run
	s.run = nil
	s.mu.Unlock()
	p.halt()
}

// Close stops the server for good; a piece of speech being made fails, and later ones are refused.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.Stop()
}

// Request is one piece of speech: the words, and the voice that says them.
type Request struct {
	Text  string
	Voice Voice
}

// seed is the voice's seed, or DefaultSeed.
func (v Voice) seed() int {
	if v.Seed > 0 {
		return v.Seed
	}
	return DefaultSeed
}

// FileName names the recording of req made by model, so the same words in the same voice are made once.
func FileName(model Model, req Request) string {
	h := sha256.New()
	for _, part := range []string{model.ID, model.URL, req.Voice.Instruction, strconv.Itoa(req.Voice.seed()),
		strconv.FormatFloat(req.Voice.Guidance, 'g', -1, 64), strings.TrimSpace(req.Text)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12]) + ".wav"
}

// Estimate is about how many seconds speaking text takes, for showing progress: Chinese characters at about four
// a second, other words at about two and a half, and a pause at the end of each sentence.
func Estimate(text string) float64 {
	seconds, inWord := 0.0, false
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			seconds += 0.24
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				seconds += 0.38
			}
			inWord = true
		default:
			if strings.ContainsRune(".!?。！？；;", r) {
				seconds += 0.35
			} else if strings.ContainsRune(",，、:：", r) {
				seconds += 0.15
			}
			inWord = false
		}
	}
	return max(seconds, 1)
}

// Language is the language code of text, for a model that takes it instead of guessing: zh, ja, ko or en, or ""
// when the text has no letters.
func Language(text string) string {
	var han, kana, hangul, latin bool
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana = true
		case unicode.Is(unicode.Hangul, r):
			hangul = true
		case unicode.Is(unicode.Han, r):
			han = true
		case unicode.Is(unicode.Latin, r):
			latin = true
		}
	}
	switch {
	case kana:
		return "ja"
	case hangul:
		return "ko"
	case han:
		return "zh"
	case latin:
		return "en"
	}
	return ""
}

// Speak makes req into a 16-bit mono WAV file at path and returns its length in seconds. It starts the server
// when it is not running with l, reporting "load" until the model is ready, then "speak" with the share made,
// estimated from the text. The file appears only once it is complete.
func (s *Server) Speak(ctx context.Context, l Launch, req Request, path string, progress transcribe.Progress) (float64, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return 0, errors.New("there is no text to speak")
	}
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	defer func() { <-s.turn }()
	p, err := s.ready(ctx, l, progress)
	if err != nil {
		return 0, err
	}
	defer s.rest(p)
	progress("speak", 0)
	out, err := newWAVFile(path)
	if err != nil {
		return 0, err
	}
	expected := Estimate(text)
	rate, err := p.api.speak(ctx, p, text, req.Voice, out, func(seconds float64) { progress("speak", min(seconds/expected, 0.97)) })
	if err == nil {
		var seconds float64
		if seconds, err = out.finish(rate); err == nil {
			progress("speak", 1)
			return seconds, nil
		}
	}
	out.abort()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	// A server that died breaks the connection a moment before it is seen to have ended.
	select {
	case <-p.Exited:
		s.forget(p)
		return 0, fmt.Errorf("the speech engine stopped: %s", p.Log.Last())
	case <-time.After(time.Second):
	}
	return 0, err
}

// ready returns the running server for l, starting it, or a new one in place of one for something else.
func (s *Server) ready(ctx context.Context, l Launch, progress transcribe.Progress) (*process, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errClosed
	}
	p := s.run
	if p != nil {
		p.timer.Stop()
		select {
		case <-p.Exited:
			p = nil // it ended by itself; start it again
		default:
			if p.key != l.key() {
				p.halt()
				p = nil
			}
		}
		s.run = p
	}
	s.mu.Unlock()
	if p != nil {
		return p, nil
	}
	progress("load", 0)
	p, err := start(ctx, l, s.client, s.loading)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.run = p
	}
	s.mu.Unlock()
	if closed {
		p.halt()
		return nil, errClosed
	}
	return p, nil
}

var errClosed = errors.New("the speech engine was closed")

// rest stops p once it has been idle long enough, unless it is in use again by then.
func (s *Server) rest(p *process) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != p {
		return
	}
	p.timer = time.AfterFunc(s.idle, func() {
		select {
		case s.turn <- struct{}{}:
			defer func() { <-s.turn }()
		default:
			return // in use; it rests again afterwards
		}
		s.mu.Lock()
		stop := s.run == p
		if stop {
			s.run = nil
		}
		s.mu.Unlock()
		if stop {
			p.halt()
		}
	})
}

func (s *Server) forget(p *process) {
	s.mu.Lock()
	if s.run == p {
		s.run = nil
	}
	s.mu.Unlock()
	p.halt()
}

// start runs the server on a free port and waits until its model is loaded and ready to speak.
func start(ctx context.Context, l Launch, client *http.Client, limit time.Duration) (*process, error) {
	a, ok := apis[l.Engine.API]
	if !ok {
		return nil, fmt.Errorf("unknown speech engine api %q", l.Engine.API)
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return nil, err
	}
	svc, err := proc.StartAs("speech:"+l.Model.ID, l.Program, func(port int) ([]string, error) {
		values, err := a.prepare(l, port)
		if err != nil {
			return nil, err
		}
		args := make([]string, len(l.Engine.Arguments))
		for i, arg := range l.Engine.Arguments {
			args[i] = placeholder.ReplaceAllStringFunc(arg, func(m string) string { return values[m[1:len(m)-1]] })
		}
		return args, nil
	})
	if err != nil {
		return nil, fmt.Errorf("starting the speech engine: %w", err)
	}
	p := &process{Service: svc, key: l.key(), model: l.Model.ID, api: a, client: client, base: svc.Base, timer: time.NewTimer(0)}
	p.timer.Stop()
	if err := svc.WaitReady(ctx, limit, "the speech engine", func(ctx context.Context) bool { return a.ready(ctx, p) }); err != nil {
		return nil, err
	}
	if err := a.warm(ctx, p); err != nil {
		p.halt()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("the speech engine failed to warm up: %w", err)
	}
	return p, nil
}

// halt ends the server's program and its idle timer.
func (p *process) halt() {
	if p == nil {
		return
	}
	p.timer.Stop()
	p.Stop()
}

// get asks the server for path with a short time limit and decodes its JSON answer into into; false when it does
// not answer, or not with 200.
func (p *process) get(ctx context.Context, path string, into any) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
	if err != nil {
		return false
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(into) == nil
}

// post asks the server for speech. A server makes one piece at a time and refuses another meanwhile, which happens
// just after a piece was given up, while the server notices; so a refusal is tried again for a while. An answer
// other than 200 is returned as an error with the server's message.
func (p *process) post(ctx context.Context, path, contentType string, body []byte) (*http.Response, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		busy := resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusServiceUnavailable
		if !busy || time.Now().After(deadline) {
			defer resp.Body.Close()
			return nil, refusal(resp)
		}
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.Exited:
			return nil, errors.New("the speech engine stopped")
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// refusal is the error a server answered with: {"error": "..."} or {"error": {"message": "..."}}.
func refusal(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && len(e.Error) > 0 {
		var text string
		var nested struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(e.Error, &text) == nil && text != "" {
			return fmt.Errorf("the speech engine refused: %s", text)
		}
		if json.Unmarshal(e.Error, &nested) == nil && nested.Message != "" {
			return fmt.Errorf("the speech engine refused: %s", nested.Message)
		}
	}
	return fmt.Errorf("the speech engine answered HTTP %d", resp.StatusCode)
}

// copySpeech copies 16-bit PCM from r to w, reporting the seconds copied at rate.
func copySpeech(ctx context.Context, w io.Writer, r io.Reader, rate int, made func(seconds float64)) error {
	buf := make([]byte, 64<<10)
	var n int64
	last := time.Time{}
	for {
		k, rerr := r.Read(buf)
		if k > 0 {
			if _, err := w.Write(buf[:k]); err != nil {
				return err
			}
			n += int64(k)
			if time.Since(last) > 150*time.Millisecond {
				made(float64(n) / float64(rate*2))
				last = time.Now()
			}
		}
		if errors.Is(rerr, io.EOF) {
			return nil
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("the speech engine stopped before the speech was finished: %w", rerr)
		}
	}
}

// wavFile is a WAV file being written: samples go to a temporary file, which becomes the file when finished, without
// the silence a model may leave after the last word.
type wavFile struct {
	path  string
	tmp   *os.File
	w     *bufio.Writer
	n     int64
	low   int   // the low byte of a sample whose high byte is still to come, or -1
	heard int64 // bytes up to the end of the last sample louder than silence
}

// Below silence a sample is quiet enough to drop at the end (about -50 dBFS); keepAfter seconds of it stay after the
// last word, so it does not stop abruptly.
const (
	silence   = 100
	keepAfter = 0.15
)

func newWAVFile(path string) (*wavFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "speech-*.part")
	if err != nil {
		return nil, err
	}
	f := &wavFile{path: path, tmp: tmp, w: bufio.NewWriterSize(tmp, 256<<10), low: -1}
	if _, err := tmp.Write(make([]byte, 44)); err != nil {
		f.abort()
		return nil, err
	}
	return f, nil
}

func (f *wavFile) Write(b []byte) (int, error) {
	n, err := f.w.Write(b)
	for i, c := range b[:n] {
		if f.low < 0 {
			f.low = int(c)
			continue
		}
		if v := int16(uint16(f.low) | uint16(c)<<8); v > silence || v < -silence {
			f.heard = f.n + int64(i) + 1
		}
		f.low = -1
	}
	f.n += int64(n)
	return n, err
}

// finish completes the file with its header for samples at rate and returns its length in seconds.
func (f *wavFile) finish(rate int) (float64, error) {
	if rate <= 0 {
		return 0, errors.New("the speech engine did not say its sample rate")
	}
	if f.heard == 0 {
		return 0, errors.New("the speech engine made no sound")
	}
	n := min(f.n-f.n%2, f.heard+int64(float64(rate)*keepAfter)*2) // a sample cut in half is dropped
	if err := f.w.Flush(); err != nil {
		return 0, err
	}
	if err := f.tmp.Truncate(44 + n); err != nil {
		return 0, err
	}
	if _, err := f.tmp.WriteAt(wavHeader(rate, n), 0); err != nil {
		return 0, err
	}
	if err := f.tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(f.tmp.Name(), f.path); err != nil {
		os.Remove(f.tmp.Name())
		return 0, err
	}
	return float64(n) / float64(rate*2), nil
}

func (f *wavFile) abort() {
	f.tmp.Close()
	os.Remove(f.tmp.Name())
}

// Length is the length in seconds of a WAV file written by Speak, or false when path is not one.
func Length(path string) (float64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	h := make([]byte, 44)
	st, err := f.Stat()
	if err != nil || st.Size() <= 44 {
		return 0, false
	}
	if _, err := io.ReadFull(f, h); err != nil || string(h[0:4]) != "RIFF" || string(h[36:40]) != "data" {
		return 0, false
	}
	rate := binary.LittleEndian.Uint32(h[24:])
	n := binary.LittleEndian.Uint32(h[40:])
	if rate == 0 || int64(n) != st.Size()-44 {
		return 0, false
	}
	return float64(n) / float64(rate*2), true
}

// wavHeader is the header of a 16-bit mono PCM WAV file of n bytes of samples.
func wavHeader(rate int, n int64) []byte {
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+n))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 1) // mono
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(n))
	return h
}
