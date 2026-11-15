#!/usr/bin/env bash
# Renders the guide drawings in assets/guides/ to PNGs under
# web/public/img/guides/, at 1x and 2x (#0666). Uses rsvg-convert only;
# never a browser (AGENTS.md §8). Run from anywhere:
#
#   scripts/render-guide-art.sh
#
# Each SVG's own width is the 1x width. Coordinates in
# web/src/lib/topics/multimeter.ts are in that 1x pixel space.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
command -v rsvg-convert >/dev/null || { echo "rsvg-convert not found (brew install librsvg)" >&2; exit 1; }

render() {
  local src="$1" dst_dir="$2"
  local name width
  name="$(basename "$src" .svg)"
  width="$(sed -n 's/.*<svg[^>]* width="\([0-9]*\)".*/\1/p' "$src" | head -1)"
  [ -n "$width" ] || { echo "no width in $src" >&2; exit 1; }
  mkdir -p "$dst_dir"
  rsvg-convert -w "$width" -o "$dst_dir/$name.png" "$src"
  rsvg-convert -w "$((width * 2))" -o "$dst_dir/$name@2x.png" "$src"
  echo "rendered $dst_dir/$name.png (+@2x)"
}

for src in "$ROOT"/assets/guides/multimeter/*.svg; do
  render "$src" "$ROOT/web/public/img/guides/multimeter"
done
