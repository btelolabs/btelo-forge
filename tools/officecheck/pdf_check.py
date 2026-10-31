"""Independent check of the PDF code with pypdf. It makes rich files with pypdf, has the Go code edit them, and confirms the
edits took and nothing else changed. Usage (from the repository): pdf_check.py"""
import os, shutil, subprocess, sys, tempfile
from pypdf import PdfReader, PdfWriter
from pypdf.annotations import FreeText as PFree
from pypdf.generic import (ArrayObject, BooleanObject, DecodedStreamObject, DictionaryObject, FloatObject, NameObject, NumberObject,
                           TextStringObject)

work = tempfile.mkdtemp(prefix="pdfcheck")


def text_page(w, words):
    p = w.add_blank_page(612, 792)
    s = DecodedStreamObject()
    s.set_data(("BT /F1 18 Tf 72 700 Td (%s) Tj ET" % words).encode())
    p[NameObject("/Contents")] = w._add_object(s)
    p[NameObject("/Resources")] = DictionaryObject({NameObject("/Font"): DictionaryObject({NameObject("/F1"): DictionaryObject({
        NameObject("/Type"): NameObject("/Font"), NameObject("/Subtype"): NameObject("/Type1"), NameObject("/BaseFont"): NameObject("/Helvetica")})})})
    return p


def rich():
    w = PdfWriter()
    for i in range(3):
        text_page(w, "Page number %d of the rich file" % (i + 1))
    w.add_outline_item("Start", 0)
    w.add_outline_item("Middle", 1)
    w.add_outline_item("End", 2)
    w.add_metadata({"/Title": "Rich file", "/Author": "pypdf", "/Custom": "kept"})
    w.add_attachment("notes.txt", b"attached bytes")
    w.add_annotation(0, PFree(text="existing note", rect=(50, 50, 200, 100)))
    w.write(os.path.join(work, "rich.pdf"))


def form():
    w = PdfWriter()
    p = text_page(w, "A form")
    t = DictionaryObject({NameObject("/FT"): NameObject("/Tx"), NameObject("/T"): TextStringObject("who"), NameObject("/V"): TextStringObject(""),
                          NameObject("/Type"): NameObject("/Annot"), NameObject("/Subtype"): NameObject("/Widget"),
                          NameObject("/Rect"): ArrayObject([FloatObject(72), FloatObject(600), FloatObject(300), FloatObject(630)]),
                          NameObject("/DA"): TextStringObject("/Helv 12 Tf 0 g"), NameObject("/F"): NumberObject(4)})
    off, on = DecodedStreamObject(), DecodedStreamObject()
    for s, body in ((off, b"0 0 20 20 re S"), (on, b"0 0 20 20 re S 2 2 16 16 re f")):
        s.set_data(body)
        s.update({NameObject("/Type"): NameObject("/XObject"), NameObject("/Subtype"): NameObject("/Form"),
                  NameObject("/BBox"): ArrayObject([FloatObject(0), FloatObject(0), FloatObject(20), FloatObject(20)])})
    c = DictionaryObject({NameObject("/FT"): NameObject("/Btn"), NameObject("/T"): TextStringObject("yes"), NameObject("/V"): NameObject("/Off"),
                          NameObject("/AS"): NameObject("/Off"), NameObject("/Type"): NameObject("/Annot"), NameObject("/Subtype"): NameObject("/Widget"),
                          NameObject("/Rect"): ArrayObject([FloatObject(72), FloatObject(560), FloatObject(92), FloatObject(580)]),
                          NameObject("/F"): NumberObject(4),
                          NameObject("/AP"): DictionaryObject({NameObject("/N"): DictionaryObject({NameObject("/Yes"): w._add_object(on), NameObject("/Off"): w._add_object(off)})})})
    tr, cr = w._add_object(t), w._add_object(c)
    p[NameObject("/Annots")] = ArrayObject([tr, cr])
    font = DictionaryObject({NameObject("/Type"): NameObject("/Font"), NameObject("/Subtype"): NameObject("/Type1"), NameObject("/BaseFont"): NameObject("/Helvetica")})
    w._root_object[NameObject("/AcroForm")] = w._add_object(DictionaryObject({NameObject("/Fields"): ArrayObject([tr, cr]),
                                                                             NameObject("/DA"): TextStringObject("/Helv 12 Tf 0 g"),
                                                                             NameObject("/DR"): DictionaryObject({NameObject("/Font"): DictionaryObject({NameObject("/Helv"): font})})}))
    w.write(os.path.join(work, "form.pdf"))


rich()
form()
env = dict(os.environ, FORGE_OFFICECHECK=work)
subprocess.run(["go", "test", "./internal/office/pdf/render", "-run", "TestOfficeCheck", "-count=1"], check=True, env=env)


