"""Compares real documents with what the app saved of them after one small edit (TestRealDocuments).

    python real_check.py <originals> <saved>

Reads both with independent libraries; the edit itself is left out of the comparison. Needs openpyxl, python-docx and
python-pptx. Prints one line a file and exits non-zero when anything differs.
"""
import sys
import zipfile
from pathlib import Path

import docx
import openpyxl
from pptx import Presentation

sys.stdout.reconfigure(encoding="utf-8")


def cells(path):
    wb = openpyxl.load_workbook(path)
    out = {}
    for s in wb.worksheets:
        for row in s.iter_rows():
            for c in row:
                if c.value is not None and c.value != "edited":
                    out[s.title, c.coordinate] = (c.value, c.number_format, c.font.b, c.fill.fgColor.rgb)
    return out


def words(path):
    d = docx.Document(path)
    text = [p.text for p in d.paragraphs]
    text[0:1] = [text[0].replace("edited ", "", 1)] if text else []
    for t in d.tables:
        for r in t.rows:
            text += [c.text for c in r.cells]
    return text


def slides(path):
    p = Presentation(path)
    out = []
    for s in p.slides:
        for sh in s.shapes:
            out.append((sh.shape_type, sh.left, sh.top, sh.width, sh.height, sh.text_frame.text if sh.has_text_frame else ""))
    return out


def parts(path):
    """Everything that should come back byte for byte: pictures, charts, embedded files."""
    with zipfile.ZipFile(path) as z:
        return {n: z.read(n) for n in z.namelist() if "/media/" in n or "/charts/" in n or "/embeddings/" in n}


def main():
    bad = 0
    for saved in sorted(Path(sys.argv[2]).iterdir()):
        original = Path(sys.argv[1]) / saved.name
        read = {".xlsx": cells, ".xlsm": cells, ".docx": words, ".pptx": slides}.get(saved.suffix.lower())
        if read is None:
            continue
        try:
            a, b = read(original), read(saved)
            same = a == b
            lost = sorted(set(parts(original).items()) ^ set(parts(saved).items()))
        except Exception as e:
            print(f"BAD  {saved.name}: {e!r}")
            bad += 1
            continue
        if same and not lost:
            print(f"ok   {saved.name}: {len(a)} items")
            continue
        bad += 1
        print(f"DIFF {saved.name}: content {'same' if same else 'differs'}, parts changed {[n for n, _ in lost][:5]}")
        if not same and isinstance(a, dict):
            for k in [k for k in a if a.get(k) != b.get(k)][:5]:
                print("    ", k, a.get(k), "->", b.get(k))
        elif not same:
            for x, y in [(x, y) for x, y in zip(a, b) if x != y][:5]:
                print("    ", x, "->", y)
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
