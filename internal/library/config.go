package library

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

	"github.com/btelolabs/btelo-video-cut/internal/locale"
)

// DefaultJSON is the built-in list of music and sound-effect sources.
//
//go:embed sources.json
var DefaultJSON []byte

// Config lists the online sources the music panel searches. It is read from JSON so new sites can be added
// without changing the program; FormatGuide describes the format.
type Config struct {
	Version int      `json:"version"`
	Sources []Source `json:"sources"`
}

// Kinds of source.
const (
	KindMusic   = "music"
	KindEffects = "effects"
)

// Modes of source: a search asks the site for every page, a catalog downloads one complete list and is searched here.
const (
	ModeSearch  = "search"
	ModeCatalog = "catalog"
)

// Source is one site.
type Source struct {
	ID       string      `json:"id"`
	Name     locale.Text `json:"name"`
	Kind     string      `json:"kind"`
	Mode     string      `json:"mode"`
	Disabled bool        `json:"disabled,omitempty"`
	NotInAll bool        `json:"notInAll,omitempty"`
	Note     locale.Text `json:"note,omitempty"`
	Hint     locale.Text `json:"hint,omitempty"`
	Filters  []string    `json:"filters,omitempty"`
	Featured int         `json:"featured,omitempty"`
	PageSize int         `json:"pageSize,omitempty"`

	Request Request                      `json:"request"`
	Results string                       `json:"results,omitempty"`
	Pages   string                       `json:"pages,omitempty"`
	Total   string                       `json:"total,omitempty"`
	Error   string                       `json:"error,omitempty"`
	Where   map[string]string            `json:"where,omitempty"`
	Let     map[string]Pick              `json:"let,omitempty"`
	Expand  *Expand                      `json:"expand,omitempty"`
	Fields  Fields                       `json:"fields"`
	Rules   []Rule                       `json:"rules,omitempty"`
	Maps    map[string]map[string]string `json:"maps,omitempty"`

	c compiled
}

