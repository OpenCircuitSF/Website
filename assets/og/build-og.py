#!/usr/bin/env python3
"""Regenerate the default Open Graph share card.

    python3 assets/og/build-og.py

Requires Pillow, numpy, and fontTools (with brotli, for woff2 decoding):

    pip install pillow numpy fonttools brotli

Run from the repo root. Writes web/public/og-default.png at 1200x630.

This replaces the placeholder/build-og.py stand-in (issue #0021). That script
depended on macOS system fonts (Arial Black, Menlo) and "fails loudly
elsewhere" per HANDOFF.md. This one is set in the project's actual brand
faces, self-hosted for #0012 under SIL OFL 1.1 in web/public/fonts/:
Archivo (display, variable) and JetBrains Mono (prompt/code). Pillow cannot
load a .woff2 directly, so this script decodes each one to a temporary .ttf
with fontTools at run time (bundled tool, no new dependency, nothing
committed) and instantiates the Archivo variable font at the same wght=800
the rest of the site uses for its hero headline (placeholder/index.html,
`.hero-word`).

Text is laid out from measured widths, not positioned by eye — the same
discipline as the placeholder script, extended to the headline block, which
now also shrinks to fit rather than assuming Arial Black's metrics transfer
to Archivo.
"""
from __future__ import annotations

import pathlib
import sys
import tempfile

try:
    import numpy as np
    from PIL import Image, ImageDraw, ImageFont
    from fontTools.ttLib import TTFont
    from fontTools.varLib.instancer import instantiateVariableFont
except ImportError:  # pragma: no cover
    sys.exit("needs pillow, numpy, fonttools, and brotli:  pip install pillow numpy fonttools brotli")

ROOT = pathlib.Path(__file__).resolve().parent.parent.parent
OUT = ROOT / "web" / "public" / "og-default.png"
LOGO_MASK = ROOT / "assets" / "logo" / "logo-mask-512.png"

ARCHIVO_WOFF2 = ROOT / "web" / "public" / "fonts" / "archivo-variable-latin.woff2"
MONO_400_WOFF2 = ROOT / "web" / "public" / "fonts" / "jetbrains-mono-400-latin.woff2"
MONO_700_WOFF2 = ROOT / "web" / "public" / "fonts" / "jetbrains-mono-700-latin.woff2"
ARCHIVO_WEIGHT = 800.0  # matches the site's hero headline weight (app.css / placeholder .hero-word)

W, H, MARGIN = 1200, 630, 70
COL_X = 530  # left edge of the text column

BG = (0x0A, 0x0D, 0x0B)
GREEN = (0x68, 0xFF, 0x23)
TEXT = (0xE8, 0xF0, 0xE8)
MUTED = (0x9A, 0xA7, 0x9E)
FAINT = (0x4E, 0x65, 0x53)
RULE = (0x24, 0x30, 0x28)

OK_LABEL = "beginners welcome · tools provided"
COMMAND = "$ opencircuitsf.com"


def _woff2_to_ttf(src: pathlib.Path, dest: pathlib.Path, wght: float | None = None) -> pathlib.Path:
    """Decode a self-hosted .woff2 to a .ttf Pillow can load, instantiating a
    static weight from the variable font when wght is given."""
    font = TTFont(str(src))
    if wght is not None:
        font = instantiateVariableFont(font, {"wght": wght})
    font.flavor = None
    font.save(str(dest))
    return dest


def _fit_size(draw: ImageDraw.ImageDraw, text: str, font_path: str, max_width: int, start: int, floor: int = 12) -> tuple[ImageFont.FreeTypeFont, int]:
    """Shrink point size until text fits max_width; return (font, size)."""
    size = start
    while size > floor:
        f = ImageFont.truetype(font_path, size)
        if draw.textlength(text, font=f) <= max_width:
            return f, size
        size -= 1
    return ImageFont.truetype(font_path, floor), floor


