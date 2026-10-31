"""Checks what the Word editor writes, with readers that are not ours: docx_check.py <folder>
The folder holds blank.docx, built.docx, rich.docx (from docx_make.py) and rich-edited.docx, the last two written by
FORGE_WORD_DIR=<folder> go test ./internal/office/docx -run TestWriteCheckFiles.
python-docx must read them and see what is expected; the XML must follow the schema's order, which Word insists on; and
everything that was not edited must be unchanged."""
import re
import sys
import zipfile
from pathlib import Path

from docx import Document
from lxml import etree

W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
R = "{http://schemas.openxmlformats.org/officeDocument/2006/relationships}"
WP = "{http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing}"

ORDER = {
    "rPr": "rStyle rFonts b bCs i iCs caps smallCaps strike dstrike outline shadow emboss imprint noProof snapToGrid vanish webHidden color spacing w kern position sz szCs highlight u effect bdr shd fitText vertAlign rtl cs em lang eastAsianLayout specVanish oMath rPrChange",
    "pPr": "pStyle keepNext keepLines pageBreakBefore framePr widowControl numPr suppressLineNumbers pBdr shd tabs suppressAutoHyphens kinsoku wordWrap overflowPunct topLinePunct autoSpaceDE autoSpaceDN bidi adjustRightInd snapToGrid spacing ind contextualSpacing mirrorIndents suppressOverlap jc textDirection textAlignment textboxTightWrap outlineLvl divId cnfStyle rPr sectPr pPrChange",
    "tcPr": "cnfStyle tcW gridSpan hMerge vMerge tcBorders shd noWrap tcMar textDirection tcFitText vAlign hideMark headers cellIns cellDel cellMerge tcPrChange",
    "trPr": "cnfStyle divId gridBefore gridAfter wBefore wAfter cantSplit trHeight tblHeader tblCellSpacing jc hidden ins del trPrChange",
    "tblPr": "tblStyle tblpPr tblOverlap bidiVisual tblStyleRowBandSize tblStyleColBandSize tblW jc tblCellSpacing tblInd tblBorders shd tblLayout tblCellMar tblLook tblCaption tblDescription tblPrChange",
    "sectPr": "headerReference footerReference footnotePr endnotePr type pgSz pgMar paperSrc pgBorders lnNumType pgNumType cols formProt vAlign noEndnote titlePg textDirection bidi rtlGutter docGrid printerSettings sectPrChange",
    "numPr": "ilvl numId numberingChange ins",
    "tbl": "bookmarkStart bookmarkEnd tblPr tblGrid tr sdt customXml",
    "lvl": "start numFmt lvlRestart pStyle isLgl suff lvlText lvlPicBulletId legacy lvlJc pPr rPr",
    "abstractNum": "nsid multiLevelType tmpl name styleLink numStyleLink lvl",
    "numbering": "numPicBullet abstractNum num numIdMacAtCleanup",
    "style": "name aliases basedOn next link autoRedefine hidden uiPriority semiHidden unhideWhenUsed qFormat locked personal personalCompose personalReply rsid pPr rPr tblPr trPr tcPr tblStylePr",
}
fails = []


def fail(msg):
    fails.append(msg)
    print("FAIL", msg)


def check_order(name, root):
    for e in root.iter():
        if not isinstance(e.tag, str) or not e.tag.startswith(W):
            continue
        local = e.tag[len(W):]
        if local in ORDER:
            order = ORDER[local].split()
            last = -1
            for c in e:
                if not isinstance(c.tag, str) or not c.tag.startswith(W):
                    continue
                k = c.tag[len(W):]
                if k not in order:
                    continue
                i = order.index(k)
                if i < last:
                    fail(f"{name}: <{local}> has {k} out of order: {[x.tag.split('}')[1] for x in e]}")
                    break
                last = i
        if local == "tc":
            kids = [c for c in e if isinstance(c.tag, str)]
            if not kids or kids[-1].tag != W + "p":
                fail(f"{name}: a cell does not end in a paragraph")
        if local == "t" and e.text and e.text != e.text.strip() and e.get("{http://www.w3.org/XML/1998/namespace}space") != "preserve":
            fail(f"{name}: spaces at the edge of {e.text!r} without xml:space")
    ids = [e.get("id") for e in root.iter(WP + "docPr")]
    if len(ids) != len(set(ids)):
        fail(f"{name}: repeated drawing ids {ids}")


def check_package(path):
    z = zipfile.ZipFile(path)
    if z.testzip():
        fail(f"{path}: damaged zip")
    names = set(z.namelist())
    ct = etree.fromstring(z.read("[Content_Types].xml"))
    defaults = {e.get("Extension").lower() for e in ct if e.tag.endswith("Default")}
    overrides = {e.get("PartName").lstrip("/") for e in ct if e.tag.endswith("Override")}
    for n in names:
        if n != "[Content_Types].xml" and n not in overrides and n.rsplit(".", 1)[-1].lower() not in defaults:
            fail(f"{path}: {n} has no content type")
    for o in overrides:
        if o not in names:
            fail(f"{path}: an override names missing part {o}")
    for n in names:
        if not n.endswith(".rels"):
            continue
        base = n.replace("_rels/", "").rsplit(".rels", 1)[0]
        folder = base.rsplit("/", 1)[0] if "/" in base else ""
        rels = etree.fromstring(z.read(n))
        rids = set()
        for r in rels:
            rids.add(r.get("Id"))
            if r.get("TargetMode") == "External":
                continue
            t = r.get("Target")
            full = t.lstrip("/") if t.startswith("/") else (folder + "/" + t if folder else t)
            parts = []
            for p in full.split("/"):
                if p == "..":
                    parts.pop()
                elif p != ".":
                    parts.append(p)
            if "/".join(parts) not in names:
                fail(f"{path}: {n} points at missing {t}")
        if base in names and base.endswith(".xml"):
            root = etree.fromstring(z.read(base))
            for e in root.iter():
                for k, v in e.attrib.items():
                    if k.startswith(R) and k.endswith(("id", "embed", "link")) and v not in rids:
                        fail(f"{base}: names relationship {v} which is not there")
    for n in names:
        if n.startswith("word/") and n.endswith(".xml") and "/theme/" not in n:
            check_order(f"{path}:{n}", etree.fromstring(z.read(n)))


