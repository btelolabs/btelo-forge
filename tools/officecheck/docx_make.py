"""Makes a rich .docx with python-docx and raw XML, for the Word editor to open: docx_make.py out.docx
Everything a real document has: styles, lists, tables with merges, pictures, a floating picture and text box, headers
and footers with fields, sections, links, a table of contents, tracked changes, a comment, an equation."""
import copy
import io
import struct
import sys
import zlib

from docx import Document
from docx.enum.section import WD_ORIENT, WD_SECTION
from docx.enum.table import WD_ALIGN_VERTICAL
from docx.enum.text import WD_ALIGN_PARAGRAPH, WD_BREAK
from docx.oxml import OxmlElement, parse_xml
from docx.oxml.ns import nsdecls, qn
from docx.shared import Cm, Emu, Pt, RGBColor


def png(w, h, rgb):
    raw = b"".join(b"\0" + bytes(rgb) * w for _ in range(h))
    def chunk(t, d):
        c = struct.pack(">I", len(d)) + t + d
        return c + struct.pack(">I", zlib.crc32(t + d))
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")


def field(par, instr, cached):
    for kind, text in (("begin", None), (None, instr), ("separate", None), ("t", cached), ("end", None)):
        run = par.add_run()
        if kind in ("begin", "separate", "end"):
            e = OxmlElement("w:fldChar")
            e.set(qn("w:fldCharType"), kind)
            run._r.append(e)
        elif kind == "t":
            run.text = text
        else:
            e = OxmlElement("w:instrText")
            e.set(qn("xml:space"), "preserve")
            e.text = " " + text + " "
            run._r.append(e)


def hyperlink(par, url, text):
    rid = par.part.relate_to(url, "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink", is_external=True)
    h = OxmlElement("w:hyperlink")
    h.set(qn("r:id"), rid)
    r = OxmlElement("w:r")
    pr = OxmlElement("w:rPr")
    c = OxmlElement("w:color")
    c.set(qn("w:val"), "0563C1")
    u = OxmlElement("w:u")
    u.set(qn("w:val"), "single")
    pr.append(c)
    pr.append(u)
    r.append(pr)
    t = OxmlElement("w:t")
    t.text = text
    r.append(t)
    h.append(r)
    par._p.append(h)


def anchor_picture(par, width, height):
    run = par.add_run()
    run.add_picture(io.BytesIO(png(90, 60, (230, 120, 60))), width=width, height=height)
    inline = run._r.find(qn("w:drawing")).find(qn("wp:inline"))
    a = parse_xml(
        f'<wp:anchor {nsdecls("wp")} distT="0" distB="0" distL="114300" distR="114300" simplePos="0" relativeHeight="251658240" behindDoc="0" locked="0" layoutInCell="1" allowOverlap="1">'
        f'<wp:simplePos x="0" y="0"/><wp:positionH relativeFrom="margin"><wp:align>right</wp:align></wp:positionH>'
        f'<wp:positionV relativeFrom="paragraph"><wp:posOffset>12700</wp:posOffset></wp:positionV>'
        f'<wp:extent cx="{width}" cy="{height}"/><wp:wrapSquare wrapText="bothSides"/></wp:anchor>')
    for child in list(inline):
        if child.tag == qn("wp:extent"):
            continue
        a.append(child)
    inline.getparent().replace(inline, a)


def text_box(par):
    xml = (
        '<w:r xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" '
        'xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" '
        'xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape" xmlns:v="urn:schemas-microsoft-com:vml">'
        '<mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:anchor distT="0" distB="0" distL="0" distR="0" simplePos="0" relativeHeight="2" behindDoc="0" locked="0" layoutInCell="1" allowOverlap="1">'
        '<wp:simplePos x="0" y="0"/><wp:positionH relativeFrom="page"><wp:posOffset>4500000</wp:posOffset></wp:positionH><wp:positionV relativeFrom="page"><wp:posOffset>8000000</wp:posOffset></wp:positionV>'
        '<wp:extent cx="2200000" cy="900000"/><wp:wrapNone/><wp:docPr id="77" name="Text box"/><wp:cNvGraphicFramePr/>'
        '<a:graphic><a:graphicData uri="http://schemas.microsoft.com/office/word/2010/wordprocessingShape"><wps:wsp><wps:cNvSpPr txBox="1"/><wps:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="2200000" cy="900000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:solidFill><a:srgbClr val="FFF2CC"/></a:solidFill><a:ln w="9525"><a:solidFill><a:srgbClr val="BF9000"/></a:solidFill></a:ln></wps:spPr>'
        '<wps:txbx><w:txbxContent><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>Text box</w:t></w:r></w:p><w:p><w:r><w:t>文本框里的文字</w:t></w:r></w:p></w:txbxContent></wps:txbx><wps:bodyPr/></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></mc:Choice>'
        '<mc:Fallback><w:pict><v:rect style="width:100pt;height:50pt"/></w:pict></mc:Fallback></mc:AlternateContent></w:r>')
    par._p.append(parse_xml(xml))


