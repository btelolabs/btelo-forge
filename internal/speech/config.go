// Package speech turns text into spoken audio with a local AI model, run by a speech server it starts and stops:
// audio.cpp's audiocpp_server (for Qwen3-TTS) or breeze-server of Breeze-TTS-2.cpp (for Breeze TTS 2). The
// engine and the chosen model are downloaded the first time they are needed. The engines, models and voices on
// offer come from a Config.
package speech

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/locale"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

// DefaultJSON is the built-in list of speech engines, models and voices.
//
//go:embed voices.json
var DefaultJSON []byte

// The kinds of speech server, which differ in how they are started and asked for speech.
const (
	APIAudioCPP = "audio.cpp"     // audiocpp_server, configured with a file naming the model
	APIBreeze   = "breeze-server" // breeze-server of Breeze-TTS-2.cpp, given the model on its command line
)

// Config lists the engines, models and voices offered for turning text into speech. It is read from JSON so other
// builds of the engines, other models and other voices can be used without changing the program; FormatGuide
// describes the format.
type Config struct {
	Version      int      `json:"version"`
	DefaultModel string   `json:"defaultModel"`
	DefaultVoice string   `json:"defaultVoice"`
	Engines      []Engine `json:"engines"`
	Models       []Model  `json:"models"`
	Voices       []Voice  `json:"voices"`
}

// Engine is a speech server program of one kind (API).
type Engine struct {
	transcribe.Engine
	API       string   `json:"api"`
	Backend   string   `json:"backend,omitempty"` // for audio.cpp: vulkan, cuda, metal or cpu
	Arguments []string `json:"arguments"`
}

// Model is a speech model, run by an engine of its API.
type Model struct {
	transcribe.Model
	API        string      `json:"api"`
	Family     string      `json:"family,omitempty"` // for audio.cpp: the model family, such as qwen3_tts
	Task       string      `json:"task,omitempty"`   // for audio.cpp: the task, such as vdes
	Commercial bool        `json:"commercial,omitempty"`
	License    locale.Text `json:"license"`
	Series     locale.Text `json:"series,omitempty"` // the heading of the models listed together under it
}

// Voice is a voice the model invents from a description; the same description and seed give the same voice.
type Voice struct {
	ID          string      `json:"id"`
	Name        locale.Text `json:"name"`
	Instruction string      `json:"instruction"`
	Seed        int         `json:"seed,omitempty"`     // 0 is DefaultSeed
	Guidance    float64     `json:"guidance,omitempty"` // classifier-free guidance of Breeze; 0 is its default (1, none)
}

// DefaultSeed picks the voice of a description when its seed is not given.
const DefaultSeed = 42

var voiceID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// placeholders are what the arguments may use, by API.
var placeholders = map[string]map[string]bool{
	APIAudioCPP: {"config": true},
	APIBreeze:   {"model": true, "port": true, "voices": true},
}

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
		panic("the built-in speech voices are invalid: " + err.Error())
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
		allowed, ok := placeholders[e.API]
		if !ok {
			return fmt.Errorf("%s: api must be %q or %q", where, APIAudioCPP, APIBreeze)
		}
		for _, arg := range e.Arguments {
			for _, m := range placeholder.FindAllStringSubmatch(arg, -1) {
				if !allowed[m[1]] {
					return fmt.Errorf("%s: arguments: unknown placeholder {%s} for %s", where, m[1], e.API)
				}
			}
		}
		need := []string{"{model}", "{port}"}
		if e.API == APIAudioCPP {
			need = []string{"{config}"}
			if e.Backend == "" {
				return fmt.Errorf("%s: backend is missing (vulkan, cuda, metal or cpu)", where)
			}
		}
		for _, p := range need {
			if !slices.ContainsFunc(e.Arguments, func(a string) bool { return strings.Contains(a, p) }) {
				return fmt.Errorf("%s: arguments must use %s", where, p)
			}
		}
	}
	models := make([]transcribe.Model, len(c.Models))
	for i, m := range c.Models {
		models[i] = m.Model
	}
	if err := transcribe.CheckModels(models); err != nil {
		return err
	}
	for i, m := range c.Models {
		where := fmt.Sprintf("models[%d] (%s)", i, m.ID)
		switch {
		case placeholders[m.API] == nil:
			return fmt.Errorf("%s: api must be %q or %q", where, APIAudioCPP, APIBreeze)
		case m.API == APIAudioCPP && (m.Family == "" || m.Task == ""):
			return fmt.Errorf("%s: an audio.cpp model needs its family and task", where)
		case m.License.In("en") == "":
			return fmt.Errorf("%s: license (what the model may be used for) is missing", where)
		}
	}
	if _, ok := c.Model(c.DefaultModel); !ok {
		return fmt.Errorf("defaultModel %q is not one of the models", c.DefaultModel)
	}
	if len(c.Voices) == 0 {
		return errors.New("voices: give at least one voice")
	}
	seen := map[string]bool{}
	for i, v := range c.Voices {
		where := fmt.Sprintf("voices[%d] (%s)", i, v.ID)
		switch {
		case !voiceID.MatchString(v.ID) || seen[v.ID]:
			return fmt.Errorf("%s: id must be unique lowercase letters, digits, dots, dashes or underscores", where)
		case v.Name.In("en") == "":
			return fmt.Errorf("%s: name is missing", where)
		case strings.TrimSpace(v.Instruction) == "":
			return fmt.Errorf("%s: instruction (the description of the voice) is missing", where)
		case v.Seed < 0:
			return fmt.Errorf("%s: seed must not be negative", where)
		case v.Guidance != 0 && (v.Guidance < 1 || v.Guidance > 5):
			return fmt.Errorf("%s: guidance must be between 1 and 5", where)
		}
		seen[v.ID] = true
	}
	if _, ok := c.Voice(c.DefaultVoice); !ok {
		return fmt.Errorf("defaultVoice %q is not one of the voices", c.DefaultVoice)
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
	if len(list) > 0 {
		return list[0], true
	}
	return Engine{}, false
}

