package media

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/locale"
	"github.com/btelolabs/btelo-forge/internal/proc"
)

// DefaultToolsJSON is the built-in description of where ffmpeg is downloaded from.
//
//go:embed ffmpeg.json
var DefaultToolsJSON []byte

// ToolsConfig says which ffmpeg is good enough and where to download one when the computer has none.
type ToolsConfig struct {
	Version   int        `json:"version"`
	Minimum   string     `json:"minimum"`
	Downloads []Download `json:"downloads"`

	minimum [2]int
}

// Download is one way to get ffmpeg and ffprobe: archives that together hold both programs.
type Download struct {
	Name     string    `json:"name"`
	OS       string    `json:"os"`
	Arch     string    `json:"arch"`
	Archives []Archive `json:"archives"`
}

// Archive is a zip file holding ffmpeg, ffprobe or both, anywhere inside it.
type Archive struct {
	URL    string `json:"url"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)$`)

// ParseToolsConfig reads and checks a configuration; errors name the place in the file.
func ParseToolsConfig(data []byte) (*ToolsConfig, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.DisallowUnknownFields()
	var c ToolsConfig
	if err := dec.Decode(&c); err != nil {
		return nil, locale.JSONError(data, err)
	}
	if dec.More() {
		return nil, errors.New("unexpected text after the configuration")
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("version must be 1, not %d", c.Version)
	}
	m := versionPattern.FindStringSubmatch(c.Minimum)
	if m == nil {
		return nil, errors.New(`minimum must be a version such as "6.0"`)
	}
	c.minimum[0], _ = strconv.Atoi(m[1])
	c.minimum[1], _ = strconv.Atoi(m[2])
	for i, d := range c.Downloads {
		where := fmt.Sprintf("downloads[%d] (%s)", i, d.Name)
		switch {
		case d.Name == "":
			return nil, fmt.Errorf("downloads[%d]: name is missing", i)
		case d.OS != "windows" && d.OS != "darwin" && d.OS != "linux":
			return nil, fmt.Errorf("%s: os must be windows, darwin or linux", where)
		case d.Arch != "amd64" && d.Arch != "arm64":
			return nil, fmt.Errorf("%s: arch must be amd64 or arm64", where)
		case len(d.Archives) == 0:
			return nil, fmt.Errorf("%s: give at least one archive", where)
		}
		for k, a := range d.Archives {
			u, err := url.Parse(a.URL)
			switch {
			case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
				return nil, fmt.Errorf("%s: archives[%d]: url must be an http:// or https:// address", where, k)
			case !strings.HasSuffix(strings.ToLower(u.Path), ".zip"):
				return nil, fmt.Errorf("%s: archives[%d]: url must point to a .zip archive", where, k)
			case a.Size < 0:
				return nil, fmt.Errorf("%s: archives[%d]: size must not be negative", where, k)
			case a.SHA256 != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a.SHA256):
				return nil, fmt.Errorf("%s: archives[%d]: sha256 must be 64 lowercase hexadecimal digits", where, k)
			}
		}
	}
	return &c, nil
}

// DefaultToolsConfig is the built-in configuration.
func DefaultToolsConfig() *ToolsConfig {
	c, err := ParseToolsConfig(DefaultToolsJSON)
	if err != nil {
		panic("the built-in ffmpeg downloads are invalid: " + err.Error())
	}
	return c
}

// For lists the downloads that run on this computer, in the order to try them.
func (c *ToolsConfig) For(goos, goarch string) []Download {
	var out []Download
	for _, d := range c.Downloads {
		if d.OS == goos && d.Arch == goarch {
			out = append(out, d)
		}
	}
	return out
}

// Tools is a usable pair of programs and the version of ffmpeg.
type Tools struct {
	FFmpeg, FFprobe string
	Version         string
}

// ErrTooOld means the only ffmpeg found is older than the configuration asks for.
var ErrTooOld = errors.New("ffmpeg is too old")

var ffmpegVersion = regexp.MustCompile(`ffmpeg version (\S+)`)

