package pptx

import (
	"fmt"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/office/opc"
)

const xmlHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

const nsDecl = `xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `"`

// officeTheme is a complete theme: PowerPoint refuses a theme missing any of its three lists' minimum of entries.
const officeTheme = xmlHead + `<a:theme xmlns:a="` + nsA + `" name="Office Theme"><a:themeElements>` +
	`<a:clrScheme name="Office"><a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>` +
	`<a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2><a:accent1><a:srgbClr val="4472C4"/></a:accent1>` +
	`<a:accent2><a:srgbClr val="ED7D31"/></a:accent2><a:accent3><a:srgbClr val="A5A5A5"/></a:accent3><a:accent4><a:srgbClr val="FFC000"/></a:accent4>` +
	`<a:accent5><a:srgbClr val="5B9BD5"/></a:accent5><a:accent6><a:srgbClr val="70AD47"/></a:accent6><a:hlink><a:srgbClr val="0563C1"/></a:hlink>` +
	`<a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme>` +
	`<a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/><a:cs typeface=""/><a:font script="Hans" typeface="等线 Light"/></a:majorFont>` +
	`<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/><a:font script="Hans" typeface="等线"/></a:minorFont></a:fontScheme>` +
	`<a:fmtScheme name="Office"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`<a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:schemeClr val="phClr"><a:lumMod val="110000"/><a:satMod val="105000"/><a:tint val="67000"/></a:schemeClr></a:gs>` +
	`<a:gs pos="50000"><a:schemeClr val="phClr"><a:lumMod val="105000"/><a:satMod val="103000"/><a:tint val="73000"/></a:schemeClr></a:gs>` +
	`<a:gs pos="100000"><a:schemeClr val="phClr"><a:lumMod val="105000"/><a:satMod val="109000"/><a:tint val="81000"/></a:schemeClr></a:gs></a:gsLst><a:lin ang="5400000" scaled="0"/></a:gradFill>` +
	`<a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:schemeClr val="phClr"><a:satMod val="103000"/><a:lumMod val="102000"/><a:tint val="94000"/></a:schemeClr></a:gs>` +
	`<a:gs pos="50000"><a:schemeClr val="phClr"><a:satMod val="110000"/><a:lumMod val="100000"/><a:shade val="100000"/></a:schemeClr></a:gs>` +
	`<a:gs pos="100000"><a:schemeClr val="phClr"><a:lumMod val="99000"/><a:satMod val="120000"/><a:shade val="78000"/></a:schemeClr></a:gs></a:gsLst><a:lin ang="5400000" scaled="0"/></a:gradFill></a:fillStyleLst>` +
	`<a:lnStyleLst><a:ln w="6350" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/><a:miter lim="800000"/></a:ln>` +
	`<a:ln w="12700" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/><a:miter lim="800000"/></a:ln>` +
	`<a:ln w="19050" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/><a:miter lim="800000"/></a:ln></a:lnStyleLst>` +
	`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle>` +
	`<a:effectStyle><a:effectLst><a:outerShdw blurRad="57150" dist="19050" dir="5400000" algn="ctr" rotWithShape="0"><a:srgbClr val="000000"><a:alpha val="63000"/></a:srgbClr></a:outerShdw></a:effectLst></a:effectStyle></a:effectStyleLst>` +
	`<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"><a:tint val="95000"/><a:satMod val="170000"/></a:schemeClr></a:solidFill>` +
	`<a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:schemeClr val="phClr"><a:tint val="93000"/><a:satMod val="150000"/><a:shade val="98000"/><a:lumMod val="102000"/></a:schemeClr></a:gs>` +
	`<a:gs pos="50000"><a:schemeClr val="phClr"><a:tint val="98000"/><a:satMod val="130000"/><a:shade val="90000"/><a:lumMod val="103000"/></a:schemeClr></a:gs>` +
	`<a:gs pos="100000"><a:schemeClr val="phClr"><a:shade val="63000"/><a:satMod val="120000"/></a:schemeClr></a:gs></a:gsLst><a:lin ang="5400000" scaled="0"/></a:gradFill></a:bgFillStyleLst></a:fmtScheme>` +
	`</a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`

