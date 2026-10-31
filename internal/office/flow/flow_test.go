package flow

import (
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

func book(t *testing.T) *Book {
	t.Helper()
	fonts, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skip(err)
	}
	return NewBook(fonts)
}

func para(text string, size float32) *Para {
	return &Para{Runs: []Run{{Text: text, Style: Style{Family: "Arial", Size: size}}}, Empty: Style{Family: "Arial", Size: size}}
}

func TestLatinBreaksAtSpaces(t *testing.T) {
	l := Layout(para("hello there wonderful world", 12), 80, book(t))
	if len(l.Lines) < 2 {
		t.Fatalf("%d lines", len(l.Lines))
	}
	for _, line := range l.Lines {
		if line.End < l.Count && line.Frags[len(line.Frags)-1].Text[len(line.Frags[len(line.Frags)-1].Text)-1] != ' ' {
			t.Fatalf("a word was split: %+v", line.Frags)
		}
	}
}

func TestCJKBreaksAnywhereButNotBeforePunctuation(t *testing.T) {
	l := Layout(para("这是一段很长的中文，用来检查标点符号不会出现在行首。这是第二句话，继续检查。", 12), 70, book(t))
	if len(l.Lines) < 3 {
		t.Fatalf("%d lines", len(l.Lines))
	}
	for _, line := range l.Lines[1:] {
		rs := []rune(line.Frags[0].Text)
		if rs[0] == '，' || rs[0] == '。' {
			t.Fatalf("a line starts with %c", rs[0])
		}
	}
}

func TestLongWordIsCut(t *testing.T) {
	l := Layout(para("wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww", 12), 50, book(t))
	if len(l.Lines) < 2 {
		t.Fatal("a word wider than the line went past it")
	}
	if l.Lines[0].Right > 50 {
		t.Fatal(l.Lines[0].Right)
	}
}

func TestEveryCaretPlaceMapsBackToItself(t *testing.T) {
	b := book(t)
	p := para("Line one wraps here\nand 中文 too", 12)
	l := Layout(p, 90, b)
	for i := 0; i <= l.Count; i++ {
		c := l.Caret(i, false)
		got, _ := l.Hit(c.X+0.01, c.Y+c.H/2)
		if got != i {
			t.Errorf("place %d is at (%v,%v) which hits %d", i, c.X, c.Y, got)
		}
	}
}

func TestTrailingLineBreakLeavesALine(t *testing.T) {
	l := Layout(para("abc\n", 12), 200, book(t))
	if len(l.Lines) != 2 {
		t.Fatalf("%d lines", len(l.Lines))
	}
	if c := l.Caret(4, false); c.Line != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestEmptyParagraphHasALine(t *testing.T) {
	l := Layout(&Para{Empty: Style{Family: "Arial", Size: 20}}, 100, book(t))
	if len(l.Lines) != 1 || l.Height < 20 {
		t.Fatalf("%+v", l)
	}
	if c := l.Caret(0, false); c.H < 20 {
		t.Fatalf("%+v", c)
	}
}

func TestAlignment(t *testing.T) {
	b := book(t)
	p := para("x", 12)
	p.Align = Right
	if l := Layout(p, 100, b); l.Lines[0].Right < 99.9 || l.Lines[0].Right > 100.1 {
		t.Fatal(l.Lines[0].Right)
	}
	p.Align = Center
	l := Layout(p, 100, b)
	f := l.Lines[0].Frags[0]
	if diff := f.X - (100 - (f.X + f.W)); diff > 0.1 || diff < -0.1 {
		t.Fatal(f.X, f.W)
	}
}

func TestJustifyFillsAllButTheLastLine(t *testing.T) {
	p := para("one two three four five six seven eight nine ten eleven twelve", 12)
	p.Align = Justify
	l := Layout(p, 120, book(t))
	if len(l.Lines) < 3 {
		t.Fatal(len(l.Lines))
	}
	first := l.Lines[0].Frags[0]
	trim := first.Offsets[len(first.Offsets)-2] + first.X
	if trim < 117 || trim > 120.5 {
		t.Fatalf("the first line ends at %v", trim)
	}
	last := l.Lines[len(l.Lines)-1].Frags[0]
	if last.Space != 0 || last.X+last.W > 119 {
		t.Fatalf("the last line was spread: %+v", last)
	}
}

func TestTabsGoToStops(t *testing.T) {
	p := para("a\tb", 12)
	p.Tabs = []float32{100}
	l := Layout(p, 300, book(t))
	if c := l.Caret(2, false); c.X != 100 {
		t.Fatal(c.X)
	}
}

func TestLineSpacing(t *testing.T) {
	b := book(t)
	single := Layout(para("x", 12), 100, b).Height
	p := para("x", 12)
	p.Line = 2
	if got := Layout(p, 100, b).Height; got < single*1.99 || got > single*2.01 {
		t.Fatal(single, got)
	}
	p.Rule, p.Line = Exact, 30
	if got := Layout(p, 100, b).Height; got != 30 {
		t.Fatal(got)
	}
}

func TestSelectionRects(t *testing.T) {
	l := Layout(para("hello there wonderful world", 12), 80, book(t))
	if got := l.Rects(0, l.Count); len(got) != len(l.Lines) {
		t.Fatalf("%d rects over %d lines", len(got), len(l.Lines))
	}
	if got := l.Rects(3, 3); got != nil {
		t.Fatal(got)
	}
}

func TestInlinePictureRaisesTheLine(t *testing.T) {
	p := &Para{Runs: []Run{{Text: "a", Style: Style{Family: "Arial", Size: 12}}, {Inline: &Inline{W: 40, H: 100}}}}
	l := Layout(p, 200, book(t))
	if l.Lines[0].H < 100 || l.Count != 2 {
		t.Fatalf("%+v", l.Lines[0])
	}
}

func TestDrawLeavesInk(t *testing.T) {
	b := book(t)
	c := gfx.NewCanvas(200, 60)
	c.Clear(color.RGBA{255, 255, 255, 255})
	p := para("Ink 墨", 14)
	p.Runs[0].Style.Underline = true
	Layout(p, 180, b).Draw(c, 4, 4, 1.5, b, nil, color.RGBA{0, 0, 0, 255})
	ink := 0
	for i := 0; i < len(c.Img.Pix); i += 4 {
		if c.Img.Pix[i] < 128 {
			ink++
		}
	}
	if ink < 30 {
		t.Fatal(ink)
	}
}

func TestUnknownFamilyIsNeverNil(t *testing.T) {
	b := book(t)
	if b.Font("No Such Font", true, true) == nil || b.Font("", false, false) == nil {
		t.Fatal("no font")
	}
}
