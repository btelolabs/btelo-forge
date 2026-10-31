package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Kind is what a model does, which decides how it is listed, what is asked of it and what comes out.
type Kind string

// The kinds of model, in the order they are listed.
const (
	KindLLM        Kind = "llm"        // writes text, and reads pictures when it is multimodal
	KindImage      Kind = "image"      // text to picture
	KindVoice      Kind = "voice"      // text to speech
	KindTranscribe Kind = "transcribe" // speech to text
	KindModel3D    Kind = "model3d"    // picture to 3D model
	KindVideo      Kind = "video"      // text or picture to video
	KindMatting    Kind = "matting"    // picture to what its subject is
)

// Kinds lists every kind in the order a list shows them.
var Kinds = []Kind{KindLLM, KindImage, KindVoice, KindTranscribe, KindModel3D, KindVideo, KindMatting}

// ChatKinds are the kinds a conversation can be with; a matting model only serves the drawing tool.
var ChatKinds = Kinds[:len(Kinds)-1]

// Valid reports whether k is one of the kinds.
func (k Kind) Valid() bool {
	for _, o := range Kinds {
		if k == o {
			return true
		}
	}
	return false
}

// Folder is the folder of the assets folder that what the kind makes is kept in, or "" for a kind that makes
// nothing to keep.
func (k Kind) Folder() string {
	switch k {
	case KindImage:
		return "ai-image"
	case KindVoice:
		return "ai-voice"
	case KindModel3D:
		return "ai-3dmodel"
	case KindVideo:
		return "ai-video"
	}
	return ""
}

// Makes reports whether the kind makes files, which are kept in Folder.
func (k Kind) Makes() bool { return k.Folder() != "" }

// Or is k, or the kind of a model that says none: language.
func (k Kind) Or() Kind {
	if k == "" {
		return KindLLM
	}
	return k
}

// Media is a file a model made, kept in the assets folder and shown in the conversation from there.
type Media struct {
	Kind Kind   `json:"kind"`
	Path string `json:"path"`
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// Slug makes a short file name part of text: its first words, with letters and digits only.
func Slug(text string) string {
	var out []rune
	for _, r := range unsafeName.ReplaceAllString(strings.TrimSpace(text), "-") {
		if len(out) >= 40 {
			break
		}
		out = append(out, unicode.ToLower(r))
	}
	if s := strings.Trim(string(out), "-"); s != "" {
		return s
	}
	return "untitled"
}

// SaveAsset writes data into the folder of kind in the assets folder root, under a name made of the time and of what
// it shows (the prompt), and returns its path. A name never replaces a file: when it is taken, a number is added.
func SaveAsset(root string, kind Kind, prompt, ext string, data []byte, at time.Time) (string, error) {
	folder := kind.Folder()
	if folder == "" {
		return "", fmt.Errorf("%s models make nothing to keep", kind)
	}
	dir := filepath.Join(root, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := at.Format("20060102-150405") + "-" + Slug(prompt)
	for n := 0; ; n++ {
		name := base + ext
		if n > 0 {
			name = fmt.Sprintf("%s-%d%s", base, n+1, ext)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(path)
			return "", werr
		}
		return path, nil
	}
}
