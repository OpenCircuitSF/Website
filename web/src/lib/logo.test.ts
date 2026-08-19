// Unit tests for the retina-blur fix carried in from #0018's review: Logo.svelte
// used to hardcode /mark-mask-64.png at every size, which is visibly soft on a
// 2x/3x display. These tests prove selectMarkRaster actually picks a higher-res
// asset once the requested size would outgrow the 64px raster at 3x DPR --
// the exact bug the reviewer caught.

import { describe, expect, it } from 'vitest';
import { selectMarkRaster, FULL_RASTER, MARK_RASTERS } from './logo';

describe('selectMarkRaster', () => {
  it('uses the 256px raster at the default nav mark size (34px)', () => {
    // This is the exact defect: the shipped code used /mark-mask-64.png here
    // unconditionally. 34 * 3 = 102 > 64, so the fix must pick the larger
    // asset.
    expect(selectMarkRaster(34)).toBe('/mark-mask-256.png');
  });

  it('uses the 64px raster for a small mark that never needs more at 3x', () => {
    // 21 * 3 = 63, just under the 64px asset's native size.
    expect(selectMarkRaster(21)).toBe('/mark-mask-64.png');
  });

  it('is exact at the boundary: needed == 64 still fits the 64px raster', () => {
    expect(selectMarkRaster(64 / 3)).toBe('/mark-mask-64.png');
  });

  it('crosses over to the 256px raster just past the boundary', () => {
    // (64/3 + 1) * 3 > 64
    expect(selectMarkRaster(64 / 3 + 1)).toBe('/mark-mask-256.png');
  });

  it('falls back to the largest raster when requested size exceeds every candidate', () => {
    expect(selectMarkRaster(200)).toBe('/mark-mask-256.png');
  });

  it('never returns a raster smaller than the requested size needs, for a range of sizes', () => {
    // Property-style check across the sizes this app actually renders the
    // mark at (nav ~24-40px, footer/hero up to ~85px).
    for (const size of [16, 20, 24, 28, 34, 40, 48, 64, 85]) {
      const url = selectMarkRaster(size);
      const raster = MARK_RASTERS.find((r) => r.url === url)!;
      const needed = size * 3;
      expect(raster.max >= needed || raster === MARK_RASTERS[MARK_RASTERS.length - 1]).toBe(true);
    }
  });
});

describe('FULL_RASTER', () => {
  it('is the 512px full-logo mask asset', () => {
    expect(FULL_RASTER).toBe('/logo-mask-512.png');
  });
});
