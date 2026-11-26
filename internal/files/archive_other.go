//go:build !windows

package files

import (
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
)

var legacyDecoders = []encoding.Encoding{
	simplifiedchinese.GB18030,
	japanese.ShiftJIS,
	charmap.Windows1252,
}

// decodeLegacyName reads a zip entry's name written in a non-UTF-8 legacy code page (such as GBK/GB18030 on Chinese
// Windows, Shift-JIS on Japanese Windows, or Windows-1252 on Western Windows).
func decodeLegacyName(name string) string {
	if name == "" || utf8.ValidString(name) {
		return name
	}
	for _, enc := range legacyDecoders {
		decoded, err := enc.NewDecoder().String(name)
		if err == nil && !strings.ContainsRune(decoded, '\uFFFD') && utf8.ValidString(decoded) {
			return decoded
		}
	}
	return strings.ToValidUTF8(name, "?")
}

// systemTar is the system's tar: libarchive's on macOS, which reads 7-Zip, RAR, and xz.
func systemTar() string {
	if runtime.GOOS == "darwin" {
		return "/usr/bin/tar"
	}
	return "tar"
}
