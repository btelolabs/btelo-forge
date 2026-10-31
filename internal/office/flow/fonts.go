package flow

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Book finds the typeface a document names on this computer. A document names families it does not carry with it, so
// each is looked for under its own name, then under a font of the same shape: what metrics a line has then stays
// close to what its author saw. Whatever a font lacks falls back to the system's CJK and symbol fonts.
type Book struct {
	base gfx.Fonts
	dirs []string

	mu    sync.Mutex
	fonts map[string]*gfx.Font
}

// NewBook is a book that falls back on the window's fonts.
func NewBook(base gfx.Fonts) *Book {
	return &Book{base: base, dirs: fontDirs(), fonts: map[string]*gfx.Font{}}
}

func fontDirs() []string {
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		return []string{"/System/Library/Fonts/Supplemental", "/System/Library/Fonts", "/Library/Fonts", filepath.Join(home, "Library/Fonts")}
	}
	dir := os.Getenv("WINDIR")
	if dir == "" {
		dir = `C:\Windows`
	}
	local := os.Getenv("LOCALAPPDATA")
	return []string{filepath.Join(dir, "Fonts"), filepath.Join(local, `Microsoft\Windows\Fonts`)}
}

// faceFiles are the files of a family: regular, bold, italic, bold italic. Files repeat where a family has fewer.
type faceFiles [4]string

// families maps a family, in lower case, to its files, on Windows and then on a Mac where the names differ.
var families = map[string]faceFiles{
	"calibri":         {"calibri.ttf", "calibrib.ttf", "calibrii.ttf", "calibriz.ttf"},
	"calibri light":   {"calibril.ttf", "calibrib.ttf", "calibrili.ttf", "calibriz.ttf"},
	"cambria":         {"cambria.ttc", "cambriab.ttf", "cambriai.ttf", "cambriaz.ttf"},
	"arial":           {"arial.ttf", "arialbd.ttf", "ariali.ttf", "arialbi.ttf"},
	"times new roman": {"times.ttf", "timesbd.ttf", "timesi.ttf", "timesbi.ttf"},
	"courier new":     {"cour.ttf", "courbd.ttf", "couri.ttf", "courbi.ttf"},
	"georgia":         {"georgia.ttf", "georgiab.ttf", "georgiai.ttf", "georgiaz.ttf"},
	"verdana":         {"verdana.ttf", "verdanab.ttf", "verdanai.ttf", "verdanaz.ttf"},
	"tahoma":          {"tahoma.ttf", "tahomabd.ttf", "tahoma.ttf", "tahomabd.ttf"},
	"segoe ui":        {"segoeui.ttf", "segoeuib.ttf", "segoeuii.ttf", "segoeuiz.ttf"},
	"consolas":        {"consola.ttf", "consolab.ttf", "consolai.ttf", "consolaz.ttf"},
	"宋体":              {"simsun.ttc", "simsunb.ttf", "simsun.ttc", "simsunb.ttf"},
	"simsun":          {"simsun.ttc", "simsunb.ttf", "simsun.ttc", "simsunb.ttf"},
	"黑体":              {"simhei.ttf", "simhei.ttf", "simhei.ttf", "simhei.ttf"},
	"simhei":          {"simhei.ttf", "simhei.ttf", "simhei.ttf", "simhei.ttf"},
	"微软雅黑":            {"msyh.ttc", "msyhbd.ttc", "msyh.ttc", "msyhbd.ttc"},
	"microsoft yahei": {"msyh.ttc", "msyhbd.ttc", "msyh.ttc", "msyhbd.ttc"},
	"等线":              {"Deng.ttf", "Dengb.ttf", "Deng.ttf", "Dengb.ttf"},
	"dengxian":        {"Deng.ttf", "Dengb.ttf", "Deng.ttf", "Dengb.ttf"},
	"楷体":              {"simkai.ttf", "simkai.ttf", "simkai.ttf", "simkai.ttf"},
	"kaiti":           {"simkai.ttf", "simkai.ttf", "simkai.ttf", "simkai.ttf"},
	"仿宋":              {"simfang.ttf", "simfang.ttf", "simfang.ttf", "simfang.ttf"},
	"fangsong":        {"simfang.ttf", "simfang.ttf", "simfang.ttf", "simfang.ttf"},
}