// Model looks a model up by id.
func (c *Config) Model(id string) (Model, bool) {
	i := slices.IndexFunc(c.Models, func(m Model) bool { return m.ID == id })
	if i < 0 {
		return Model{}, false
	}
	return c.Models[i], true
}

// Voice looks a voice up by id.
func (c *Config) Voice(id string) (Voice, bool) {
	i := slices.IndexFunc(c.Voices, func(v Voice) bool { return v.ID == id })
	if i < 0 {
		return Voice{}, false
	}
	return c.Voices[i], true
}

// FormatGuide explains the speech voices file to people and to AI assistants that edit it.
const FormatGuide = `Speech voices configuration (JSON)

{"version": 1, "defaultModel": id, "defaultVoice": id, "engines": [...], "models": [...], "voices": [...]}.
The built-in file is a complete example.

engines  speech server programs; only those whose os and arch match this computer are offered (os: windows,
         darwin; arch: amd64, arm64; leave out for any). Each has an "api":
           "audio.cpp"      audiocpp_server of audio.cpp; "backend" is vulkan, cuda, metal or cpu, and "arguments"
                            must pass {config}, a file the app writes naming the model.
           "breeze-server"  breeze-server of Breeze-TTS-2.cpp; "arguments" must pass {model} (the model file) and
                            {port}, and may pass {voices} (a folder for its saved voices).
         Downloaded: {"id", "api", "name", "note", "url": a .zip archive, "size": bytes (for progress), "sha256":
         optional checksum of the archive, "executable": the program at the top of the archive, or its path there
         with forward slashes (a folder holding everything is looked inside), "backend", "arguments"}. Each url gets its own folder, so a new version
         downloads anew.
         Installed: {"id", "api", "name", "note", "commands": names looked up on PATH, "paths": full paths to try,
         "install": how to install it, shown when it is missing, "backend", "arguments"}.
models   {"id", "api": the kind of engine that runs it, "family" and "task": for audio.cpp, the model family and task
         (qwen3_tts and vdes for Qwen3-TTS VoiceDesign), "series": optional heading over the models after one
         another that share it, "name", "note", "license": what it may be used for, shown with it, "commercial":
         true when that includes commercial work, "url": the GGUF model file, "size": bytes, "sha256": optional
         checksum checked after downloading}. Each url is stored separately.
voices   {"id", "name", "instruction": an English description of the voice and how it speaks, "seed": optional
         number that picks one of the voices fitting the description (default 42), "guidance": optional 1 to 5,
         for Breeze only: how strictly the description is followed (default 1; 2 to 3 also makes tags like (laugh)
         heard)}

Names, notes and licenses are text or {"en": ..., "zh": ...}.`