def main() -> None:
    for f in (LOGO_MASK, ARCHIVO_WOFF2, MONO_400_WOFF2, MONO_700_WOFF2):
        if not f.exists():
            sys.exit(f"missing dependency: {f}")

    with tempfile.TemporaryDirectory() as tmp:
        tmp = pathlib.Path(tmp)
        archivo_800 = _woff2_to_ttf(ARCHIVO_WOFF2, tmp / "archivo-800.ttf", ARCHIVO_WEIGHT)
        archivo_400 = _woff2_to_ttf(ARCHIVO_WOFF2, tmp / "archivo-400.ttf", 400.0)
        mono_400 = _woff2_to_ttf(MONO_400_WOFF2, tmp / "mono-400.ttf")
        mono_700 = _woff2_to_ttf(MONO_700_WOFF2, tmp / "mono-700.ttf")

        im = Image.new("RGB", (W, H), BG)
        d = ImageDraw.Draw(im)

        avail = (W - MARGIN) - COL_X  # 600px text column

        # Logo, tinted from the white alpha mask. Full logo (not the chip
        # mark) is fine at this size -- illegible only below ~96px (README).
        mask = Image.open(LOGO_MASK).convert("RGBA")
        a = np.asarray(mask)[..., 3]
        t = np.zeros(a.shape + (4,), np.uint8)
        t[..., 0], t[..., 1], t[..., 2] = GREEN
        t[..., 3] = a
        logo = Image.fromarray(t).resize((400, 400), Image.LANCZOS)
        im.paste(logo, (MARGIN, 115), logo)

        # Prompt line.
        f_prompt = ImageFont.truetype(str(mono_700), 30)
        d.text((COL_X, 120), "> open_circuit_sf", font=f_prompt, fill=GREEN)

        # Headline, in the brand display face at the site's hero weight
        # (wght=800), shrunk to fit rather than assuming a fixed point size.
        f_disp, disp_size = _fit_size(d, "CIRCUIT", str(archivo_800), avail, start=100)
        f_sf = ImageFont.truetype(str(archivo_800), int(disp_size * 0.52))

        line1_y = 178
        d.text((COL_X, line1_y), "OPEN", font=f_disp, fill=TEXT)
        bbox1 = d.textbbox((COL_X, line1_y), "OPEN", font=f_disp)
        line2_y = bbox1[3] + 8
        d.text((COL_X, line2_y), "CIRCUIT", font=f_disp, fill=TEXT)
        bbox2 = d.textbbox((COL_X, line2_y), "CIRCUIT", font=f_disp)
        sf_x = bbox2[2] + 12
        d.text((sf_x, bbox2[3] - int(disp_size * 0.58)), "_SF", font=f_sf, fill=FAINT)
        assert bbox2[2] < W - MARGIN, "CIRCUIT overflows"
        assert sf_x + d.textlength("_SF", font=f_sf) < W - MARGIN, "_SF overflows"

        # Subtext, two lines, Archivo regular.
        f_sub = ImageFont.truetype(str(archivo_400), 30)
        sub1_y = bbox2[3] + 26
        d.text((COL_X, sub1_y), "Hands-on electronics workshops", font=f_sub, fill=MUTED)
        sub1_bbox = d.textbbox((COL_X, sub1_y), "Hands-on electronics workshops", font=f_sub)
        sub2_y = sub1_bbox[3] + 6
        d.text((COL_X, sub2_y), "in San Francisco", font=f_sub, fill=MUTED)
        sub2_bbox = d.textbbox((COL_X, sub2_y), "in San Francisco", font=f_sub)
        assert sub1_bbox[2] < W - MARGIN, "subtext line 1 overflows"

        # [ OK ] row -- shrink to fit rather than trusting a fixed size.
        ok_y = sub2_bbox[3] + 30
        f_ok, ok_size = _fit_size(d, "[ OK ] " + OK_LABEL, str(mono_400), avail, start=26)
        d.text((COL_X, ok_y), "[ OK ]", font=f_ok, fill=GREEN)
        d.text((COL_X + d.textlength("[ OK ] ", font=f_ok), ok_y), OK_LABEL, font=f_ok, fill=MUTED)
        assert COL_X + d.textlength("[ OK ] " + OK_LABEL, font=f_ok) < W - MARGIN, "[ OK ] row overflows"

        # Rule + command line.
        rule_y = ok_y + ok_size + 30
        cmd_y = rule_y + 20
        assert cmd_y + 34 < H - 30, "composition overflows card height"
        d.line([(COL_X, rule_y), (W - MARGIN, rule_y)], fill=RULE, width=1)
        f_cmd = ImageFont.truetype(str(mono_700), 30)
        d.text((COL_X, cmd_y), COMMAND, font=f_cmd, fill=TEXT)
        cx = COL_X + d.textlength(COMMAND, font=f_cmd) + 6
        d.rectangle([cx, cmd_y + 3, cx + 14, cmd_y + 30], fill=GREEN)
        assert cx + 14 < W - MARGIN, "cursor overflows"

        assert im.size == (W, H), "canvas size drifted"
        im.save(OUT, optimize=True)
        kb = OUT.stat().st_size / 1024
        print(f"wrote {OUT} ({kb:.1f} KB), headline fitted at {disp_size}px, [ OK ] row at {ok_size}px")
        assert kb < 300, "og-default.png exceeds the 300KB budget"


if __name__ == "__main__":
    main()