// versionOf runs ffmpeg -version and returns the version it reports.
func versionOf(ctx context.Context, ffmpeg string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-version")
	proc.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	m := ffmpegVersion.FindSubmatch(out)
	if m == nil {
		return "", errors.New("ffmpeg -version printed no version")
	}
	return string(m[1]), nil
}

var releaseVersion = regexp.MustCompile(`^n?(\d+)\.(\d+)`)

// newEnough reports whether version is at least minimum. Builds from the development branch ("N-12345-g…", or a
// date) are newer than any release.
func newEnough(version string, minimum [2]int) bool {
	m := releaseVersion.FindStringSubmatch(version)
	if m == nil {
		return strings.HasPrefix(version, "N-") || regexp.MustCompile(`^(git-)?20\d\d-`).MatchString(version)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > minimum[0] || major == minimum[0] && minor >= minimum[1]
}

func programName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// installDir is where a download is unpacked: its own folder per set of addresses, so a changed address installs
// afresh.
func installDir(root string, d Download) string {
	h := sha1.New()
	for _, a := range d.Archives {
		h.Write([]byte(a.URL + "\n"))
	}
	return filepath.Join(root, hex.EncodeToString(h.Sum(nil)[:5]))
}

// candidates lists the folders and programs to try, best first: next to this program, the downloads of the
// configuration, then whatever the system has.
func candidates(c *ToolsConfig, root string) []Tools {
	var out []Tools
	pair := func(dir string) Tools {
		return Tools{FFmpeg: filepath.Join(dir, programName("ffmpeg")), FFprobe: filepath.Join(dir, programName("ffprobe"))}
	}
	if exe, err := os.Executable(); err == nil {
		out = append(out, pair(filepath.Dir(exe)))
	}
	for _, d := range c.For(runtime.GOOS, runtime.GOARCH) {
		out = append(out, pair(installDir(root, d)))
	}
	ffmpeg, err1 := exec.LookPath("ffmpeg")
	ffprobe, err2 := exec.LookPath("ffprobe")
	if err1 == nil && err2 == nil {
		out = append(out, Tools{FFmpeg: ffmpeg, FFprobe: ffprobe})
	}
	if runtime.GOOS == "darwin" {
		// Apps started from Finder do not get the shell's PATH, so look where Homebrew installs too.
		out = append(out, pair("/opt/homebrew/bin"), pair("/usr/local/bin"))
	}
	return out
}

// Locate finds the best ffmpeg and ffprobe already on this computer that are new enough.
func Locate(ctx context.Context, c *ToolsConfig, root string) (Tools, error) {
	tooOld := ""
	for _, t := range candidates(c, root) {
		if !fileExists(t.FFmpeg) || !fileExists(t.FFprobe) {
			continue
		}
		v, err := versionOf(ctx, t.FFmpeg)
		if err != nil {
			continue
		}
		if !newEnough(v, c.minimum) {
			tooOld = v
			continue
		}
		t.Version = v
		return t, nil
	}
	if tooOld != "" {
		return Tools{}, fmt.Errorf("%w: found %s, need %s or newer", ErrTooOld, tooOld, c.Minimum)
	}
	return Tools{}, ErrMissingTool
}

// Progress reports how much of a download has arrived, from 0 to 1.
type Progress func(fraction float64)

// Install downloads ffmpeg and ffprobe into root with the first download of c that works on this computer.
func Install(ctx context.Context, c *ToolsConfig, root string, client *http.Client, progress Progress) (Tools, error) {
	downloads := c.For(runtime.GOOS, runtime.GOARCH)
	if len(downloads) == 0 {
		return Tools{}, fmt.Errorf("no ffmpeg download is configured for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	var errs []error
	for _, d := range downloads {
		t, err := installOne(ctx, c, root, d, client, progress)
		if err == nil {
			return t, nil
		}
		if ctx.Err() != nil {
			return Tools{}, ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s: %w", d.Name, err))
	}
	return Tools{}, errors.Join(errs...)
}

func installOne(ctx context.Context, c *ToolsConfig, root string, d Download, client *http.Client, progress Progress) (Tools, error) {
	dir := installDir(root, d)
	part := dir + ".part"
	os.RemoveAll(part)
	if err := os.MkdirAll(part, 0o755); err != nil {
		return Tools{}, err
	}
	defer os.RemoveAll(part)
	for i, a := range d.Archives {
		zipPath := filepath.Join(part, fmt.Sprintf("archive-%d.zip", i))
		share := func(n, size int64) {
			if size > 0 {
				progress(min((float64(i)+float64(n)/float64(size))/float64(len(d.Archives)), 1))
			}
		}
		if err := fetch(ctx, client, a, zipPath, share); err != nil {
			return Tools{}, err
		}
		if err := unpackPrograms(zipPath, part); err != nil {
			return Tools{}, err
		}
		os.Remove(zipPath)
	}
	t := Tools{FFmpeg: filepath.Join(part, programName("ffmpeg")), FFprobe: filepath.Join(part, programName("ffprobe"))}
	if !fileExists(t.FFmpeg) || !fileExists(t.FFprobe) {
		return Tools{}, errors.New("the archives hold no ffmpeg and ffprobe")
	}
	v, err := versionOf(ctx, t.FFmpeg)
	if err != nil {
		return Tools{}, fmt.Errorf("the downloaded ffmpeg does not run: %w", err)
	}
	if !newEnough(v, c.minimum) {
		return Tools{}, fmt.Errorf("%w: the download is %s, need %s or newer", ErrTooOld, v, c.Minimum)
	}
	os.RemoveAll(dir)
	if err := os.Rename(part, dir); err != nil {
		return Tools{}, err
	}
	return Tools{FFmpeg: filepath.Join(dir, programName("ffmpeg")), FFprobe: filepath.Join(dir, programName("ffprobe")), Version: v}, nil
}

// fetch downloads one archive, checking its checksum when one is given; progress gets the bytes so far and the
// expected size (0 when unknown).
func fetch(ctx context.Context, client *http.Client, a Archive, dest string, progress func(n, size int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "btelo-forge")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", a.URL, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	var sum hash.Hash
	var w io.Writer = f
	if a.SHA256 != "" {
		sum = sha256.New()
		w = io.MultiWriter(f, sum)
	}
	size := a.Size
	if resp.ContentLength > 0 {
		size = resp.ContentLength
	}
	var n int64
	buf := make([]byte, 256<<10)
	last := time.Time{}
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			if _, err := w.Write(buf[:k]); err != nil {
				f.Close()
				return err
			}
			n += int64(k)
			if time.Since(last) > 150*time.Millisecond {
				progress(n, size)
				last = time.Now()
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if resp.ContentLength > 0 && n != resp.ContentLength {
		return fmt.Errorf("download incomplete: %d of %d bytes", n, resp.ContentLength)
	}
	if sum != nil && hex.EncodeToString(sum.Sum(nil)) != a.SHA256 {
		return errors.New("the download does not match its sha256 checksum")
	}
	progress(n, max(size, n))
	return nil
}

// unpackPrograms copies ffmpeg and ffprobe out of an archive into dir, wherever they are inside it.
func unpackPrograms(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	wanted := []string{programName("ffmpeg"), programName("ffprobe")}
	for _, f := range r.File {
		name := path.Base(f.Name)
		if f.FileInfo().IsDir() || !slices.Contains(wanted, name) {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
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

// FormatGuide explains the ffmpeg configuration to people and to AI assistants that edit it.
const FormatGuide = `ffmpeg configuration (JSON)

{"version": 1, "minimum": "6.0", "downloads": [download, ...]}

The app uses the first ffmpeg and ffprobe it finds whose version is at least minimum, looking next to the program,
then in the folders of the downloads below, then on the system (PATH, and Homebrew on macOS). When there is none it
downloads one: the downloads for this computer are tried in order until one works.

download  {"name", "os": windows | darwin | linux, "arch": amd64 | arm64, "archives": [archive, ...]}
archive   {"url": a .zip holding ffmpeg and/or ffprobe anywhere inside, "size": bytes (for progress, optional),
           "sha256": optional checksum checked after downloading}

Each download is unpacked into its own folder, named after its addresses, so changing an address installs anew.
The builds keep their own licences (the GPL builds include x264, x265, SVT-AV1 and libaom).`
