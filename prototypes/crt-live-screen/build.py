#!/usr/bin/env python3
"""Generate a self-contained index.html from index.src.html.

Why this exists: the prototype is meant to be opened and dragged around, and
opening it from the Finder means a file:// URL. Under file:// a browser will
not reliably fetch subresources from outside the page's own directory, so the
photographs and webfonts silently fail to load and the CRT renders as an empty
box -- which looks exactly like a broken stylesheet.

Rather than duplicate the assets into this folder (two copies to drift apart),
the source file keeps ordinary relative paths and this script inlines them as
data: URIs. Edit index.src.html; re-run this; commit both.

    python3 prototypes/crt-live-screen/build.py
"""
import base64, pathlib, re, sys

HERE = pathlib.Path(__file__).resolve().parent
SRC  = HERE / "index.src.html"
OUT  = HERE / "index.html"

MIME = {".webp": "image/webp", ".woff2": "font/woff2", ".png": "image/png"}

def main() -> int:
    html = SRC.read_text(encoding="utf-8")
    seen: dict[str, int] = {}

    def inline(m: re.Match) -> str:
        quote, rel = m.group(1), m.group(2)
        path = (HERE / rel).resolve()
        if not path.is_file():
            print(f"missing asset: {rel} -> {path}", file=sys.stderr)
            raise SystemExit(1)
        mime = MIME.get(path.suffix)
        if mime is None:
            print(f"unknown media type for {rel}", file=sys.stderr)
            raise SystemExit(1)
        seen[rel] = seen.get(rel, 0) + 1
        data = base64.b64encode(path.read_bytes()).decode("ascii")
        return f"url({quote}data:{mime};base64,{data}{quote})"

    # Only rewrite paths that climb out of this directory -- anything local
    # already works under file:// and is left alone.
    out = re.sub(r"""url\((['"]?)((?:\.\./)+[^'")]+)\1\)""", inline, html)

    if not seen:
        print("no external assets found -- did index.src.html change?", file=sys.stderr)
        return 1

    banner = (
        "<!-- GENERATED FILE - do not edit.\n"
        "     Source: index.src.html   Regenerate: python3 build.py\n"
        "     Assets are inlined as data: URIs so this opens from the Finder\n"
        "     over file:// with no server. -->\n"
    )
    OUT.write_text(banner + out, encoding="utf-8")

    for rel, n in sorted(seen.items()):
        print(f"  inlined x{n}  {rel}")
    print(f"wrote {OUT.name}  ({OUT.stat().st_size // 1024} KB)")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
