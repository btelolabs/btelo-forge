package xlsx

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
)

const chartPart = xmlDecl + `<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><c:chart><c:plotArea><c:barChart><c:barDir val="col"/><c:ser><c:idx val="0"/><c:order val="0"/><c:cat><c:strRef><c:f>'My Sheet'!$A$1:$A$3</c:f><c:strCache><c:ptCount val="1"/><c:pt idx="0"><c:v>old</c:v></c:pt></c:strCache></c:strRef></c:cat><c:val><c:numRef><c:f>'My Sheet'!$B$1:$B$3</c:f><c:numCache><c:ptCount val="1"/><c:pt idx="0"><c:v>1</c:v></c:pt></c:numCache></c:numRef></c:val></c:ser><c:axId val="1"/><c:axId val="2"/></c:barChart><c:catAx><c:axId val="1"/><c:crossAx val="2"/></c:catAx><c:valAx><c:axId val="2"/><c:crossAx val="1"/></c:valAx></c:plotArea><c:plotVisOnly val="1"/></c:chart></c:chartSpace>`

const drawingPart = xmlDecl + `<xdr:wsDr xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
	`<xdr:twoCellAnchor><xdr:from><xdr:col>3</xdr:col><xdr:colOff>9525</xdr:colOff><xdr:row>4</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:from><xdr:to><xdr:col>8</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>20</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:to><xdr:graphicFrame macro=""><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart"><c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" r:id="rId1"/></a:graphicData></a:graphic></xdr:graphicFrame><xdr:clientData/></xdr:twoCellAnchor>` +
	`<xdr:oneCellAnchor><xdr:from><xdr:col>0</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>30</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:from><xdr:ext cx="952500" cy="476250"/><xdr:pic><xdr:blipFill><a:blip r:embed="rId2"/><a:srcRect l="50000"/></xdr:blipFill></xdr:pic><xdr:clientData/></xdr:oneCellAnchor>` +
	`</xdr:wsDr>`

// twoPixels is a PNG two pixels wide.
func twoPixels(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func workbookWithDrawing(t *testing.T) *Workbook {
	t.Helper()
	blank, err := Blank("My Sheet")
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(blank)
	if err != nil {
		t.Fatal(err)
	}
	p.Put("xl/drawings/drawing1.xml", "application/vnd.openxmlformats-officedocument.drawing+xml", []byte(drawingPart))
	p.Put("xl/charts/chart1.xml", "application/vnd.openxmlformats-officedocument.drawingml.chart+xml", []byte(chartPart))
	p.SetDefault("png", "image/png")
	p.Put("xl/media/image1.png", "", twoPixels(t))
	p.AddRel("xl/worksheets/sheet1.xml", relDrawing, "../drawings/drawing1.xml", false)
	p.AddRel("xl/drawings/drawing1.xml", relChart, "../charts/chart1.xml", false)
	p.AddRel("xl/drawings/drawing1.xml", opc.RelImage, "../media/image1.png", false)
	data, err := p.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	wb, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	return wb
}

func TestDrawingsAreReadFromTheirPart(t *testing.T) {
	wb := workbookWithDrawing(t)
	s := wb.Sheets[0]
	list := s.Drawings()
	if len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	c, p := list[0], list[1]
	if c.Chart != "xl/charts/chart1.xml" || c.From != (Marker{Row: 4, Col: 3, DX: 1}) || c.To.Row != 20 || c.Sized {
		t.Fatalf("%+v", c)
	}
	if p.Picture != "xl/media/image1.png" || !p.Sized || p.W != 100 || p.H != 50 || p.Crop[0] != 0.5 {
		t.Fatalf("%+v", p)
	}
	if img := wb.Picture(&p); img == nil || img.Rect.Dx() != 1 {
		t.Fatalf("%v", img)
	}
	a, ok := s.DrawingArea()
	if !ok || a.R1 != 4 || a.C1 != 0 || a.R2 < 30 || a.C2 != 8 {
		t.Fatalf("%+v", a)
	}
}

func TestAChartFollowsTheSheet(t *testing.T) {
	wb := workbookWithDrawing(t)
	s := wb.Sheets[0]
	for r, row := range []struct {
		name string
		v    float64
	}{{"a", 5}, {"b", 6}, {"c", 7}} {
		if err := s.SetValue(r, 0, formula.Str(row.name)); err != nil {
			t.Fatal(err)
		}
		if err := s.SetValue(r, 1, formula.Num(row.v)); err != nil {
			t.Fatal(err)
		}
	}
	wb.Calc(0)
	ch := wb.Chart("xl/charts/chart1.xml")
	if ch == nil {
		t.Fatal("no chart")
	}
	se := ch.Plots[0].Series[0]
	if len(se.Vals) != 3 || se.Vals[2] != 7 || se.Cats[1] != "b" {
		t.Fatalf("%+v", se)
	}
	if wb.Chart("xl/charts/chart1.xml") != ch {
		t.Fatal("read again with nothing changed")
	}
	wb.Begin()
	s.SetRowsHidden(1, 1, true)
	se = wb.Chart("xl/charts/chart1.xml").Plots[0].Series[0]
	if len(se.Vals) != 2 || se.Vals[1] != 7 {
		t.Fatalf("a hidden row is plotted: %+v", se)
	}
}

func TestAChartPartThatIsNoneShowsNothing(t *testing.T) {
	wb := workbookWithDrawing(t)
	if wb.Chart("xl/charts/none.xml") != nil {
		t.Fatal("a missing part")
	}
}

func TestReferencesSplitOutsideQuotes(t *testing.T) {
	got := splitRefs("'a,b'!$A$1,c!$B$2")
	if len(got) != 2 || got[0] != "'a,b'!$A$1" {
		t.Fatal(got)
	}
}
