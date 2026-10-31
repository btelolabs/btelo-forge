package docx

import (
	"strings"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/chart"
	"github.com/btelolabs/btelo-forge/internal/office/dml"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Charts in a document: a graphic names a chart part, and the page draws it where a picture would be. The key of such
// a graphic starts with chartKey and goes on with the part's name.
const chartKey = "?chart\x00"

// charts keeps the charts read from the document's parts. A part that cannot be drawn keeps its nil, so it is not read
// again, and the page shows what it is instead.
type charts struct {
	mu   sync.Mutex
	list map[string]*chartEntry
}

type chartEntry struct {
	ch    *chart.Chart
	cache chart.Cache
}

// graphicKey is what a drawing without a picture is called: its chart's part, or a word for what it is.
func graphicKey(n *ox.Node, part *Part) string {
	if c := firstNamed(n, "c:chart", 0); c != nil {
		if rel, ok := opc.RelByID(part.Rels, c.Attr("r:id")); ok && !rel.External {
			return chartKey + relResolve(part.Name, rel.Target)
		}
	}
	return "?" + graphicLabel(n)
}

// chartIn is the chart of a key, nil for one that has none or cannot be drawn.
func (d *Doc) chartIn(key string) *chartEntry {
	name, ok := strings.CutPrefix(key, chartKey)
	if !ok {
		return nil
	}
	d.charts.mu.Lock()
	defer d.charts.mu.Unlock()
	if e, ok := d.charts.list[name]; ok {
		return e
	}
	if d.charts.list == nil {
		d.charts.list = map[string]*chartEntry{}
	}
	var e *chartEntry
	if p := d.Pkg.Part(name); p != nil {
		if ch, err := chart.Parse(p.Data, chart.Source{Colors: dml.Scheme(d.Theme.Colors).Lookup}); err == nil {
			e = &chartEntry{ch: ch}
		}
	}
	d.charts.list[name] = e
	return e
}

// drawChart draws the chart of a key in r and reports that there was one.
func (d *Doc) drawChart(c *gfx.Canvas, r gfx.Rect, key string, scale float32) bool {
	e := d.chartIn(key)
	if e == nil {
		return false
	}
	e.cache.Draw(c, r, scale, e.ch, chart.FacesOf(d.Fonts))
	return true
}
