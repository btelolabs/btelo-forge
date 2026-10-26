package web

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// MaxUpload is the largest request a phone may send, in bytes.
const MaxUpload = 32 << 30

// Uploadable lists the file types a phone may send: videos, sound and pictures.
var Uploadable = []string{".mp4", ".mov", ".m4v", ".mkv", ".webm", ".avi", ".3gp", ".mts", ".hevc",
	".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg", ".opus",
	".jpg", ".jpeg", ".png", ".heic", ".webp", ".gif"}

// errNotMedia means a part was not a video, sound or picture.
var errNotMedia = errors.New("not a video, sound or picture")

// receive stores every file of a multipart upload in dir and returns the paths written; files of other types are
// reported by name and skipped.
func receive(r *http.Request, dir string) (saved, refused []string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, err
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return saved, refused, nil
		}
		if err != nil {
			return saved, refused, err
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		path, err := store(part, dir)
		part.Close()
		switch {
		case errors.Is(err, errNotMedia):
			refused = append(refused, part.FileName())
		case err != nil:
			return saved, refused, err
		default:
			saved = append(saved, path)
		}
	}
}

// store writes one uploaded file under a safe, unused name, only once it has arrived whole.
func store(part *multipart.Part, dir string) (string, error) {
	name := cleanName(part.FileName())
	if !slices.Contains(Uploadable, strings.ToLower(filepath.Ext(name))) {
		return "", errNotMedia
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, part); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	dest := unused(dir, name)
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	return dest, nil
}

// cleanName keeps the last element of a file name a browser sent, without characters file systems refuse.
func cleanName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if name == "" {
		name = "upload"
	}
	return name
}

// unused returns path dir/name, numbered when a file of that name is already there.
func unused(dir, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", base, n, ext)
		}
		path := filepath.Join(dir, candidate)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return path
		}
	}
}
