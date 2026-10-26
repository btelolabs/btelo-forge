package win

import (
	"slices"
	"testing"
	"unicode/utf16"
)

func TestSplitMultiStringStopsAtTheEmptyString(t *testing.T) {
	buf := utf16.Encode([]rune("zh-CN\x00en-US\x00\x00junk\x00"))
	if got := splitMultiString(buf); !slices.Equal(got, []string{"zh-CN", "en-US"}) {
		t.Fatalf("got %q", got)
	}
	if got := splitMultiString([]uint16{0, 0}); got != nil {
		t.Fatalf("empty list gave %q", got)
	}
}

func TestLanguagesNamesTheSystemLanguage(t *testing.T) {
	got := Languages()
	if len(got) == 0 || got[0] == "" {
		t.Fatalf("no preferred language: %q", got)
	}
}
