package ui

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// sparseFile makes a file of size bytes that takes no room on the disc: NTFS keeps a sparse file's zeros unwritten.
func sparseFile(t testing.TB, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var n uint32
	if err := windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &n, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}
