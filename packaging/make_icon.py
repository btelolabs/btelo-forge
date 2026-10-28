"""Makes Btelo Forge's icons: an anvil with a spark struck off it, one flat colour on a dark tile tinted with it, as
the symbols of the start page's cards are drawn.

  packaging/icon.png     the app icon, 1024 square with rounded, transparent corners: the source of the macOS icon
  docs/logo.png          the same at 512, for the README
  cmd/btelo-forge/winres/icon.png   the same at 256, for the Windows icon (go-winres)
  internal/ui/assets/logo.jpg       the mark as a square, which the app and its phone page round as they show it

Run it from the repository's root with Pillow: python packaging/make_icon.py
Then, in cmd/btelo-forge, remake the Windows resources from the new icon:
  go-winres simply --arch amd64,arm64 --manifest gui --product-version 0.1.0 --file-version 0.1.0
    --file-description "Btelo Forge" --product-name "Btelo Forge" --copyright "Copyright 2026 btelolabs"
    --original-filename "Btelo Forge.exe" --icon winres/icon.png
"""
from PIL import Image, ImageDraw

ROUNDING = 0.18  # ui.LogoRounding
HUE = (0xff, 0x8a, 0x3d)  # the forge's ember
PANEL, BORDER = (0x13, 0x14, 0x18), (0x25, 0x27, 0x2d)  # ui.colPanel, ui.colBorder
N, SS = 1024, 4  # the icon's side, and how many times larger the shapes are drawn before they are reduced


def mix(a, b, t):
    return tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def cubic(p0, p1, p2, p3, steps=48):
    return [tuple((1 - t) ** 3 * a + 3 * (1 - t) ** 2 * t * b + 3 * (1 - t) * t * t * c + t ** 3 * d
                  for a, b, c, d in zip(p0, p1, p2, p3))
            for t in (i / steps for i in range(1, steps + 1))]


def quad(p0, p1, p2, steps=32):
    return cubic(p0, tuple(a + (b - a) * 2 / 3 for a, b in zip(p0, p1)),
                 tuple(c + (b - c) * 2 / 3 for b, c in zip(p1, p2)), p2, steps)


# The symbol on a 24-unit grid, as Material Icons are drawn: the anvil with its horn to the left, and a four-pointed
# spark above its face.
ANVIL = [(2, 7.5), (21.5, 7.5), (21.5, 10), (18, 10), *quad((18, 10), (15.5, 10), (15.5, 12.5)),
         (15.5, 15), (18, 17), (18, 19.5), (6, 19.5), (6, 17), (8.5, 15), (8.5, 12.5),
         *cubic((8.5, 12.5), (6, 11.5), (3.5, 9.5), (2, 7.5))]


def spark(cx, cy, r):
    """A four-pointed star whose sides curve inwards."""
    pts = []
    for (dx, dy), (ex, ey) in zip([(0, -1), (1, 0), (0, 1), (-1, 0)], [(1, 0), (0, 1), (-1, 0), (0, -1)]):
        pts += quad((cx + dx * r, cy + dy * r), (cx + (dx + ex) * r * 0.16, cy + (dy + ey) * r * 0.16),
                    (cx + ex * r, cy + ey * r))
    return pts


SPARK = spark(15.5, 3.4, 2.9)
GRID_BOX = (2, 0.5, 21.5, 19.5)  # what the symbol covers, centred on the tile
SYMBOL = 0.66  # the symbol's width as a share of the tile's


def mark():
    """The icon as an opaque square: the tinted tile, its hairline and the symbol."""
    S = N * SS
    img = Image.new('RGB', (S, S), mix(PANEL, HUE, 0.1))
    d = ImageDraw.Draw(img)
    line = round(S * 0.008)
    d.rounded_rectangle((line // 2, line // 2, S - 1 - line // 2, S - 1 - line // 2), radius=round(S * ROUNDING),
                        outline=mix(BORDER, HUE, 0.35), width=line)
    x0, y0, x1, y1 = GRID_BOX
    k = S * SYMBOL / (x1 - x0)
    ox, oy = (S - (x1 - x0) * k) / 2 - x0 * k, (S - (y1 - y0) * k) / 2 - y0 * k
    for shape in (ANVIL, SPARK):
        d.polygon([(ox + x * k, oy + y * k) for x, y in shape], fill=HUE)
    return img.resize((N, N), Image.LANCZOS)


icon = mark()
icon.resize((512, 512), Image.LANCZOS).save('internal/ui/assets/logo.jpg', quality=92, optimize=True)
# Rounded corners, drawn four times larger and reduced, so their edge is smooth.
big = Image.new('L', (N * 4, N * 4), 0)
ImageDraw.Draw(big).rounded_rectangle((0, 0, N * 4 - 1, N * 4 - 1), radius=round(N * 4 * ROUNDING), fill=255)
icon = icon.convert('RGBA')
icon.putalpha(big.resize((N, N), Image.LANCZOS))
icon.save('packaging/icon.png', optimize=True)
icon.resize((512, 512), Image.LANCZOS).save('docs/logo.png', optimize=True)
icon.resize((256, 256), Image.LANCZOS).save('cmd/btelo-forge/winres/icon.png', optimize=True)
