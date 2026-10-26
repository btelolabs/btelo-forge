package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemLanguagePicksTheFirstPreferenceOrEnglish(t *testing.T) {
	cases := []struct {
		tags []string
		want Language
	}{
		{[]string{"zh-CN", "en-US"}, Chinese},
		{[]string{"zh-Hans-CN"}, Chinese},
		{[]string{"zh_TW"}, Chinese},
		{[]string{"ZH"}, Chinese},
		{[]string{"en-GB", "zh-CN"}, English},
		{[]string{"ja-JP", "zh-CN"}, English},
		{[]string{"fr"}, English},
		{nil, English},
		{[]string{""}, English},
	}
	for _, c := range cases {
		if got := systemLanguage(c.tags); got != c.want {
			t.Errorf("systemLanguage(%q) = %q, want %q", c.tags, got, c.want)
		}
	}
}

func TestFirstRunFollowsTheSystemAndLaterRunsKeepTheChoice(t *testing.T) {
	dir := t.TempDir()
	a := &App{host: &fakeHost{languages: []string{"zh-CN"}}, stateDir: dir}
	a.loadSettings()
	if a.settings.Language != Chinese {
		t.Fatalf("first run on a Chinese system gave %q", a.settings.Language)
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"language":"en"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadSettings()
	if a.settings.Language != English {
		t.Fatalf("a saved choice of English was replaced by %q", a.settings.Language)
	}
}
