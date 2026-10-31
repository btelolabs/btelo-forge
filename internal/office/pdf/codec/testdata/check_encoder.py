"""Reads the test encoder's streams with OpenJPEG, to prove they are conforming.

Go side: set JPX_DUMP to a folder and run go test -run TestJPXDump.
Then: C:\\Python313\\python.exe -I check_encoder.py FOLDER
"""
import glob
import os
import sys

import numpy as np
from PIL import Image


def main():
    failed = 0
    for path in sorted(glob.glob(os.path.join(sys.argv[1], "*.j2k"))):
        name = os.path.basename(path)
        if name == "styles_sub.j2k":
            continue  # Pillow turns subsampled streams into RGB as sYCC; compare planes with ffmpeg
        with open(path[:-4] + ".plain", "rb") as f:
            want = np.frombuffer(f.read(), dtype=np.uint8)
        im = Image.open(path)
        im.load()
        got = np.frombuffer(im.tobytes(), dtype=np.uint8)
        if got.size != want.size:
            print(f"{name}: {im.mode} {im.size}, {got.size} bytes, want {want.size}")
            failed += 1
            continue
        diff = np.abs(got.astype(int) - want.astype(int))
        mse = float((diff**2).mean())
        psnr = 99.0 if mse == 0 else 10 * np.log10(255 * 255 / mse)
        print(f"{name}: {im.mode} {im.size} max difference {diff.max()}, PSNR {psnr:.1f}")
        if diff.max() != 0 and "lossy" not in name:
            failed += 1
    print("FAILED" if failed else "all identical")


if __name__ == "__main__":
    main()
