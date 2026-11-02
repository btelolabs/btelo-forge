package win

import (
	_ "embed"
	"unsafe"
)

//go:embed webview2loader_amd64.dll
var webviewLoaderDLL []byte

const ctrlPutBounds = 6

// putBounds passes box as the x64 convention passes a structure of its size: by address.
func (v *WebView) putBounds() {
	v.controller.call(ctrlPutBounds, uintptr(unsafe.Pointer(&v.box)))
}
