"""Checks a PDF exported by Btelo Forge: what is in it, and pictures of its pages to look at.

Usage: python print_check.py OUT_DIR file.pdf [file.pdf...]
Uses PyMuPDF (pip install pymupdf) in the office-check venv.
"""
import os
import sys

import fitz

sys.stdout.reconfigure(encoding="utf-8")


def main():
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    for path in sys.argv[2:]:
        doc = fitz.open(path)
        name = os.path.splitext(os.path.basename(path))[0]
        print(f"{name}: {doc.page_count} pages, {os.path.getsize(path)} bytes")
        for i, page in enumerate(doc):
            images = page.get_images(full=True)
            drawings = page.get_drawings()
            text = page.get_text().strip()
            fonts = page.get_fonts()
            kinds = {f[2] for f in fonts}
            print(f"  page {i + 1}: {page.rect.width:.1f} x {page.rect.height:.1f} pt, "
                  f"{len(images)} images, {len(drawings)} drawings, {len(text)} chars, fonts {sorted(kinds)}")
            if text:
                print("    text:", " ".join(text.split())[:110])
            page.get_pixmap(dpi=90).save(os.path.join(out, f"{name}-{i + 1}.png"))


if __name__ == "__main__":
    main()
