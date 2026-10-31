//go:build !windows

package matting

import "errors"

// load has no implementation here yet: ONNX Runtime is reached without cgo on Windows only.
func load(string, string) (network, error) {
	return nil, errors.New("the subject of a picture can only be found on Windows for now")
}
