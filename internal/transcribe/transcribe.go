// Package transcribe turns speech into timed captions with whisper.cpp, downloading the engine and the chosen model
// into a local cache the first time they are needed. The engines and models on offer come from a Config.
package transcribe

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
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
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/proc"
)

// Manager keeps engines and models under dir.
type Manager struct {
	dir    string
	client *http.Client
	mu     sync.Mutex
	active map[string]bool // the files being downloaded
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
		engineDir := filepath.Join(m.dir, "engine", e.ID+"-"+urlKey(e.URL))
		archiveName := "engine.zip"
		if isTarGz(urlPath(e.URL)) {
			archiveName = "engine.tar.gz"
		}
		archivePath := filepath.Join(engineDir, archiveName)
		if err := m.Download(ctx, e.URL, archivePath, e.Size, e.SHA256, func(f float64) { progress("engine", f) }); err != nil {
			return "", "", fmt.Errorf("downloading the speech engine: %w", err)
		}
		var unpackErr error
		if isTarGz(urlPath(e.URL)) {
			unpackErr = untar(archivePath, engineDir)
		} else {
			unpackErr = unzip(archivePath, engineDir)
		}
		if unpackErr != nil {
			return "", "", fmt.Errorf("unpacking the speech engine: %w", unpackErr)
		}
		os.Remove(archivePath)
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
		if err := m.Download(ctx, model.URL, modelPath, model.Size, model.SHA256, func(f float64) { progress("model", f) }); err != nil {
			return "", "", fmt.Errorf("downloading the %s model: %w", model.Name.In("en"), err)
		}
	}
	return cli, modelPath, nil
}

// errDownloading means the same file is being downloaded already.
var errDownloading = errors.New("this file is already being downloaded")

// Download fetches url into path atomically, reporting progress against the expected size and checking the
// SHA-256 checksum when one is given. What came before an interruption is kept in path.part and continued from
// the next time, so a model of many gigabytes is not downloaded again from its start.
func (m *Manager) Download(ctx context.Context, url, path string, size int64, checksum string, progress func(float64)) error {
	m.mu.Lock()
	if m.active[path] {
		m.mu.Unlock()
		return errDownloading
	}
	if m.active == nil {
		m.active = map[string]bool{}
	}
	m.active[path] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.active, path)
		m.mu.Unlock()
	}()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	part := path + ".part"
	err := m.fetch(ctx, url, part, size, checksum, progress)
	if errors.Is(err, errRestart) {
		os.Remove(part)
		err = m.fetch(ctx, url, part, size, checksum, progress)
	}
	if err != nil {
		if errors.Is(err, errCorrupt) || errors.Is(err, errRestart) {
			os.Remove(part)
		}
		return err
	}
	return os.Rename(part, path)
}

var (
	errRestart = errors.New("the server cannot continue the download")
	errCorrupt = errors.New("the downloaded file does not match its sha256 checksum")
)

// fetch appends what part lacks of url to part and checks it.
func (m *Manager) fetch(ctx context.Context, url, part string, size int64, checksum string, progress func(float64)) error {
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "btelo-forge/1.0")
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		return errRestart
	case resp.StatusCode == http.StatusPartialContent && have > 0:
	case resp.StatusCode == http.StatusOK:
		have = 0 // the server sends the whole file, whether or not a part was asked for
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	total := size
	if resp.ContentLength > 0 {
		total = have + resp.ContentLength
	}
	flags := os.O_WRONLY | os.O_CREATE
	if have == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	tmp, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	var sum hash.Hash
	var out io.Writer = tmp
	if checksum != "" {
		sum = sha256.New()
		if have > 0 {
			if err := hashFile(sum, part, have); err != nil {
				tmp.Close()
				return err
			}
		}
		out = io.MultiWriter(tmp, sum)
	}
	done := have
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
			if time.Since(last) > 200*time.Millisecond && total > 0 {
				progress(min(float64(done)/float64(total), 1))
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
	if resp.ContentLength > 0 && done != total {
		return fmt.Errorf("download incomplete: %d of %d bytes", done, total)
	}
	if sum != nil && hex.EncodeToString(sum.Sum(nil)) != checksum {
		return errCorrupt
	}
	return nil
}

// hashFile feeds the first n bytes of the file at path to h.
func hashFile(h hash.Hash, path string, n int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(h, io.LimitReader(f, n))
	return err
}

