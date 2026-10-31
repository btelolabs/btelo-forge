"""Generates the JPEG 2000 fixtures with Pillow, whose encoder and decoder are OpenJPEG.

Run with: C:\\Python313\\python.exe -I gen_fixtures.py  (from this folder)
Each NAME.jp2/.j2k comes with NAME.raw, the pixels OpenJPEG decodes it to, and a line in
manifest.txt: file, width, height, components, lossless flag, PSNR floor against the source.
"""
import numpy as np
from PIL import Image

W, H = 48, 40


def source(mode, w=W, h=H):
    y, x = np.mgrid[0:h, 0:w]
    rng = np.random.default_rng(7)
    r = (x * 255 / (w - 1)).astype(np.uint8)
    g = (y * 255 / (h - 1)).astype(np.uint8)
    b = (128 + 100 * np.sin(x / 3.0) * np.cos(y / 4.0)).astype(np.uint8)
    # A hard edged block and a little noise exercise every coding pass.
    r[8:20, 10:26] = 250
    g[14:30, 20:40] = 12
    b ^= rng.integers(0, 8, (h, w), dtype=np.uint8)
    a = ((x + y) * 255 / (w + h - 2)).astype(np.uint8)
    if mode == "RGB":
        return Image.fromarray(np.dstack([r, g, b]), "RGB")
    if mode == "RGBA":
        return Image.fromarray(np.dstack([r, g, b, a]), "RGBA")
    if mode == "L":
        return Image.fromarray(g // 2 + b // 2, "L")
    if mode == "LA":
        return Image.fromarray(np.dstack([g // 2 + b // 2, a]), "LA")
    if mode == "I;16":
        v = (x * 900 + y * 300 + 37).astype(np.uint16)
        v[10:20, 10:30] = 60000
        return Image.fromarray(v, "I;16")
    raise ValueError(mode)


CASES = [
    # name, extension, mode, save options, lossless, PSNR floor
    ("rgb_lrcp", "jp2", "RGB", dict(progression="LRCP"), True, 0),
    ("rgb_rlcp", "j2k", "RGB", dict(progression="RLCP"), True, 0),
    ("rgb_rpcl", "j2k", "RGB", dict(progression="RPCL"), True, 0),
    ("rgb_pcrl", "j2k", "RGB", dict(progression="PCRL"), True, 0),
    ("rgb_cprl", "j2k", "RGB", dict(progression="CPRL"), True, 0),
    ("rgb_nomct", "jp2", "RGB", dict(mct=0), True, 0),
    ("gray", "jp2", "L", {}, True, 0),
    ("gray_res1", "j2k", "L", dict(num_resolutions=1), True, 0),
    ("gray_res6", "j2k", "L", dict(num_resolutions=6), True, 0),
    ("gray16", "jp2", "I;16", {}, True, 0),
    ("rgba", "jp2", "RGBA", {}, True, 0),
    ("la", "jp2", "LA", {}, True, 0),
    ("rgb_tiles", "j2k", "RGB", dict(tile_size=(16, 16)), True, 0),
    ("rgb_offset", "j2k", "RGB", dict(offset=(5, 3), tile_offset=(2, 1), tile_size=(20, 18)), True, 0),
    ("rgb_precincts", "j2k", "RGB",
     dict(progression="RPCL", codeblock_size=(16, 16), precinct_size=((32, 32), (16, 16), (16, 16))), True, 0),
    ("gray_precincts_pcrl", "j2k", "L",
     dict(progression="PCRL", num_resolutions=3, codeblock_size=(8, 32), precinct_size=((16, 8), (8, 8), (8, 8))), True, 0),
    ("rgb_layers", "j2k", "RGB", dict(quality_mode="rates", quality_layers=[8, 3, 1], progression="LRCP"), True, 0),
    ("rgb_lossy", "jp2", "RGB", dict(irreversible=True, quality_mode="rates", quality_layers=[2]), False, 34),
    ("gray_lossy_layers", "j2k", "L",
     dict(irreversible=True, quality_mode="rates", quality_layers=[8, 4, 2], progression="RLCP"), False, 32),
    ("rgb_lossy_tiles", "j2k", "RGB",
     dict(irreversible=True, quality_mode="rates", quality_layers=[3], tile_size=(24, 24), progression="CPRL"), False, 32),
]


def main():
    lines = []
    for name, ext, mode, opts, lossless, floor in CASES:
        im = source(mode)
        path = f"{name}.{ext}"
        fmt = "JPEG2000"
        im.save(path, fmt, **opts)
        back = Image.open(path)
        back.load()
        data = back.tobytes()
        if mode == "I;16":
            # Stored as 16 bit little endian; the codec gives 8 bit scaled samples.
            v = np.frombuffer(data, dtype=np.uint16).astype(np.uint32)
            data = ((v * 255 + 32767) // 65535).astype(np.uint8).tobytes()
        if lossless and name != "rgb_layers" and mode != "I;16":
            assert data == im.tobytes(), name
        with open(f"{name}.raw", "wb") as f:
            f.write(data)
        comps = len(im.getbands())
        lines.append(f"{path} {im.width} {im.height} {comps} {int(lossless)} {floor}")
        if not lossless:
            with open(f"{name}.src", "wb") as f:
                f.write(im.tobytes())
    with open("manifest.txt", "w", newline="\n") as f:
        f.write("\n".join(lines) + "\n")


if __name__ == "__main__":
    main()
