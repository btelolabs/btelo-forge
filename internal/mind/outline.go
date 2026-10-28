package mind

import (
	"strings"
	"unicode"
)

// Outlines: an indented list or Markdown headings and bullets become a tree, and a tree becomes a Markdown list, so
// notes can be pasted in as topics and a map copied out as text.

// outlineLine is one line of an outline: its depth and words.
type outlineLine struct {
	depth      int
	text       string
	task, done bool // a task item: - [ ] or - [x]
}

// parseOutline reads an outline: headings (#, ##…) go deeper with each #, and list items (-, *, +, 1.) or plain
// lines go deeper with their indent, a tab or two spaces a step, under the last heading.
func parseOutline(src string) []outlineLine {
	var out []outlineLine
	heading := -1
	for _, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indent := 0
		for _, r := range raw {
			if r == '\t' {
				indent += 2
			} else if r == ' ' {
				indent++
			} else {
				break
			}
		}
		line := strings.TrimSpace(raw)
		if h := len(line) - len(strings.TrimLeft(line, "#")); h > 0 && h <= 6 && strings.HasPrefix(line[h:], " ") {
			heading = h - 1
			out = append(out, outlineLine{depth: heading, text: strings.TrimSpace(line[h:])})
			continue
		}
		text, task, done := trimBullet(line)
		out = append(out, outlineLine{heading + 1 + indent/2, text, task, done})
	}
	// Depths step down one at a time, so a line indented too far goes under the line before it.
	for i := range out {
		limit := 0
		if i > 0 {
			limit = out[i-1].depth + 1
		}
		out[i].depth = min(out[i].depth, limit)
	}
	return out
}

// trimBullet removes a list item's mark and a task's box, reporting whether there was a box and whether it was
// ticked.
func trimBullet(s string) (string, bool, bool) {
	switch {
	case strings.HasPrefix(s, "- "), strings.HasPrefix(s, "* "), strings.HasPrefix(s, "+ "):
		s = strings.TrimSpace(s[2:])
	default:
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i > 0 && i+1 < len(s) && (s[i] == '.' || s[i] == ')') && s[i+1] == ' ' {
			s = strings.TrimSpace(s[i+2:])
		}
	}
	for _, box := range []string{"[ ] ", "[x] ", "[X] "} {
		if rest, ok := strings.CutPrefix(s, box); ok {
			return rest, true, box != "[ ] "
		}
	}
	return s, false, false
}

// IsOutline reports whether text reads as an outline of more than one topic rather than one topic's words.
func IsOutline(text string) bool {
	lines := parseOutline(text)
	if len(lines) < 2 {
		return false
	}
	for _, l := range lines[1:] {
		if l.depth > 0 {
			return true
		}
	}
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	return strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "#")
}

// AddOutline adds an outline's topics under parent at index (free at x, y when parent is 0: then the outline's first
// topics become free trees, one under another), as one step; it returns the ids of its top-level topics.
func (m *Map) AddOutline(text string, parent ID, index int, x, y float64) ([]ID, error) {
	lines := parseOutline(text)
	var tops []ID
	err := m.step("Paste", func() error {
		stack := []ID{}
		for _, l := range lines {
			stack = stack[:min(len(stack), l.depth)]
			p, at := parent, index
			if len(stack) > 0 {
				p, at = stack[len(stack)-1], -1
			} else if index >= 0 {
				index++
			}
			n := Node{Text: l.text, Task: l.task, Done: l.done}
			if p == 0 {
				n.X, n.Y = x, y+float64(len(tops))*80
			}
			id, err := m.Add(n, p, at)
			if err != nil {
				return err
			}
			if len(stack) == 0 {
				tops = append(tops, id)
			}
			stack = append(stack, id)
		}
		return nil
	})
	return tops, err
}

// Outline writes the subtrees of ids as a Markdown list, two spaces a level.
func (m *Map) Outline(ids ...ID) string {
	var b strings.Builder
	for _, id := range ids {
		n := m.Node(id)
		if n == nil {
			continue
		}
		base := m.Depth(n)
		m.eachUnder(n, func(d *Node) {
			text := d.Text
			if text == "" {
				text = d.Caption()
			}
			if d.Kind == Table && d.Cells != nil {
				text = strings.TrimSpace(text + " " + tableText(d.Cells))
			}
			b.WriteString(strings.Repeat("  ", m.Depth(d)-base))
			b.WriteString("- ")
			if d.Task {
				if d.Done {
					b.WriteString("[x] ")
				} else {
					b.WriteString("[ ] ")
				}
			}
			b.WriteString(strings.ReplaceAll(text, "\n", " "))
			b.WriteString("\n")
		})
	}
	return b.String()
}

// tableText is a table's cells as shown, a row per bracket, cells split by bars.
func tableText(g *Grid) string {
	vals := evalGrid(g)
	var rows []string
	for r := range g.Rows {
		rows = append(rows, "["+strings.Join(vals[r*g.Cols:(r+1)*g.Cols], " | ")+"]")
	}
	return strings.Join(rows, " ")
}

// TSV is a table's cells as shown, tab-separated rows, as spreadsheets copy them.
func TSV(g *Grid) string {
	vals := evalGrid(g)
	var b strings.Builder
	for r := range g.Rows {
		b.WriteString(strings.Join(vals[r*g.Cols:(r+1)*g.Cols], "\t"))
		b.WriteString("\n")
	}
	return b.String()
}

// ParseTSV reads tab-separated rows, as spreadsheets copy cells, into a grid; ok is false for text without tabs.
func ParseTSV(text string) (*Grid, bool) {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if !strings.Contains(text, "\t") {
		return nil, false
	}
	lines := strings.Split(text, "\n")
	g := &Grid{Rows: len(lines)}
	for _, l := range lines {
		g.Cols = max(g.Cols, len(strings.Split(l, "\t")))
	}
	g.Cells = make([]string, g.Rows*g.Cols)
	g.ColW = make([]float64, g.Cols)
	for r, l := range lines {
		for c, v := range strings.Split(l, "\t") {
			g.Cells[r*g.Cols+c] = v
		}
	}
	return g, true
}
