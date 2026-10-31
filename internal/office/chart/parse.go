// Package chart reads the DrawingML charts that Word, Excel and PowerPoint keep in parts of their own, and draws them
// on a gfx canvas, so that a screen and a recording canvas get the same chart, the second as vector shapes. A chart is
// only ever read: the part stays in the file as it was.
package chart

import (
	"errors"
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/dml"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// ErrNotChart says that a part is not a chart this package can draw, such as the newer waterfall and funnel charts.
var ErrNotChart = errors.New("not a chart that can be drawn")

const (
	maxPoints = 1 << 18 // a series
	maxSeries = 1 << 10
)

// Rel is the relationship type of a chart part.
const Rel = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart"

// Kind is how a plot shows its series.
type Kind uint8

const (
	Bar Kind = iota
	Line
	Area
	Pie
	Doughnut
	Scatter
	Bubble
	Radar
)

// Fill is a shape's inside. Gradients are taken at their first stop.
type Fill struct {
	Color color.RGBA
	None  bool
	Set   bool
}

// Stroke is a line.
type Stroke struct {
	Color color.RGBA
	Width float32 // points; 0 leaves it to the kind of line
	Dash  []float32
	None  bool
	Set   bool
}

// Text is how words look.
type Text struct {
	Family       string
	Size         float32 // points
	Bold, Italic bool
	Color        color.RGBA
	HasColor     bool
	boldSet      bool
	Rotation     float64 // degrees clockwise
	HasRotation  bool
}

// Marker is what marks a point on a line.
type Marker struct {
	Symbol string // "" leaves it to the plot
	Size   float32
	Fill   Fill
	Line   Stroke
}

// Labels says what is written at the points.
type Labels struct {
	Value, Category, Series, Percent bool
	Position                         string
	Format                           string
	Text                             Text
	Separator                        string
}

// any reports that a label has something to say.
func (l *Labels) any() bool { return l != nil && (l.Value || l.Category || l.Series || l.Percent) }

// Style is a point's own look.
type Style struct {
	Fill      Fill
	Line      Stroke
	Marker    *Marker
	Explosion float64
}

// Series is one row of values.
type Series struct {
	Index, Order int
	Name         string
	Cats         []string
	CatNums      []float64 // the categories' numbers on a date axis, or nil
	Vals         []float64 // NaN for a blank
	Xs           []float64 // a scatter's
	Sizes        []float64 // a bubble's
	Format       string
	Fill         Fill
	Line         Stroke
	Marker       Marker
	Points       map[int]*Style
	Labels       *Labels
	Hidden       map[int]bool // labels deleted one by one
	Smooth       bool
	Explosion    float64
}

// Plot is the series drawn one way.
type Plot struct {
	Kind       Kind
	Grouping   string // "clustered", "stacked", "percentStacked", "standard"
	Horizontal bool
	GapWidth   float64
	Overlap    float64
	Vary       bool
	Markers    bool
	Filled     bool // a filled radar
	Stock      bool
	Hole       float64 // a doughnut's, a share of its radius
	FirstAngle float64 // degrees
	BubbleSize float64
	Series     []*Series
	Labels     *Labels
	Axes       [2]int64 // the ids in the file: category or X, value or Y
}

// Axis is one axis of a plot.
type Axis struct {
	ID, Cross  int64
	Role       string // "cat", "val", "date", "ser"
	Hidden     bool
	Min, Max   *float64
	Major      float64
	Log        float64
	Reverse    bool
	Format     string
	Linked     bool
	Title      *Title
	Grid       *Stroke
	MinorGrid  *Stroke
	Text       Text
	Line       Stroke
	Labels     string // "nextTo", "low", "high", "none"
	Crosses    string // "autoZero", "min", "max"
	CrossesAt  *float64
	Between    bool // the crossing axis puts values between tick marks
	MajorTicks string
	Skip       int
	Divide     float64 // display units
}

// Layout is a place set by hand, in shares of the chart's size.
type Layout struct {
	X, Y, W, H float64
	Inner      bool
	Set, Sized bool
}

// Title is a chart's or an axis's title.
type Title struct {
	Words   string
	Text    Text
	Overlay bool
	Layout  Layout
}

// Legend says which series is which.
type Legend struct {
	Pos     string
	Overlay bool
	Text    Text
	Layout  Layout
	Fill    Fill
	Line    Stroke
	Hidden  map[int]bool
}

// Chart is a chart as read.
type Chart struct {
	Title      *Title
	Legend     *Legend
	Plots      []*Plot
	Axes       map[int64]*Axis
	Base       Text
	Fill       Fill
	Line       Stroke
	PlotFill   Fill
	PlotLine   Stroke
	PlotLayout Layout
	Rounded    bool
	Blanks     string // "gap", "zero", "span"
	Plain      bool
	Date1904   bool
	colors     func(string) (color.RGBA, bool)
}

// Point is a cell's value, as a Source's Range gives it.
type Point struct {
	Text   string
	Num    float64
	Number bool
	Blank  bool
}

// Source is what a chart is read against.
type Source struct {
	// Colors says what a scheme colour's name stands for; nil is Office's theme.
	Colors func(string) (color.RGBA, bool)
	// Plain leaves the chart without a ground or frame of its own when the file does not ask for one, as on a slide.
	Plain bool
	// Range reads a reference from the workbook the chart sits in, so that the chart follows its cells; nil keeps to
	// the copy of the values in the chart's part. hidden says that hidden rows and columns count; ok is false for a
	// reference it cannot read.
	Range    func(ref string, hidden bool) (cells []Point, ok bool)
	Date1904 bool
}

// Parse reads a chart part.
func Parse(data []byte, src Source) (*Chart, error) {
	doc, err := ox.Parse(data)
	if err != nil {
		return nil, err
	}
	root := doc.Root
	if root == nil || root.Name != "c:chartSpace" {
		return nil, ErrNotChart
	}
	if src.Colors == nil {
		src.Colors = dml.DefaultScheme().Lookup
	}
	r := &reader{src: src}
	ch := &Chart{Axes: map[int64]*Axis{}, Plain: src.Plain, Date1904: src.Date1904 || val(root.Child("c:date1904")) == "1", colors: src.Colors}
	r.ch = ch
	ch.Base = Text{Size: 10, Color: color.RGBA{89, 89, 89, 255}, HasColor: true}
	r.txPr(root.Child("c:txPr"), &ch.Base)
	ch.Fill, ch.Line = r.shape(root.Child("c:spPr"))
	if n := root.Child("c:roundedCorners"); n != nil {
		ch.Rounded = onVal(n, true)
	}
	c := root.Child("c:chart")
	if c == nil {
		return nil, ErrNotChart
	}
	ch.Blanks = valOr(c.Child("c:dispBlanksAs"), "gap")
	r.hidden = !onVal(c.Child("c:plotVisOnly"), true)
	area := c.Child("c:plotArea")
	if area == nil {
		return nil, ErrNotChart
	}
	ch.PlotFill, ch.PlotLine = r.shape(area.Child("c:spPr"))
	ch.PlotLayout = readLayout(area.Child("c:layout"))
	ser := 0
	for _, n := range area.Elements() {
		if p := r.plot(n); p != nil {
			if ser += len(p.Series); ser > maxSeries {
				return nil, ErrNotChart
			}
			ch.Plots = append(ch.Plots, p)
		}
		switch n.Name {
		case "c:catAx", "c:valAx", "c:dateAx", "c:serAx":
			a := r.axis(n)
			ch.Axes[a.ID] = a
		}
	}
	if len(ch.Plots) == 0 {
		return nil, ErrNotChart
	}
	if t := c.Child("c:title"); t != nil {
		ch.Title = r.title(t, &ch.Base, 14)
		if ch.Title.Words == "" {
			if s := ch.onlySeries(); s != nil {
				ch.Title.Words = s.Name
			}
		}
	} else if !onVal(c.Child("c:autoTitleDeleted"), false) {
		if s := ch.onlySeries(); s != nil && s.Name != "" {
			ch.Title = &Title{Words: s.Name, Text: r.titleText(&ch.Base, 14)}
		}
	}
	if l := c.Child("c:legend"); l != nil {
		ch.Legend = r.legend(l)
	}
	return ch, nil
}

func (ch *Chart) onlySeries() *Series {
	if len(ch.Plots) == 1 && len(ch.Plots[0].Series) == 1 {
		return ch.Plots[0].Series[0]
	}
	return nil
}

type reader struct {
	src    Source
	ch     *Chart
	hidden bool // hidden cells are plotted
}

func val(n *ox.Node) string {
	if n == nil {
		return ""
	}
	return n.Attr("val")
}

func valOr(n *ox.Node, or string) string {
	if v := val(n); v != "" {
		return v
	}
	return or
}

// onVal is a boolean element: a missing one is or, and one with no value says yes, as the schema has it.
func onVal(n *ox.Node, or bool) bool {
	if n == nil {
		return or
	}
	v, ok := n.Lookup("val")
	return !ok || v == "1" || v == "true"
}

func floatVal(n *ox.Node, or float64) float64 {
	if f, ok := number(val(n)); ok {
		return f
	}
	return or
}

func number(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

func floatPtr(n *ox.Node) *float64 {
	if f, ok := number(val(n)); ok && n != nil {
		return &f
	}
	return nil
}

func emuPt(s string) float32 {
	f, _ := number(s)
	return float32(f / 12700)
}

func (r *reader) color(parent *ox.Node) (color.RGBA, bool) {
	if parent == nil {
		return color.RGBA{}, false
	}
	for _, e := range parent.Elements() {
		if c, ok := dml.Resolve(e, r.src.Colors); ok {
			return c, true
		}
	}
	return color.RGBA{}, false
}

// fill reads the fill among a shape's or a run's properties.
func (r *reader) fill(sp *ox.Node) Fill {
	if sp == nil {
		return Fill{}
	}
	switch {
	case sp.Child("a:noFill") != nil:
		return Fill{None: true, Set: true}
	case sp.Child("a:solidFill") != nil:
		if c, ok := r.color(sp.Child("a:solidFill")); ok {
			return Fill{Color: c, Set: true}
		}
	case sp.Child("a:gradFill") != nil:
		if c, ok := r.color(sp.Child("a:gradFill").Path("a:gsLst", "a:gs")); ok {
			return Fill{Color: c, Set: true}
		}
	case sp.Child("a:pattFill") != nil:
		if c, ok := r.color(sp.Child("a:pattFill").Child("a:fgClr")); ok {
			return Fill{Color: c, Set: true}
		}
	}
	return Fill{}
}

var dashes = map[string][]float32{"dash": {4, 3}, "sysDash": {3, 1}, "sysDot": {1, 1}, "dot": {1, 3}, "lgDash": {8, 3},
	"dashDot": {4, 3, 1, 3}, "lgDashDot": {8, 3, 1, 3}, "lgDashDotDot": {8, 3, 1, 3, 1, 3}, "sysDashDot": {3, 1, 1, 1}, "sysDashDotDot": {3, 1, 1, 1, 1, 1}}

func (r *reader) stroke(ln *ox.Node) Stroke {
	if ln == nil {
		return Stroke{}
	}
	s := Stroke{Width: emuPt(ln.Attr("w"))}
	f := r.fill(ln)
	s.Color, s.None, s.Set = f.Color, f.None, f.Set
	if d := ln.Child("a:prstDash"); d != nil {
		s.Dash = dashes[d.Attr("val")]
	}
	return s
}

func (r *reader) shape(sp *ox.Node) (Fill, Stroke) {
	if sp == nil {
		return Fill{}, Stroke{}
	}
	return r.fill(sp), r.stroke(sp.Child("a:ln"))
}

// run takes what character properties say.
func (r *reader) run(rp *ox.Node, t *Text) {
	if rp == nil {
		return
	}
	if v, ok := rp.Lookup("sz"); ok {
		if f, ok := number(v); ok && f > 0 {
			t.Size = min(float32(f/100), 400)
		}
	}
	if v, ok := rp.Lookup("b"); ok {
		t.Bold, t.boldSet = v == "1" || v == "true", true
	}
	if v, ok := rp.Lookup("i"); ok {
		t.Italic = v == "1" || v == "true"
	}
	if f := r.fill(rp); f.Set && !f.None {
		t.Color, t.HasColor = f.Color, true
	}
	for _, name := range []string{"a:latin", "a:ea"} {
		if v := rp.Child(name).Attr("typeface"); v != "" && !strings.HasPrefix(v, "+") {
			t.Family = v
			break
		}
	}
}

func (r *reader) body(b *ox.Node, t *Text) {
	if b == nil {
		return
	}
	if v, ok := b.Lookup("rot"); ok {
		if f, ok := number(v); ok && f != -1000 && math.Abs(f) <= 5400000 {
			t.Rotation, t.HasRotation = f/60000, true
		}
	}
	switch b.Attr("vert") {
	case "vert":
		t.Rotation, t.HasRotation = 90, true
	case "vert270":
		t.Rotation, t.HasRotation = -90, true
	}
}

// txPr takes the text properties of an element.
func (r *reader) txPr(n *ox.Node, t *Text) {
	if n == nil {
		return
	}
	r.body(n.Child("a:bodyPr"), t)
	for _, p := range n.Elements("a:p") {
		r.run(p.Path("a:pPr", "a:defRPr"), t)
		r.run(p.Child("a:endParaRPr"), t)
	}
}

func (r *reader) titleText(base *Text, size float32) Text {
	t := *base
	t.Size = size * base.Size / 10
	if t.Size <= 0 {
		t.Size = size
	}
	return t
}

func (r *reader) title(n *ox.Node, base *Text, size float32) *Title {
	t := &Title{Text: r.titleText(base, size), Overlay: onVal(n.Child("c:overlay"), false), Layout: readLayout(n.Child("c:layout"))}
	r.txPr(n.Child("c:txPr"), &t.Text)
	tx := n.Child("c:tx")
	if rich := tx.Child("c:rich"); rich != nil {
		r.body(rich.Child("a:bodyPr"), &t.Text)
		var lines []string
		for i, p := range rich.Elements("a:p") {
			r.run(p.Path("a:pPr", "a:defRPr"), &t.Text)
			var b strings.Builder
			for _, run := range p.Elements("a:r", "a:fld") {
				if i == 0 && b.Len() == 0 {
					r.run(run.Child("a:rPr"), &t.Text)
				}
				b.WriteString(run.Child("a:t").InnerText())
			}
			lines = append(lines, b.String())
		}
		t.Words = strings.Join(lines, "\n")
	} else if ref := tx.Child("c:strRef"); ref != nil {
		if cells := r.strings(ref); len(cells) > 0 {
			t.Words = strings.Join(cells, " ")
		}
	}
	return t
}

func readLayout(n *ox.Node) Layout {
	m := n.Child("c:manualLayout")
	if m == nil {
		return Layout{}
	}
	var l Layout
	x, okX := number(val(m.Child("c:x")))
	y, okY := number(val(m.Child("c:y")))
	// Only what is measured from the edge is a place of its own; a shift from where it would be is ignored.
	if okX && okY && valOr(m.Child("c:xMode"), "factor") == "edge" && valOr(m.Child("c:yMode"), "factor") == "edge" {
		l.X, l.Y, l.Set = x, y, true
	}
	if w, ok := number(val(m.Child("c:w"))); ok {
		if h, ok := number(val(m.Child("c:h"))); ok && w > 0 && h > 0 {
			l.W, l.H, l.Sized = w, h, true
		}
	}
	l.Inner = val(m.Child("c:layoutTarget")) == "inner"
	return l
}

func (r *reader) legend(n *ox.Node) *Legend {
	l := &Legend{Pos: valOr(n.Child("c:legendPos"), "r"), Overlay: onVal(n.Child("c:overlay"), false), Text: r.ch.Base, Layout: readLayout(n.Child("c:layout")), Hidden: map[int]bool{}}
	l.Fill, l.Line = r.shape(n.Child("c:spPr"))
	r.txPr(n.Child("c:txPr"), &l.Text)
	for _, e := range n.Elements("c:legendEntry") {
		if onVal(e.Child("c:delete"), false) {
			if i, err := strconv.Atoi(val(e.Child("c:idx"))); err == nil {
				l.Hidden[i] = true
			}
		}
	}
	return l
}

func (r *reader) plot(n *ox.Node) *Plot {
	p := &Plot{Grouping: valOr(n.Child("c:grouping"), "standard"), GapWidth: floatVal(n.Child("c:gapWidth"), 150), Overlap: floatVal(n.Child("c:overlap"), 0),
		Vary: onVal(n.Child("c:varyColors"), false), BubbleSize: 100}
	switch n.Name {
	case "c:barChart", "c:bar3DChart":
		p.Kind, p.Horizontal = Bar, val(n.Child("c:barDir")) == "bar"
		if p.Grouping == "standard" {
			p.Grouping = valOr(n.Child("c:grouping"), "clustered")
		}
		if n.Child("c:overlap") == nil && strings.HasSuffix(p.Grouping, "tacked") {
			p.Overlap = 100
		}
	case "c:lineChart", "c:line3DChart":
		p.Kind, p.Markers = Line, onVal(n.Child("c:marker"), false)
	case "c:stockChart":
		p.Kind, p.Stock, p.Markers = Line, true, true
	case "c:areaChart", "c:area3DChart":
		p.Kind = Area
	case "c:pieChart", "c:pie3DChart", "c:ofPieChart":
		p.Kind, p.Vary = Pie, onVal(n.Child("c:varyColors"), true)
		p.FirstAngle = floatVal(n.Child("c:firstSliceAng"), 0)
	case "c:doughnutChart":
		p.Kind, p.Vary = Doughnut, onVal(n.Child("c:varyColors"), true)
		p.FirstAngle = floatVal(n.Child("c:firstSliceAng"), 0)
		p.Hole = min(max(floatVal(n.Child("c:holeSize"), 50), 1), 90) / 100
	case "c:scatterChart":
		p.Kind, p.Markers = Scatter, true
		p.Vary = false
	case "c:bubbleChart":
		p.Kind = Bubble
		p.BubbleSize = min(max(floatVal(n.Child("c:bubbleScale"), 100), 1), 300)
	case "c:radarChart":
		p.Kind = Radar
		style := val(n.Child("c:radarStyle"))
		p.Filled, p.Markers = style == "filled", style == "marker"
	default:
		return nil
	}
	p.Labels = r.labels(n.Child("c:dLbls"), nil)
	for i, a := range n.Elements("c:axId") {
		if i < 2 {
			// Ids can be negative: python-pptx writes them so.
			if v, err := strconv.ParseInt(val(a), 10, 64); err == nil {
				p.Axes[i] = v
			}
		}
	}
	for i, s := range n.Elements("c:ser") {
		if i >= maxSeries {
			break
		}
		p.Series = append(p.Series, r.series(s, i, p))
	}
	return p
}

func (r *reader) labels(n *ox.Node, inherit *Labels) *Labels {
	if n == nil {
		return inherit
	}
	if onVal(n.Child("c:delete"), false) {
		return nil
	}
	l := &Labels{Text: r.ch.Base, Separator: ", "}
	if inherit != nil {
		*l = *inherit
	}
	flag := func(name string, f *bool) {
		if e := n.Child(name); e != nil {
			*f = onVal(e, true)
		}
	}
	flag("c:showVal", &l.Value)
	flag("c:showCatName", &l.Category)
	flag("c:showSerName", &l.Series)
	flag("c:showPercent", &l.Percent)
	if v := val(n.Child("c:dLblPos")); v != "" {
		l.Position = v
	}
	if f := n.Child("c:numFmt"); f != nil {
		l.Format = f.Attr("formatCode")
	}
	if s := n.Child("c:separator"); s != nil {
		l.Separator = s.InnerText()
	}
	r.txPr(n.Child("c:txPr"), &l.Text)
	return l
}

func (r *reader) series(n *ox.Node, at int, p *Plot) *Series {
	s := &Series{Index: at, Order: at, Points: map[int]*Style{}, Hidden: map[int]bool{}}
	if i, err := strconv.Atoi(val(n.Child("c:idx"))); err == nil {
		s.Index = i
	}
	if i, err := strconv.Atoi(val(n.Child("c:order"))); err == nil {
		s.Order = i
	}
	if tx := n.Child("c:tx"); tx != nil {
		if ref := tx.Child("c:strRef"); ref != nil {
			s.Name = strings.Join(r.strings(ref), " ")
		} else {
			s.Name = tx.Child("c:v").InnerText()
		}
	}
	s.Fill, s.Line = r.shape(n.Child("c:spPr"))
	s.Marker = r.marker(n.Child("c:marker"))
	s.Smooth = onVal(n.Child("c:smooth"), false)
	s.Explosion = floatVal(n.Child("c:explosion"), 0)
	for _, d := range n.Elements("c:dPt") {
		i, err := strconv.Atoi(val(d.Child("c:idx")))
		if err != nil || i < 0 {
			continue
		}
		st := &Style{Explosion: -1}
		st.Fill, st.Line = r.shape(d.Child("c:spPr"))
		if m := d.Child("c:marker"); m != nil {
			k := r.marker(m)
			st.Marker = &k
		}
		if e := d.Child("c:explosion"); e != nil {
			st.Explosion = floatVal(e, 0)
		}
		s.Points[i] = st
	}
	s.Labels = r.labels(n.Child("c:dLbls"), p.Labels)
	for _, d := range n.Child("c:dLbls").Elements("c:dLbl") {
		if onVal(d.Child("c:delete"), false) {
			if i, err := strconv.Atoi(val(d.Child("c:idx"))); err == nil {
				s.Hidden[i] = true
			}
		}
	}
	switch p.Kind {
	case Scatter, Bubble:
		s.Xs, _ = r.numbers(n.Child("c:xVal"))
		s.Vals, s.Format = r.numbers(n.Child("c:yVal"))
		if p.Kind == Bubble {
			s.Sizes, _ = r.numbers(n.Child("c:bubbleSize"))
		}
		// Words for X, or none, count the points from one.
		if len(s.Xs) != len(s.Vals) || allBlank(s.Xs) {
			s.Xs = make([]float64, len(s.Vals))
			for i := range s.Xs {
				s.Xs[i] = float64(i + 1)
			}
		}
	default:
		cat := n.Child("c:cat")
		s.Cats = r.strings(cat)
		if ref := cat.Child("c:numRef"); ref != nil {
			s.CatNums, _ = r.numbers(cat)
			if code := ref.Path("c:numCache", "c:formatCode").InnerText(); code != "" {
				s.Cats = r.formatted(s.CatNums, code)
			}
		}
		s.Vals, s.Format = r.numbers(n.Child("c:val"))
	}
	return s
}

func allBlank(v []float64) bool {
	for _, x := range v {
		if !math.IsNaN(x) {
			return false
		}
	}
	return true
}

func (r *reader) formatted(v []float64, code string) []string {
	out := make([]string, len(v))
	f := formats{date1904: r.ch.Date1904}
	for i, x := range v {
		if !math.IsNaN(x) {
			out[i] = f.number(code, x)
		}
	}
	return out
}

func (r *reader) marker(n *ox.Node) Marker {
	var m Marker
	if n == nil {
		return m
	}
	m.Symbol = val(n.Child("c:symbol"))
	m.Size = min(max(float32(floatVal(n.Child("c:size"), 5)), 2), 72)
	m.Fill, m.Line = r.shape(n.Child("c:spPr"))
	return m
}

// cache reads the pts of a numCache, strCache, numLit, strLit or a level.
func cachePoints(c *ox.Node) []Point {
	if c == nil {
		return nil
	}
	n, _ := strconv.Atoi(val(c.Child("c:ptCount")))
	pts := c.Elements("c:pt")
	n = min(max(n, len(pts)), maxPoints)
	out := make([]Point, n)
	for i := range out {
		out[i].Blank = true
	}
	for k, p := range pts {
		i := k
		if v, ok := p.Lookup("idx"); ok {
			j, err := strconv.Atoi(v)
			if err != nil {
				continue
			}
			i = j
		}
		if i < 0 || i >= n {
			continue
		}
		t := p.Child("c:v").InnerText()
		f, ok := number(t)
		out[i] = Point{Text: t, Num: f, Number: ok}
	}
	return out
}

// points reads a reference or a literal: from the workbook where there is one, else the part's own copy.
func (r *reader) points(n *ox.Node) ([]Point, string) {
	for _, name := range []string{"c:numRef", "c:strRef", "c:multiLvlStrRef"} {
		ref := n
		if n != nil && n.Name != name {
			ref = n.Child(name)
		}
		if ref == nil || ref.Name != name {
			continue
		}
		code := ""
		cache := ref.Child("c:numCache")
		if cache != nil {
			code = cache.Child("c:formatCode").InnerText()
		} else if cache = ref.Child("c:strCache"); cache == nil {
			// The nearest level is the labels next to the points.
			cache = ref.Path("c:multiLvlStrCache", "c:lvl")
		}
		if r.src.Range != nil && name != "c:multiLvlStrRef" {
			if f := ref.Child("c:f").InnerText(); f != "" {
				if cells, ok := r.src.Range(f, r.hidden); ok {
					return cells[:min(len(cells), maxPoints)], code
				}
			}
		}
		return cachePoints(cache), code
	}
	if n == nil {
		return nil, ""
	}
	for _, name := range []string{"c:numLit", "c:strLit"} {
		if lit := n.Child(name); lit != nil {
			return cachePoints(lit), lit.Child("c:formatCode").InnerText()
		}
	}
	return nil, ""
}

func (r *reader) numbers(n *ox.Node) ([]float64, string) {
	pts, code := r.points(n)
	out := make([]float64, len(pts))
	for i, p := range pts {
		if p.Number && !p.Blank {
			out[i] = p.Num
		} else {
			out[i] = math.NaN()
		}
	}
	return out, code
}

func (r *reader) strings(n *ox.Node) []string {
	pts, _ := r.points(n)
	out := make([]string, len(pts))
	for i, p := range pts {
		out[i] = p.Text
	}
	return out
}

func (r *reader) axis(n *ox.Node) *Axis {
	a := &Axis{Role: strings.TrimSuffix(strings.TrimPrefix(n.Name, "c:"), "Ax"), Hidden: onVal(n.Child("c:delete"), false),
		Labels: valOr(n.Child("c:tickLblPos"), "nextTo"), MajorTicks: valOr(n.Child("c:majorTickMark"), "out"), Text: r.ch.Base, Divide: 1}
	a.ID, _ = strconv.ParseInt(val(n.Child("c:axId")), 10, 64)
	a.Cross, _ = strconv.ParseInt(val(n.Child("c:crossAx")), 10, 64)
	sc := n.Child("c:scaling")
	a.Min, a.Max = floatPtr(sc.Child("c:min")), floatPtr(sc.Child("c:max"))
	a.Reverse = val(sc.Child("c:orientation")) == "maxMin"
	if b := floatVal(sc.Child("c:logBase"), 0); b >= 2 && b <= 1000 {
		a.Log = b
	}
	if f := n.Child("c:numFmt"); f != nil {
		a.Format = f.Attr("formatCode")
		a.Linked = f.Attr("sourceLinked") == "1"
	} else {
		a.Linked = true
	}
	if t := n.Child("c:title"); t != nil {
		a.Title = r.title(t, &r.ch.Base, 10)
		if !a.Title.Text.boldSet {
			a.Title.Text.Bold = false
		}
	}
	if g := n.Child("c:majorGridlines"); g != nil {
		_, s := r.shape(g.Child("c:spPr"))
		a.Grid = &s
	}
	if g := n.Child("c:minorGridlines"); g != nil {
		_, s := r.shape(g.Child("c:spPr"))
		a.MinorGrid = &s
	}
	_, a.Line = r.shape(n.Child("c:spPr"))
	r.txPr(n.Child("c:txPr"), &a.Text)
	a.Major = max(floatVal(n.Child("c:majorUnit"), 0), 0)
	a.Crosses = valOr(n.Child("c:crosses"), "autoZero")
	a.CrossesAt = floatPtr(n.Child("c:crossesAt"))
	a.Between = valOr(n.Child("c:crossBetween"), "between") == "between"
	a.Skip, _ = strconv.Atoi(val(n.Child("c:tickLblSkip")))
	if d := n.Child("c:dispUnits"); d != nil {
		if v := floatVal(d.Child("c:custUnit"), 0); v > 0 {
			a.Divide = v
		} else if v, ok := builtInUnits[val(d.Child("c:builtInUnit"))]; ok {
			a.Divide = v
		}
	}
	return a
}

var builtInUnits = map[string]float64{"hundreds": 1e2, "thousands": 1e3, "tenThousands": 1e4, "hundredThousands": 1e5, "millions": 1e6,
	"tenMillions": 1e7, "hundredMillions": 1e8, "billions": 1e9, "trillions": 1e12}