def read(name):
    r = PdfReader(os.path.join(work, name), strict=True)
    # Reading every object is what a strict reader does eventually.
    for p in r.pages:
        p.extract_text()
        c = p.get_contents()
        if c is not None:
            c.get_data()
    return r


def texts(r):
    return [p.extract_text().strip() for p in r.pages]


ok = 0


def check(cond, what):
    global ok
    if not cond:
        print("FAILED:", what)
        sys.exit(1)
    ok += 1


orig = read("rich.pdf")
orig_bytes = open(os.path.join(work, "rich.pdf"), "rb").read()

e = read("edited.pdf")
edited_bytes = open(os.path.join(work, "edited.pdf"), "rb").read()
check(edited_bytes.startswith(orig_bytes), "the incremental save kept the original bytes")
check(len(e.pages) == 3, "pages kept")
check("你好世界" in e.pages[0].extract_text(), "added Chinese text reads through ToUnicode: " + repr(e.pages[0].extract_text()))
check("added text" in e.pages[0].extract_text(), "added Latin text reads")
check(e.pages[1].extract_text() == orig.pages[1].extract_text(), "page 2's words kept: covered, not removed")
annots = e.pages[0]["/Annots"]
kinds = [a.get_object()["/Subtype"] for a in annots]
for k in ["/Highlight", "/Underline", "/StrikeOut", "/Ink", "/Square", "/Circle", "/Line", "/Text", "/FreeText"]:
    check(k in kinds, "annotation " + k)
for a in annots:
    a = a.get_object()
    if a["/Subtype"] != "/FreeText" or a.get("/Contents") != "existing note":
        check("/AP" in a and "/N" in a["/AP"], "an appearance on " + a["/Subtype"])
        n = a["/AP"]["/N"].get_object()
        check(n["/Subtype"] == "/Form" and len(n.get_data()) > 0, "the appearance is a form")
check(any(a.get_object().get("/Contents") == "existing note" for a in annots), "the old annotation stayed")
check(e.pages[2].get("/Rotate") == 90, "rotation")
check(e.metadata.get("/Custom") == "kept" and e.metadata.title == "Rich file", "metadata kept")
check(e.attachments.get("notes.txt") == [b"attached bytes"], "attachment kept")
check([o.title for o in e.outline] == ["Start", "Middle", "End"], "outline kept")
check(e.get_destination_page_number(e.outline[2]) == 2, "outline target kept")

d = read("deleted.pdf")
check(len(d.pages) == 2, "a page deleted")
check(texts(d) == [texts(orig)[0], texts(orig)[2]], "the right page deleted")
check(b"number 2" not in open(os.path.join(work, "deleted.pdf"), "rb").read(), "its content left the file")
check(d.metadata.get("/Custom") == "kept" and d.attachments.get("notes.txt") == [b"attached bytes"], "the rewrite kept metadata and attachments")
check(d.get_destination_page_number(d.outline[2]) == 1, "the outline follows the pages")

m = read("merged.pdf")
check(len(m.pages) == 6, "merged")
check(texts(m)[1:4] == texts(orig), "merged pages in place")
check(len(m.outline) == 6 and m.get_destination_page_number(m.outline[5]) == 3, "merged outline: %r" % m.outline)

r = read("reordered.pdf")
check(len(r.pages) == 4 and r.pages[1].mediabox.width == 300, "a blank page in place")
check(texts(r)[0] == texts(orig)[1] and texts(r)[3] == texts(orig)[0], "pages moved")
check(r.get_destination_page_number(r.outline[0]) == 3, "the outline followed the move")
check(open(os.path.join(work, "reordered.pdf"), "rb").read().startswith(orig_bytes), "reordering appends")

c = read("compressed.pdf")
check(texts(c) == texts(orig), "compressed text")
check(c.attachments.get("notes.txt") == [b"attached bytes"], "compressed attachment")
check(len(c.outline) == 3, "compressed outline")

ce = read("compressed-edited.pdf")
check(texts(ce) == texts(orig) and any(a.get_object()["/Subtype"] == "/Square" for a in ce.pages[0]["/Annots"]), "an update over cross-reference streams")
check(open(os.path.join(work, "compressed-edited.pdf"), "rb").read().startswith(open(os.path.join(work, "compressed.pdf"), "rb").read()), "and it appended")

x = read("extracted.pdf")
check(texts(x) == [texts(orig)[0], texts(orig)[2]], "extracted")

f = read("form-filled.pdf")
fields = f.get_fields()
check(fields["who"]["/V"] == "filled 中文", "text field: %r" % fields["who"].get("/V"))
check(fields["yes"]["/V"] == "/Yes", "check box")
w = f.pages[0]["/Annots"][0].get_object()
check("/AP" in w, "a filled field has an appearance")
print(ok, "checks passed;", work)
shutil.rmtree(work, ignore_errors=True)
