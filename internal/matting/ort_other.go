//go:build !windows && !darwin

package matting

import "errors"

// load has no implementation here yet: ONNX Runtime is supported on Windows and macOS.
func load(string, string) (network, error) {
	return nil, errors.New("the subject of a picture can only be found on Windows and macOS for now")
}
