// Pure raster-selection logic for Logo.svelte's mask technique (PRD §4.5,
// #0018/#0017). Extracted into a plain module rather than inlined in the
// component so it is unit-testable without mounting Svelte (CLAUDE.md: SPA
// logic lives in web/src/lib/ as plain TypeScript; components stay thin).
//
// Carried in from #0018's review (2026-08-18): Logo.svelte hardcoded
// /mark-mask-64.png at every requested size, which renders visibly soft on a
// 2x/3x display once the mark is shown above ~21 CSS px (21 * 3 = 63, just
// under the asset's native 64px). mark-mask-256.png (copied into
// web/public/ alongside this fix) covers every mark size this app uses today
// at up to 3x device pixel ratio.

/** The mark rasters available, in ascending native size. Each entry's `max`
 * is the largest CSS-pixel * device-pixel-ratio product it can serve without
 * upscaling. */
export const MARK_RASTERS: readonly { readonly max: number; readonly url: string }[] = [
  { max: 64, url: '/mark-mask-64.png' },
  { max: 256, url: '/mark-mask-256.png' },
];

/** Worst-case device pixel ratio this selection targets. Covers effectively
 * every phone/tablet display in use (iOS caps at 3x; most Android tops out
 * at 3-4x but the visual difference beyond 3x is not perceptible for a
 * mark this small) without needing to read the real
 * window.devicePixelRatio, which would make selectMarkRaster impure. */
const MAX_DPR = 3;

/**
 * Pick the smallest mark raster whose native resolution is >= size * MAX_DPR,
 * so the element never renders softer than a real retina asset would allow.
 * Falls back to the largest available raster when the requested size exceeds
 * every candidate -- a slightly-scaled-down large asset beats an upscaled
 * small one.
 */
export function selectMarkRaster(size: number): string {
  const needed = size * MAX_DPR;
  const fit = MARK_RASTERS.find((r) => r.max >= needed);
  return (fit ?? MARK_RASTERS[MARK_RASTERS.length - 1]).url;
}

/** The single raster used for the "full" (wordmark + mark) variant. Already
 * high enough resolution (512px) for every size it's used at -- Logo.svelte's
 * own illegibility floor keeps "full" to >=128 CSS px, and 512/128 = 4x
 * already exceeds MAX_DPR -- so no size-based branching is needed the way
 * selectMarkRaster needs it for the much smaller "mark" variant. */
export const FULL_RASTER = '/logo-mask-512.png';
