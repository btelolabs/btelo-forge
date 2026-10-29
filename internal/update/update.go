// Package update finds a newer release of the app on GitHub, downloads it and checks it against the checksum the
// release gives, and puts it in place of the installed copy, so that the app started next is the new one. The
// running program is renamed out of the way rather than overwritten, which Windows allows while it runs; what is
// replaced is kept beside it under a name of its own until the next start removes it, and a failure half way puts
// everything back.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LatestURL is where GitHub answers with the newest release of the app that is neither a draft nor a pre-release.
const LatestURL = "https://api.github.com/repos/btelolabs/btelo-forge/releases/latest"

// Release is a published version of the app.
type Release struct {
	Tag       string    `json:"tag_name"`
	Page      string    `json:"html_url"` // the release's page, with what is new in it
	Published time.Time `json:"published_at"`
	Assets    []Asset   `json:"assets"`
}

// Asset is a file of a release.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // "sha256:" and the file's checksum in hexadecimal
}

// Asset is the file of the release called name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

var (
	// ErrNoDownload means the release has no file for this system.
	ErrNoDownload = errors.New("the release has no download for this system")
	// ErrNoChecksum means the release does not say what its file should hold, so it is not installed.
	ErrNoChecksum = errors.New("the release gives no checksum for its download")
	// ErrChecksum means what was downloaded is not what the release published.
	ErrChecksum = errors.New("the download does not match the release's checksum")
	// ErrNotThisApp means the download does not hold the app where it should.
	ErrNotThisApp = errors.New("the download does not hold the app")
	// ErrUnsupported means this copy of the app cannot replace itself: it does not run from where a release puts it.
	ErrUnsupported = errors.New("this copy of the app cannot update itself")
	// ErrTranslocated means macOS runs the app from a read-only copy, as it does for an app not yet moved out of the
	// folder it was downloaded to.
	ErrTranslocated = errors.New("macOS runs this copy of the app from a read-only place")
	// ErrReadOnly means the folder the app is installed in cannot be written to.
	ErrReadOnly = errors.New("the app's folder cannot be written to")
)

// Latest asks url, a GitHub API address such as LatestURL, for the newest release.
func Latest(ctx context.Context, client *http.Client, url string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "btelo-forge")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("the release could not be read: %w", err)
	}
	if _, ok := ParseVersion(r.Tag); !ok {
		return Release{}, fmt.Errorf("the newest release is called %q, which is not a version", r.Tag)
	}
	return r, nil
}

// Version is a release's major, minor and patch numbers.
type Version [3]int

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// ParseVersion reads a release's version, such as "v0.3.1"; builds that are not releases ("dev", "dev-1a2b3c4")
// have none.
func ParseVersion(s string) (Version, bool) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, false
	}
	var v Version
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, false
		}
		v[i] = n
	}
	return v, true
}

// Newer reports whether release is a later version than current; it is never so for a build that is not a release.
func Newer(release, current string) bool {
	r, ok1 := ParseVersion(release)
	c, ok2 := ParseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range r {
		if r[i] != c[i] {
			return r[i] > c[i]
		}
	}
	return false
}

// Same reports whether a and b name the same release, with or without the leading v.
func Same(a, b string) bool {
	va, ok1 := ParseVersion(a)
	vb, ok2 := ParseVersion(b)
	return ok1 && ok2 && va == vb
}

// AssetName is the name of the release's file for a system, "" when there is none: the build publishes one for each
// Windows processor and one for macOS that runs on both.
func AssetName(goos, goarch string) string {
	switch {
	case goos == "windows" && (goarch == "amd64" || goarch == "arm64"):
		return "btelo-forge-windows-" + goarch + ".zip"
	case goos == "darwin":
		return "btelo-forge-macos.zip"
	}
	return ""
}

var digestPattern = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)

// Download fetches a release's file to dest, checking its size and its checksum; progress hears the bytes so far
// and how many are expected. A download that fails or does not match leaves nothing at dest.
func Download(ctx context.Context, client *http.Client, a Asset, dest string, progress func(done, total int64)) error {
	m := digestPattern.FindStringSubmatch(a.Digest)
	if m == nil {
		return ErrNoChecksum
	}
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
		return fmt.Errorf("%s: HTTP %d", a.Name, resp.StatusCode)
	}
	total := a.Size
	if total <= 0 {
		total = resp.ContentLength
	}
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(part)
		}
	}()
	sum := sha256.New()
	w := io.MultiWriter(f, sum)
	var done int64
	buf := make([]byte, 256<<10)
	last := time.Time{}
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			done += int64(n)
			if a.Size > 0 && done > a.Size {
				f.Close()
				return fmt.Errorf("%s: more than the %d bytes the release gives", a.Name, a.Size)
			}
			if time.Since(last) > 100*time.Millisecond {
				progress(done, total)
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
	if a.Size > 0 && done != a.Size {
		return fmt.Errorf("%s: download incomplete, %d of %d bytes", a.Name, done, a.Size)
	}
	if hex.EncodeToString(sum.Sum(nil)) != m[1] {
		return ErrChecksum
	}
	progress(done, done)
	if err := os.Rename(part, dest); err != nil {
		return err
	}
	keep = true
	return nil
}