def main(path):
    doc = Document()
    doc.core_properties.author = "officecheck"
    normal = doc.styles["Normal"]
    normal.font.size = Pt(11)
    normal.font.name = "Calibri"
    normal.element.rPr.rFonts.set(qn("w:eastAsia"), "宋体")

    sec = doc.sections[0]
    sec.page_width, sec.page_height = Cm(21), Cm(29.7)
    sec.different_first_page_header_footer = True
    sec.header.paragraphs[0].text = "Running header\tcentre\tright"
    sec.first_page_header.paragraphs[0].text = "First page header"
    foot = sec.footer.paragraphs[0]
    foot.alignment = WD_ALIGN_PARAGRAPH.CENTER
    foot.add_run("Page ")
    field(foot, "PAGE", "1")
    foot.add_run(" of ")
    field(foot, "NUMPAGES", "9")

    doc.add_heading("Report on the rich document", 0)
    p = doc.add_paragraph()
    field(p, 'TOC \\o "1-3" \\h \\z \\u', "Contents shown from the file's cache")
    doc.add_heading("Introduction 引言", 1)
    p = doc.add_paragraph("Normal text with ")
    p.add_run("bold").bold = True
    p.add_run(", ")
    p.add_run("italic").italic = True
    p.add_run(", ")
    p.add_run("underlined").underline = True
    r = p.add_run(" coloured")
    r.font.color.rgb = RGBColor(0xC0, 0, 0)
    r = p.add_run(" large")
    r.font.size = Pt(16)
    p.add_run(" 中文混排 and a link to ")
    hyperlink(p, "https://example.com/page", "example.com")
    p.add_run(".")
    r = p.add_run(" x")
    r = p.add_run("2")
    r.font.superscript = True
    p = doc.add_paragraph("Centred paragraph with spacing.")
    p.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p.paragraph_format.space_before = Pt(12)
    p.paragraph_format.space_after = Pt(12)
    p = doc.add_paragraph("Justified paragraph. " * 12)
    p.alignment = WD_ALIGN_PARAGRAPH.JUSTIFY
    p.paragraph_format.first_line_indent = Cm(0.74)
    for t in ("First bullet", "Second bullet"):
        doc.add_paragraph(t, style="List Bullet")
    doc.add_paragraph("Nested bullet", style="List Bullet 2")
    for t in ("Step one", "Step two", "Step three"):
        doc.add_paragraph(t, style="List Number")
    doc.add_heading("Tables 表格", 2)
    t = doc.add_table(rows=4, cols=4)
    t.style = "Light Grid Accent 1"
    for r_i, row in enumerate(t.rows):
        for c_i, cell in enumerate(row.cells):
            cell.text = f"R{r_i + 1}C{c_i + 1}"
    t.cell(1, 0).merge(t.cell(2, 0))
    t.cell(0, 1).merge(t.cell(0, 2))
    t.cell(3, 3).vertical_alignment = WD_ALIGN_VERTICAL.CENTER
    tc = t.cell(3, 1)._tc.get_or_add_tcPr()
    shd = OxmlElement("w:shd")
    shd.set(qn("w:val"), "clear")
    shd.set(qn("w:color"), "auto")
    shd.set(qn("w:fill"), "FFE699")
    tc.append(shd)
    doc.add_paragraph()
    doc.add_paragraph("A picture in the line:")
    doc.add_picture(io.BytesIO(png(240, 120, (70, 130, 200))), width=Cm(8))
    p = doc.add_paragraph("This paragraph has a floating picture anchored to it at the right of the margin. ")
    anchor_picture(p, Cm(3).emu, Cm(2).emu)
    p = doc.add_paragraph("And this one holds a text box placed on the page.")
    text_box(p)
    p = doc.add_paragraph("Tracked: ")
    p._p.append(parse_xml('<w:ins xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" w:id="901" w:author="A" w:date="2024-01-01T00:00:00Z"><w:r><w:t>inserted</w:t></w:r></w:ins>'))
    p._p.append(parse_xml('<w:del xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" w:id="902" w:author="A" w:date="2024-01-01T00:00:00Z"><w:r><w:delText>deleted</w:delText></w:r></w:del>'))
    p = doc.add_paragraph("Equation: ")
    p._p.append(parse_xml('<m:oMath xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"><m:r><m:t>E=mc2</m:t></m:r></m:oMath>'))

    r = doc.add_paragraph().add_run()
    r.add_break(WD_BREAK.PAGE)
    doc.add_heading("A long table", 2)
    t = doc.add_table(rows=1, cols=3)
    t.style = "Table Grid"
    for c, name in zip(t.rows[0].cells, ("Item", "Description", "Amount")):
        c.text = name
    trPr = t.rows[0]._tr.get_or_add_trPr()
    trPr.append(OxmlElement("w:tblHeader"))
    for i in range(70):
        row = t.add_row().cells
        row[0].text = str(i + 1)
        row[1].text = "A row of a long table which crosses pages " + ("with several lines of words in this cell " * (i % 4))
        row[2].text = str((i + 1) * 13)

    s2 = doc.add_section(WD_SECTION.NEW_PAGE)
    s2.orientation = WD_ORIENT.LANDSCAPE
    s2.page_width, s2.page_height = Cm(29.7), Cm(21)
    doc.add_heading("A landscape section", 1)
    doc.add_paragraph("Wide pages. " * 40)
    s3 = doc.add_section(WD_SECTION.NEW_PAGE)
    s3.orientation = WD_ORIENT.PORTRAIT
    s3.page_width, s3.page_height = Cm(21), Cm(29.7)
    cols = s3._sectPr.find(qn("w:cols"))
    if cols is None:
        cols = OxmlElement("w:cols")
        s3._sectPr.append(cols)
    cols.set(qn("w:num"), "2")
    cols.set(qn("w:space"), "720")
    doc.add_heading("Two columns", 1)
    for i in range(30):
        doc.add_paragraph(f"Column text {i}. " * 10)
    doc.save(path)


if __name__ == "__main__":
    main(sys.argv[1])
