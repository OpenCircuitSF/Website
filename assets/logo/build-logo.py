#!/usr/bin/env python3
"""Regenerate every derived logo asset from the source master.

    python3 assets/logo/build-logo.py

Requires Pillow and numpy (`pip install pillow numpy`). Run from the repo root.

Reads  assets/logo/source/Logo-2026.png  (green artwork on solid black)
Writes assets/logo/*.png                 (transparent, tinted, and mask variants)

See assets/logo/README.md for the contrast measurements and CSS usage that
motivate the two tints, and issue #0018 for the remaining vector-trace work.
"""
from __future__ import annotations

import pathlib
import sys

try:
    import numpy as np
    from PIL import Image
except ImportError:  # pragma: no cover
    sys.exit("needs Pillow and numpy:  pip install pillow numpy")

OUT = pathlib.Path(__file__).resolve().parent
SRC = OUT / "source" / "Logo-2026.png"

# Luminance key. The source background sits at luminance <= 2 for 75% of its
# pixels, so a low black point removes it cleanly. The white point is set just
# below the artwork's solid-body plateau (~205) so the body reads as opaque
# while the anti-aliasing ramp below it keeps its partial alpha — that is what
# prevents a black fringe when the logo is composited onto a light surface.
BLACK_PT, WHITE_PT = 6.0, 212.0

# Two tints, because #68FF23 measures 14.8:1 on the dark ground but only
# 1.32:1 on white. #30800C is the same hue and saturation with the value
# lowered until it clears 4.5:1 on white.
GREEN_DARKBG = (0x68, 0xFF, 0x23)
GREEN_LIGHTBG = (0x30, 0x80, 0x0C)

# Crop box around the central chip in the 1254px master, then trimmed to ink.
# The full logo is illegible below ~96px; the chip with the `>_` prompt is the
# one element that survives, and reads cleanly from 32px.
CHIP_BOX = (505, 392, 785, 672)


def alpha_from(img: Image.Image) -> np.ndarray:
    v = np.asarray(img.convert("RGB")).astype(np.float32).max(axis=2)
    return np.round(np.clip((v - BLACK_PT) / (WHITE_PT - BLACK_PT), 0.0, 1.0) * 255).astype(np.uint8)


def tint(alpha: np.ndarray, rgb: tuple[int, int, int]) -> Image.Image:
    out = np.zeros(alpha.shape + (4,), np.uint8)
    out[..., 0], out[..., 1], out[..., 2] = rgb
    out[..., 3] = alpha
    return Image.fromarray(out, "RGBA")


def resize(img: Image.Image, size: int) -> Image.Image:
    """Downscale through premultiplied alpha so edges don't pick up dark fringing."""
    arr = np.asarray(img).astype(np.float32)
    pre = arr.copy()
    pre[..., :3] *= arr[..., 3:4] / 255.0
    small = np.asarray(
        Image.fromarray(pre.astype(np.uint8), "RGBA").resize((size, size), Image.LANCZOS)
    ).astype(np.float32)
    a = small[..., 3:4] / 255.0
    with np.errstate(divide="ignore", invalid="ignore"):
        rgb = np.where(a > 0, small[..., :3] / a, 0)
    return Image.fromarray(
        np.concatenate([np.clip(rgb, 0, 255), small[..., 3:4]], axis=2).astype(np.uint8), "RGBA"
    )


def main() -> None:
    if not SRC.exists():
        sys.exit(f"source master not found: {SRC}")

    src = Image.open(SRC)
    if src.size != (1254, 1254):
        print(f"warning: expected a 1254x1254 master, got {src.size}; CHIP_BOX may be wrong")
    full_alpha = alpha_from(src)

    written = []

    # ── Full logo ──────────────────────────────────────────────────────────
    variants = {
        "logo-green": GREEN_DARKBG,
        "logo-light": GREEN_LIGHTBG,
        "logo-mask": (255, 255, 255),
    }
    for name, rgb in variants.items():
        master = tint(full_alpha, rgb)
        master.save(OUT / f"{name}.png", optimize=True)
        written.append(f"{name}.png")
        sizes = (512,) if name == "logo-light" else (512, 256, 128, 64, 32)
        for n in sizes:
            resize(master, n).save(OUT / f"{name}-{n}.png", optimize=True)
            written.append(f"{name}-{n}.png")

    # ── Chip-only mark ─────────────────────────────────────────────────────
    chip = tint(full_alpha, (255, 255, 255)).crop(CHIP_BOX)
    ys, xs = np.where(np.asarray(chip)[..., 3] > 16)
    chip = chip.crop((xs.min(), ys.min(), xs.max() + 1, ys.max() + 1))
    side = max(chip.size) + int(max(chip.size) * 0.10) * 2
    square = Image.new("RGBA", (side, side), (0, 0, 0, 0))
    square.alpha_composite(chip, ((side - chip.size[0]) // 2, (side - chip.size[1]) // 2))
    chip_alpha = np.asarray(square)[..., 3]

    for name, rgb in (("mark-green", GREEN_DARKBG), ("mark-light", GREEN_LIGHTBG),
                      ("mark-mask", (255, 255, 255))):
        master = tint(chip_alpha, rgb)
        master.save(OUT / f"{name}.png", optimize=True)
        written.append(f"{name}.png")
        for n in (512, 256, 180, 64, 48, 32, 16):
            resize(master, n).save(OUT / f"{name}-{n}.png", optimize=True)
            written.append(f"{name}-{n}.png")

    # ── apple-touch-icon: deliberately opaque ──────────────────────────────
    # iOS composites a transparent touch icon onto an unpredictable ground and
    # it can come out black on black.
    touch = Image.new("RGBA", (180, 180), (0x0A, 0x0D, 0x0B, 255))
    touch.alpha_composite(resize(tint(chip_alpha, GREEN_DARKBG), 150), (15, 15))
    touch.convert("RGB").save(OUT / "apple-touch-icon.png", optimize=True)
    written.append("apple-touch-icon.png")

    print(f"wrote {len(written)} files to {OUT}")
    print("remember to re-copy the four assets the placeholder serves:")
    print("  cp assets/logo/{logo-mask-512,mark-mask-64,mark-green-32,apple-touch-icon}.png placeholder/")


if __name__ == "__main__":
    main()
