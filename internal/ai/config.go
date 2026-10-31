// Package ai runs the AI models the app uses and keeps one list of them: models that run on this computer, started and
// stopped as programs of their own, and models of online services reached with an API key. Chat is the first thing
// they do; the same list and the same loading and unloading serve whatever else uses a model. The models on offer
// come from a Config.
package ai

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/locale"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// DefaultJSON is the built-in list of engines, models and online services.
//
//go:embed models.json
var DefaultJSON []byte

// The kinds of model server, which differ in how they are started and asked.
const (
	APILlama = "llama-server" // llama-server of llama.cpp: a GGUF model behind an OpenAI-style chat interface
	APISD    = "sd-server"    // sd-server of stable-diffusion.cpp: an image model behind an OpenAI-style images interface
	// APIMatting is this program's own server (internal/matting) over a downloaded ONNX Runtime, the engine's executable.
	APIMatting = "matting-server"
)

// apiKind is the kind of model each model server runs, unless the model says it is of another of apiKinds.
var apiKind = map[string]Kind{APILlama: KindLLM, APISD: KindImage, APIMatting: KindMatting}

// apiKinds are the kinds of model each model server can run.
var apiKinds = map[string][]Kind{APILlama: {KindLLM}, APISD: {KindImage, KindVideo}, APIMatting: {KindMatting}}

// DefaultContext is how many tokens a model remembers when its entry does not say.
const DefaultContext = 8192

// Config lists the engines, models and online services offered for chat. It is read from JSON so other builds of the
// engines, other models and other services can be used without changing the program; FormatGuide describes the format.
type Config struct {
	Version      int        `json:"version"`
	DefaultModel string     `json:"defaultModel"`
	Engines      []Engine   `json:"engines"`
	Models       []Model    `json:"models"`
	Providers    []Provider `json:"providers"`
}

// Engine is a model server program of one kind (API), downloaded or installed.
type Engine struct {
	transcribe.Engine
	API       string   `json:"api"`
	Backend   string   `json:"backend,omitempty"` // cpu, vulkan, cuda or metal: where it computes
	Arguments []string `json:"arguments"`
}

