#!/usr/bin/env python3
"""Generate a self-contained index.html from index.src.html.

    python3 prototypes/crt-live-screen/build.py            # build
    python3 prototypes/crt-live-screen/build.py --sync      # refresh assets/ first

WHY THE ASSETS ARE DUPLICATED HERE

This folder is meant to be zipped and sent to someone who does not have the
repository. So `assets/` holds its own copies of the two hero photographs and
the three webfonts, and index.src.html references them by a path that never
climbs out of this directory. Unzip anywhere and every file still resolves.

That duplication is a real cost -- two copies of an asset drift apart -- so it
is checked rather than trusted. When this script runs inside the repository it
compares assets/ against web/public/ by SHA-256 and reports any difference.
`--sync` copies the upstream versions over. Outside the repository (i.e. after
someone unzips it) there is nothing to compare against, and the script says so
and carries on.

WHY index.html IS GENERATED AT ALL

Under file:// a browser will not reliably fetch subresources from outside the
page's own directory, and same-directory behaviour varies by browser and by
settings. Inlining everything as data: URIs removes the question: index.html
is one file with no subresources, so it opens from the Finder, and it can also
be sent on its own without the folder around it.
"""
import base64, hashlib, pathlib, re, shutil, sys

HERE   = pathlib.Path(__file__).resolve().parent
SRC    = HERE / "index.src.html"
OUT    = HERE / "index.html"
ASSETS = HERE / "assets"
UPSTREAM = HERE.parent.parent / "web" / "public"

# assets/<name> -> path under web/public/
TRACKED = {
    "hero-crt-760.webp":              "hero-crt-760.webp",
    "hero-crt-light-760.webp":        "hero-crt-light-760.webp",
    "archivo-variable-latin.woff2":   "fonts/archivo-variable-latin.woff2",
    "jetbrains-mono-400-latin.woff2": "fonts/jetbrains-mono-400-latin.woff2",
    "jetbrains-mono-700-latin.woff2": "fonts/jetbrains-mono-700-latin.woff2",
    "FONT-LICENSE":                   "fonts/LICENSE",
}

MIME = {".webp": "image/webp", ".woff2": "font/woff2", ".png": "image/png"}


def digest(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def check_assets(sync: bool) -> int:
    """Compare assets/ with web/public/. Returns the number of differences."""
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
        print("assets/ matches web/public/")
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
    sync = "--sync" in sys.argv[1:]
    drift = check_assets(sync)
    build()
    # A drifted asset is not a build failure -- index.html is still valid -- but
    # it must not pass silently in CI or a scripted run.
    sys.exit(1 if drift and not sync else 0)
