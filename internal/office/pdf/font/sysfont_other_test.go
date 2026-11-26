//go:build !windows && !darwin

package font

import "testing"

func arialFont(t *testing.T) []byte {
	t.Helper()
	t.Skip("no arial.ttf")
	return nil
}

type namedCollection struct {
	name string
	data []byte
}

func cjkCollections(t *testing.T) []namedCollection {
	t.Helper()
	t.Skip("no CJK .ttc collection")
	return nil
}

func openTypeFontPaths() []string { return nil }