// File is a file to download besides the model itself.
type File struct {
	URL    string `json:"url"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// Model is a model that runs on this computer.
type Model struct {
	transcribe.Model
	API        string      `json:"api"`
	Kind       Kind        `json:"kind,omitempty"`   // what it does; language when left out
	Series     locale.Text `json:"series,omitempty"` // the heading of the models listed together under it
	License    locale.Text `json:"license"`
	Commercial bool        `json:"commercial,omitempty"`
	Vision     bool        `json:"vision,omitempty"`    // it reads pictures; needs a Projector
	Context    int         `json:"context,omitempty"`   // tokens it remembers; 0 is DefaultContext
	Projector  *File       `json:"projector,omitempty"` // the file that lets it read pictures
	Arguments  []string    `json:"arguments,omitempty"` // added to the engine's; may use {model} and a {role} for each of Parts
	// Parts are the files a model is made of besides its main one, such as an image model's VAE and text encoder; each is
	// downloaded with it and its path put where {role} stands in Arguments.
	Parts []Part `json:"parts,omitempty"`
	// ImageSize ("1024x1024") and Steps are what an image model draws with when the request does not say; 0 leaves it to
	// the model.
	ImageSize string `json:"imageSize,omitempty"`
	Steps     int    `json:"steps,omitempty"`
	// Frames and FPS are the length of what a video model makes, and ImageSize and Steps apply to it as well.
	Frames int `json:"frames,omitempty"`
	FPS    int `json:"fps,omitempty"`
}

// Part is a file of a model besides its main one.
type Part struct {
	Role string `json:"role"` // what it is, and the placeholder in the arguments: vae, llm
	File
}

// Provider is an online service with an OpenAI-style interface, used with the user's API key.
type Provider struct {
	ID      string        `json:"id"`
	Name    locale.Text   `json:"name"`
	Note    locale.Text   `json:"note,omitempty"`
	BaseURL string        `json:"baseUrl"`
	KeyURL  string        `json:"keyUrl,omitempty"` // where the key is made
	Models  []RemoteModel `json:"models"`
}

// RemoteModel is a model of a Provider.
type RemoteModel struct {
	ID     string      `json:"id"`
	Name   locale.Text `json:"name"`
	Note   locale.Text `json:"note,omitempty"`
	Model  string      `json:"model"`          // the name the service knows it by
	Kind   Kind        `json:"kind,omitempty"` // what it does; language when left out
	Vision bool        `json:"vision,omitempty"`
	// Task is the interface of a model that takes minutes, which is every video or 3D model: one of TaskMiniMax, TaskTripo
	// or TaskMeshy. Version is what such a service calls the way it works, when that is not Model.
	Task    string `json:"task,omitempty"`
	Version string `json:"version,omitempty"`
	// Edits says an image model also redraws a picture attached to the message, asked at "/images/edits" with JSON.
	Edits bool `json:"edits,omitempty"`
}

// onlineKinds are the kinds an online service's model can be of: those the app knows how to ask for.
var onlineKinds = []Kind{KindLLM, KindImage, KindModel3D, KindVideo}

var (
	configID  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// placeholders are what an engine's arguments may use.
var placeholders = map[string]bool{"model": true, "port": true, "context": true, "engine": true}

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
		panic("the built-in AI models are invalid: " + err.Error())
	}
	return c
}

func (c *Config) check() error {
	if c.Version != 1 {
		return fmt.Errorf("version must be 1, not %d", c.Version)
	}
	engines := make([]transcribe.Engine, len(c.Engines))
	for i, e := range c.Engines {
		engines[i] = e.Engine
	}
	if err := transcribe.CheckEngines(engines); err != nil {
		return err
	}
	for i, e := range c.Engines {
		where := fmt.Sprintf("engines[%d] (%s)", i, e.ID)
		if _, ok := apiKind[e.API]; !ok {
			return fmt.Errorf("%s: api must be %q or %q", where, APILlama, APISD)
		}
		for _, arg := range e.Arguments {
			for _, m := range placeholder.FindAllStringSubmatch(arg, -1) {
				if !placeholders[m[1]] {
					return fmt.Errorf("%s: arguments: unknown placeholder {%s} (use {model}, {port} or {context})", where, m[1])
				}
			}
		}
		needs := []string{"{model}", "{port}"}
		if e.API == APISD {
			needs = []string{"{port}"} // each image model says how its files are given, in its own arguments
		}
		for _, need := range needs {
			if !slices.ContainsFunc(e.Arguments, func(a string) bool { return strings.Contains(a, need) }) {
				return fmt.Errorf("%s: arguments must use %s", where, need)
			}
		}
	}
	if len(c.Models) > 0 {
		models := make([]transcribe.Model, len(c.Models))
		for i, m := range c.Models {
			models[i] = m.Model
		}
		if err := transcribe.CheckModels(models); err != nil {
			return err
		}
	}
	for i, m := range c.Models {
		where := fmt.Sprintf("models[%d] (%s)", i, m.ID)
		switch {
		case apiKind[m.API] == "":
			return fmt.Errorf("%s: api must be %q, %q or %q", where, APILlama, APISD, APIMatting)
		case m.License.In("en") == "":
			return fmt.Errorf("%s: license (what the model may be used for) is missing", where)
		case !slices.Contains(apiKinds[m.API], m.Kind.Or()):
			return fmt.Errorf("%s: kind must be one of %q for api %q, not %q", where, apiKinds[m.API], m.API, m.Kind)
		case m.Context < 0:
			return fmt.Errorf("%s: context must not be negative", where)
		case m.Vision && m.Projector == nil:
			return fmt.Errorf("%s: a model that reads pictures needs its projector file", where)
		case m.Projector != nil:
			if err := checkFile(*m.Projector); err != nil {
				return fmt.Errorf("%s: projector: %w", where, err)
			}
		}
	}
	for i, m := range c.Models {
		if err := m.checkFiles(); err != nil {
			return fmt.Errorf("models[%d] (%s): %w", i, m.ID, err)
		}
	}
	seen := map[string]bool{}
	for i, p := range c.Providers {
		where := fmt.Sprintf("providers[%d] (%s)", i, p.ID)
		u, err := url.Parse(p.BaseURL)
		switch {
		case !configID.MatchString(p.ID) || seen[p.ID]:
			return fmt.Errorf("%s: id must be unique lowercase letters, digits, dots, dashes or underscores", where)
		case p.ID == ExtSpeech || p.ID == ExtSubtitles:
			return fmt.Errorf("%s: id %q is kept for the models of the app's own voice-over and subtitles", where, p.ID)
		case p.Name.In("en") == "":
			return fmt.Errorf("%s: name is missing", where)
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			return fmt.Errorf("%s: baseUrl must be an http:// or https:// address", where)
		case p.KeyURL != "" && !validWebURL(p.KeyURL):
			return fmt.Errorf("%s: keyUrl must be an http:// or https:// address", where)
		case len(p.Models) == 0:
			return fmt.Errorf("%s: give at least one model", where)
		}
		seen[p.ID] = true
		ids := map[string]bool{}
		for j, m := range p.Models {
			at := fmt.Sprintf("%s models[%d] (%s)", where, j, m.ID)
			switch {
			case !configID.MatchString(m.ID) || ids[m.ID]:
				return fmt.Errorf("%s: id must be unique lowercase letters, digits, dots, dashes or underscores", at)
			case m.Name.In("en") == "":
				return fmt.Errorf("%s: name is missing", at)
			case strings.TrimSpace(m.Model) == "":
				return fmt.Errorf("%s: model (the name the service knows it by) is missing", at)
			case !slices.Contains(onlineKinds, m.Kind.Or()):
				return fmt.Errorf("%s: kind must be one of %q, not %q", at, onlineKinds, m.Kind)
			case m.Kind.Or() != KindLLM && m.Vision:
				return fmt.Errorf("%s: vision is for language models", at)
			case m.Edits && m.Kind.Or() != KindImage:
				return fmt.Errorf("%s: edits is for image models", at)
			case m.Task == TaskSDVideo:
				return fmt.Errorf("%s: task %q is for a model on this computer", at, m.Task)
			case m.Task != "" && taskKinds[m.Task] == "":
				return fmt.Errorf("%s: task must be %q, %q or %q, not %q", at, TaskMiniMax, TaskTripo, TaskMeshy, m.Task)
			case m.Task != "" && taskKinds[m.Task] != m.Kind.Or():
				return fmt.Errorf("%s: task %q makes kind %q, not %q", at, m.Task, taskKinds[m.Task], m.Kind)
			case m.Task == "" && (m.Kind == KindVideo || m.Kind == KindModel3D):
				return fmt.Errorf("%s: a %s model needs a task", at, m.Kind)
			}
			ids[m.ID] = true
		}
	}
	if _, ok := c.Choice(c.DefaultModel); !ok {
		return fmt.Errorf("defaultModel %q is not one of the models (an online one is written provider/model)", c.DefaultModel)
	}
	return nil
}

var (
	partRole  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)
	imageSize = regexp.MustCompile(`^[1-9][0-9]{2,4}x[1-9][0-9]{2,4}$`)
)

// checkFiles checks what a model says of its files and of how they are given to the engine.
func (m Model) checkFiles() error {
	roles := map[string]bool{}
	for _, p := range m.Parts {
		switch {
		case !partRole.MatchString(p.Role) || placeholders[p.Role] || roles[p.Role]:
			return fmt.Errorf("parts: role %q must be unique lowercase letters, digits or underscores, and not model, port or context", p.Role)
		}
		if err := checkFile(p.File); err != nil {
			return fmt.Errorf("parts: %s: %w", p.Role, err)
		}
		roles[p.Role] = true
	}
	used := map[string]bool{}
	for _, arg := range m.Arguments {
		for _, found := range placeholder.FindAllStringSubmatch(arg, -1) {
			name := found[1]
			if !placeholders[name] && !roles[name] {
				return fmt.Errorf("arguments: unknown placeholder {%s} (use {model}, {port}, {context} or a part's role)", name)
			}
			used[name] = true
		}
	}
	for _, p := range m.Parts {
		if !used[p.Role] {
			return fmt.Errorf("parts: {%s} is not in the model's arguments, so the file would not be given to the engine", p.Role)
		}
	}
	if m.API == APISD && !used["model"] {
		return errors.New("arguments must use {model}: an image model says how its file is given to the engine")
	}
	switch {
	case m.Kind.Or() != KindImage && m.Kind.Or() != KindVideo && (m.ImageSize != "" || m.Steps != 0):
		return errors.New("imageSize and steps are for image and video models")
	case m.Kind.Or() != KindVideo && (m.Frames != 0 || m.FPS != 0):
		return errors.New("frames and fps are for video models")
	case m.Frames < 0 || m.Frames > 2000 || m.FPS < 0 || m.FPS > 120:
		return errors.New("frames must be from 0 to 2000 and fps from 0 to 120")
	case m.ImageSize != "" && !imageSize.MatchString(m.ImageSize):
		return fmt.Errorf("imageSize must be like 1024x1024, not %q", m.ImageSize)
	case m.Steps < 0 || m.Steps > 200:
		return errors.New("steps must be from 0 to 200")
	}
	return nil
}

func validWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func checkFile(f File) error {
	switch {
	case !validWebURL(f.URL):
		return errors.New("url must be an http:// or https:// address")
	case f.Size < 0:
		return errors.New("size must not be negative")
	case f.SHA256 != "" && !sha256Hex.MatchString(f.SHA256):
		return errors.New("sha256 must be 64 lowercase hexadecimal digits")
	}
	return nil
}

// Available lists the engines that run model on this computer.
func (c *Config) Available(model Model) []Engine {
	var out []Engine
	for _, e := range c.Engines {
		if e.API == model.API && e.Runs() {
			out = append(out, e)
		}
	}
	return out
}

// Engine returns the engine with id that runs model on this computer, or else the first one that does.
func (c *Config) Engine(model Model, id string) (Engine, bool) {
	list := c.Available(model)
	if i := slices.IndexFunc(list, func(e Engine) bool { return e.ID == id }); i >= 0 {
		return list[i], true
	}
	// The choice may be an engine of another kind of model: the user chose where models compute, and that is the same
	// backend (graphics card or processor) whichever program runs it.
	if i := slices.IndexFunc(c.Engines, func(e Engine) bool { return e.ID == id }); i >= 0 {
		if j := slices.IndexFunc(list, func(e Engine) bool { return e.Backend == c.Engines[i].Backend }); j >= 0 {
			return list[j], true
		}
	}
	if len(list) > 0 {
		return list[0], true
	}
	return Engine{}, false
}

// Model looks a local model up by id.
func (c *Config) Model(id string) (Model, bool) {
	i := slices.IndexFunc(c.Models, func(m Model) bool { return m.ID == id })
	if i < 0 {
		return Model{}, false
	}
	return c.Models[i], true
}

// Provider looks an online service up by id.
func (c *Config) Provider(id string) (Provider, bool) {
	i := slices.IndexFunc(c.Providers, func(p Provider) bool { return p.ID == id })
	if i < 0 {
		return Provider{}, false
	}
	return c.Providers[i], true
}

// Choice is a model to talk to, whether it runs here or online.
type Choice struct {
	ID       string // a local model's id, or provider/model for an online one
	Kind     Kind
	Name     locale.Text
	Note     locale.Text
	Series   locale.Text // the heading of the models listed together under it, for a model of another part of the app
	Vision   bool
	Local    *Model
	Provider *Provider
	Remote   *RemoteModel
	// Ext names the part of the app that runs a model it keeps its own list of: ExtSpeech or ExtSubtitles. Such a model
	// has neither Local nor Provider.
	Ext string
}

// The parts of the app that keep lists of models of their own, which are listed beside these.
const (
	ExtSpeech    = "speech"    // the voice-over models, kind voice
	ExtSubtitles = "subtitles" // the speech recognition models, kind transcribe
)

// External reports whether another part of the app runs the model.
func (c Choice) External() bool { return c.Ext != "" }

// ExtID is the id of a model of another part of the app in the list of choices.
func ExtID(ext, id string) string { return ext + "/" + id }

// ReadsPictures reports whether the model takes pictures with a message: a multimodal language model.
func (c Choice) ReadsPictures() bool { return c.Kind == KindLLM && c.Vision }

// StartsFromPicture reports whether the model works from a picture attached to the message: an image model that
// redraws it, and video and 3D models.
func (c Choice) StartsFromPicture() bool {
	switch c.Kind {
	case KindVideo, KindModel3D:
		return true
	case KindImage:
		return c.Local != nil || (c.Remote != nil && c.Remote.Edits)
	}
	return false
}

// Task is the interface of a model that takes minutes, or "" for one that answers at once.
func (c Choice) Task() string {
	switch {
	case c.Remote != nil:
		return c.Remote.Task
	case c.Local != nil && c.Kind == KindVideo:
		return TaskSDVideo
	}
	return ""
}

// Online reports whether the model is an online service's.
func (c Choice) Online() bool { return c.Provider != nil }

// Choices lists every model: the local ones, then each service's.
func (c *Config) Choices() []Choice {
	var out []Choice
	for i := range c.Models {
		m := &c.Models[i]
		out = append(out, Choice{ID: m.ID, Kind: m.Kind.Or(), Name: m.Name, Note: m.Note, Vision: m.Vision, Local: m})
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		for j := range p.Models {
			m := &p.Models[j]
			out = append(out, Choice{ID: p.ID + "/" + m.ID, Kind: m.Kind.Or(), Name: m.Name, Note: m.Note, Vision: m.Vision, Provider: p, Remote: m})
		}
	}
	return out
}

// Choice looks a model up by id.
func (c *Config) Choice(id string) (Choice, bool) {
	if provider, model, ok := strings.Cut(id, "/"); ok {
		p, found := c.Provider(provider)
		if !found {
			return Choice{}, false
		}
		i := slices.IndexFunc(p.Models, func(m RemoteModel) bool { return m.ID == model })
		if i < 0 {
			return Choice{}, false
		}
		return Choice{ID: id, Kind: p.Models[i].Kind.Or(), Name: p.Models[i].Name, Note: p.Models[i].Note, Vision: p.Models[i].Vision, Provider: &p, Remote: &p.Models[i]}, true
	}
	m, ok := c.Model(id)
	if !ok {
		return Choice{}, false
	}
	return Choice{ID: m.ID, Kind: m.Kind.Or(), Name: m.Name, Note: m.Note, Vision: m.Vision, Local: &m}, true
}

// ContextOf is how many tokens model remembers.
func (m Model) ContextOf() int {
	if m.Context > 0 {
		return m.Context
	}
	return DefaultContext
}

// FormatGuide explains the AI models file to people and to AI assistants that edit it.
const FormatGuide = `AI models configuration (JSON)

{"version": 1, "defaultModel": id, "engines": [...], "models": [...], "providers": [...]}.
The built-in file is a complete example.

engines    model server programs; only those whose os and arch match this computer are offered (os: windows,
           darwin; arch: amd64, arm64; leave out for any). "api" is "llama-server" (llama.cpp's server, for language models) or
           "sd-server" (stable-diffusion.cpp's, for image and video models); "backend" says where it computes (cpu, vulkan,
           cuda, metal), and the backend the user chose for one kind of model is used for the other;
           "arguments" is its command line. Placeholders: {model} the model file (required for llama-server, whose
           engine says it; an sd-server model says it in its own arguments), {port} the port it listens on (required),
           {context} the tokens the model remembers.
           Downloaded: {"id", "api", "backend", "name", "note", "url": a .zip archive, "size": bytes (for
           progress), "sha256": optional checksum, "executable": the program at the top of the archive,
           "arguments"}. Each url gets its own folder, so a new version downloads anew.
           Installed: {"id", "api", "backend", "name", "note", "commands": names looked up on PATH, "paths":
           full paths to try, "install": how to install it, shown when it is missing, "arguments"}.
models     models that run on this computer: {"id", "api": "llama-server", "series": optional heading over the
           models after one another that share it, "name", "note", "license": what it may be used for, shown
           with it, "commercial": true when that includes commercial work, "url": the GGUF file, "size": bytes,
           "sha256": optional checksum, "context": tokens it remembers (default 8192; more takes more memory),
           "vision": true for a model that reads pictures, with "projector": {"url", "size", "sha256"}, the
           file that lets it, "arguments": added to the engine's, and for a model of several files "parts":
           [{"role": "vae", "url", "size", "sha256"}], each downloaded with it and put where {role} stands in its
           arguments; an image model ("api": "sd-server", "kind": "image") gives its files in its arguments, such as
           ["--diffusion-model", "{model}", "--vae", "{vae}"], and "imageSize": "1024x1024" and "steps" for what it
           draws with; a video model ("api": "sd-server", "kind": "video") adds "frames" and "fps"}. Each url is
           stored separately.
providers  online services with an OpenAI-style interface, used with the user's API key, which is entered in
           Settings → AI models and never kept in this file: {"id", "name", "note", "baseUrl": the address
           that "/chat/completions" is added to, "keyUrl": where the key is made, "models": [{"id", "name",
           "note", "model": the name the service knows it by, "kind": "llm" (the default), "image" (a model
           asked at "/images/generations"; what it makes is kept in the assets folder's ai-image folder),
           "video" or "model3d", "vision": true when a language model reads pictures, "edits": true when an
           image model redraws an attached picture at "/images/edits", "task": the interface of a video or 3D
           model, "minimax-video", "tripo" or "meshy", "version": what Tripo calls the way it works}]}.
           A service's model is chosen as provider/model, such as deepseek/deepseek-v4-flash.
           Kinds: llm, image, voice, transcribe, model3d, video. Voice-over and subtitle models are listed under
           voice and transcribe from their own settings.

Names, notes and licenses are text or {"en": ..., "zh": ...}.`
