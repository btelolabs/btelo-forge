package pptx

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
)

func spXML(id int, xf, rest, text string) string {
	body := ""
	if text != "" {
		body = `<p:txBody>` + text + `</p:txBody>`
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="s%d"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>%s%s</p:spPr>%s</p:sp>`, id, id, xf, rest, body)
}

func xf(x, y, w, h int, attrs string) string {
	return fmt.Sprintf(`<a:xfrm%s><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, attrs, x*12700, y*12700, w*12700, h*12700)
}

const rect = `<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`

func solid(hex string) string { return `<a:solidFill><a:srgbClr val="` + hex + `"/></a:solidFill>` }

func plainText(s string) string {
	return `<a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="1400"/><a:t>` + s + `</a:t></a:r></a:p>`
}

// richSlide sets slide 1 of a blank deck to markup using most of what the renderer handles.
func richSlide(t *testing.T) []byte {
	t.Helper()
	data, _ := Blank()
	pkg, err := opc.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			c := color.RGBA{uint8(x * 4), uint8(y * 4), 160, 255}
			if (x/8+y/8)%2 == 0 {
				c = color.RGBA{250, 240, 200, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var pic bytes.Buffer
	png.Encode(&pic, img)
	pkg.SetDefault("png", "image/png")
	pkg.Put("ppt/media/image1.png", "image/png", pic.Bytes())
	rid := pkg.AddRel("ppt/slides/slide1.xml", opc.RelImage, "../media/image1.png", false)
	var b strings.Builder
	id := 2
	add := func(x, y, w, h int, attrs, rest, text string) {
		b.WriteString(spXML(id, xf(x, y, w, h, attrs), rest, text))
		id++
	}
	// Custom geometry with guides, arcs and a curve.
	add(20, 20, 120, 80, "", `<a:custGeom><a:avLst><a:gd name="k" fmla="val 25000"/></a:avLst><a:gdLst><a:gd name="x1" fmla="*/ w k 100000"/><a:gd name="y1" fmla="+- b 0 hd4"/></a:gdLst><a:ahLst/><a:cxnLst/><a:rect l="x1" t="t" r="r" b="y1"/><a:pathLst>`+
		`<a:path><a:moveTo><a:pt x="l" y="y1"/></a:moveTo><a:lnTo><a:pt x="x1" y="t"/></a:lnTo><a:arcTo wR="wd4" hR="hd4" stAng="10800000" swAng="10800000"/><a:cubicBezTo><a:pt x="r" y="t"/><a:pt x="r" y="b"/><a:pt x="hc" y="b"/></a:cubicBezTo><a:close/></a:path></a:pathLst></a:custGeom>`+
		solid("3AA655")+`<a:ln w="19050">`+solid("114422")+`</a:ln>`, plainText("custGeom"))
	// Gradients: linear scaled, circle, rect.
	grad := func(tail string) string {
		return `<a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:srgbClr val="FFEE00"/></a:gs><a:gs pos="60000"><a:srgbClr val="FF0066"/></a:gs><a:gs pos="100000"><a:srgbClr val="2200AA"/></a:gs></a:gsLst>` + tail + `</a:gradFill>`
	}
	add(160, 20, 110, 80, ` rot="1800000"`, rect+grad(`<a:lin ang="2700000" scaled="1"/>`), "")
	add(290, 20, 80, 80, "", `<a:prstGeom prst="ellipse"><a:avLst/></a:prstGeom>`+grad(`<a:path path="circle"><a:fillToRect l="50000" t="50000" r="50000" b="50000"/></a:path>`), "")
	add(390, 20, 80, 80, "", rect+grad(`<a:path path="rect"><a:fillToRect l="50000" t="50000" r="50000" b="50000"/></a:path>`), "")
	// Picture fill, stretched and tiled, with flips and rotation.
	add(490, 20, 100, 80, ` flipH="1" rot="600000"`, `<a:prstGeom prst="hexagon"><a:avLst/></a:prstGeom><a:blipFill><a:blip r:embed="`+rid+`"/><a:stretch><a:fillRect/></a:stretch></a:blipFill>`, "")
	add(610, 20, 100, 80, "", `<a:prstGeom prst="roundRect"><a:avLst/></a:prstGeom><a:blipFill><a:blip r:embed="`+rid+`"><a:alphaModFix amt="60000"/></a:blip><a:tile tx="0" ty="0" sx="50000" sy="50000" flip="none" algn="tl"/></a:blipFill>`, "")
	// A picture with crop, alpha and an outline, and a flipped one.
	fmt.Fprintf(&b, `<p:pic><p:nvPicPr><p:cNvPr id="%d" name="p"/><p:cNvPicPr/><p:nvPr/></p:nvPicPr><p:blipFill><a:blip r:embed="%s"><a:alphaModFix amt="70000"/></a:blip><a:srcRect l="25000" t="25000" r="25000" b="25000"/><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>%s<a:prstGeom prst="ellipse"><a:avLst/></a:prstGeom><a:ln w="38100">%s</a:ln></p:spPr></p:pic>`,
		id, rid, xf(730, 20, 90, 90, ` rot="900000" flipV="1"`), solid("AA2211"))
	id++
	// Lines: dashes, caps, arrows.
	dashes := []string{"solid", "dash", "dot", "lgDashDot", "sysDash"}
	heads := []string{"triangle", "stealth", "diamond", "oval", "arrow"}
	for i, d := range dashes {
		fmt.Fprintf(&b, `<p:cxnSp><p:nvCxnSpPr><p:cNvPr id="%d" name="c"/><p:cNvCxnSpPr/><p:nvPr/></p:nvCxnSpPr><p:spPr>%s<a:prstGeom prst="line"><a:avLst/></a:prstGeom><a:ln w="31750" cap="%s">%s<a:prstDash val="%s"/><a:round/><a:headEnd type="%s" w="med" len="med"/><a:tailEnd type="%s" w="lg" len="lg"/></a:ln></p:spPr></p:cxnSp>`,
			id, xf(20, 130+i*22, 180, 14, ""), []string{"flat", "rnd", "sq", "flat", "flat"}[i], solid("224488"), d, heads[i], heads[(i+1)%5])
		id++
	}
	// A shadow, on a shape and on a shape with no fill.
	add(230, 130, 100, 60, "", `<a:prstGeom prst="roundRect"><a:avLst/></a:prstGeom>`+solid("FFFFFF")+`<a:effectLst><a:outerShdw blurRad="127000" dist="50800" dir="2700000"><a:srgbClr val="000000"><a:alpha val="50000"/></a:srgbClr></a:outerShdw></a:effectLst>`, plainText("shadow"))
	// Text turned, sideways, anchored, in columns, not wrapping.
	add(350, 130, 130, 60, ` rot="-900000"`, rect+solid("DDEEFF"), plainText("rotated with a flip")+"")
	add(490, 120, 50, 120, "", rect+solid("EEDDFF"), `<a:bodyPr vert="vert270" anchor="ctr"/><a:lstStyle/><a:p><a:pPr algn="ctr"/><a:r><a:rPr lang="en-US" sz="1400"/><a:t>vert 270</a:t></a:r></a:p>`)
	add(550, 120, 50, 120, "", rect+solid("FFEEDD"), `<a:bodyPr vert="vert"/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="1400"/><a:t>vert 90</a:t></a:r></a:p>`)
	add(610, 120, 210, 120, "", rect+`<a:ln w="9525">`+solid("888888")+`</a:ln>`, `<a:bodyPr numCol="2" spcCol="91440"/><a:lstStyle/>`+strings.Repeat(`<a:p><a:r><a:rPr lang="en-US" sz="1100"/><a:t>Column words that wrap to fill the first column and then flow on.</a:t></a:r></a:p>`, 3))
	add(830, 120, 110, 40, "", rect, `<a:bodyPr wrap="none" anchor="ctr"/><a:lstStyle/><a:p><a:pPr algn="ctr"/><a:r><a:rPr lang="en-US" sz="1200"/><a:t>no wrap, long past the box edge</a:t></a:r></a:p>`)
	// Bullets, numbering by scheme, spacing, a highlight, a super and subscript, caps, a field.
	add(20, 260, 300, 250, "", rect+`<a:ln w="9525">`+solid("888888")+`</a:ln>`, `<a:bodyPr><a:normAutofit fontScale="85000" lnSpcReduction="10000"/></a:bodyPr><a:lstStyle/>`+
		`<a:p><a:pPr marL="285750" indent="-285750"><a:buFont typeface="Wingdings"/><a:buChar char="§"/></a:pPr><a:r><a:rPr lang="en-US" sz="1800"/><a:t>Wingdings square</a:t></a:r></a:p>`+
		`<a:p><a:pPr marL="342900" indent="-342900"><a:buClr><a:srgbClr val="CC0000"/></a:buClr><a:buSzPct val="120000"/><a:buFont typeface="+mj-lt"/><a:buAutoNum type="romanUcPeriod"/></a:pPr><a:r><a:rPr lang="en-US" sz="1800"/><a:t>Roman one</a:t></a:r></a:p>`+
		`<a:p><a:pPr marL="342900" indent="-342900"><a:buClr><a:srgbClr val="CC0000"/></a:buClr><a:buFont typeface="+mj-lt"/><a:buAutoNum type="romanUcPeriod"/></a:pPr><a:r><a:rPr lang="en-US" sz="1800"/><a:t>Roman two</a:t></a:r></a:p>`+
		`<a:p><a:pPr marL="342900" indent="-342900" algn="r"><a:spcBef><a:spcPts val="1200"/></a:spcBef><a:buNone/></a:pPr><a:r><a:rPr lang="en-US" sz="1600" u="sng" strike="sngStrike"/><a:t>Right under strike</a:t></a:r></a:p>`+
		`<a:p><a:r><a:rPr lang="en-US" sz="1600" cap="all"><a:highlight><a:srgbClr val="FFFF00"/></a:highlight></a:rPr><a:t>caps marked</a:t></a:r><a:r><a:rPr lang="en-US" sz="1600" baseline="30000"/><a:t>up</a:t></a:r><a:r><a:rPr lang="en-US" sz="1600" baseline="-25000"/><a:t>down</a:t></a:r></a:p>`+
		`<a:p><a:r><a:rPr lang="zh-CN" sz="1600"><a:latin typeface="Arial"/><a:ea typeface="黑体"/></a:rPr><a:t>Mixed 中英文 run</a:t></a:r><a:br><a:rPr lang="en-US" sz="1600"/></a:br><a:fld id="{B6F15528-21DE-4FAA-801E-634DDDAF4B2B}" type="slidenum"><a:rPr lang="en-US" sz="1600"/><a:t>9</a:t></a:fld></a:p>`)
	// A group, rotated and flipped, scaled from its child space, with a table of merges and borders by the side.
	fmt.Fprintf(&b, `<p:grpSp><p:nvGrpSpPr><p:cNvPr id="%d" name="g"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm rot="1200000" flipH="1"><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/><a:chOff x="0" y="0"/><a:chExt cx="2000000" cy="1000000"/></a:xfrm></p:grpSpPr>%s%s</p:grpSp>`,
		id, 350*12700, 270*12700, 170*12700, 100*12700,
		spXML(id+1, `<a:xfrm><a:off x="0" y="0"/><a:ext cx="1000000" cy="1000000"/></a:xfrm>`, `<a:prstGeom prst="rightArrow"><a:avLst/></a:prstGeom>`+solid("E07040"), plainText("→")),
		spXML(id+2, `<a:xfrm rot="2700000"><a:off x="1200000" y="200000"/><a:ext cx="600000" cy="600000"/></a:xfrm>`, rect+solid("40A0E0"), ""))
	id += 3
	cell := func(text, extra, tcPr string) string {
		return `<a:tc` + extra + `><a:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="1200"/><a:t>` + text + `</a:t></a:r></a:p></a:txBody><a:tcPr` + tcPr + `</a:tc>`
	}
	border := `><a:lnL w="38100">` + solid("00AA00") + `</a:lnL><a:lnB w="38100"><a:solidFill><a:srgbClr val="AA0000"/></a:solidFill><a:prstDash val="dash"/></a:lnB>` + solid("FFF0C0") + `</a:tcPr>`
	fmt.Fprintf(&b, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="t"/><p:cNvGraphicFramePr/><p:nvPr/></p:nvGraphicFramePr><p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblPr firstRow="1" bandRow="1" firstCol="1"><a:tableStyleId>{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}</a:tableStyleId></a:tblPr><a:tblGrid><a:gridCol w="1200000"/><a:gridCol w="1200000"/><a:gridCol w="1200000"/></a:tblGrid>`+
		`<a:tr h="330000">%s%s%s</a:tr><a:tr h="330000">%s%s%s</a:tr><a:tr h="330000">%s%s%s</a:tr></a:tbl></a:graphicData></a:graphic></p:graphicFrame>`,
		id, 540*12700, 260*12700, 3600000, 990000,
		cell("Head", ` gridSpan="2"`, `/>`), cell("", ` hMerge="1"`, `/>`), cell("Three", "", `/>`),
		cell("Tall", ` rowSpan="2"`, ` anchor="ctr"/>`), cell("Mid", "", border), cell("x", "", `/>`),
		cell("", ` vMerge="1"`, `/>`), cell("Long words in a cell that make the row grow as they wrap", "", ` marL="0"/>`), cell("y", "", `/>`))
	id++
	// A chart frame, and something in mc:AlternateContent with a picture to fall back on.
	fmt.Fprintf(&b, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="c"/><p:cNvGraphicFramePr/><p:nvPr/></p:nvGraphicFramePr><p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart"/></a:graphic></p:graphicFrame>`,
		id, 540*12700, 420*12700, 180*12700, 100*12700)
	id++
	fmt.Fprintf(&b, `<mc:AlternateContent xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"><mc:Choice Requires="cx1"><p:graphicFrame/></mc:Choice><mc:Fallback><p:pic><p:nvPicPr><p:cNvPr id="%d" name="f"/><p:cNvPicPr/><p:nvPr/></p:nvPicPr><p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic></mc:Fallback></mc:AlternateContent>`,
		id, rid, xf(740, 420, 100, 100, ""))
	id++
	bg := `<p:bg><p:bgPr><a:gradFill><a:gsLst><a:gs pos="0"><a:schemeClr val="bg1"/></a:gs><a:gs pos="100000"><a:schemeClr val="accent1"><a:lumMod val="20000"/><a:lumOff val="80000"/></a:schemeClr></a:gs></a:gsLst><a:lin ang="5400000" scaled="0"/></a:gradFill><a:effectLst/></p:bgPr></p:bg>`
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld ` + nsDecl + `><p:cSld>` + bg + `<p:spTree>` + treeHead + b.String() + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
	pkg.SetData("ppt/slides/slide1.xml", []byte(xml))
	out, err := pkg.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRichRender(t *testing.T) {
	data := richSlide(t)
	d, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	r := renderer(t, d)
	start := time.Now()
	shot(t, r, d.Slides[0], "rich-markup", Options{})
	t.Log("first render", time.Since(start))
	start = time.Now()
	shot(t, r, d.Slides[0], "rich-markup", Options{})
	t.Log("second render", time.Since(start))
	// The round trip keeps it the same.
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(out); err != nil {
		t.Fatal(err)
	}
}

func TestManySlidesOpenFast(t *testing.T) {
	d := blankDoc(t)
	l := d.Masters[0].Layouts[1]
	for i := 0; i < 600; i++ {
		s, err := d.AddSlide(l, i+1)
		if err != nil {
			t.Fatal(err)
		}
		list := d.Shapes(s)
		d.Edit("", []*Slide{s}, false, func() error {
			b, _ := d.Body(s, Target{ID: list[0].ID})
			TypeText(b, Pos{}, fmt.Sprintf("Slide %d", i))
			return nil
		})
	}
	data, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	re, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	t.Logf("opened %d slides in %v", len(re.Slides), took)
	if took > 2*time.Second {
		t.Fatalf("opening took %v", took)
	}
	if len(re.Slides) != 601 {
		t.Fatal(len(re.Slides))
	}
}

func TestSmartArtDrawsItsSavedDrawing(t *testing.T) {
	data, _ := Blank()
	pkg, _ := opc.Open(data)
	pkg.AddRel("ppt/slides/slide1.xml", diagramDrawingRel, "../diagrams/drawing1.xml", false)
	pkg.Put("ppt/diagrams/drawing1.xml", "application/vnd.ms-office.drawingml.diagramDrawing+xml", []byte(`<dsp:drawing xmlns:dsp="http://schemas.microsoft.com/office/drawing/2008/diagram" xmlns:a="`+nsA+`"><dsp:spTree><dsp:nvGrpSpPr/><dsp:grpSpPr/>`+
		`<dsp:sp modelId="{1}"><dsp:nvSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvSpPr/></dsp:nvSpPr><dsp:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="2540000" cy="1270000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:solidFill><a:srgbClr val="00FF00"/></a:solidFill></dsp:spPr></dsp:sp></dsp:spTree></dsp:drawing>`))
	frame := `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="9" name="d"/><p:cNvGraphicFramePr/><p:nvPr/></p:nvGraphicFramePr><p:xfrm><a:off x="1270000" y="1270000"/><a:ext cx="2540000" cy="1270000"/></p:xfrm><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/diagram"/></a:graphic></p:graphicFrame>`
	pkg.SetData("ppt/slides/slide1.xml", []byte(slideXML(frame)))
	out, _ := pkg.Bytes()
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	c := shot(t, renderer(t, d), d.Slides[0], "smartart", Options{})
	k := 1280 / d.W
	if got := colorAt(c, int(150*k), int(125*k)); got.G != 255 || got.R != 0 {
		t.Fatalf("the saved drawing was not drawn: %v", got)
	}
}
