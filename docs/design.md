# Brand & Design System

Not yet implemented in the SPA — `#0011` ("Define the terminal design
tokens in app.css") and `#0013` port the validated design from
`placeholder/index.html` into `web/src/app.css`. This document is a stub
with real headings pending that work; `PRD.md` §4 is the authoritative
source in the meantime.

## Direction

A **terminal/console aesthetic**: near-black background, bright electronics
green, monospace type, command-prompt and status-line motifs. Reads as
"electronics and code" without being a novelty theme — most of the visual
interest comes from type and color, not illustration or photography
(`PRD.md` §4.1).

## Already built and reproducible

- `assets/logo/` — the full logo asset set: `logo-*` (wordmark) and `mark-*`
  (icon-only) variants, each in `green` (dark-ground), `light`, and `mask`
  (for `mask-image`/`currentColor` tinting) flavors, at multiple pixel
  sizes, plus `apple-touch-icon.png`. `assets/logo/source/Logo-2026.png` is
  the source art; `assets/logo/build-logo.py` regenerates the derived set.
- `placeholder/` — the validated design tokens and terminal motifs
  (`index.html`), plus the Open Graph default card (`og-default.png`,
  `build-og.py`). `placeholder/index.html` is the porting source for
  `#0011` and `#0013`.

## Known gotchas (`CLAUDE.md` §8 — read before touching any of this)

- **CSS masks fail silently under `file://`.** The logo uses
  `mask-image` tinted by `currentColor`; under `file://` the mask never
  loads and the element renders fully transparent, while the `<img>`
  favicon still works — easy to mistake for a CSS bug. Always preview with
  `python3 -m http.server`.
- **Transparency alone does not make a logo theme-adaptive.** Brand green
  `#68FF23` measures 14.8:1 on the dark ground but only 1.32:1 on white —
  well under any accessible contrast threshold. Two tints exist (`#30800C`
  for light); use the `mask-*` assets, which apply the correct tint per
  theme, rather than the flat-color assets.
- **The full logo is illegible below ~96px.** Use the `mark-*` assets for
  favicons and avatars, never the full wordmark.
- **Focus rings use `--accent`, never `--border-strong`.** The latter
  measures under 3:1 against the page background in both themes — not
  accessible for a focus indicator.
- **Headless Chromium enforces a ~485px minimum window width.**
  `--window-size=390` silently clips a wider render, which looks exactly
  like a horizontal-overflow bug in a screenshot-based check. Test narrow
  layouts in a sized `<iframe>` instead, or use the iOS Simulator
  (`docs/dev.md`'s mobile-rendering section), which has no such floor.

## Planned: design tokens (`#0011`)

`web/src/app.css` will define the color tokens, typography scale, and
reusable motifs (`PRD.md` §4.2–4.4) as CSS custom properties, ported from
`placeholder/index.html`. Until that lands, the SPA still carries
ShortLinks' own design system unchanged.

## Where to look

| Concern | File |
|---|---|
| Logo source + derived assets | `assets/logo/` |
| Validated design reference | `placeholder/index.html` |
| OG default card | `placeholder/og-default.png`, `placeholder/build-og.py` |
| Future design tokens | `web/src/app.css` (once `#0011` lands) |
