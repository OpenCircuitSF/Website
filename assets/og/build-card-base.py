#!/usr/bin/env python3
"""Regenerate the pre-rendered base card internal/seo embeds for the
per-workshop Open Graph card (#0273).

    python3 assets/og/build-card-base.py

Requires Pillow and numpy (the same subset build-og.py already needs, minus
fontTools -- this script draws no text at all):

    pip install pillow numpy

Run from the repo root. Writes internal/seo/cardassets/base-card.png at
1200x630 -- the dark ground and the tinted logo mark, plus the one rule that
never moves regardless of workshop content. Every workshop-specific pixel
(title, date/venue, the "$ opencircuitsf.com" command line) is composed on
top of this at REQUEST time by internal/seo/card.go, using
golang.org/x/image/font/opentype -- see that file's doc comment for why text
is not baked in here: three of a workshop's four fields are admin-authored
and change per slug, so decoding this once at process start and drawing text
per request is the whole point of the design (#0273's plan §1).

Coordinates here are load-bearing: card.go's cardMargin, cardLogoSize, and
cardRuleY constants must match this script's MARGIN, LOGO_SIZE, and RULE_Y
exactly, or the baked-in logo/rule and the request-time text will not line
up. Keep the two in sync by hand (there are only three shared numbers) rather
than generating one from the other -- go:embed cannot read Python constants
and there is no third file worth inventing to hold three integers.
"""
from __future__ import annotations

import pathlib
import sys

try:
    import numpy as np
    from PIL import Image
except ImportError:  # pragma: no cover
    sys.exit("needs pillow and numpy:  pip install pillow numpy")

ROOT = pathlib.Path(__file__).resolve().parent.parent.parent
OUT = ROOT / "internal" / "seo" / "cardassets" / "base-card.png"
LOGO_MASK = ROOT / "assets" / "logo" / "mark-mask-512.png"

W, H = 1200, 630
MARGIN = 70
LOGO_SIZE = 120  # above CLAUDE.md §8's ~96px illegibility floor for the mark asset
RULE_Y = 540  # must match card.go's cardRuleY

BG = (0x0A, 0x0D, 0x0B)
GREEN = (0x68, 0xFF, 0x23)  # 14.8:1 on BG (CLAUDE.md §8) -- never the 1.32:1 light-ground case
RULE = (0x24, 0x30, 0x28)


def main() -> None:
    if not LOGO_MASK.exists():
        sys.exit(f"missing dependency: {LOGO_MASK}")

    im = Image.new("RGB", (W, H), BG)

    mask = Image.open(LOGO_MASK).convert("RGBA")
    a = np.asarray(mask)[..., 3]
    tinted = np.zeros(a.shape + (4,), np.uint8)
    tinted[..., 0], tinted[..., 1], tinted[..., 2] = GREEN
    tinted[..., 3] = a
    logo = Image.fromarray(tinted).resize((LOGO_SIZE, LOGO_SIZE), Image.LANCZOS)
    im.paste(logo, (MARGIN, MARGIN), logo)

    px = im.load()
    for x in range(MARGIN, W - MARGIN):
        px[x, RULE_Y] = RULE

    assert im.size == (W, H), "canvas size drifted"
    im.save(OUT, optimize=True)
    kb = OUT.stat().st_size / 1024
    print(f"wrote {OUT} ({kb:.1f} KB)")


if __name__ == "__main__":
    main()
