//go:build !windows

package printing

import (
	"context"
	"errors"
)

// Target is a printer. Only Windows has one here: elsewhere a page goes to a PDF and the system's viewer prints it.
type Target struct {
	Name    string
	Copies  int
	Collate bool
	Output  string
}

// Available says this system has a print system this package drives.
const Available = false

// Printers lists no printers where Available is false.
func Printers() []string { return nil }

// PrinterTarget is not used where Available is false.
func PrinterTarget(string) (*Target, error) { return nil, errors.New("no printing here") }

// ChoosePrinter is not used where Available is false.
func ChoosePrinter(uintptr, *Target) (*Target, error) { return nil, errors.New("no printing here") }

// Print is not used where Available is false.
func (t *Target) Print(context.Context, *Doc, func(done, total int)) error {
	return errors.New("no printing here")
}
