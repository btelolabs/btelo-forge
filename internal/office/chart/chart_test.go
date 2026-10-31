package chart

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

const space = `<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">`

func numRef(f string, v ...float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<c:numRef><c:f>%s</c:f><c:numCache><c:formatCode>General</c:formatCode><c:ptCount val="%d"/>`, f, len(v))
	for i, x := range v {
		fmt.Fprintf(&b, `<c:pt idx="%d"><c:v>%v</c:v></c:pt>`, i, x)
	}
	b.WriteString(`</c:numCache></c:numRef>`)
	return b.String()
}

func strRef(v ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<c:strRef><c:f>S!A1</c:f><c:strCache><c:ptCount val="%d"/>`, len(v))
	for i, x := range v {
		fmt.Fprintf(&b, `<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, i, x)
	}
	b.WriteString(`</c:strCache></c:strRef>`)
	return b.String()
}

func column(extra string, vals ...float64) string {
	return space + `<c:chart><c:plotArea><c:barChart><c:barDir val="col"/><c:grouping val="clustered"/>` + extra +
		`<c:ser><c:idx val="0"/><c:order val="0"/><c:tx>` + strRef("Sales") + `</c:tx><c:cat>` + strRef("a", "b") + `</c:cat><c:val>` + numRef("S!B1:B2", vals...) + `</c:val></c:ser>` +
		`<c:axId val="-5"/><c:axId val="7"/></c:barChart>` +
		`<c:catAx><c:axId val="-5"/><c:scaling/><c:delete val="0"/><c:axPos val="b"/><c:crossAx val="7"/></c:catAx>` +
		`<c:valAx><c:axId val="7"/><c:scaling/><c:delete val="0"/><c:axPos val="l"/><c:crossAx val="-5"/></c:valAx>` +
		`</c:plotArea></c:chart></c:chartSpace>`
}

func parse(t *testing.T, xml string, src Source) *Chart {
	t.Helper()
	ch, err := Parse([]byte(xml), src)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestSeriesAreReadFromTheirCache(t *testing.T) {
	ch := parse(t, column("", 3, 4), Source{})
	s := ch.Plots[0].Series[0]
	if s.Name != "Sales" || len(s.Cats) != 2 || s.Cats[1] != "b" || s.Vals[1] != 4 {
		t.Fatalf("%+v", s)
	}
	if ch.Plots[0].Axes != [2]int64{-5, 7} || ch.Axes[-5] == nil || ch.Axes[7].Cross != -5 {
		t.Fatalf("the axes are lost: %v %v", ch.Plots[0].Axes, ch.Axes)
	}
}

func TestAChartFollowsItsCells(t *testing.T) {
	ch := parse(t, column("", 3, 4), Source{Range: func(ref string, hidden bool) ([]Point, bool) {
		if ref == "S!B1:B2" {
			return []Point{{Num: 9, Number: true}, {Blank: true}}, true
		}
		return nil, false
	}})
	v := ch.Plots[0].Series[0].Vals
	if v[0] != 9 || !math.IsNaN(v[1]) {
		t.Fatal(v)
	}
}

func TestOnlyChartsAreCharts(t *testing.T) {
	for _, xml := range []string{`<a/>`, space + `</c:chartSpace>`, space + `<c:chart><c:plotArea/></c:chart></c:chartSpace>`,
		`<cx:chartSpace xmlns:cx="http://schemas.microsoft.com/office/drawing/2014/chartex"/>`} {
		if _, err := Parse([]byte(xml), Source{}); err == nil {
			t.Fatal(xml)
		}
	}
}

func TestTicksAreRound(t *testing.T) {
	v := &axisView{a: &Axis{}}
	v.scale(0, 87, 6, true)
	if v.major != 20 || v.hi != 100 || v.lo != 0 {
		t.Fatalf("%+v", v)
	}
	lo, hi := 3.0, 8.0
	v = &axisView{a: &Axis{Min: &lo, Max: &hi, Major: 2.5}}
	v.scale(0, 87, 6, false)
	if v.lo != 3 || v.hi != 8 || len(v.ticks) != 3 || v.ticks[1] != 5.5 {
		t.Fatalf("%+v", v)
	}
	v = &axisView{a: &Axis{Log: 10}}
	v.scale(5, 900, 6, false)
	if v.lo != 1 || v.hi != 1000 || len(v.ticks) != 4 {
		t.Fatalf("%+v", v)
	}
	v = &axisView{a: &Axis{}, percent: true}
	v.scale(0, 1, 12, true)
	if v.hi != 1 || len(v.ticks) != 11 {
		t.Fatalf("%+v", v)
	}
}

func TestAStackAddsUp(t *testing.T) {
	pl := &Plot{Kind: Bar, Grouping: "stacked", Series: []*Series{{Vals: []float64{2, -1}}, {Vals: []float64{3, -4}}}}
	if lo, hi := extent(pl); lo != -5 || hi != 5 {
		t.Fatal(lo, hi)
	}
	if from, to, ok := stackedValue(pl, 1, 0); !ok || from != 2 || to != 5 {
		t.Fatal(from, to, ok)
	}
	pl.Grouping = "percentStacked"
	if _, to, _ := stackedValue(pl, 1, 0); to != 1 {
		t.Fatal(to)
	}
}

func draw(t *testing.T, ch *Chart) *gfx.Canvas {
	t.Helper()
	c := gfx.NewCanvas(400, 300)
	Draw(c, gfx.Rect{W: 400, H: 300}, 1, ch, testFaces(t))
	return c
}

func testFaces(t *testing.T) Faces {
	f, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skip(err)
	}
	return func(string, bool, bool, float32) *gfx.Face { return f.Regular.Face(12) }
}

func colourIn(c *gfx.Canvas, want color.RGBA) int {
	n := 0
	p := c.Img.Pix
	for i := 0; i+3 < len(p); i += 4 {
		if p[i] == want.R && p[i+1] == want.G && p[i+2] == want.B {
			n++
		}
	}
	return n
}

func TestBarsStandOnTheAxis(t *testing.T) {
	ch := parse(t, column(`<c:gapWidth val="50"/>`, 10, 20), Source{})
	c := draw(t, ch)
	blue := color.RGBA{0x44, 0x72, 0xc4, 255}
	if colourIn(c, blue) < 2000 {
		t.Fatal("the bars are not drawn in the first accent")
	}
}

func TestAHostileChartIsDrawnOrRefusedQuietly(t *testing.T) {
	nasty := []string{
		column(`<c:gapWidth val="-99999"/><c:overlap val="99999"/>`, 1e308, -1e308),
		column("", 0, 0),
		strings.ReplaceAll(column("", 1, 2), `<c:ptCount val="2"/>`, `<c:ptCount val="2000000000"/>`),
		strings.ReplaceAll(column("", 1, 2), `<c:scaling/>`, `<c:scaling><c:logBase val="2"/><c:min val="-5"/><c:max val="-9"/></c:scaling>`),
		strings.ReplaceAll(column("", 1, 2), `<c:crossAx val="7"/>`, `<c:crossAx val="-5"/><c:majorUnit val="0.000000001"/>`),
		strings.ReplaceAll(column("", 1, 2), `<c:axPos val="l"/>`, `<c:majorUnit val="1e-300"/>`),
		strings.ReplaceAll(strings.ReplaceAll(column("", 1, 2), `<c:barChart>`, `<c:pieChart>`), `</c:barChart>`, `</c:pieChart>`),
		strings.ReplaceAll(strings.ReplaceAll(column("", 1, 2), `<c:barChart>`, `<c:radarChart>`), `</c:barChart>`, `</c:radarChart>`),
		strings.ReplaceAll(strings.ReplaceAll(column("", 1, 2), `<c:barChart>`, `<c:doughnutChart><c:holeSize val="1000"/>`), `</c:barChart>`, `</c:doughnutChart>`),
		strings.ReplaceAll(strings.ReplaceAll(column("", 1, 2), `<c:barChart>`, `<c:bubbleChart>`), `</c:barChart>`, `</c:bubbleChart>`),
	}
	for _, xml := range nasty {
		ch, err := Parse([]byte(xml), Source{})
		if err != nil {
			continue
		}
		draw(t, ch)
	}
}

func TestTheCacheKeepsAPicture(t *testing.T) {
	ch := parse(t, column("", 1, 2), Source{})
	var ca Cache
	c := gfx.NewCanvas(400, 300)
	faces := testFaces(t)
	ca.Draw(c, gfx.Rect{W: 200, H: 150}, 1, ch, faces)
	first := ca.img
	ca.Draw(c, gfx.Rect{X: 5, W: 200, H: 150}, 1, ch, faces)
	if first == nil || ca.img != first {
		t.Fatal("drawn again")
	}
	ca.Draw(c, gfx.Rect{W: 300, H: 150}, 1, ch, faces)
	if ca.img == first {
		t.Fatal("a new size needs a new picture")
	}
}
