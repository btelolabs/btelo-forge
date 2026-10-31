"""Makes PDFs with matplotlib and Pillow for looking at the renderer: Type 3 and TrueType fonts, Gouraud meshes,
transparency, image masks, JPEG in RGB, grey and CMYK, and JPEG 2000. Usage: pdf_samples.py OUTDIR"""
import sys, os, io
import numpy as np
import matplotlib
matplotlib.use("pdf")
import matplotlib.pyplot as plt
from PIL import Image

out = sys.argv[1]
os.makedirs(out, exist_ok=True)


def fig(name, fonttype):
    matplotlib.rcParams["pdf.fonttype"] = fonttype
    plt.rcParams["font.sans-serif"] = ["Microsoft YaHei", "DejaVu Sans"]
    f, ax = plt.subplots(2, 2, figsize=(8, 8))
    x = np.linspace(0, 6, 200)
    ax[0, 0].plot(x, np.sin(x), "r-", label=r"$\sin(x)$")
    ax[0, 0].plot(x, np.cos(x), "b--", label="余弦 cos")
    ax[0, 0].legend()
    ax[0, 0].set_title(r"$\int_0^\infty e^{-x^2}dx=\frac{\sqrt{\pi}}{2}$ 中文")
    X, Y = np.meshgrid(np.linspace(0, 1, 30), np.linspace(0, 1, 30))
    ax[0, 1].pcolormesh(X, Y, np.sin(X * 6) * np.cos(Y * 6), shading="gouraud", cmap="viridis")
    ax[1, 0].imshow(np.random.RandomState(1).rand(16, 16), cmap="magma", interpolation="nearest")
    ax[1, 1].bar(range(5), [3, 5, 2, 7, 4], alpha=0.5, color="g", hatch="//")
    ax[1, 1].fill_between(range(5), [1, 2, 1, 3, 2], alpha=0.4)
    f.savefig(os.path.join(out, name))
    plt.close(f)


fig("mpl_type3.pdf", 3)
fig("mpl_type42.pdf", 42)

rs = np.random.RandomState(2)
w, h = 256, 180
xs, ys = np.meshgrid(np.linspace(0, 1, w), np.linspace(0, 1, h))
rgb = (np.dstack([xs, ys, 1 - xs]) * 255).astype("uint8")
base = Image.fromarray(rgb)
base.save(os.path.join(out, "img_rgb.pdf"), quality=90)
base.convert("L").save(os.path.join(out, "img_gray.pdf"))
base.convert("CMYK").save(os.path.join(out, "img_cmyk.pdf"))
base.convert("1").save(os.path.join(out, "img_bilevel.pdf"))
base.convert("P").save(os.path.join(out, "img_palette.pdf"))
rgba = base.convert("RGBA")
rgba.putalpha(Image.fromarray((xs * 255).astype("uint8")))
rgba.save(os.path.join(out, "img_rgba.pdf"))
try:
    base.save(os.path.join(out, "img_jp2.pdf"), format="PDF", resolution=72)
except Exception as e:
    print(e)
base.save(os.path.join(out, "img_multi.pdf"), save_all=True, append_images=[base.convert("L"), base.rotate(90, expand=True)])
