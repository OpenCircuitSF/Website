<script lang="ts">
  // The Open Circuit SF mark, rendered via the alpha-mask technique (PRD
  // §4.5, #0018): a single white-alpha PNG tinted with `background-color:
  // var(--logo-color)` through `mask-image`, so one asset follows both the
  // OS `prefers-color-scheme` setting and the in-page theme toggle (#0011)
  // via the same --logo-color token that already resolves per-theme in
  // app.css. See assets/logo/README.md for the full rationale and the
  // measured contrast that makes --logo-color a separate token from
  // --accent rather than an alias.
  //
  // CSS masks fail silently under file:// (CLAUDE.md §8, docs/frontend.md) —
  // always preview over an actual HTTP origin (`./scripts/dev.sh` or
  // `python3 -m http.server`), never by opening dist/index.html directly.
  //
  // A masked element has no `alt`; the accessible name comes from
  // role="img" + aria-label instead.
  //
  // "mark" (the chip only) is the default and is what most call sites want —
  // the full logo is illegible below ~96px (CLAUDE.md §8), so "full" is only
  // appropriate at 128px and above (e.g. a footer or hero placement, not the
  // header nav bar). #0017 (site header) is the first real consumer of this
  // component.
  interface Props {
    variant?: 'mark' | 'full';
    /** Square size in pixels. */
    size?: number;
  }

  let { variant = 'mark', size = 34 }: Props = $props();

  const maskUrl = $derived(variant === 'full' ? '/logo-mask-512.png' : '/mark-mask-64.png');
</script>

<span
  class="logo"
  style="--logo-size: {size}px; --logo-mask-url: url('{maskUrl}');"
  role="img"
  aria-label="Open Circuit SF"
></span>

<style>
  .logo {
    display: inline-block;
    flex: none;
    width: var(--logo-size);
    height: var(--logo-size);
    background-color: var(--logo-color);
    -webkit-mask-image: var(--logo-mask-url);
    mask-image: var(--logo-mask-url);
    -webkit-mask-repeat: no-repeat;
    mask-repeat: no-repeat;
    -webkit-mask-position: center;
    mask-position: center;
    -webkit-mask-size: contain;
    mask-size: contain;
  }
</style>
