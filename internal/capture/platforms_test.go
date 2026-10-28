package capture

import (
	"strings"
	"testing"
)

func TestBuiltInPlatforms(t *testing.T) {
	p := DefaultPlatforms()
	for _, id := range []string{"youtube", "twitch", "bilibili", "douyin", "kuaishou", "wechat-channels", "custom"} {
		pl, ok := p.Find(id)
		if !ok {
			t.Fatalf("%s is not offered", id)
		}
		if pl.Name.In("zh") == "" || pl.Help.In("zh") == "" || pl.Help.In("en") == "" {
			t.Errorf("%s lacks a name or help in both languages: %+v", id, pl)
		}
	}
	if pl, _ := p.Find("douyin"); !pl.Portrait || len(pl.Servers) != 0 {
		t.Errorf("douyin %+v", pl)
	}
	if pl, _ := p.Find("youtube"); StreamURL(pl.Servers[0].URL, "k") != "rtmp://a.rtmp.youtube.com/live2/k" {
		t.Errorf("youtube %+v", pl)
	}
}

func TestParsePlatformsExplainsMistakes(t *testing.T) {
	for _, c := range []struct{ json, want string }{
		{`{"version": 2, "platforms": []}`, "version must be 1"},
		{`{"version": 1, "platforms": [{"id": "A B", "name": "x"}]}`, "platforms[0] (A B): id must be"},
		{`{"version": 1, "platforms": [{"id": "a"}]}`, "name is missing"},
		{`{"version": 1, "platforms": [{"id": "a", "name": "A"}, {"id": "a", "name": "B"}]}`, "platforms[1] (a): the id is used twice"},
		{`{"version": 1, "platforms": [{"id": "a", "name": "A", "servers": [{"name": "s", "url": "http://x/live"}]}]}`, "servers[0]: s: a stream is sent to"},
		{`{"version": 1, "platforms": [{"id": "a", "name": "A", "page": "ftp://x"}]}`, "page must be"},
		{`{"version": 1, "platforms": [{"id": "a", "name": "A", "colour": "red"}]}`, "unknown field"},
		{"{\"version\": 1,\n \"platforms\": [}", "line 2"},
	} {
		_, err := ParsePlatforms([]byte(c.json))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.json, err, c.want)
		}
	}
	p, err := ParsePlatforms([]byte(`{"version": 1, "platforms": [{"id": "my-server", "name": {"en": "Mine", "zh": "我的"}, "servers": [{"name": "Home", "url": "srt://10.0.0.2:9000"}], "maxKbps": 4000}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if pl, ok := p.Find("my-server"); !ok || pl.Name.In("zh") != "我的" || pl.MaxKbps != 4000 {
		t.Errorf("%+v", pl)
	}
}
