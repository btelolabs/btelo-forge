// Native macOS printing: NSPrintPanel for choosing a printer/settings and CorePrinting (PrintCore/CUPS)
// for submitting vector PDFs natively without blocking the UI thread.
package printing

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices
#include <stdlib.h>
#include "print_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"
)

// Target is a macOS printer and its saved PrintCore settings.
type Target struct {
	Name       string
	Settings   []byte // XML PMPrintSettings representation; empty uses printer defaults
	PageFormat []byte // XML PMPageFormat representation; empty uses printer defaults
	Copies     int
	Collate    bool
	Output     string // when set, writes vector PDF to this file instead of sending to the printer
}

// Available says this system has a print system this package drives.
const Available = true

// Printers lists the printers configured on this Mac.
func Printers() []string {
	p := C.forge_printers()
	if p == nil {
		return nil
	}
	defer C.forge_print_free(unsafe.Pointer(p))
	s := C.GoString(p)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// PrinterTarget returns a Target for an existing macOS printer by name, using its defaults.
func PrinterTarget(name string) (*Target, error) {
	if name == "" {
		return nil, errors.New("the printer name is empty")
	}
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	if C.forge_printer_exists(cn) == 0 {
		return nil, fmt.Errorf("the printer %s cannot be found", name)
	}
	return &Target{Name: name, Copies: 1}, nil
}

// ChoosePrinter shows the native macOS NSPrintPanel initialized from last, returning the chosen target or nil, nil on cancel.
// Must be called on the main Cocoa thread.
func ChoosePrinter(_ uintptr, last *Target) (*Target, error) {
	var cName *C.char
	var lastCopies, lastCollate C.int
	var cSettings, cFormat unsafe.Pointer
	var settingsLen, formatLen C.int

	if last != nil {
		if last.Name != "" {
			cName = C.CString(last.Name)
			defer C.free(unsafe.Pointer(cName))
		}
		lastCopies = C.int(max(last.Copies, 1))
		if last.Collate {
			lastCollate = 1
		}
		if len(last.Settings) > 0 {
			cSettings = unsafe.Pointer(&last.Settings[0])
			settingsLen = C.int(len(last.Settings))
		}
		if len(last.PageFormat) > 0 {
			cFormat = unsafe.Pointer(&last.PageFormat[0])
			formatLen = C.int(len(last.PageFormat))
		}
	}

	res := C.forge_choose_printer(cName, lastCopies, lastCollate, cSettings, settingsLen, cFormat, formatLen)
	defer C.forge_free_print_choice(&res)

	if res.ok < 0 {
		if res.errMsg != nil {
			return nil, errors.New(C.GoString(res.errMsg))
		}
		return nil, errors.New("the print dialog failed")
	}
	if res.ok == 0 {
		return nil, nil
	}
	t := &Target{
		Copies:  max(int(res.copies), 1),
		Collate: res.collate != 0,
	}
	if res.name != nil {
		t.Name = C.GoString(res.name)
	}
	if res.output != nil {
		t.Output = C.GoString(res.output)
	}
	if res.settings != nil && res.settingsLen > 0 {
		t.Settings = C.GoBytes(res.settings, res.settingsLen)
	}
	if res.pageFormat != nil && res.pageFormatLen > 0 {
		t.PageFormat = C.GoBytes(res.pageFormat, res.pageFormatLen)
	}
	if t.Name == "" && t.Output == "" {
		return nil, errors.New("the print dialog named no printer")
	}
	return t, nil
}

// Print sends the document as a vector PDF to t.Output (if set) or to the macOS printer via CorePrinting.
// It is safe to call from any goroutine.
func (t *Target) Print(ctx context.Context, d *Doc, progress func(done, total int)) error {
	if len(d.Pages) == 0 {
		return errors.New("there is nothing to print")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	copies := max(t.Copies, 1)

	// When Output is set (e.g. Save as PDF or tests), write the vector PDF directly without touching hardware.
	if t.Output != "" {
		pages := make([]*Page, 0, len(d.Pages)*copies)
		if t.Collate {
			for range copies {
				pages = append(pages, d.Pages...)
			}
		} else {
			for _, pg := range d.Pages {
				for range copies {
					pages = append(pages, pg)
				}
			}
		}
		outDoc := &Doc{Title: d.Title, Pages: pages}
		tmp := t.Output + ".tmp"
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		err = WritePDF(ctx, f, outDoc, func(done int) {
			if progress != nil {
				progress(done, len(pages))
			}
		})
		cerr := f.Close()
		if err != nil {
			os.Remove(tmp)
			return err
		}
		if cerr != nil {
			os.Remove(tmp)
			return cerr
		}
		return os.Rename(tmp, t.Output)
	}

	// Build a temporary vector PDF and submit it natively to CorePrinting (CUPS handles copies/collation).
	total := len(d.Pages)
	f, err := os.CreateTemp("", "btelo-forge-print-*.pdf")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)

	err = WritePDF(ctx, f, d, func(done int) {
		if progress != nil {
			progress(done, total)
		}
	})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	cPrinter := C.CString(t.Name)
	defer C.free(unsafe.Pointer(cPrinter))
	cTitle := C.CString(d.Title)
	defer C.free(unsafe.Pointer(cTitle))
	cPath := C.CString(tmpPath)
	defer C.free(unsafe.Pointer(cPath))

	var cSettings, cFormat unsafe.Pointer
	var settingsLen, formatLen C.int
	if len(t.Settings) > 0 {
		cSettings = unsafe.Pointer(&t.Settings[0])
		settingsLen = C.int(len(t.Settings))
	}
	if len(t.PageFormat) > 0 {
		cFormat = unsafe.Pointer(&t.PageFormat[0])
		formatLen = C.int(len(t.PageFormat))
	}
	collate := C.int(0)
	if t.Collate {
		collate = 1
	}

	errPtr := C.forge_print_pdf(cPrinter, cTitle, cPath, C.int(copies), collate, cSettings, settingsLen, cFormat, formatLen)
	if errPtr != nil {
		defer C.forge_print_free(unsafe.Pointer(errPtr))
		return fmt.Errorf("the printer %s failed: %s", t.Name, C.GoString(errPtr))
	}
	if progress != nil {
		progress(total, total)
	}
	return nil
}
