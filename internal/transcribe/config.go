package transcribe

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/locale"
)

// DefaultJSON is the built-in list of speech engines, models and languages.
//
//go:embed models.json
var DefaultJSON []byte

// Config lists the speech engines and models offered for AI subtitles. It is read from JSON so other versions of
// whisper.cpp and other models can be used without changing the program; FormatGuide describes the format.
type Config struct {
	Version         int        `json:"version"`
	DefaultModel    string     `json:"defaultModel"`
	Engines         []Engine   `json:"engines"`
	Models          []Model    `json:"models"`
	Languages       []Language `json:"languages"`
	Arguments       []string   `json:"arguments"`
	PromptArguments []string   `json:"promptArguments,omitempty"`
	Progress        string     `json:"progress,omitempty"`

	progress *regexp.Regexp
}

// Engine is a whisper.cpp command-line program: downloaded as a zip archive, or already installed on the system.
type Engine struct {
	ID         string      `json:"id"`
	Name       locale.Text `json:"name"`
	Note       locale.Text `json:"note,omitempty"`
	OS         []string    `json:"os,omitempty"`
	Arch       []string    `json:"arch,omitempty"`
	URL        string      `json:"url,omitempty"`
	Size       int64       `json:"size,omitempty"`
	SHA256     string      `json:"sha256,omitempty"`
	Executable string      `json:"executable,omitempty"`
	Commands   []string    `json:"commands,omitempty"`
	Paths      []string    `json:"paths,omitempty"`
	Install    string      `json:"install,omitempty"`
}

// Model is one ggml model file.
type Model struct {
	ID     string      `json:"id"`
	Name   locale.Text `json:"name"`
	Note   locale.Text `json:"note,omitempty"`
	URL    string      `json:"url"`
	Size   int64       `json:"size,omitempty"`
	SHA256 string      `json:"sha256,omitempty"`
}

// Language is a spoken language; the code "auto" detects it.
type Language struct {
	Code   string      `json:"code"`
	Name   locale.Text `json:"name"`
	Prompt string      `json:"prompt,omitempty"`
}

var (
	configID  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// placeholders are what the arguments may use.
var placeholders = map[string]bool{"model": true, "audio": true, "language": true, "output": true, "threads": true, "prompt": true}

var placeholder = regexp.MustCompile(`\{([^{}]*)\}`)

// ParseConfig reads and checks a configuration; errors name the place in the file.
func ParseConfig(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, locale.JSONError(data, err)
	}
	if dec.More() {
		return nil, errors.New("unexpected text after the configuration")
	}
	if err := c.check(); err != nil {
		return nil, err
	}
	return &c, nil
}

// DefaultConfig is the built-in configuration.
func DefaultConfig() *Config {
	c, err := ParseConfig(DefaultJSON)
	if err != nil {
		panic("the built-in subtitle models are invalid: " + err.Error())
	}
	return c
}

func (c *Config) check() error {
	if c.Version != 1 {
		return fmt.Errorf("version must be 1, not %d", c.Version)
	}
	if err := CheckEngines(c.Engines); err != nil {
		return err
	}
	if err := CheckModels(c.Models); err != nil {
		return err
	}
	seen := map[string]bool{}
	if _, ok := c.Model(c.DefaultModel); !ok {
		return fmt.Errorf("defaultModel %q is not one of the models", c.DefaultModel)
	}
	if len(c.Languages) == 0 {
		return errors.New("languages: give at least one language")
	}
	for i, l := range c.Languages {
		if l.Code == "" || seen["language:"+l.Code] {
			return fmt.Errorf("languages[%d]: code must be given once", i)
		}
		if l.Name.In("en") == "" {
			return fmt.Errorf("languages[%d] (%s): name is missing", i, l.Code)
		}
		seen["language:"+l.Code] = true
	}
	for _, group := range []struct {
		name string
		args []string
	}{{"arguments", c.Arguments}, {"promptArguments", c.PromptArguments}} {
		for _, arg := range group.args {
			for _, m := range placeholder.FindAllStringSubmatch(arg, -1) {
				if !placeholders[m[1]] {
					return fmt.Errorf("%s: unknown placeholder {%s} (use {model}, {audio}, {language}, {output}, {threads} or {prompt})", group.name, m[1])
				}
			}
		}
	}
	for _, need := range []string{"{model}", "{audio}", "{output}"} {
		if !slices.ContainsFunc(c.Arguments, func(a string) bool { return strings.Contains(a, need) }) {
			return fmt.Errorf("arguments must use %s", need)
		}
	}
	if c.Progress == "" {
		c.Progress = `progress =\s*(\d+)%`
	}
	re, err := regexp.Compile(c.Progress)
	if err != nil {
		return fmt.Errorf("progress: %w", err)
	}
	if re.NumSubexp() != 1 {
		return errors.New("progress must have one group that captures the percentage")
	}
	c.progress = re
	return nil
}

