#!/usr/bin/env python3
"""Reconstruct the photographed CRT screen with its baked-in text removed.

    python3 prototypes/crt-live-screen/tools/blank-screen.py

Writes assets/hero-crt-blank-760.webp and assets/hero-crt-blank-light-760.webp
from the corresponding photographs in web/public/. Run this if those
photographs are ever replaced.

WHY: the prototype's canvas is transparent, so the photograph's own screen
shows through -- texture, curvature, vignette, corner glow -- and only the live
glyphs are drawn over it. But the photographs have "> Open Circuit SF _" baked
into the screen, which would sit underneath the live text.

HOW, and what did NOT work:

  1. Median-filtering the bright pixels removes the glyph strokes but leaves
     the bloom. The glow around the text is several times wider than the
     strokes, so the result is a row of bright smears.
  2. Flagging "brighter than the column median" and interpolating across it
     grabs the pool of glow at the top of the screen too, because that is also
     brighter than the median -- producing tall vertical streaks.

  What works is discriminating on *horizontal high-frequency detail* rather
  than brightness. Glyphs have sharp vertical edges; the screen's glow is
  smooth. |G - horizontal_median(G, 21)| is large only on text, which locates
  the band precisely. That band is then rebuilt per column by interpolating
  between the clean rows immediately above and below it, so the screen's own
  vertical gradient is preserved rather than flattened, and re-grained with
  light noise so it does not read as a blurred patch against real texture.
"""
import pathlib
import numpy as np
from PIL import Image, ImageDraw, ImageFilter

HERE = pathlib.Path(__file__).resolve().parent
ASSETS = HERE.parent / "assets"
UPSTREAM = HERE.parent.parent.parent / "web" / "public"

# The photographed raster's corners, % of the image (measured for #0233).
CORNERS = {"tl": (23.03, 28.84), "tr": (68.38, 23.03),
           "br": (67.68, 77.51), "bl": (23.00, 72.30)}

PAIRS = [("hero-crt-760.webp", "hero-crt-blank-760.webp"),
         ("hero-crt-light-760.webp", "hero-crt-blank-light-760.webp")]


def raster_mask(w, h, erode=11):
    pts = [(CORNERS[k][0] * w / 100, CORNERS[k][1] * h / 100)
           for k in ("tl", "tr", "br", "bl")]
    m = Image.new("L", (w, h), 0)
    ImageDraw.Draw(m).polygon(pts, fill=255)
    return np.asarray(m.filter(ImageFilter.MinFilter(erode))) > 127


def horizontal_median(gray, k=21):
    pad = k // 2
    padded = np.pad(gray, ((0, 0), (pad, pad)), mode="edge")
    out = np.empty_like(gray)
    for i in range(gray.shape[1]):
        out[:, i] = np.median(padded[:, i:i + k], axis=1)
    return out


def blank(src: pathlib.Path, dst: pathlib.Path) -> None:
    im = Image.open(src).convert("RGB")
    w, h = im.size
    inside = raster_mask(w, h)
    a = np.asarray(im).astype(float)

    detail = np.abs(a[:, :, 1] - horizontal_median(a[:, :, 1], 21))
    score = np.where(inside, detail, 0).sum(axis=1)
    rows = np.nonzero(score > score.max() * 0.18)[0]
    pad = 14                                   # cover the bloom, not just glyphs
    y0, y1 = max(0, rows.min() - pad), min(h - 1, rows.max() + pad)

    res = a.copy()
    for x in range(w):
        col = np.nonzero(inside[:, x])[0]
        if len(col) < 40:
            continue
        top_y, bot_y = col.min(), col.max()
        s, e = max(y0, top_y + 1), min(y1, bot_y - 1)
        if e <= s or s - 1 < top_y or e + 1 > bot_y:
            continue
        upper, lower = a[s - 1, x, :], a[e + 1, x, :]
        n = e - s + 1
        for i in range(n):
            t = (i + 1) / (n + 1)
            res[s + i, x, :] = upper * (1 - t) + lower * t

    patch = np.zeros((h, w), bool)
    patch[y0:y1 + 1, :] = True
    patch &= inside

    rng = np.random.default_rng(7)             # fixed seed: reproducible output
    res = np.where(patch[:, :, None],
                   np.clip(res + rng.normal(0, 1.5, (h, w))[:, :, None], 0, 255), res)
    smoothed = np.asarray(
        Image.fromarray(res.astype("uint8")).filter(ImageFilter.GaussianBlur(0.7))
    ).astype(float)
    res = np.where(patch[:, :, None], smoothed, res)

    Image.fromarray(np.clip(res, 0, 255).astype("uint8")).save(
        dst, "WEBP", quality=82, method=6)
    print(f"  {dst.name}: text band rows {y0}..{y1}, {dst.stat().st_size} B")


if __name__ == "__main__":
    for src_name, dst_name in PAIRS:
        src = UPSTREAM / src_name
        if not src.is_file():
            raise SystemExit(f"upstream photograph missing: {src}")
        blank(src, ASSETS / dst_name)