// macFamilies are the same families where a Mac has them, or the closest it has, as "file.ttc#n" where a collection
// holds the style.
var macFamilies = map[string]faceFiles{
	"arial":           {"Arial.ttf", "Arial Bold.ttf", "Arial Italic.ttf", "Arial Bold Italic.ttf"},
	"times new roman": {"Times New Roman.ttf", "Times New Roman Bold.ttf", "Times New Roman Italic.ttf", "Times New Roman Bold Italic.ttf"},
	"courier new":     {"Courier New.ttf", "Courier New Bold.ttf", "Courier New Italic.ttf", "Courier New Bold Italic.ttf"},
	"georgia":         {"Georgia.ttf", "Georgia Bold.ttf", "Georgia Italic.ttf", "Georgia Bold Italic.ttf"},
	"verdana":         {"Verdana.ttf", "Verdana Bold.ttf", "Verdana Italic.ttf", "Verdana Bold Italic.ttf"},
	"tahoma":          {"Tahoma.ttf", "Tahoma Bold.ttf", "Tahoma.ttf", "Tahoma Bold.ttf"},
}

// similar names, for a family not on this computer, one of the same shape and of about the same widths.
var similar = map[string]string{
	"calibri":          "arial",
	"calibri light":    "arial",
	"carlito":          "calibri",
	"aptos":            "calibri",
	"aptos display":    "calibri",
	"helvetica":        "arial",
	"liberation sans":  "arial",
	"cambria":          "times new roman",
	"caladea":          "cambria",
	"times":            "times new roman",
	"liberation serif": "times new roman",
	"courier":          "courier new",
	"consolas":         "courier new",
	"segoe ui":         "arial",
	"等线":               "微软雅黑",
	"dengxian":         "微软雅黑",
	"等线 light":         "等线",
	"宋体":               "微软雅黑",
	"simsun":           "微软雅黑",
	"新宋体":              "宋体",
	"nsimsun":          "宋体",
	"楷体":               "宋体",
	"仿宋":               "宋体",
	"黑体":               "微软雅黑",
}

// Font is the family at a style. An unknown family is the window's own font; it never fails.
func (b *Book) Font(family string, bold, italic bool) *gfx.Font {
	key := strings.ToLower(strings.TrimSpace(family))
	style := 0
	if bold {
		style = 1
	}
	if italic {
		style += 2
	}
	id := key + "\x00" + string(rune('0'+style))
	b.mu.Lock()
	defer b.mu.Unlock()
	if f, ok := b.fonts[id]; ok {
		return f
	}
	f := b.find(key, style)
	b.fonts[id] = f
	return f
}

func (b *Book) find(key string, style int) *gfx.Font {
	seen := map[string]bool{}
	for key != "" && !seen[key] {
		seen[key] = true
		if f := b.open(key, style); f != nil {
			return f
		}
		key = similar[key]
	}
	if style&1 != 0 {
		return b.base.Bold
	}
	return b.base.Regular
}

// open loads the family's file for the style, with the system's fonts behind it for what it lacks.
func (b *Book) open(key string, style int) *gfx.Font {
	files, ok := families[key]
	if runtime.GOOS == "darwin" {
		if m, has := macFamilies[key]; has {
			files, ok = m, true
		}
	}
	if !ok {
		return nil
	}
	for _, dir := range b.dirs {
		path := filepath.Join(dir, files[style])
		if _, err := os.Stat(path); err != nil {
			continue
		}
		f, err := gfx.LoadFont(path)
		if err != nil {
			continue
		}
		f.WithFallbacks(fallbackFiles(b.dirs)...)
		return f
	}
	return nil
}

// fallbackFiles are searched for characters a document's font lacks: Chinese first, as most mixed documents are.
func fallbackFiles(dirs []string) []string {
	var out []string
	for _, name := range []string{"msyh.ttc", "PingFang.ttc", "Hiragino Sans GB.ttc", "STHeiti Medium.ttc", "segoeui.ttf", "malgun.ttf",
		"seguisym.ttf", "seguiemj.ttf", "Arial Unicode.ttf"} {
		for _, dir := range dirs {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err == nil {
				out = append(out, path)
				break
			}
		}
	}
	return out
}