// CheckEngines checks a list of engines: unique ids, and either an archive to download or an installed program.
func CheckEngines(engines []Engine) error {
	seen := map[string]bool{}
	for i, e := range engines {
		where := fmt.Sprintf("engines[%d] (%s)", i, e.ID)
		switch {
		case !configID.MatchString(e.ID) || seen["engine:"+e.ID]:
			return fmt.Errorf("%s: id must be unique lowercase letters, digits, dots, dashes or underscores", where)
		case e.Name.In("en") == "":
			return fmt.Errorf("%s: name is missing", where)
		case e.URL == "" && len(e.Commands) == 0 && len(e.Paths) == 0:
			return fmt.Errorf("%s: give url (a zip to download) or commands / paths (an installed program)", where)
		case e.URL != "" && (len(e.Commands) > 0 || len(e.Paths) > 0):
			return fmt.Errorf("%s: give url or commands / paths, not both", where)
		case e.URL != "" && e.Executable == "":
			return fmt.Errorf("%s: executable (the program inside the zip) is missing", where)
		case e.URL != "" && !strings.HasSuffix(strings.ToLower(urlPath(e.URL)), ".zip"):
			return fmt.Errorf("%s: url must point to a .zip archive", where)
		case e.Executable != "" && (strings.ContainsAny(e.Executable, `/\`) || e.Executable == "." || e.Executable == ".."):
			return fmt.Errorf("%s: executable must be a file name", where)
		case e.Size < 0:
			return fmt.Errorf("%s: size must not be negative", where)
		case e.SHA256 != "" && !sha256Hex.MatchString(e.SHA256):
			return fmt.Errorf("%s: sha256 must be 64 lowercase hexadecimal digits", where)
		}
		if err := checkURL(e.URL); e.URL != "" && err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		seen["engine:"+e.ID] = true
	}
	return nil
}

// CheckModels checks a list of models: at least one, unique ids, and an address for each.
func CheckModels(models []Model) error {
	seen := map[string]bool{}
	if len(models) == 0 {
		return errors.New("models: give at least one model")
	}
	for i, m := range models {
		where := fmt.Sprintf("models[%d] (%s)", i, m.ID)
		switch {
		case !configID.MatchString(m.ID) || seen["model:"+m.ID]:
			return fmt.Errorf("%s: id must be unique lowercase letters, digits, dots, dashes or underscores", where)
		case m.Name.In("en") == "":
			return fmt.Errorf("%s: name is missing", where)
		case m.Size < 0:
			return fmt.Errorf("%s: size must not be negative", where)
		case m.SHA256 != "" && !sha256Hex.MatchString(m.SHA256):
			return fmt.Errorf("%s: sha256 must be 64 lowercase hexadecimal digits", where)
		}
		if err := checkURL(m.URL); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		seen["model:"+m.ID] = true
	}
	return nil
}

func urlPath(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	return u.Path
}

func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url must be an http:// or https:// address")
	}
	if path.Base(u.Path) == "/" || path.Base(u.Path) == "." {
		return errors.New("url must name a file")
	}
	return nil
}

// Available lists the engines that run on this computer.
func (c *Config) Available() []Engine { return Runnable(c.Engines) }

// Runnable lists the engines of list that run on this computer.
func Runnable(list []Engine) []Engine {
	var out []Engine
	for _, e := range list {
		if e.Runs() {
			out = append(out, e)
		}
	}
	return out
}

// Runs reports whether the engine is made for this computer's system and processor.
func (e Engine) Runs() bool {
	return (len(e.OS) == 0 || slices.Contains(e.OS, runtime.GOOS)) && (len(e.Arch) == 0 || slices.Contains(e.Arch, runtime.GOARCH))
}

// Engine returns an available engine by id, or the first available one when id is unknown.
func (c *Config) Engine(id string) (Engine, bool) { return PickEngine(c.Engines, id) }

// PickEngine returns the engine of list with id that runs on this computer, or the first one that does.
func PickEngine(engines []Engine, id string) (Engine, bool) {
	list := Runnable(engines)
	for _, e := range list {
		if e.ID == id {
			return e, true
		}
	}
	if len(list) > 0 {
		return list[0], true
	}
	return Engine{}, false
}

// Model looks a model up by id.
func (c *Config) Model(id string) (Model, bool) {
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Language looks a language up by code.
func (c *Config) Language(code string) (Language, bool) {
	for _, l := range c.Languages {
		if l.Code == code {
			return l, true
		}
	}
	return Language{}, false
}

// FormatGuide explains the subtitle models file to people and to AI assistants that edit it.
const FormatGuide = `Subtitle models configuration (JSON)

{"version": 1, "defaultModel": id, "engines": [...], "models": [...], "languages": [...], "arguments": [...],
 "promptArguments": [...], "progress": regexp}. The built-in file is a complete example.

engines  whisper.cpp command-line programs; only those whose os and arch match this computer are offered
         (os: windows, darwin; arch: amd64, arm64; leave out for any).
         Downloaded: {"id", "name", "note", "url": a .zip archive, "size": bytes (for progress), "sha256": optional
         checksum of the archive, "executable": the program's file name at the top of the archive (a folder holding
         everything is looked inside)}. Each url gets its own folder, so a new version downloads anew.
         Installed: {"id", "name", "note", "commands": names looked up on PATH, "paths": full paths to try,
         "install": the command that installs it, shown when it is missing}.
models   {"id", "name", "note", "url": the model file, "size": bytes, "sha256": optional checksum checked after
         downloading}. Each url is stored separately, so changing it downloads the new file.
languages {"code": the code passed to the engine ("auto" detects), "name", "prompt": optional text that steers
         the engine, passed with promptArguments}
arguments the command line of the engine. Placeholders: {model} model file, {audio} 16 kHz mono WAV,
         {language} code, {output} output path without extension, {threads}, {prompt}. The engine must write
         whisper.cpp JSON output ({"transcription": [{"offsets": {"from": ms, "to": ms}, "text": ...}]}) to
         {output}.json.
progress  regular expression matched against the engine's error output; its one group is the percentage done.

Names and notes are text or {"en": ..., "zh": ...}.`
