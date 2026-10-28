package capture

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"

	"github.com/btelolabs/btelo-forge/internal/locale"
)

// DefaultPlatformsJSON is the built-in list of streaming platforms.
//
//go:embed platforms.json
var DefaultPlatformsJSON []byte

// Platforms are the places a stream can be sent to, as offered when adding a destination. Any server that takes
// RTMP, RTMPS or SRT works; a platform only fills in its servers and says where its stream key is found.
type Platforms struct {
	Version   int        `json:"version"`
	Platforms []Platform `json:"platforms"`
}

// Platform is one streaming service.
type Platform struct {
	ID       string      `json:"id"`
	Name     locale.Text `json:"name"`
	Servers  []Server    `json:"servers,omitempty"` // none: the platform gives an address with each broadcast
	Help     locale.Text `json:"help,omitempty"`    // where the address and stream key are found
	Page     string      `json:"page,omitempty"`    // the web page they are found on
	MaxKbps  int         `json:"maxKbps,omitempty"` // the highest bit rate the platform takes for the picture
	Portrait bool        `json:"portrait,omitempty"`
}

// Server is a platform's server address, to which the stream key is added.
type Server struct {
	Name locale.Text `json:"name"`
	URL  string      `json:"url"`
}

var platformID = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// ParsePlatforms reads and checks a list of platforms; errors name the place in the file.
func ParsePlatforms(data []byte) (*Platforms, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.DisallowUnknownFields()
	var p Platforms
	if err := dec.Decode(&p); err != nil {
		return nil, locale.JSONError(data, err)
	}
	if dec.More() {
		return nil, errors.New("unexpected text after the configuration")
	}
	if p.Version != 1 {
		return nil, fmt.Errorf("version must be 1, not %d", p.Version)
	}
	seen := map[string]bool{}
	for i, pl := range p.Platforms {
		where := fmt.Sprintf("platforms[%d]", i)
		if pl.ID != "" {
			where += " (" + pl.ID + ")"
		}
		if err := pl.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if seen[pl.ID] {
			return nil, fmt.Errorf("%s: the id is used twice", where)
		}
		seen[pl.ID] = true
	}
	return &p, nil
}

func (pl Platform) check() error {
	if !platformID.MatchString(pl.ID) {
		return errors.New("id must be 1–32 lowercase letters, digits or dashes")
	}
	if pl.Name.In("en") == "" {
		return errors.New("name is missing")
	}
	for i, s := range pl.Servers {
		if s.Name.In("en") == "" {
			return fmt.Errorf("servers[%d]: name is missing", i)
		}
		if err := (Target{Name: s.Name.In("en"), URL: s.URL}).Check(); err != nil {
			return fmt.Errorf("servers[%d]: %w", i, err)
		}
	}
	if pl.Page != "" {
		if u, err := url.Parse(pl.Page); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("page must be an http:// or https:// address")
		}
	}
	if pl.MaxKbps < 0 {
		return errors.New("maxKbps cannot be negative")
	}
	return nil
}

// DefaultPlatforms is the built-in list.
func DefaultPlatforms() *Platforms {
	p, err := ParsePlatforms(DefaultPlatformsJSON)
	if err != nil {
		panic("the built-in streaming platforms are invalid: " + err.Error())
	}
	return p
}

// Find returns the platform with an id.
func (p *Platforms) Find(id string) (Platform, bool) {
	for _, pl := range p.Platforms {
		if pl.ID == id {
			return pl, true
		}
	}
	return Platform{}, false
}

// PlatformsGuide explains the platform list to people and to AI assistants that edit it.
const PlatformsGuide = `Streaming platforms (JSON)

{"version": 1, "platforms": [platform, ...]}

The platforms are offered, in this order, when a destination is added to a live stream. Every destination gets the
same stream, encoded once. A platform fills in its server addresses and tells the user where to find the stream
key; any server that takes RTMP, RTMPS or SRT can be streamed to, so a new service needs no code, only an entry.

platform  {"id": lowercase letters, digits and dashes, unique (required),
           "name": text (required),
           "servers": [server, ...] — the addresses to choose from; leave out when the service gives a new
                       address with each broadcast, so the user pastes it,
           "help": text — where the address and stream key are found,
           "page": the https:// page they are found on,
           "maxKbps": the highest bit rate the service takes for the picture (a warning above it),
           "portrait": true for services watched upright on phones (a 9:16 canvas is suggested)}
server    {"name": text, "url": an rtmp://, rtmps:// or srt:// address; the stream key is added after a "/"}

text is a string, or an object of language codes such as {"en": "Bilibili", "zh": "哔哩哔哩"}.
Destinations the user has set up keep their own address and key; changing a platform changes what is offered.`
