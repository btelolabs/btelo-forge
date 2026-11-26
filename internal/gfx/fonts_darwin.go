package gfx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font/sfnt"
)

// pingFangPath finds PingFang.ttc, which Apple moved from /System/Library/Fonts into MobileAsset storage on macOS 15+.
func pingFangPath() string {
	const legacy = "/System/Library/Fonts/PingFang.ttc"
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	matches, _ := filepath.Glob("/System/Library/AssetsV2/com_apple_MobileAsset_Font*/*/AssetData/PingFang.ttc")
	for _, m := range matches {
		if _, err := os.Stat(m); err == nil {
			return m
		}
	}
	return ""
}

// pingFangIndices returns the TTC indices for PingFang SC Regular and Semibold.
// macOS 15+ ships 24 faces (HK/MO/TC/SC × 6 weights); older macOS shipped 18 faces (HK/TC/SC × 6 weights).
func pingFangIndices(path string) (regular, bold int) {
	regular, bold = 0, 6
	data, err := os.ReadFile(path)
	if err != nil {
		return regular, bold
	}
	coll, err := sfnt.ParseCollection(data)
	if err != nil {
		return regular, bold
	}
	var buf sfnt.Buffer
	foundReg, foundBold := false, false
	for i := range coll.NumFonts() {
		f, err := coll.Font(i)
		if err != nil {
			continue
		}
		name, err := f.Name(&buf, sfnt.NameIDFull)
		if err != nil {
			continue
		}
		switch {
		case strings.EqualFold(name, "PingFang SC Regular"):
			regular, foundReg = i, true
		case strings.EqualFold(name, "PingFang SC Semibold"):
			bold, foundBold = i, true
		case !foundBold && strings.EqualFold(name, "PingFang SC Medium"):
			bold = i
		}
		if foundReg && foundBold {
			break
		}
	}
	return regular, bold
}

func systemFonts() (regular, bold, mono, fallbacks []string) {
	const sys, sup = "/System/Library/Fonts/", "/System/Library/Fonts/Supplemental/"
	if pf := pingFangPath(); pf != "" {
		regIdx, boldIdx := pingFangIndices(pf)
		regular = append(regular, fmt.Sprintf("%s#%d", pf, regIdx))
		bold = append(bold, fmt.Sprintf("%s#%d", pf, boldIdx))
	}
	regular = append(regular, sys+"Hiragino Sans GB.ttc", sys+"STHeiti Light.ttc", sup+"Arial Unicode.ttf", sys+"Helvetica.ttc")
	bold = append(bold, sys+"Hiragino Sans GB.ttc#2", sys+"STHeiti Medium.ttc", sup+"Arial Unicode.ttf", sys+"Helvetica.ttc#1")
	mono = []string{sys + "Menlo.ttc", sys + "SFNSMono.ttf", sys + "Monaco.ttf", sup + "Courier New.ttf"}
	fallbacks = []string{sys + "SFNS.ttf", sys + "AppleSDGothicNeo.ttc", sup + "Arial Unicode.ttf", sup + "Thonburi.ttc", sys + "ThonburiUI.ttc", sys + "Kohinoor.ttc", sys + "Apple Symbols.ttf"}
	return regular, bold, mono, fallbacks
}
