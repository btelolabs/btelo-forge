package win

import (
	_ "embed"
	"unsafe"
)

//go:embed webview2loader_arm64.dll
var webviewLoaderDLL []byte

const ctrlPutBounds = 6

// putBounds passes box as the ARM64 convention passes a structure of its size: in two registers.
func (v *WebView) putBounds() {
	words := (*[2]uintptr)(unsafe.Pointer(&v.box))
	v.controller.call(ctrlPutBounds, words[0], words[1])
}
