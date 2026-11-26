//go:build darwin

package flow

import (
	"os"
	"path/filepath"
)

func fontDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{
		"/System/Library/Fonts/Supplemental",
		"/System/Library/Fonts",
		"/Library/Fonts",
		filepath.Join(home, "Library/Fonts"),
	}
	// macOS 15+ stores downloadable/system CJK fonts (PingFang, Kai, STFANGSO, etc.) under MobileAsset storage.
	if assets, err := filepath.Glob("/System/Library/AssetsV2/com_apple_MobileAsset_Font*/*/AssetData"); err == nil {
		dirs = append(dirs, assets...)
	}
	return dirs
}

// macFamilies maps common document family names (including Windows Chinese office fonts) to native macOS TrueType
// font files so both screen rendering and PDF subset embedding work out of the box.
var macFamilies = map[string]faceFiles{
	"arial":           {"Arial.ttf", "Arial Bold.ttf", "Arial Italic.ttf", "Arial Bold Italic.ttf"},
	"times new roman": {"Times New Roman.ttf", "Times New Roman Bold.ttf", "Times New Roman Italic.ttf", "Times New Roman Bold Italic.ttf"},
	"courier new":     {"Courier New.ttf", "Courier New Bold.ttf", "Courier New Italic.ttf", "Courier New Bold Italic.ttf"},
	"georgia":         {"Georgia.ttf", "Georgia Bold.ttf", "Georgia Italic.ttf", "Georgia Bold Italic.ttf"},
	"verdana":         {"Verdana.ttf", "Verdana Bold.ttf", "Verdana Italic.ttf", "Verdana Bold Italic.ttf"},
	"tahoma":          {"Tahoma.ttf", "Tahoma Bold.ttf", "Tahoma.ttf", "Tahoma Bold.ttf"},
	"微软雅黑":            {"STHeiti Light.ttc", "STHeiti Medium.ttc", "STHeiti Light.ttc", "STHeiti Medium.ttc"},
	"microsoft yahei": {"STHeiti Light.ttc", "STHeiti Medium.ttc", "STHeiti Light.ttc", "STHeiti Medium.ttc"},
	"黑体":              {"STHeiti Light.ttc", "STHeiti Medium.ttc", "STHeiti Light.ttc", "STHeiti Medium.ttc"},
	"simhei":          {"STHeiti Light.ttc", "STHeiti Medium.ttc", "STHeiti Light.ttc", "STHeiti Medium.ttc"},
	"等线":              {"STHeiti Light.ttc", "STHeiti Medium.ttc", "STHeiti Light.ttc", "STHeiti Medium.ttc"},
	"dengxian":        {"STHeiti Light.ttc", "STHeiti Medium.ttc", "STHeiti Light.ttc", "STHeiti Medium.ttc"},
	"宋体":              {"Songti.ttc", "Songti.ttc", "Songti.ttc", "Songti.ttc"},
	"simsun":          {"Songti.ttc", "Songti.ttc", "Songti.ttc", "Songti.ttc"},
	"楷体":              {"Kai.ttf", "Kai.ttf", "Kai.ttf", "Kai.ttf"},
	"kaiti":           {"Kai.ttf", "Kai.ttf", "Kai.ttf", "Kai.ttf"},
	"仿宋":              {"STFANGSO.ttf", "STFANGSO.ttf", "STFANGSO.ttf", "STFANGSO.ttf"},
	"fangsong":        {"STFANGSO.ttf", "STFANGSO.ttf", "STFANGSO.ttf", "STFANGSO.ttf"},
}

func platformFamilies(key string) (faceFiles, bool) {
	if f, ok := macFamilies[key]; ok {
		return f, true
	}
	f, ok := families[key]
	return f, ok
}

func fallbackNames() []string {
	return []string{
		"STHeiti Light.ttc",
		"STHeiti Medium.ttc",
		"Arial Unicode.ttf",
		"Songti.ttc",
		"PingFang.ttc",
		"Hiragino Sans GB.ttc",
		"AppleSDGothicNeo.ttc",
	}
}
