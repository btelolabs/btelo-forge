package docx

import (
	"testing"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

const testChart = `<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart"><c:chart><c:plotArea><c:barChart><c:barDir val="col"/><c:ser><c:val><c:numLit><c:ptCount val="1"/><c:pt idx="0"><c:v>1</c:v></c:pt></c:numLit></c:val></c:ser></c:barChart></c:plotArea></c:chart></c:chartSpace>`

func TestAChartIsFoundThroughItsRelationship(t *testing.T) {
	doc, err := ox.Parse([]byte(`<wp:inline xmlns:wp="w" xmlns:r="r"><c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" r:id="rId9"/></wp:inline>`))
	if err != nil {
		t.Fatal(err)
	}
	part := &Part{Name: "word/document.xml", Rels: []opc.Rel{{ID: "rId9", Target: "charts/chart1.xml"}}}
	if got := graphicKey(doc.Root, part); got != chartKey+"word/charts/chart1.xml" {
		t.Fatalf("%q", got)
	}
	part.Rels = nil
	if got := graphicKey(doc.Root, part); got != "?chart" {
		t.Fatalf("%q", got)
	}
}

func TestAChartPartIsReadOnce(t *testing.T) {
	d := blankDoc(t)
	d.Pkg.Put("word/charts/chart1.xml", "application/vnd.openxmlformats-officedocument.drawingml.chart+xml", []byte(testChart))
	d.Pkg.Put("word/charts/chart2.xml", "application/vnd.ms-office.chartex+xml", []byte(`<cx:chartSpace xmlns:cx="x"/>`))
	e := d.chartIn(chartKey + "word/charts/chart1.xml")
	if e == nil || e.ch == nil {
		t.Fatal("no chart")
	}
	if d.chartIn(chartKey+"word/charts/chart1.xml") != e {
		t.Fatal("read twice")
	}
	if d.chartIn(chartKey+"word/charts/chart2.xml") != nil || d.chartIn("?diagram") != nil {
		t.Fatal("not a chart")
	}
}