// levelStyle writes the nine levels of a text style. marL, indent and sizes follow PowerPoint's blank theme.
func levelStyle(name string, font string, sizes [9]int, marks bool) string {
	var b strings.Builder
	b.WriteString("<p:" + name + ">")
	for i := 0; i < 9; i++ {
		marL := 228600 + 457200*i
		indent := -228600
		space := `<a:spcBef><a:spcPts val="500"/></a:spcBef>`
		if i == 0 {
			space = `<a:spcBef><a:spcPts val="1000"/></a:spcBef>`
		}
		bullet := `<a:buFont typeface="Arial" panose="020B0604020202020204" pitchFamily="34" charset="0"/><a:buChar char="&#8226;"/>`
		if !marks {
			marL, indent, space, bullet = 0, 0, `<a:spcBef><a:spcPct val="0"/></a:spcBef>`, `<a:buNone/>`
		}
		fmt.Fprintf(&b, `<a:lvl%dpPr marL="%d" indent="%d" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1"><a:lnSpc><a:spcPct val="90000"/></a:lnSpc>%s%s`+
			`<a:defRPr sz="%d" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+%s-lt"/><a:ea typeface="+%s-ea"/><a:cs typeface="+%s-cs"/></a:defRPr></a:lvl%dpPr>`,
			i+1, marL, indent, space, bullet, sizes[i], font, font, font, i+1)
	}
	b.WriteString("</p:" + name + ">")
	return b.String()
}

// defaultLevels is a defaultTextStyle's or otherStyle's nine levels.
func defaultLevels() string {
	var b strings.Builder
	b.WriteString(`<a:defPPr><a:defRPr lang="en-US"/></a:defPPr>`)
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&b, `<a:lvl%dpPr marL="%d" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1"><a:defRPr sz="1800" kern="1200">`+
			`<a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr></a:lvl%dpPr>`, i+1, 457200*i, i+1)
	}
	return b.String()
}

