#!/usr/bin/env python3
"""Generate a self-contained index.html from index.src.html.

    python3 prototypes/eu-crt/build.py            # build
    python3 prototypes/eu-crt/build.py --sync      # refresh the fonts first

WHY THE ASSETS ARE DUPLICATED HERE

This folder is meant to be zipped and sent to someone who does not have the
repository, so assets/ holds its own copies of the three webfonts and
index.src.html references them by a path that never climbs out of this
directory. Unzip anywhere and every file still resolves.

That duplication is a real cost -- two copies of an asset drift apart -- so it
is checked rather than trusted. When this script runs inside the repository it
compares the fonts against web/public/fonts/ by SHA-256 and reports any
difference; `--sync` copies the upstream versions over. Outside the repository
(i.e. after someone unzips it) there is nothing to compare against, and the
script says so and carries on.

The two CRT photographs have no upstream counterpart -- they are this
prototype's own source material, and the .webp files under assets/ are derived
from the .png originals beside this script by build_photos(). Delete a .webp
and it is regenerated; edit a .png and pass --photos to regenerate both.

The two are NOT derived the same way. eu-crt-dark.png is RGBA and converts
straight across. eu-crt-light.png is RGB with the editor's transparency
checkerboard baked into its pixels, so its alpha has to be reconstructed first
-- that is tools/key-light-photo.py, which carries the explanation.

WHY index.html IS GENERATED AT ALL

Under file:// a browser will not reliably fetch subresources from outside the
page's own directory, and same-directory behaviour varies by browser and by
settings. Inlining everything as data: URIs removes the question: index.html
is one file with no subresources, so it opens from the Finder, and it can also
be sent on its own without the folder around it.
"""
import base64, hashlib, pathlib, re, shutil, subprocess, sys

HERE   = pathlib.Path(__file__).resolve().parent
SRC    = HERE / "index.src.html"
OUT    = HERE / "index.html"
ASSETS = HERE / "assets"
UPSTREAM = HERE.parent.parent / "web" / "public"

# assets/<name> -> path under web/public/
TRACKED = {
    "archivo-variable-latin.woff2":   "fonts/archivo-variable-latin.woff2",
    "jetbrains-mono-400-latin.woff2": "fonts/jetbrains-mono-400-latin.woff2",
    "jetbrains-mono-700-latin.woff2": "fonts/jetbrains-mono-700-latin.woff2",
    "FONT-LICENSE":                   "fonts/LICENSE",
}

# <png beside this script> -> assets/<webp>. The PNGs are ~1.5 MB each and
# would make the inlined index.html around 4 MB; at quality 88 the WebP pair
# is under 260 KB together with no visible difference on the glass, and the
# alpha channel (these are cut-outs, not rectangles) survives the conversion.
PHOTOS = {
    "eu-crt-dark.png":  "eu-crt-dark.webp",
    "eu-crt-light.png": "eu-crt-light.webp",
}
# The light photograph does not convert straight across: its alpha has to be
# reconstructed from a baked-in checkerboard first. See the module docstring.
KEYED = {"eu-crt-light.png": HERE / "tools" / "key-light-photo.py"}

MIME = {".webp": "image/webp", ".woff2": "font/woff2", ".png": "image/png"}


def digest(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def build_photos(force: bool) -> None:
    """Derive assets/*.webp from the PNG originals when missing or forced."""
    todo = [(HERE / png, ASSETS / webp) for png, webp in PHOTOS.items()
            if force or not (ASSETS / webp).is_file()]
    if not todo:
        return
    missing = [src for src, _ in todo if not src.is_file()]
    if missing:
        # Only a problem for a checkout that has the PNGs; a standalone unzip
        # ships the .webp files and never needs to regenerate them.
        raise SystemExit("missing photograph source(s): " +
                         ", ".join(p.name for p in missing))
    try:
        from PIL import Image
    except ImportError:
        raise SystemExit("Pillow is needed to regenerate the .webp photographs "
                         "(pip install Pillow), or restore assets/*.webp")
    for src, dst in todo:
        keyer = KEYED.get(src.name)
        if keyer is not None:
            subprocess.run([sys.executable, str(keyer)], check=True)
            continue
        Image.open(src).save(dst, "WEBP", quality=88, method=6)
        print(f"  derived  {dst.name}  ({dst.stat().st_size // 1024} KB)")


def check_assets(sync: bool) -> int:
    """Compare the fonts in assets/ with web/public/. Returns the difference count."""
    if not UPSTREAM.is_dir():
        print("upstream web/public/ not present — standalone copy, skipping drift check")
        return 0

    drift = 0
    for local_name, upstream_rel in TRACKED.items():
        local, up = ASSETS / local_name, UPSTREAM / upstream_rel
        if not up.is_file():
            print(f"  ?  upstream missing: {upstream_rel}")
            continue
        if not local.is_file():
            print(f"  +  {local_name}: absent locally")
            drift += 1
            if sync:
                shutil.copy2(up, local)
            continue
        if digest(local) != digest(up):
            print(f"  !  {local_name}: DIFFERS from web/public/{upstream_rel}")
            drift += 1
            if sync:
                shutil.copy2(up, local)

    if drift and sync:
        print(f"synced {drift} asset(s) from web/public/")
        return 0
    if drift:
        print(f"\n{drift} asset(s) differ from web/public/. Re-run with --sync to update.",
              file=sys.stderr)
    else:
        print("fonts in assets/ match web/public/fonts/")
    return drift


def build() -> None:
    html = SRC.read_text(encoding="utf-8")
    seen: dict[str, int] = {}

    def inline(m: re.Match) -> str:
        quote, rel = m.group(1), m.group(2)
        path = (HERE / rel).resolve()
        if not path.is_file():
            raise SystemExit(f"missing asset: {rel} -> {path}")
        mime = MIME.get(path.suffix)
        if mime is None:
            raise SystemExit(f"unknown media type for {rel}")
        seen[rel] = seen.get(rel, 0) + 1
        data = base64.b64encode(path.read_bytes()).decode("ascii")
        return f"url({quote}data:{mime};base64,{data}{quote})"

    out = re.sub(r"""url\((['"]?)(assets/[^'")]+)\1\)""", inline, html)
    if not seen:
        raise SystemExit("no assets/ references found — did index.src.html change?")

    banner = ("<!-- GENERATED FILE - do not edit.\n"
              "     Source: index.src.html   Regenerate: python3 build.py\n"
              "     Every asset is inlined as a data: URI, so this single file\n"
              "     opens from the Finder with no server and can be sent alone. -->\n")
    OUT.write_text(banner + out, encoding="utf-8")

    for rel, n in sorted(seen.items()):
        print(f"  inlined x{n}  {rel}")
    print(f"wrote {OUT.name}  ({OUT.stat().st_size // 1024} KB)")


if __name__ == "__main__":
    args = sys.argv[1:]
    build_photos("--photos" in args)
    drift = check_assets("--sync" in args)
    build()
    # A drifted asset is not a build failure -- index.html is still valid -- but
    # it must not pass silently in CI or a scripted run.
    sys.exit(1 if drift and "--sync" not in args else 0)
