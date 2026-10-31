package ui

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/printing"
)

// The tools that make PDFs. There is no tool that prints: paper is the user's to spend, from the window.

// pdfToolIn is what every export tool takes.
type pdfToolIn struct {
	Path string `json:"path,omitempty" jsonschema:"the file; left out: the active tab"`
	To   string `json:"to,omitempty" jsonschema:"where to write the PDF; left out: beside the document, with its name"`
}

// pdfPath is where an export goes.
func (a *App) pdfPath(src, to string) string {
	if to != "" {
		return to
	}
	return a.freePath(filepath.Join(filepath.Dir(src), docName(src)+".pdf"))
}

// exportTool adds a tool that makes a PDF of a document: its pages are drawn on the window's goroutine and written on
// the tool's own.
func exportTool(s *mcp.Server, a *App, name, what string, source func(in pdfToolIn) (printSource, string, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: "Write " + what + " as a PDF with vector shapes and searchable text, and return where it went."},
		func(ctx context.Context, req *mcp.CallToolRequest, in pdfToolIn) (*mcp.CallToolResult, any, error) {
			type job struct {
				doc *printing.Doc
				to  string
			}
			j, err := call(a, func() (job, error) {
				src, path, err := source(in)
				if err != nil {
					return job{}, err
				}
				d, err := allPages(src)
				if err != nil {
					return job{}, err
				}
				return job{d, a.pdfPath(path, in.To)}, nil
			})
			if err != nil {
				return result(nil, err)
			}
			data, err := encodePDF(ctx, j.doc, nil)
			if err != nil {
				return result(nil, err)
			}
			if err := writeFileWhole(j.to, data); err != nil {
				return result(nil, err)
			}
			return result(map[string]any{"path": j.to, "pages": len(j.doc.Pages), "bytes": len(data)}, nil)
		})
}

// addWordExport, and the like, add a format's export tool.
func (a *App) addWordExport(s *mcp.Server) {
	exportTool(s, a, "word_export_pdf", "the document", func(in pdfToolIn) (printSource, string, error) {
		e, err := a.wordTarget(in.Path)
		if err != nil {
			return nil, "", err
		}
		return &wordPrint{e: e, images: printing.Images{}}, e.od.path, nil
	})
}

func (a *App) addSheetExport(s *mcp.Server) {
	exportTool(s, a, "sheet_export_pdf", "every visible sheet of the workbook, by each sheet's own page setup,", func(in pdfToolIn) (printSource, string, error) {
		e, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, "", err
		}
		sp := newSheetPrint(e)
		sp.what = sheetWorkbook
		return sp, e.od.path, nil
	})
}

func (a *App) addSlidesExport(s *mcp.Server) {
	exportTool(s, a, "slides_export_pdf", "the presentation, a page to each visible slide,", func(in pdfToolIn) (printSource, string, error) {
		e, err := a.slidesTarget(in.Path)
		if err != nil {
			return nil, "", err
		}
		e.endText()
		return &slidesPrint{e: e}, e.od.path, nil
	})
}

// addCanvasExport adds the canvas's tool.
func (a *App) addCanvasExport(s *mcp.Server) {
	exportTool(s, a, "canvas_export_pdf", "the canvas, fitted to a page,", func(in pdfToolIn) (printSource, string, error) {
		if in.Path != "" {
			return nil, "", errors.New("canvas_export_pdf acts on the active tab; open the canvas first")
		}
		ad, err := a.canvasNow()
		if err != nil {
			return nil, "", err
		}
		return newCanvasPrint(a, ad), ad.path, nil
	})
}

// addMindExport adds the mind map's tool.
func (a *App) addMindExport(s *mcp.Server) {
	exportTool(s, a, "mind_export_pdf", "the whole board, fitted to a page,", func(in pdfToolIn) (printSource, string, error) {
		md, err := a.mindNow(in.Path)
		if err != nil {
			return nil, "", err
		}
		return newMindPrint(a, md), md.path, nil
	})
}