// sp is a placeholder shape for the master and layouts.
func sp(id int, name, ph, xfrm, body string) string {
	if xfrm != "" {
		xfrm = "<a:xfrm>" + xfrm + "</a:xfrm>"
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph %s/></p:nvPr></p:nvSpPr><p:spPr>%s</p:spPr>%s</p:sp>`,
		id, name, ph, xfrm, body)
}

func box(x, y, cx, cy int) string {
	return fmt.Sprintf(`<a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/>`, x, y, cx, cy)
}

const treeHead = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

func txt(s string) string {
	return `<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US"/><a:t>` + s + `</a:t></a:r><a:endParaRPr lang="en-US"/></a:p></p:txBody>`
}

// footers are the date, footer and number placeholders every layout carries.
func footers(id int) string {
	return sp(id, "Date Placeholder", `type="dt" sz="half" idx="10"`, "", `<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody>`) +
		sp(id+1, "Footer Placeholder", `type="ftr" sz="quarter" idx="11"`, "", `<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody>`) +
		sp(id+2, "Slide Number Placeholder", `type="sldNum" sz="quarter" idx="12"`, "",
			`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:fld id="{B6F15528-21DE-4FAA-801E-634DDDAF4B2B}" type="slidenum"><a:rPr lang="en-US"/><a:t>‹#›</a:t></a:fld><a:endParaRPr lang="en-US"/></a:p></p:txBody>`)
}

// layoutXML writes a layout.
func layoutXML(name, kind, shapes string) string {
	typ := ""
	if kind != "" {
		typ = ` type="` + kind + `"`
	}
	return xmlHead + `<p:sldLayout ` + nsDecl + typ + ` preserve="1"><p:cSld name="` + name + `"><p:spTree>` + treeHead + shapes +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`
}

const bodyLevels = `<a:p><a:pPr lvl="0"/><a:r><a:rPr lang="en-US"/><a:t>Click to edit Master text styles</a:t></a:r></a:p>` +
	`<a:p><a:pPr lvl="1"/><a:r><a:rPr lang="en-US"/><a:t>Second level</a:t></a:r></a:p>` +
	`<a:p><a:pPr lvl="2"/><a:r><a:rPr lang="en-US"/><a:t>Third level</a:t></a:r></a:p>` +
	`<a:p><a:pPr lvl="3"/><a:r><a:rPr lang="en-US"/><a:t>Fourth level</a:t></a:r></a:p>` +
	`<a:p><a:pPr lvl="4"/><a:r><a:rPr lang="en-US"/><a:t>Fifth level</a:t></a:r><a:endParaRPr lang="en-US"/></a:p>`

const titleText = "Click to edit Master title style"

func blankLayouts() []struct{ name, xml string } {
	titleOnly := sp(2, "Title", `type="title"`, "", txt(titleText))
	body := func(id, idx int, xfrm string) string {
		return sp(id, "Content Placeholder", fmt.Sprintf(`idx="%d"`, idx), xfrm, `<p:txBody><a:bodyPr/><a:lstStyle/>`+bodyLevels+`</p:txBody>`)
	}
	centred := `<p:txBody><a:bodyPr anchor="b"/><a:lstStyle><a:lvl1pPr algn="ctr"><a:defRPr sz="6000"/></a:lvl1pPr></a:lstStyle><a:p><a:r><a:rPr lang="en-US"/><a:t>` + titleText + `</a:t></a:r><a:endParaRPr lang="en-US"/></a:p></p:txBody>`
	sub := `<p:txBody><a:bodyPr/><a:lstStyle><a:lvl1pPr marL="0" indent="0" algn="ctr"><a:buNone/><a:defRPr sz="2400"/></a:lvl1pPr><a:lvl2pPr marL="457200" indent="0" algn="ctr"><a:buNone/><a:defRPr sz="2000"/></a:lvl2pPr></a:lstStyle><a:p><a:r><a:rPr lang="en-US"/><a:t>Click to edit Master subtitle style</a:t></a:r><a:endParaRPr lang="en-US"/></a:p></p:txBody>`
	sect := `<p:txBody><a:bodyPr anchor="b"/><a:lstStyle><a:lvl1pPr><a:defRPr sz="6000"/></a:lvl1pPr></a:lstStyle><a:p><a:r><a:rPr lang="en-US"/><a:t>` + titleText + `</a:t></a:r><a:endParaRPr lang="en-US"/></a:p></p:txBody>`
	sectBody := `<p:txBody><a:bodyPr/><a:lstStyle><a:lvl1pPr marL="0" indent="0"><a:buNone/><a:defRPr sz="2400"><a:solidFill><a:schemeClr val="tx1"><a:tint val="75000"/></a:schemeClr></a:solidFill></a:defRPr></a:lvl1pPr></a:lstStyle><a:p><a:pPr lvl="0"/><a:r><a:rPr lang="en-US"/><a:t>Click to edit Master text styles</a:t></a:r></a:p></p:txBody>`
	return []struct{ name, xml string }{
		{"Title Slide", layoutXML("Title Slide", "title",
			sp(2, "Title", `type="ctrTitle"`, box(1524000, 1122363, 9144000, 2387600), centred)+
				sp(3, "Subtitle", `type="subTitle" idx="1"`, box(1524000, 3602038, 9144000, 1655762), sub)+footers(4))},
		{"Title and Content", layoutXML("Title and Content", "obj", titleOnly+body(3, 1, "")+footers(4))},
		{"Section Header", layoutXML("Section Header", "secHead",
			sp(2, "Title", `type="title"`, box(831850, 1709738, 10515600, 2852737), sect)+
				sp(3, "Text Placeholder", `type="body" idx="1"`, box(831850, 4589463, 10515600, 1500187), sectBody)+footers(4))},
		{"Two Content", layoutXML("Two Content", "twoObj", titleOnly+
			body(3, 1, box(838200, 1825625, 5181600, 4351338))+body(4, 2, box(6172200, 1825625, 5181600, 4351338))+footers(5))},
		{"Title Only", layoutXML("Title Only", "titleOnly", titleOnly+footers(3))},
		{"Blank", layoutXML("Blank", "blank", footers(2))},
	}
}

func masterXML(layouts int) string {
	ids := &strings.Builder{}
	for i := 0; i < layouts; i++ {
		fmt.Fprintf(ids, `<p:sldLayoutId id="%d" r:id="rId%d"/>`, 2147483649+i, i+1)
	}
	small := func(algn string) string {
		return `<a:lstStyle><a:lvl1pPr algn="` + algn + `"><a:defRPr sz="1200"><a:solidFill><a:schemeClr val="tx1"><a:tint val="75000"/></a:schemeClr></a:solidFill></a:defRPr></a:lvl1pPr></a:lstStyle>`
	}
	tiny := func(algn, content string) string {
		return `<p:txBody><a:bodyPr vert="horz" lIns="91440" tIns="45720" rIns="91440" bIns="45720" rtlCol="0" anchor="ctr"/>` + small(algn) + `<a:p>` + content + `<a:endParaRPr lang="en-US"/></a:p></p:txBody>`
	}
	body := sp(3, "Text Placeholder", `type="body" idx="1"`, box(838200, 1825625, 10515600, 4351338),
		`<p:txBody><a:bodyPr vert="horz" lIns="91440" tIns="45720" rIns="91440" bIns="45720" rtlCol="0"><a:normAutofit/></a:bodyPr><a:lstStyle/>`+bodyLevels+`</p:txBody>`)
	title := sp(2, "Title Placeholder", `type="title"`, box(838200, 365125, 10515600, 1325563),
		`<p:txBody><a:bodyPr vert="horz" lIns="91440" tIns="45720" rIns="91440" bIns="45720" rtlCol="0" anchor="ctr"><a:normAutofit/></a:bodyPr><a:lstStyle/><a:p><a:r><a:rPr lang="en-US"/><a:t>`+titleText+`</a:t></a:r><a:endParaRPr lang="en-US"/></a:p></p:txBody>`)
	foot := sp(4, "Date Placeholder", `type="dt" sz="half" idx="2"`, box(838200, 6356350, 2743200, 365125), tiny("l", "")) +
		sp(5, "Footer Placeholder", `type="ftr" sz="quarter" idx="3"`, box(4038600, 6356350, 4114800, 365125), tiny("ctr", "")) +
		sp(6, "Slide Number Placeholder", `type="sldNum" sz="quarter" idx="4"`, box(8610600, 6356350, 2743200, 365125),
			tiny("r", `<a:fld id="{B6F15528-21DE-4FAA-801E-634DDDAF4B2B}" type="slidenum"><a:rPr lang="en-US"/><a:t>‹#›</a:t></a:fld>`))
	styles := `<p:txStyles><p:titleStyle><a:lvl1pPr algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1"><a:lnSpc><a:spcPct val="90000"/></a:lnSpc><a:spcBef><a:spcPct val="0"/></a:spcBef><a:buNone/>` +
		`<a:defRPr sz="4400" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mj-lt"/><a:ea typeface="+mj-ea"/><a:cs typeface="+mj-cs"/></a:defRPr></a:lvl1pPr></p:titleStyle>` +
		levelStyle("bodyStyle", "mn", [9]int{2800, 2400, 2000, 1800, 1800, 1800, 1800, 1800, 1800}, true) +
		`<p:otherStyle>` + defaultLevels() + `</p:otherStyle></p:txStyles>`
	return xmlHead + `<p:sldMaster ` + nsDecl + `><p:cSld><p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + treeHead + title + body + foot +
		`</p:spTree></p:cSld><p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
		`<p:sldLayoutIdLst>` + ids.String() + `</p:sldLayoutIdLst>` + styles + `</p:sldMaster>`
}

// emptyPlaceholder is a placeholder on a slide, holding no words yet.
func emptyPlaceholder(id int, name, ph string) string {
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph %s/></p:nvPr></p:nvSpPr><p:spPr/>`+
		`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>`, id, name, ph)
}

func slideXML(shapes string) string {
	return xmlHead + `<p:sld ` + nsDecl + `><p:cSld><p:spTree>` + treeHead + shapes + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
}

// Blank is a new presentation: 16:9, the Office theme, a master, six layouts, and a title slide with nothing typed.
func Blank() ([]byte, error) {
	pkg := opc.NewPackage()
	layouts := blankLayouts()
	put := func(name, ct, xml string) { pkg.Put(name, ct, []byte(xml)) }
	master := "ppt/slideMasters/slideMaster1.xml"
	put("ppt/presentation.xml", ctPresentation, xmlHead+`<p:presentation `+nsDecl+` saveSubsetFonts="1"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`+
		`<p:sldIdLst><p:sldId id="256" r:id="rId2"/></p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/><p:defaultTextStyle>`+defaultLevels()+`</p:defaultTextStyle></p:presentation>`)
	pkg.AddRel("", opc.RelOfficeDocument, "ppt/presentation.xml", false)
	pkg.AddRel("ppt/presentation.xml", relMaster, "slideMasters/slideMaster1.xml", false)
	pkg.AddRel("ppt/presentation.xml", relSlide, "slides/slide1.xml", false)
	pkg.AddRel("ppt/presentation.xml", relPresProps, "presProps.xml", false)
	pkg.AddRel("ppt/presentation.xml", relViewProps, "viewProps.xml", false)
	pkg.AddRel("ppt/presentation.xml", relTheme, "theme/theme1.xml", false)
	pkg.AddRel("ppt/presentation.xml", relTableStyles, "tableStyles.xml", false)
	put(master, ctMaster, masterXML(len(layouts)))
	for i, l := range layouts {
		name := fmt.Sprintf("ppt/slideLayouts/slideLayout%d.xml", i+1)
		put(name, ctLayout, l.xml)
		pkg.AddRel(name, relMaster, "../slideMasters/slideMaster1.xml", false)
		pkg.AddRel(master, relLayout, fmt.Sprintf("../slideLayouts/slideLayout%d.xml", i+1), false)
	}
	pkg.AddRel(master, relTheme, "../theme/theme1.xml", false)
	put("ppt/theme/theme1.xml", ctTheme, officeTheme)
	put("ppt/slides/slide1.xml", ctSlide, slideXML(emptyPlaceholder(2, "Title 1", `type="ctrTitle"`)+emptyPlaceholder(3, "Subtitle 2", `type="subTitle" idx="1"`)))
	pkg.AddRel("ppt/slides/slide1.xml", relLayout, "../slideLayouts/slideLayout1.xml", false)
	put("ppt/presProps.xml", ctPresProps, xmlHead+`<p:presentationPr `+nsDecl+`/>`)
	put("ppt/viewProps.xml", ctViewProps, xmlHead+`<p:viewPr `+nsDecl+`/>`)
	put("ppt/tableStyles.xml", ctTableStyles, xmlHead+`<a:tblStyleLst xmlns:a="`+nsA+`" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`)
	put("docProps/core.xml", ctCore, xmlHead+`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>Presentation</dc:title></cp:coreProperties>`)
	put("docProps/app.xml", ctApp, xmlHead+`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>Btelo Forge</Application><Slides>1</Slides></Properties>`)
	pkg.AddRel("", opc.RelCoreProps, "docProps/core.xml", false)
	pkg.AddRel("", opc.RelAppProps, "docProps/app.xml", false)
	return pkg.Bytes()
}
