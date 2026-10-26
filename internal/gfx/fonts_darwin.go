package gfx

func systemFonts() (regular, bold, mono, fallbacks []string) {
	const sys, sup = "/System/Library/Fonts/", "/System/Library/Fonts/Supplemental/"
	return []string{sys + "PingFang.ttc", sys + "Hiragino Sans GB.ttc", sys + "STHeiti Light.ttc", sup + "Arial Unicode.ttf", sys + "Helvetica.ttc"},
		[]string{sys + "PingFang.ttc#6", sys + "Hiragino Sans GB.ttc#1", sys + "STHeiti Medium.ttc", sup + "Arial Unicode.ttf", sys + "Helvetica.ttc#1"},
		[]string{sys + "Menlo.ttc", sys + "SFNSMono.ttf", sys + "Monaco.ttf", sup + "Courier New.ttf"},
		[]string{sys + "SFNS.ttf", sys + "AppleSDGothicNeo.ttc", sup + "Arial Unicode.ttf", sys + "Thonburi.ttc", sup + "Kohinoor.ttc"}
}
