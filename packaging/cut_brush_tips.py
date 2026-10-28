"""Cuts the brush preset tips (internal/paint/tips) out of their sheets: three pictures of four marks each, in a 2x2
grid of black on white, drawn by Gemini (Nano Banana, gemini.google.com, 2026-10-28) from prompts asking for scanned
marks of the tools on plain white — pencil, charcoal, soft pastel, ink pen; sumi ink brush, flat bristle brush, round
bristle brush, dry fan brush; sea sponge, spray spatter, grunge, inked oval. Each mark's darkness above the paper's
grain becomes the tip's grey, which is what the tip lays down, cut to the mark and kept to at most 384 px.

The sheets are kept in packaging/brush-tips. Run it from the repository's root with Pillow and NumPy:
  python packaging/cut_brush_tips.py packaging/brush-tips/sheet-1.png packaging/brush-tips/sheet-2.png packaging/brush-tips/sheet-3.png internal/paint/tips
"""
import sys
from pathlib import Path

import numpy as np
from PIL import Image

NAMES = [
    ["pencil", "charcoal", "pastel", "ink-pen"],
    ["ink-brush", "flat-bristle", "round-bristle", "fan"],
    ["sponge", "splatter", "grunge", "ellipse"],
]
MAX_SIDE = 384


def grid_line(dark, axis):
    """The index of the dividing line: the row or column darkest across the whole sheet, near the middle."""
    prof = dark.mean(axis=axis)
    n = len(prof)
    lo, hi = n // 3, 2 * n // 3
    return lo + int(np.argmax(prof[lo:hi]))


def tip(dark):
    # The paper's grain: what the border ring of the cell holds, which no mark reaches.
    ring = np.concatenate([dark[:24].ravel(), dark[-24:].ravel(), dark[:, :24].ravel(), dark[:, -24:].ravel()])
    lo = float(np.percentile(ring, 99.9)) + 10
    hi = 215.0
    v = np.clip((dark - lo) / (hi - lo), 0, 1)
    ys, xs = np.nonzero(v > 0.02)
    pad = 3
    y0, y1 = max(ys.min() - pad, 0), min(ys.max() + pad + 1, v.shape[0])
    x0, x1 = max(xs.min() - pad, 0), min(xs.max() + pad + 1, v.shape[1])
    img = Image.fromarray((v[y0:y1, x0:x1] * 255 + 0.5).astype(np.uint8), "L")
    if max(img.size) > MAX_SIDE:
        k = MAX_SIDE / max(img.size)
        img = img.resize((max(1, round(img.width * k)), max(1, round(img.height * k))), Image.LANCZOS)
    return img, lo


def main(sheets, out):
    out = Path(out)
    out.mkdir(parents=True, exist_ok=True)
    for names, path in zip(NAMES, sheets):
        g = np.asarray(Image.open(path).convert("L"), dtype=np.float32)
        dark = 255 - g
        cy, cx = grid_line(dark, 1), grid_line(dark, 0)
        m = 8  # clear of the line and its blur
        cells = [dark[:cy - m, :cx - m], dark[:cy - m, cx + m:], dark[cy + m:, :cx - m], dark[cy + m:, cx + m:]]
        for name, cell in zip(names, cells):
            img, lo = tip(cell)
            img.save(out / f"{name}.png", optimize=True)
            print(f"{name}: {img.size} paper<{lo:.0f}")


if __name__ == "__main__":
    main(sys.argv[1:4], sys.argv[4])
