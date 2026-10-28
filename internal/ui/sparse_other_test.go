//go:build !windows

package ui

import (
	"os"
	"testing"
)

// sparseFile makes a file of size bytes that takes no room on the disc: lengthening a file leaves a hole.
func sparseFile(t testing.TB, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}
