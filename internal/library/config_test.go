package library

import (
	"strings"
	"testing"
)

func TestTemplatesRenderPathsAndFilters(t *testing.T) {
	maps := map[string]map[string]string{"g": {"22": "Soundtrack"}}
	item := map[string]any{
		"name": "My Song.MP3", "genre": "22", "tags": ",media,piano,ambient,jazz,", "empty": "",
		"list": []any{"", "b", "c"}, "n": 3155.0, "deep": map[string]any{"files": []any{map[string]any{"x": "first"}}},
	}
	s := &scope{item: item, parent: map[string]any{"id": "album"}, vars: map[string]string{"query": "a b"}, maps: maps}
	for tmpl, want := range map[string]string{
		"{name|unsuffix:.mp3}":                            "My Song",
		"{genre|map:g} / {genre|map:g|lower}":             "Soundtrack / soundtrack",
		"{tags|split:,|words|drop:media|take:2|join: · }": "piano · ambient",
		"{empty,name|path}":                               "My%20Song.MP3",
		"{list|first}":                                    "b",
		"{list|join:+}":                                   "b+c",
		"{n} ms":                                          "3155 ms",
		"{deep.files.0.x}":                                "first",
		"{$parent.id}/{name|query}":                       "album/My+Song.MP3",
		"{$query|ifset:(%s) AND }x":                       "(a b) AND x",
		"{empty|ifset:(%s)}{empty|ifunset:rank}":          "rank",
		"{missing|default:none} {name|quote}":             `none "My Song.MP3"`,
		"{{literal}} {name|replace: ,_}":                  "{literal} My_Song.MP3",
		"https://x/a.WAV?y=1 {$query|ifunset:no}":         "https://x/a.WAV?y=1 ",
	} {
		c, err := compile(tmpl, maps)
		if err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}
		if got := c.render(s); got != want {
			t.Errorf("%s = %q, want %q", tmpl, got, want)
		}
	}
	ext, _ := compile("{u|ext}", nil)
	if got := ext.render(&scope{item: map[string]any{"u": "https://cdn/x/A.WAV?k=1"}}); got != "wav" {
		t.Errorf("ext = %q", got)
	}
}

func TestTemplateErrorsAreReported(t *testing.T) {
	for tmpl, want := range map[string]string{
		"{name":          "unclosed",
		"name}":          "unmatched",
		"{name|nope}":    "unknown filter",
		"{name|map:zz}":  "no map",
		"{name|join}":    "needs an argument",
		"{$nope.x}":      "unknown scope",
		"{name|take:-1}": "count",
		"{}":             "empty path",
	} {
		if _, err := compile(tmpl, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", tmpl, err, want)
		}
	}
}

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		pattern, s string
		ok         bool
	}{
		{"*.mp3", "a.MP3", true}, {"*.mp3", "a.mp3.jpg", false}, {"*", "", false}, {"*", "x", true},
		{"cc0", "CC0", true}, {"*publicdomain*", "http://creativecommons.org/publicdomain/zero/1.0/", true},
		{"a*b*c", "aXbYc", true}, {"a*a", "a", false},
	} {
		if glob(c.pattern, c.s) != c.ok {
			t.Errorf("glob(%q, %q) != %v", c.pattern, c.s, c.ok)
		}
	}
}

func TestDefaultConfigIsValidAndCoversEveryKind(t *testing.T) {
	c := DefaultConfig()
	kinds := map[string]int{}
	for _, s := range c.Enabled() {
		kinds[s.Kind]++
	}
	if kinds[KindMusic] < 3 || kinds[KindEffects] < 1 {
		t.Fatalf("%v", kinds)
	}
}

func TestConfigErrorsNameTheSourceAndPlace(t *testing.T) {
	valid := `{"version":1,"sources":[{"id":"x","name":"X","kind":"music","request":{"url":"https://x/"},"fields":{"id":"{a}","title":"{b}","url":"{c}"}}]}`
	if _, err := ParseConfig([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for data, want := range map[string]string{
		`{"version":2,"sources":[]}`:                                  "version must be 1",
		"{\n\"version\": 1,\n\"sources\": [}":                         "line 3",
		`{"version":1,"sources":[],"x":1}`:                            "unknown field",
		strings.Replace(valid, `"kind":"music"`, `"kind":"video"`, 1): "sources[0] (x): kind",
		strings.Replace(valid, `"https://x/"`, `"ftp://x/"`, 1):       "http://",
		strings.Replace(valid, `"title":"{b}",`, ``, 1):               "fields.title is missing",
		strings.Replace(valid, `"{c}"`, `"{c|nope}"`, 1):              "fields.url",
		strings.Replace(valid, `"id":"x"`, `"id":"Bad Id"`, 1):        "id must be",
		strings.Replace(valid, `}]}`, `},{"id":"x","name":"Y","kind":"music","request":{"url":"https://y/"},"fields":{"id":"{a}","title":"{b}","url":"{c}"}}]}`, 1): "used twice",
		strings.Replace(valid, `"fields"`, `"rules":[{"if":"{a}","matches":"*","set":{"colour":"x"}}],"fields"`, 1):                                                 "unknown field \"colour\"",
	} {
		if _, err := ParseConfig([]byte(data)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s\n  error %v, want %q", data, err, want)
		}
	}
}