def texts(doc):
    return [p.text for p in doc.paragraphs]


def main(folder):
    f = Path(folder)
    for name in ("blank.docx", "built.docx", "rich-edited.docx"):
        check_package(f / name)
    d = Document(f / "blank.docx")
    if len(d.paragraphs) != 1 or d.paragraphs[0].text != "":
        fail("blank: not one empty paragraph")
    for s in ("Normal", "Heading 1", "Heading 2", "Heading 3", "Title", "Table Grid"):
        if s not in [x.name for x in d.styles]:
            fail(f"blank: no style {s}")
    if abs(d.sections[0].page_width.cm - 21) > 0.01:
        fail("blank: not A4")

    d = Document(f / "built.docx")
    ps = d.paragraphs
    if ps[0].style.name != "Heading 1" or ps[0].text != "Heading":
        fail(f"built: heading {ps[0].style.name} {ps[0].text!r}")
    runs = {r.text: r for r in ps[1].runs}
    r = runs.get("text")
    if r is None or not (r.bold and r.italic and r.underline and r.font.strike) or r.font.size.pt != 14 or str(r.font.color.rgb) != "FF0000" or r.font.name != "Arial" or not r.font.superscript:
        fail(f"built: formatted run {[(x.text, x.bold) for x in ps[1].runs]}")
    if ps[1].paragraph_format.line_spacing != 1.5 or ps[1].paragraph_format.space_after.pt != 12 or ps[1].alignment != 3:
        fail("built: paragraph format")
    if "one" not in texts(d) or "three" not in texts(d) or "bullet" not in texts(d):
        fail(f"built: list items {texts(d)}")
    if len(d.tables) != 1 or len(d.tables[0].rows) != 4 or len(d.tables[0].columns) != 5:
        fail(f"built: table {len(d.tables)}")
    else:
        t = d.tables[0]
        if t.cell(1, 1)._tc is not t.cell(1, 2)._tc or t.cell(1, 1)._tc is not t.cell(2, 1)._tc:
            fail("built: the merge does not show")
    if len(d.inline_shapes) != 1:
        fail(f"built: pictures {len(d.inline_shapes)}")
    if "Header text" not in d.sections[0].header.paragraphs[0].text or d.sections[0].footer.paragraphs[0].text != "Footer":
        fail(f"built: header {d.sections[0].header.paragraphs[0].text!r}")
    x = zipfile.ZipFile(f / "built.docx").read("word/document.xml").decode("utf8")
    if "w:hyperlink" not in x or 'w:type="page"' not in x:
        fail("built: no link or page break")
    rels = zipfile.ZipFile(f / "built.docx").read("word/_rels/document.xml.rels").decode("utf8")
    if "https://example.com/x?a=1&amp;b=2" not in rels:
        fail("built: the link's address")

    a, b = Document(f / "rich.docx"), Document(f / "rich-edited.docx")
    ta, tb = texts(a), texts(b)
    want = []
    for t in ta:
        if t.startswith("Centred"):
            t = "EDITED " + t
        if t == "Step two":
            t = "Step 2"
        want.append(t)
        if t == "First bullet":
            want.append("Added bullet")
    if tb != want:
        for i, (x1, y1) in enumerate(zip(want, tb)):
            if x1 != y1:
                fail(f"rich: paragraph {i}: {x1!r} became {y1!r}")
                break
        else:
            fail(f"rich: {len(want)} paragraphs wanted, {len(tb)} found")
    if len(a.tables) != len(b.tables) or len(b.tables[0].rows) != len(a.tables[0].rows) + 1:
        fail("rich: the row was not added")
    if len(a.inline_shapes) != len(b.inline_shapes):
        fail("rich: pictures differ")
    for i, (x1, y1) in enumerate(zip(a.tables[1].rows, b.tables[1].rows)):
        if [c.text for c in x1.cells] != [c.text for c in y1.cells]:
            fail(f"rich: long table row {i} changed")
    za, zb = zipfile.ZipFile(f / "rich.docx"), zipfile.ZipFile(f / "rich-edited.docx")
    for n in za.namelist():
        if n in ("word/document.xml", "[Content_Types].xml"):
            continue
        if n not in zb.namelist():
            fail(f"rich: part {n} went")
        elif za.read(n) != zb.read(n):
            fail(f"rich: part {n} changed")
    x1, x2 = za.read("word/document.xml").decode("utf8"), zb.read("word/document.xml").decode("utf8")
    for tag in ("wp:anchor", "w:ins ", "w:del ", "m:oMath", "mc:AlternateContent", "w:txbxContent", "w:sectPr", "w:hyperlink", "w:fldChar", "w:instrText"):
        if x1.count("<" + tag) != x2.count("<" + tag):
            fail(f"rich: <{tag}> count {x1.count('<' + tag)} became {x2.count('<' + tag)}")
    print("FAILED" if fails else "all good")
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main(sys.argv[1])
