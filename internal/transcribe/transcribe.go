// Package transcribe turns speech into timed captions with whisper.cpp, downloading the engine and the chosen model
// into a local cache the first time they are needed. The engines and models on offer come from a Config.
package transcribe

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/proc"
)

// Manager keeps engines and models under dir.
type Manager struct {
	dir    string
	client *http.Client
}

// NewManager caches downloads under dir.
func NewManager(dir string) *Manager {
	return &Manager{dir: dir, client: &http.Client{}}
}

// urlKey names the cache entry of a download, so a changed address never reuses an old file.
func urlKey(u string) string {
	sum := sha1.Sum([]byte(u))
	return hex.EncodeToString(sum[:5])
}

// ModelPath is where a model lives, and whether it is downloaded.
func (m *Manager) ModelPath(model Model) (string, bool) {
	name := path.Base(urlPath(model.URL))
	p := filepath.Join(m.dir, "models", model.ID+"-"+urlKey(model.URL), name)
	st, err := os.Stat(p)
	return p, err == nil && st.Size() > 0
}

// EnginePath finds the engine's program: the downloaded one, or an installed one.
func (m *Manager) EnginePath(e Engine) (string, bool) {
	if e.URL != "" {
		p := filepath.Join(m.dir, "engine", e.ID+"-"+urlKey(e.URL), e.Executable)
		_, err := os.Stat(p)
		return p, err == nil
	}
	for _, name := range e.Commands {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	for _, p := range e.Paths {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// ErrNoEngine means an installed engine is missing; MissingEngine carries how to install it.
var ErrNoEngine = errors.New("the speech engine is not installed")

// MissingEngine is returned when an installed engine cannot be found.
type MissingEngine struct {
	Engine  string
	Install string
}

func (e *MissingEngine) Error() string {
	if e.Install == "" {
		return fmt.Sprintf("%s is not installed", e.Engine)
	}
	return fmt.Sprintf("%s is not installed; install it with: %s", e.Engine, e.Install)
}

func (e *MissingEngine) Unwrap() error { return ErrNoEngine }

// Progress reports a download or transcription step: what is happening and how far it is, from 0 to 1.
type Progress func(stage string, fraction float64)

// Prepare makes sure the engine and model are present, downloading what is missing.
func (m *Manager) Prepare(ctx context.Context, e Engine, model Model, progress Progress) (cli, modelPath string, err error) {
	cli, ok := m.EnginePath(e)
	if !ok {
		if e.URL == "" {
			return "", "", &MissingEngine{Engine: e.Name.In("en"), Install: e.Install}
		}
		zipPath := cli + ".zip"
		if err := m.download(ctx, e.URL, zipPath, e.Size, "", func(f float64) { progress("engine", f) }); err != nil {
			return "", "", fmt.Errorf("downloading the speech engine: %w", err)
		}
		if err := unzip(zipPath, filepath.Dir(cli)); err != nil {
			return "", "", fmt.Errorf("unpacking the speech engine: %w", err)
		}
		os.Remove(zipPath)
		if _, ok := m.EnginePath(e); !ok {
			return "", "", fmt.Errorf("the speech engine archive has no %s", e.Executable)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(cli, 0o755); err != nil {
				return "", "", err
			}
		}
	}
	modelPath, ok = m.ModelPath(model)
	if !ok {
		if err := m.download(ctx, model.URL, modelPath, model.Size, model.SHA256, func(f float64) { progress("model", f) }); err != nil {
			return "", "", fmt.Errorf("downloading the %s model: %w", model.Name.In("en"), err)
		}
	}
	return cli, modelPath, nil
}

// download fetches url into path atomically, reporting progress against the expected size and checking the
// SHA-256 checksum when one is given.
func (m *Manager) download(ctx context.Context, url, path string, size int64, checksum string, progress func(float64)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "btelo-forge/1.0")
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 0 {
		size = resp.ContentLength
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	var sum hash.Hash
	var out io.Writer = tmp
	if checksum != "" {
		sum = sha256.New()
		out = io.MultiWriter(tmp, sum)
	}
	var done int64
	last := time.Time{}
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				tmp.Close()
				return err
			}
			done += int64(n)
			if time.Since(last) > 200*time.Millisecond && size > 0 {
				progress(min(float64(done)/float64(size), 1))
				last = time.Now()
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			tmp.Close()
			return rerr
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if resp.ContentLength > 0 && done != resp.ContentLength {
		return fmt.Errorf("download incomplete: %d of %d bytes", done, resp.ContentLength)
	}
	if sum != nil && hex.EncodeToString(sum.Sum(nil)) != checksum {
		return errors.New("the downloaded file does not match its sha256 checksum")
	}
	return os.Rename(tmp.Name(), path)
}

// unzip extracts the files of an archive into dir, flattening the archive's top folder.
func unzip(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.Base(f.Name)
		if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Line is one recognised caption.
type Line struct {
	Start, End float64
	Text       string
}

// Run transcribes a 16 kHz mono WAV file with the command line of c and returns its captions.
func Run(ctx context.Context, c *Config, cli, model, wav, language string, progress Progress) ([]Line, error) {
	out := strings.TrimSuffix(wav, filepath.Ext(wav))
	values := map[string]string{"model": model, "audio": wav, "language": language, "output": out,
		"threads": strconv.Itoa(max(1, min(runtime.NumCPU()-1, 8)))}
	args := fill(c.Arguments, values)
	if l, ok := c.Language(language); ok && l.Prompt != "" {
		values["prompt"] = l.Prompt
		args = append(args, fill(c.PromptArguments, values)...)
	}
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.Dir = filepath.Dir(cli)
	proc.Hide(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var tail []string
	scan := bufio.NewScanner(stderr)
	for scan.Scan() {
		line := scan.Text()
		if m := c.progress.FindStringSubmatch(line); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			progress("transcribe", min(max(v/100, 0), 1))
		} else if strings.TrimSpace(line) != "" {
			tail = append(tail, line)
			if len(tail) > 4 {
				tail = tail[1:]
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("whisper: %w: %s", err, strings.Join(tail, " | "))
	}
	defer os.Remove(out + ".json")
	data, err := os.ReadFile(out + ".json")
	if err != nil {
		return nil, err
	}
	return parse(data)
}

// fill replaces the {placeholders} of each argument.
func fill(args []string, values map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = placeholder.ReplaceAllStringFunc(a, func(m string) string { return values[m[1:len(m)-1]] })
	}
	return out
}

func parse(data []byte) ([]Line, error) {
	var r struct {
		Transcription []struct {
			Offsets struct {
				From, To float64
			} `json:"offsets"`
			Text string `json:"text"`
		} `json:"transcription"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("reading whisper output: %w", err)
	}
	var lines []Line
	for _, s := range r.Transcription {
		text := strings.TrimSpace(s.Text)
		if text == "" || strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			continue
		}
		lines = append(lines, Line{Start: s.Offsets.From / 1000, End: s.Offsets.To / 1000, Text: text})
	}
	return lines, nil
}