// cleanArchivePath normalizes an archive member name and rejects paths that escape dir.
func cleanArchivePath(raw string) (string, error) {
	name := path.Clean(strings.ReplaceAll(raw, `\`, "/"))
	name = strings.TrimPrefix(name, "./")
	if name == "." {
		return ".", nil
	}
	if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, ":") {
		return "", fmt.Errorf("the archive has a file outside its folder: %s", raw)
	}
	return name, nil
}

// safeSymlink checks that linkTarget, placed at dst inside dir, resolves inside dir.
func safeSymlink(dir, dst, linkTarget string) error {
	if linkTarget == "" || strings.HasPrefix(linkTarget, "/") || strings.Contains(linkTarget, `\`) || strings.Contains(linkTarget, ":") {
		return fmt.Errorf("the archive has a symlink outside its folder: %s", linkTarget)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(dst), filepath.FromSlash(linkTarget)))
	rel, err := filepath.Rel(filepath.Clean(dir), resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("the archive has a symlink outside its folder: %s", linkTarget)
	}
	return nil
}

// fileModeFor returns 0o755 when any execute bit is set in mode, otherwise 0o644.
func fileModeFor(mode os.FileMode) os.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// unzip extracts an archive into dir, keeping its folders, symlinks and execute permissions, and dropping a top folder
// that holds everything, so the program it carries lands in dir. Names may use either slash; one that would land
// outside dir fails the whole archive.
func unzip(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	names := make([]string, len(r.File))
	top, shared := "", true // the folder every entry is in, while there is one
	for i, f := range r.File {
		name, err := cleanArchivePath(f.Name)
		if err != nil {
			return err
		}
		names[i] = name
		if name == "." {
			continue
		}
		first, _, nested := strings.Cut(name, "/")
		switch {
		case !nested && !isDir(f), top != "" && first != top:
			shared = false
		default:
			top = first
		}
	}
	if !shared {
		top = ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, f := range r.File {
		name := names[i]
		if top != "" {
			name = strings.TrimPrefix(strings.TrimPrefix(name, top), "/")
		}
		if isDir(f) || name == "" || name == "." {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 {
			src, err := f.Open()
			if err != nil {
				return err
			}
			targetBytes, err := io.ReadAll(io.LimitReader(src, 4096))
			src.Close()
			if err != nil {
				return err
			}
			target := string(targetBytes)
			if err := safeSymlink(dir, dst, target); err != nil {
				return err
			}
			os.Remove(dst)
			if err := os.Symlink(target, dst); err != nil {
				return err
			}
			continue
		}
		if err := extract(f, dst); err != nil {
			return err
		}
	}
	return nil
}

// isDir reports whether an entry is a folder, also when it is named with a backslash, as Windows tools may write.
func isDir(f *zip.File) bool { return f.FileInfo().IsDir() || strings.HasSuffix(f.Name, `\`) }

func extract(f *zip.File, dst string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileModeFor(f.Mode()))
	if err != nil {
		return err
	}
	_, err = io.Copy(out, src)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// untar extracts a .tar.gz / .tgz archive into dir with the same single-top-folder stripping, path validation, symlink
// handling and execute-bit preservation as unzip.
func untar(archive, dir string) error {
	top, err := scanTarTop(archive)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeGNULongName || hdr.Typeflag == tar.TypeGNULongLink {
			continue
		}
		name, err := cleanArchivePath(hdr.Name)
		if err != nil {
			return err
		}
		if top != "" {
			name = strings.TrimPrefix(strings.TrimPrefix(name, top), "/")
		}
		if hdr.Typeflag == tar.TypeDir || name == "" || name == "." {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileModeFor(os.FileMode(hdr.Mode)))
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := safeSymlink(dir, dst, hdr.Linkname); err != nil {
				return err
			}
			os.Remove(dst)
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				return err
			}
		case tar.TypeLink:
			target, err := cleanArchivePath(hdr.Linkname)
			if err != nil {
				return err
			}
			if top != "" {
				target = strings.TrimPrefix(strings.TrimPrefix(target, top), "/")
			}
			linkSrc := filepath.Join(dir, filepath.FromSlash(target))
			os.Remove(dst)
			if err := os.Link(linkSrc, dst); err != nil {
				return err
			}
		}
	}
}

// scanTarTop validates all entry paths in archive and returns the shared top folder when every entry sits inside one.
func scanTarTop(archive string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	top, shared := "", true
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeGNULongName || hdr.Typeflag == tar.TypeGNULongLink {
			continue
		}
		name, err := cleanArchivePath(hdr.Name)
		if err != nil {
			return "", err
		}
		if name == "." {
			continue
		}
		first, _, nested := strings.Cut(name, "/")
		switch {
		case !nested && hdr.Typeflag != tar.TypeDir, top != "" && first != top:
			shared = false
		default:
			top = first
		}
	}
	if !shared {
		return "", nil
	}
	return top, nil
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
