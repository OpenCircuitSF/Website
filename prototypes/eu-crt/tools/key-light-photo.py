#!/usr/bin/env python3
"""Recover an alpha channel for eu-crt-light.png.

    python3 prototypes/eu-crt/tools/key-light-photo.py
    python3 prototypes/eu-crt/tools/key-light-photo.py --preview   # also write a
                                                                   # magenta check

WHY THIS EXISTS

The two photographs arrived in different states. eu-crt-dark.png is RGBA with a
real alpha channel — 36% of it is genuinely transparent. eu-crt-light.png is
RGB, fully opaque, with the editor's transparency CHECKERBOARD baked into the
pixels: somebody exported a screenshot of the canvas rather than the canvas.
Dropped onto the page as-is it renders the checkerboard, which reads as a
broken image rather than as a bad export.

So the checkerboard is keyed back out here. If a properly exported RGBA
original ever turns up, delete this script and the DERIVED note in build.py and
convert it the same way the dark one is converted.

HOW THE KEY WORKS, AND WHY NOT A FLOOD FILL

The obvious approach — flood fill inward from the border over "neutral and
bright" pixels — was tried first and leaks. The case is cream (243,229,207:
red minus blue is 36, comfortably non-neutral) but its specular highlights are
near-white and near-neutral, and they touch the white checker squares directly,
so the fill walks straight through them and eats a stripe out of the top of the
case. Connectivity is the wrong tool: it has no way to tell "white because it
is background" from "white because it is a highlight touching background".

What works is the checkerboard's own structure. Its squares are ~16px and the
mid-grey ones (around 175) alternate with the white ones in BOTH axes, so a
background pixel has a grey square within ~20px in at least three of the four
cardinal directions. A highlight pixel on the edge of the case has one at most
— the side facing outward. That test is local rather than connected, which also
means it catches the enclosed pockets of background between the stand legs that
a border flood fill can never reach.

A morphological close then fills the few speckles left where a highlight
happens to satisfy the direction test, and a one-pixel erode plus a slight
feather takes the hard white fringe off the silhouette, which is composited
into the source pixels and cannot be recovered.
"""
import pathlib, sys

import numpy as np
from PIL import Image, ImageFilter

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
SRC  = ROOT / "eu-crt-light.png"
OUT  = ROOT / "assets" / "eu-crt-light.webp"

# The checkerboard's period is ~16px, so a 20px reach always finds the
# neighbouring square without reaching across the whole subject.
REACH = 20
# Three of four directions. Four would reject the outermost ring of background
# against the image edge; two lets a case highlight in.
MIN_DIRECTIONS = 3


def near(mask: np.ndarray, axis: int, sign: int, reach: int) -> np.ndarray:
    """True where `mask` is set within `reach` pixels along one direction.

    Off-image counts as set, so background against the border of the picture
    still satisfies the direction test."""
    h, w = mask.shape
    out = np.zeros_like(mask)
    for i in range(1, reach + 1):
        cur = np.roll(mask, i * sign, axis=axis)
        if axis == 0:
            if sign > 0: cur[:i] = True
            else:        cur[h - i:] = True
        else:
            if sign > 0: cur[:, :i] = True
            else:        cur[:, w - i:] = True
        out |= cur
    return out


def main(preview: bool) -> None:
    if not SRC.is_file():
        raise SystemExit(f"missing {SRC}")
    rgb = np.asarray(Image.open(SRC).convert("RGB")).astype(np.int16)
    hi, lo = rgb.max(2), rgb.min(2)

    neutral = (hi - lo) <= 12
    bright  = neutral & (lo >= 130)                      # either checker square
    grey    = neutral & (lo >= 145) & (hi <= 210)        # the mid-grey ones only

    hits = (near(grey, 0,  1, REACH).astype(np.uint8)
          + near(grey, 0, -1, REACH)
          + near(grey, 1,  1, REACH)
          + near(grey, 1, -1, REACH))
    background = bright & (hits >= MIN_DIRECTIONS)

    alpha = Image.fromarray(np.where(background, 0, 255).astype(np.uint8), "L")
    alpha = (alpha
             .filter(ImageFilter.MaxFilter(5))    # close: fill speckle holes
             .filter(ImageFilter.MinFilter(5))
             .filter(ImageFilter.MinFilter(3))    # erode 1px off the fringe
             .filter(ImageFilter.GaussianBlur(0.7)))

    img = Image.fromarray(np.dstack([rgb.astype(np.uint8), np.asarray(alpha)]), "RGBA")
    img.save(OUT, "WEBP", quality=88, method=6)
    cut = 100 * (np.asarray(alpha) < 128).mean()
    print(f"wrote {OUT.relative_to(ROOT.parent)}  "
          f"({OUT.stat().st_size // 1024} KB, {cut:.1f}% transparent)")

    if preview:
        # Over magenta, because a remaining scrap of checkerboard and a chewed
        # edge are both invisible against white and obvious against this.
        small = img.resize((img.width // 2, img.height // 2))
        flat = Image.new("RGB", small.size, (255, 0, 140))
        flat.paste(small, (0, 0), small)
        p = ROOT / "tools" / "key-preview.png"
        flat.save(p)
        print(f"wrote {p.relative_to(ROOT.parent)}")


if __name__ == "__main__":
    main("--preview" in sys.argv[1:])
