#!/usr/bin/env python3
"""Regenerate the two static TTFs internal/seo embeds for per-workshop Open
Graph cards (#0273).

    python3 assets/og/build-card-fonts.py

Requires fontTools (with brotli, for woff2 decoding) -- the same dependency
assets/og/build-og.py already needs:

    pip install fonttools brotli

Run from the repo root. Writes:

    internal/seo/cardassets/archivo-800.ttf
    internal/seo/cardassets/jetbrains-mono-400.ttf

Why this exists rather than embedding the project's self-hosted .woff2 files
directly: golang.org/x/image/font/sfnt (which golang.org/x/image/font/opentype
wraps) cannot parse .woff2 -- bare SFNT/TTF/OTF only -- and has no
variable-font support (`grep -rn fvar` over the module's font/sfnt source
returns nothing), so a variable Archivo would render at its default instance,
not the site's wght=800. This is the exact _woff2_to_ttf conversion
assets/og/build-og.py already performs at *run time* for the default card;
here it happens once, at commit time, because a Go program cannot shell out to
fontTools in production (no Python on the deploy target, CLAUDE.md §7).

A `//go:embed` path cannot reach outside its own package directory (`..` is
rejected), so the output lands under internal/seo/cardassets/, not
web/public/fonts/ where the source .woff2 files live. Do NOT subset further --
workshop titles are admin-authored free text, so the full latin subset each
.woff2 already carries is needed, unchanged from build-og.py's own choice.

The OFL requires the licence to travel with the font files it covers, so this
script also copies web/public/fonts/LICENSE alongside the two TTFs.
"""
from __future__ import annotations

import pathlib
import shutil
import sys

try:
    from fontTools.ttLib import TTFont
    from fontTools.varLib.instancer import instantiateVariableFont
except ImportError:  # pragma: no cover
    sys.exit("needs fonttools and brotli:  pip install fonttools brotli")

ROOT = pathlib.Path(__file__).resolve().parent.parent.parent
SRC_DIR = ROOT / "web" / "public" / "fonts"
OUT_DIR = ROOT / "internal" / "seo" / "cardassets"

ARCHIVO_WOFF2 = SRC_DIR / "archivo-variable-latin.woff2"
MONO_400_WOFF2 = SRC_DIR / "jetbrains-mono-400-latin.woff2"
LICENSE_SRC = SRC_DIR / "LICENSE"

ARCHIVO_WEIGHT = 800.0  # matches build-og.py's ARCHIVO_WEIGHT / the site's hero headline weight


def _woff2_to_ttf(src: pathlib.Path, dest: pathlib.Path, wght: float | None = None) -> None:
    """Decode a self-hosted .woff2 to a static .ttf, instantiating wght from
    the variable font when given -- identical to build-og.py's own helper."""
    font = TTFont(str(src))
    if wght is not None:
        font = instantiateVariableFont(font, {"wght": wght})
    font.flavor = None
    font.save(str(dest))


def main() -> None:
    for f in (ARCHIVO_WOFF2, MONO_400_WOFF2, LICENSE_SRC):
        if not f.exists():
            sys.exit(f"missing dependency: {f}")

    OUT_DIR.mkdir(parents=True, exist_ok=True)

    archivo_out = OUT_DIR / "archivo-800.ttf"
    mono_out = OUT_DIR / "jetbrains-mono-400.ttf"

    _woff2_to_ttf(ARCHIVO_WOFF2, archivo_out, ARCHIVO_WEIGHT)
    _woff2_to_ttf(MONO_400_WOFF2, mono_out)

    shutil.copyfile(LICENSE_SRC, OUT_DIR / "LICENSE")

    for out in (archivo_out, mono_out):
        kb = out.stat().st_size / 1024
        print(f"wrote {out} ({kb:.1f} KB)")


if __name__ == "__main__":
    main()