// Request is an HTTP GET.
type Request struct {
	URL        string            `json:"url"`
	Params     map[string]Param  `json:"params,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	CacheHours float64           `json:"cacheHours,omitempty"`
}

// Param is one query parameter: a template, or a list of templates for a repeated parameter.
type Param []string

// UnmarshalJSON accepts a string or a list of strings.
func (p *Param) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*p = Param{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return errors.New("expected a string or a list of strings")
	}
	*p = list
	return nil
}

// MarshalJSON writes a single value as a plain string.
func (p Param) MarshalJSON() ([]byte, error) {
	if len(p) == 1 {
		return json.Marshal(p[0])
	}
	return json.Marshal([]string(p))
}

// Pick chooses the first element of a list in the result whose fields match.
type Pick struct {
	From  string            `json:"from"`
	Where map[string]string `json:"where,omitempty"`
}

// Expand fetches a second document for every result and lists the tracks found in it.
type Expand struct {
	Request
	Results string            `json:"results"`
	Where   map[string]string `json:"where,omitempty"`
	Limit   int               `json:"limit,omitempty"`
}

// Fields are templates that build a track from one result.
type Fields struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Artist       string `json:"artist,omitempty"`
	Genre        string `json:"genre,omitempty"`
	Mood         string `json:"mood,omitempty"`
	Keywords     string `json:"keywords,omitempty"`
	Duration     string `json:"duration,omitempty"`
	DurationUnit string `json:"durationUnit,omitempty"`
	URL          string `json:"url"`
	Referer      string `json:"referer,omitempty"`
	Ext          string `json:"ext,omitempty"`
	License      string `json:"license,omitempty"`
	Credit       string `json:"credit,omitempty"`
	Page         string `json:"page,omitempty"`
}

// Rule overrides fields when a template's value matches a pattern.
type Rule struct {
	If      string            `json:"if"`
	Matches string            `json:"matches"`
	Set     map[string]string `json:"set"`
}

// fieldNames are the Fields members by their JSON names, in order.
var fieldNames = []string{"id", "title", "artist", "genre", "mood", "keywords", "duration", "url", "referer", "ext",
	"license", "credit", "page"}

func (f *Fields) templates() map[string]string {
	return map[string]string{"id": f.ID, "title": f.Title, "artist": f.Artist, "genre": f.Genre, "mood": f.Mood,
		"keywords": f.Keywords, "duration": f.Duration, "url": f.URL, "referer": f.Referer, "ext": f.Ext,
		"license": f.License, "credit": f.Credit, "page": f.Page}
}

type compiledRequest struct {
	url     template
	params  map[string][]template
	headers map[string]template
}

type compiledRule struct {
	cond    template
	matches string
	set     map[string]template
}

type compiled struct {
	request, expand compiledRequest
	fields          map[string]template
	rules           []compiledRule
}

var sourceID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

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
	if c.Version != 1 {
		return nil, fmt.Errorf("version must be 1, not %d", c.Version)
	}
	seen := map[string]bool{}
	for i := range c.Sources {
		s := &c.Sources[i]
		if err := s.check(); err != nil {
			name := s.ID
			if name == "" {
				name = fmt.Sprint(i)
			}
			return nil, fmt.Errorf("sources[%d] (%s): %w", i, name, err)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("sources[%d]: the id %q is used twice", i, s.ID)
		}
		seen[s.ID] = true
	}
	return &c, nil
}

// DefaultConfig is the built-in configuration.
func DefaultConfig() *Config {
	c, err := ParseConfig(DefaultJSON)
	if err != nil {
		panic("the built-in music sources are invalid: " + err.Error())
	}
	return c
}

func (s *Source) check() error {
	if !sourceID.MatchString(s.ID) || s.ID == string(Local) || s.ID == "all" {
		return fmt.Errorf("id must be 1–32 lowercase letters, digits or dashes (and not %q or \"all\")", Local)
	}
	if s.Name.In("en") == "" {
		return errors.New("name is missing")
	}
	if s.Kind != KindMusic && s.Kind != KindEffects {
		return fmt.Errorf("kind must be %q or %q", KindMusic, KindEffects)
	}
	if s.Mode == "" {
		s.Mode = ModeSearch
	}
	if s.Mode != ModeSearch && s.Mode != ModeCatalog {
		return fmt.Errorf("mode must be %q or %q", ModeSearch, ModeCatalog)
	}
	if s.PageSize == 0 {
		s.PageSize = 20
	}
	if s.PageSize < 1 || s.PageSize > 200 {
		return errors.New("pageSize must be between 1 and 200")
	}
	if s.Featured < 0 || s.Featured > 100 {
		return errors.New("featured must be between 0 and 100")
	}
	if s.Mode == ModeCatalog && s.Featured == 0 {
		s.Featured = 10
	}
	if len(s.Filters) > 0 && s.Mode != ModeCatalog {
		return errors.New("filters only work with the catalog mode")
	}
	if s.Pages != "" && s.Total != "" {
		return errors.New("give pages or total, not both")
	}
	var err error
	if s.c.request, err = s.compileRequest(s.Request); err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if s.Expand != nil {
		if s.Expand.Results == "" {
			return errors.New("expand: results is missing")
		}
		if s.c.expand, err = s.compileRequest(s.Expand.Request); err != nil {
			return fmt.Errorf("expand: %w", err)
		}
		if s.Expand.Limit < 0 {
			return errors.New("expand: limit must not be negative")
		}
	}
	for name, p := range s.Let {
		if !sourceID.MatchString(name) {
			return fmt.Errorf("let: %q is not a usable name", name)
		}
		if p.From == "" {
			return fmt.Errorf("let.%s: from is missing", name)
		}
	}
	if s.Fields.DurationUnit != "" && s.Fields.DurationUnit != "seconds" && s.Fields.DurationUnit != "milliseconds" {
		return errors.New(`fields.durationUnit must be "seconds" or "milliseconds"`)
	}
	s.c.fields = map[string]template{}
	for name, text := range s.Fields.templates() {
		if text == "" {
			if name == "id" || name == "title" || name == "url" {
				return fmt.Errorf("fields.%s is missing", name)
			}
			continue
		}
		t, err := compile(text, s.Maps)
		if err != nil {
			return fmt.Errorf("fields.%s: %w", name, err)
		}
		s.c.fields[name] = t
	}
	for i, r := range s.Rules {
		cond, err := compile(r.If, s.Maps)
		if err != nil {
			return fmt.Errorf("rules[%d].if: %w", i, err)
		}
		cr := compiledRule{cond: cond, matches: r.Matches, set: map[string]template{}}
		if len(r.Set) == 0 {
			return fmt.Errorf("rules[%d]: set is empty", i)
		}
		for name, text := range r.Set {
			if !slices.Contains(fieldNames, name) {
				return fmt.Errorf("rules[%d].set: unknown field %q", i, name)
			}
			if cr.set[name], err = compile(text, s.Maps); err != nil {
				return fmt.Errorf("rules[%d].set.%s: %w", i, name, err)
			}
		}
		s.c.rules = append(s.c.rules, cr)
	}
	return nil
}

func (s *Source) compileRequest(r Request) (compiledRequest, error) {
	var c compiledRequest
	var err error
	if r.URL == "" {
		return c, errors.New("url is missing")
	}
	if c.url, err = compile(r.URL, s.Maps); err != nil {
		return c, fmt.Errorf("url: %w", err)
	}
	if u, err := url.Parse(c.url.literalPrefix()); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return c, errors.New("url must start with http:// or https://")
	}
	if r.CacheHours < 0 {
		return c, errors.New("cacheHours must not be negative")
	}
	c.params = map[string][]template{}
	for name, values := range r.Params {
		for _, v := range values {
			t, err := compile(v, s.Maps)
			if err != nil {
				return c, fmt.Errorf("params.%s: %w", name, err)
			}
			c.params[name] = append(c.params[name], t)
		}
	}
	c.headers = map[string]template{}
	for name, v := range r.Headers {
		t, err := compile(v, s.Maps)
		if err != nil {
			return c, fmt.Errorf("headers.%s: %w", name, err)
		}
		c.headers[name] = t
	}
	return c, nil
}

// literalPrefix is the text before the first expression, used to check the scheme of a URL.
func (t template) literalPrefix() string {
	var b strings.Builder
	for _, p := range t.parts {
		if !p.literal {
			break
		}
		b.WriteString(p.text)
	}
	return b.String()
}

// Enabled lists the sources in use.
func (c *Config) Enabled() []*Source {
	var out []*Source
	for i := range c.Sources {
		if !c.Sources[i].Disabled {
			out = append(out, &c.Sources[i])
		}
	}
	return out
}

// Find returns the source with an id.
func (c *Config) Find(id string) (*Source, bool) {
	for i := range c.Sources {
		if c.Sources[i].ID == id {
			return &c.Sources[i], true
		}
	}
	return nil, false
}
