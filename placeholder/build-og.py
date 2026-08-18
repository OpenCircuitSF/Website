#!/usr/bin/env python3
"""Regenerate the Open Graph share card for the placeholder site.

    python3 placeholder/build-og.py

Requires Pillow and numpy. Run from the repo root. Writes placeholder/og-default.png
at 1200x630.

This is a stand-in set in Arial Black. Issue #0021 replaces it once the real
display face (Archivo) is available. Text is laid out from measured widths and
the script asserts nothing overflows the right margin, because the first two
hand-positioned versions collided.
"""
from __future__ import annotations

import pathlib
import sys

try:
    import numpy as np
    from PIL import Image, ImageDraw, ImageFont
except ImportError:  # pragma: no cover
    sys.exit("needs Pillow and numpy:  pip install pillow numpy")

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "placeholder" / "og-default.png"
LOGO_MASK = ROOT / "assets" / "logo" / "logo-mask-512.png"

W, H, MARGIN = 1200, 630, 70
COL_X = 530  # left edge of the text column

BG = (0x0A, 0x0D, 0x0B)
GREEN = (0x68, 0xFF, 0x23)
TEXT = (0xE8, 0xF0, 0xE8)
MUTED = (0x9A, 0xA7, 0x9E)
FAINT = (0x4E, 0x65, 0x53)
RULE = (0x24, 0x30, 0x28)

BLACK_TTF = "/System/Library/Fonts/Supplemental/Arial Black.ttf"
ARIAL_TTF = "/System/Library/Fonts/Supplemental/Arial.ttf"
MENLO_TTC = "/System/Library/Fonts/Menlo.ttc"

OK_LABEL = "beginners welcome · tools provided"
COMMAND = "$ opencircuitsf.com"


def main() -> None:
    for f in (BLACK_TTF, ARIAL_TTF, MENLO_TTC, LOGO_MASK):
        if not pathlib.Path(f).exists():
            sys.exit(f"missing dependency: {f}")

    im = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(im)
    f_disp = ImageFont.truetype(BLACK_TTF, 84)
    f_sf = ImageFont.truetype(BLACK_TTF, 44)
    f_mono = ImageFont.truetype(MENLO_TTC, 30)
    f_sub = ImageFont.truetype(ARIAL_TTF, 32)

    # Logo, tinted from the white alpha mask.
    mask = Image.open(LOGO_MASK).convert("RGBA")
    a = np.asarray(mask)[..., 3]
    t = np.zeros(a.shape + (4,), np.uint8)
    t[..., 0], t[..., 1], t[..., 2] = GREEN
    t[..., 3] = a
    logo = Image.fromarray(t, "RGBA").resize((400, 400), Image.LANCZOS)
    im.paste(logo, (MARGIN, 115), logo)

    d.text((COL_X, 120), "> open_circuit_sf", font=f_mono, fill=GREEN)
    d.text((COL_X, 175), "OPEN", font=f_disp, fill=TEXT)
    d.text((COL_X, 262), "CIRCUIT", font=f_disp, fill=TEXT)
    d.text((COL_X + d.textlength("CIRCUIT", font=f_disp) + 12, 300), "_SF", font=f_sf, fill=FAINT)
    d.text((COL_X, 375), "Hands-on electronics workshops", font=f_sub, fill=MUTED)
    d.text((COL_X, 415), "in San Francisco", font=f_sub, fill=MUTED)

    # Shrink the [ OK ] row until it fits rather than trusting a fixed size.
    avail = (W - MARGIN) - COL_X
    size = 26
    while size > 14:
        f_ok = ImageFont.truetype(MENLO_TTC, size)
        if d.textlength("[ OK ] " + OK_LABEL, font=f_ok) <= avail:
            break
        size -= 1
    f_ok = ImageFont.truetype(MENLO_TTC, size)
    d.text((COL_X, 482), "[ OK ]", font=f_ok, fill=GREEN)
    d.text((COL_X + d.textlength("[ OK ] ", font=f_ok), 482), OK_LABEL, font=f_ok, fill=MUTED)
    assert COL_X + d.textlength("[ OK ] " + OK_LABEL, font=f_ok) < W - MARGIN, "[ OK ] row overflows"

    d.line([(COL_X, 545), (W - MARGIN, 545)], fill=RULE, width=1)
    d.text((COL_X, 565), COMMAND, font=f_mono, fill=TEXT)
    cx = COL_X + d.textlength(COMMAND, font=f_mono) + 6
    d.rectangle([cx, 568, cx + 14, 595], fill=GREEN)
    assert cx + 14 < W - MARGIN, "cursor overflows"

    im.save(OUT, optimize=True)
    print(f"wrote {OUT} ({OUT.stat().st_size / 1024:.1f} KB), [ OK ] row fitted at {size}px")


if __name__ == "__main__":
    main()
