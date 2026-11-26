package gfx

import (
	"os"
	"strings"
	"testing"
)

func TestMacSystemFontsLoadAndFindPingFang(t *testing.T) {
	regular, bold, mono, fallbacks := systemFonts()
	if len(regular) == 0 || len(bold) == 0 || len(mono) == 0 {
		t.Fatalf("empty font lists: reg=%v bold=%v mono=%v", regular, bold, mono)
	}
	if pf := pingFangPath(); pf != "" {
		if !strings.HasPrefix(regular[0], pf+"#") {
			t.Errorf("expected PingFang first in regular, got %q", regular[0])
		}
		if !strings.HasPrefix(bold[0], pf+"#") || bold[0] == regular[0] {
			t.Errorf("expected distinct PingFang bold index, got reg=%q bold=%q", regular[0], bold[0])
		}
	}
	for _, f := range fallbacks {
		if _, err := os.Stat(f); err != nil {
			t.Logf("fallback %s not present on this host: %v", f, err)
		}
	}
	fonts, err := LoadSystemFonts()
	if err != nil {
		t.Fatalf("LoadSystemFonts: %v", err)
	}
	if fonts.Regular == nil || fonts.Bold == nil || fonts.Mono == nil || fonts.Icons == nil {
		t.Fatalf("missing loaded font: %+v", fonts)
	}
	// Verify both Latin and CJK glyphs measure with positive width.
	if w := fonts.Regular.Face(16).Measure("Hello 世界"); w <= 0 {
		t.Fatalf("expected positive width for Latin+CJK, got %v", w)
	}
	if w := fonts.Bold.Face(16).Measure("Hello 世界"); w <= 0 {
		t.Fatalf("expected positive bold width, got %v", w)
	}
}
