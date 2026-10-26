package gfx

import (
	"os"
	"path/filepath"
)

func systemFonts() (regular, bold, mono, fallbacks []string) {
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	if os.Getenv("WINDIR") == "" {
		dir = `C:\Windows\Fonts`
	}
	in := func(names ...string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = filepath.Join(dir, n)
		}
		return out
	}
	return in("msyh.ttc", "msyh.ttf", "simhei.ttf", "segoeui.ttf"),
		in("msyhbd.ttc", "msyhbd.ttf", "simhei.ttf", "segoeuib.ttf"),
		in("consola.ttf", "CascadiaMono.ttf", "cour.ttf"),
		in("segoeui.ttf", "malgun.ttf", "Nirmala.ttc", "nirmala.ttf", "LeelawUI.ttf", "seguisym.ttf", "seguiemj.ttf")
}
