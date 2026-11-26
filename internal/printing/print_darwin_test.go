package printing

import (
	"bytes"
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestMacPrintingAvailableAndEnumeratesPrinters(t *testing.T) {
	if !Available {
		t.Fatal("expected Available == true on macOS")
	}
	printers := Printers()
	t.Logf("macOS printers: %v", printers)
	for _, name := range printers {
		tg, err := PrinterTarget(name)
		if err != nil {
			t.Fatalf("PrinterTarget(%q): %v", name, err)
		}
		if tg.Name != name || tg.Copies != 1 {
			t.Fatalf("unexpected target: %+v", tg)
		}
	}
	if _, err := PrinterTarget("NoSuchPrinter-btelo-forge-99999"); err == nil {
		t.Fatal("expected error for nonexistent printer")
	}
}

func TestMacPrintToOutputFileNeverTouchesHardware(t *testing.T) {
	pg1 := &Page{W: 595, H: 842}
	pg1.Add(&Fill{Path: RoundRectPath(40, 40, 200, 100, 10), Color: color.NRGBA{30, 120, 220, 255}})
	pg2 := &Page{W: 842, H: 595}
	pg2.Add(&Stroke{Path: EllipsePath(60, 60, 200, 100), Width: 2, Color: color.NRGBA{200, 30, 30, 255}})
	doc := &Doc{Title: "Mac Print Test", Pages: []*Page{pg1, pg2}}

	outPath := filepath.Join(t.TempDir(), "printed.pdf")
	tg := &Target{Name: "TestVirtual", Copies: 2, Collate: true, Output: outPath}

	var lastDone, lastTotal int
	if err := tg.Print(context.Background(), doc, func(done, total int) {
		lastDone, lastTotal = done, total
	}); err != nil {
		t.Fatalf("Print to file: %v", err)
	}
	if lastDone != 4 || lastTotal != 4 {
		t.Fatalf("progress got %d/%d, want 4/4", lastDone, lastTotal)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output PDF: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-1.7")) || !bytes.Contains(data, []byte("/Count 4")) {
		t.Fatalf("invalid PDF output (%d bytes)", len(data))
	}

	// Empty document must error.
	if err := tg.Print(context.Background(), &Doc{Title: "Empty"}, nil); err == nil {
		t.Fatal("expected error for empty doc")
	}

	// Cancelled context must abort without leaving output.
	cancelPath := filepath.Join(t.TempDir(), "cancelled.pdf")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tg2 := &Target{Name: "TestVirtual", Copies: 1, Output: cancelPath}
	if err := tg2.Print(ctx, doc, nil); err == nil {
		t.Fatal("expected cancellation error")
	}
	if _, err := os.Stat(cancelPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled print left file behind: %v", err)
	}
}
