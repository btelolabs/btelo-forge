"""Makes Btelo Forge's icons from the brand picture: the emblem, cut out, on a dark rounded square of the picture's
own metal.

  packaging/brand.jpg    the brand picture
  packaging/emblem.png   its emblem with the background taken away (BiRefNet, from the picture's area 455,55-955,535)
  packaging/icon.png     the app icon, 1024 square with rounded, transparent corners: the source of the macOS icon
  docs/logo.png          the same at 512, for the README
  cmd/btelo-forge/winres/icon.png   the same at 256, for the Windows icon (go-winres)
  internal/ui/assets/logo.jpg       the mark as a square, which the app and its phone page round as they show it

Run it from the repository's root with Pillow: python packaging/make_icon.py
"""
from PIL import Image, ImageDraw, ImageFilter

SRC, CUT = 'packaging/brand.jpg', 'packaging/emblem.png'
ROUNDING = 0.18  # ui.LogoRounding

N = 1024
brand = Image.open(SRC).convert('RGB')
# The metal: a stretch of the picture's background with nothing on it.
metal = brand.crop((20, 180, 440, 600)).resize((N, N), Image.LANCZOS)
# Darker towards the corners, as a lit plate is.
shade = Image.new('L', (N, N), 0)
ImageDraw.Draw(shade).ellipse((-N * 0.25, -N * 0.25, N * 1.25, N * 1.25), fill=255)
shade = shade.filter(ImageFilter.GaussianBlur(N * 0.12))
dark = Image.new('RGB', (N, N), (8, 9, 10))
plate = Image.composite(metal, dark, shade)

emblem = Image.open(CUT).convert('RGBA')
emblem = emblem.crop(emblem.getbbox())
scale = min(N * 0.80 / emblem.width, N * 0.78 / emblem.height)
emblem = emblem.resize((round(emblem.width * scale), round(emblem.height * scale)), Image.LANCZOS)
icon = plate.convert('RGBA')
icon.alpha_composite(emblem, ((N - emblem.width) // 2, (N - emblem.height) // 2 + round(N * 0.01)))

icon.convert('RGB').resize((512, 512), Image.LANCZOS).save('internal/ui/assets/logo.jpg', quality=92, optimize=True)
# Rounded corners, drawn four times larger and reduced, so their edge is smooth.
big = Image.new('L', (N * 4, N * 4), 0)
ImageDraw.Draw(big).rounded_rectangle((0, 0, N * 4 - 1, N * 4 - 1), radius=round(N * 4 * ROUNDING), fill=255)
icon.putalpha(big.resize((N, N), Image.LANCZOS))
icon.save('packaging/icon.png', optimize=True)
icon.resize((512, 512), Image.LANCZOS).save('docs/logo.png', optimize=True)
icon.resize((256, 256), Image.LANCZOS).save('cmd/btelo-forge/winres/icon.png', optimize=True)
