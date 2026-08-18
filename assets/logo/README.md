# Logo assets

Derived from `~/Documents/Open Circuit SF/Logo 2026.png` (1254×1254, green on solid black).
Regenerate with the script recorded in issue [#0018](../../issues/0018.md).

## What was done

The black background was removed by **luminance keying**, not by a colour-match
delete: alpha is taken from `max(R,G,B)` with a black point of 6 and a white
point of 212, so anti-aliased edges keep their partial transparency and there is
no black fringe on light backgrounds. RGB is then replaced with a flat brand
colour, which means the same alpha channel can be tinted any colour.

The source is a clean render — 75% of its pixels sit at luminance ≤ 2 — so the
key is essentially lossless.

## The contrast problem, and why there are two greens

The brand green is **`#68FF23`** (hue 101°, sat 86%). Measured against the WCAG
2.1 relative-luminance formula:

| Colour | On `#0A0D0B` (dark UI) | On `#FFFFFF` |
|---|---|---|
| `#68FF23` | **14.8:1** | **1.32:1** ← unusable |
| `#30800C` | 1.9:1 ← unusable | **4.98:1** |

Transparency alone does **not** make the logo theme-adaptive: dropped onto a
white page, `#68FF23` is close to invisible. Light mode needs a darkened green.
`#30800C` is the same hue and saturation with the value reduced until it clears
4.5:1 on white.

## Files

| File | Colour | Use |
|---|---|---|
| `logo-green.png` + `-512/256/128/64/32` | `#68FF23` | Full logo on dark backgrounds |
| `logo-light.png` + `-512` | `#30800C` | Full logo on light backgrounds |
| `logo-mask.png` + `-512/256/128/64/32` | white | **Alpha mask** — tint to any colour via CSS |
| `mark-green.png` + `-512/256/180/64/48/32/16` | `#68FF23` | Chip-only mark, dark backgrounds |
| `mark-light.png` + sizes | `#30800C` | Chip-only mark, light backgrounds |
| `mark-mask.png` + sizes | white | Chip-only alpha mask |
| `apple-touch-icon.png` | — | 180×180, **opaque** dark ground |

### Why there is a separate chip mark

The full logo is illegible below about 96 px — the traces, the board outlines and
the `hack · build · learn` line all collapse into noise. The central chip with the
`>_` prompt is the one element that survives, and it reads cleanly from 32 px.
Use `mark-*` for favicons, avatars, and any inline icon; use `logo-*` at 128 px
and above.

`apple-touch-icon.png` is deliberately **opaque** — iOS composites a transparent
touch icon onto an unpredictable ground and a transparent one can come out black
on black.

## Recommended usage — one asset, both themes

The mask approach tints a single file with `currentColor`, so it follows an
in-page theme toggle as well as the OS setting:

```css
.logo {
  width: 12rem;
  aspect-ratio: 1;
  background-color: var(--logo-color);
  -webkit-mask: url('/logo-mask-512.png') center / contain no-repeat;
          mask: url('/logo-mask-512.png') center / contain no-repeat;
}

:root                                  { --logo-color: #30800C; }  /* light */
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"])      { --logo-color: #68FF23; }
}
:root[data-theme="dark"]               { --logo-color: #68FF23; }
```

Give it an accessible name, since a masked element has no `alt`:

```html
<div class="logo" role="img" aria-label="Open Circuit SF"></div>
```

The `-webkit-mask` prefix is still required for Safari.

### Alternative — `<picture>`

Simpler, keeps a real `alt`, but follows only the OS setting and will not respond
to an in-page theme toggle:

```html
<picture>
  <source srcset="/logo-green-512.png" media="(prefers-color-scheme: dark)">
  <img src="/logo-light-512.png" alt="Open Circuit SF" width="256" height="256">
</picture>
```

## Still to do

These are raster derivatives of a raster source. A vector trace of the mark —
tracked in [#0018](../../issues/0018.md) — would give crisp rendering at every
size, a real `favicon.svg`, and `currentColor` tinting without the mask trick.
