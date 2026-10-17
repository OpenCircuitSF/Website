import { describe, expect, it } from 'vitest';
import type { FlyerSelection } from './types';
import {
  CUT_AT_IN,
  DEFAULT_FLYER_TEMPLATE,
  FLYER_TEMPLATES,
  FULL_PHOTO_BOX,
  QR_ASSUMED_MODULES,
  SITE_URL_TEXT,
  composite,
  contrastRatio,
  noBreakHyphens,
  qrQuietIn,
  HALF,
  LETTER,
  PRINT_PAGE_CSS,
  SAFE_IN,
  contentBox,
  coverPosition,
  displayShortUrl,
  expandCopies,
  flyerLabel,
  focalFromPointer,
  focalText,
  missingQrCount,
  missingQrNote,
  nudgeFocal,
  objectPosition,
  paginate,
  photoBox,
  photoWarning,
  rotatedHalfBounds,
  selectionSummary,
  sheetCount,
  sheetsFull,
  sheetsHalf,
  utmSummary,
  validateTagline,
} from './flyers';

function sel(over: Partial<FlyerSelection> & { id: number }): FlyerSelection {
  const { id, ...rest } = over;
  return {
    location_id: id,
    name: `Board ${id}`,
    code: `b${id}`,
    address: '',
    active: true,
    copies: 1,
    link: { id: id * 10, short_link_key: `k${id}`, short_url: `https://go.example/k${id}`, has_qr: true, qr_url: `/admin/outreach-flyer-links/${id * 10}/qr.svg`, put_up_at: null },
    ...rest,
  };
}

describe('sheet geometry constants', () => {
  it('uses Letter, half-size, a 0.25in safe area and a cut at 5.5in', () => {
    expect(LETTER).toEqual({ w: 8.5, h: 11 });
    expect(HALF).toEqual({ w: 5.5, h: 8.5 });
    expect(SAFE_IN).toBe(0.25);
    expect(CUT_AT_IN).toBe(5.5);
    expect(PRINT_PAGE_CSS).toBe('@page { size: letter portrait; margin: 0; }');
  });
  it('two half flyers rotated -90deg about their corner exactly fill the slot', () => {
    expect(rotatedHalfBounds()).toEqual({ x: 0, y: 0, w: 8.5, h: 5.5 });
    expect(HALF.h).toBe(LETTER.w);
    expect(HALF.w * 2).toBe(LETTER.h);
    expect(Object.is(rotatedHalfBounds().x, -0)).toBe(false);
  });
  it('the content box is the flyer minus the safe area on each side', () => {
    expect(contentBox('full')).toEqual({ w: 8, h: 10.5 });
    expect(contentBox('half')).toEqual({ w: 5, h: 8 });
  });
});

describe('expandCopies and sheets', () => {
  it('repeats each printable location by its copies, copies consecutive, in selection order', () => {
    const out = expandCopies([sel({ id: 1, copies: 2 }), sel({ id: 2, copies: 1 }), sel({ id: 3, copies: 3 })]);
    expect(out.map((i) => i.locationId)).toEqual([1, 1, 2, 3, 3, 3]);
    expect(out[0]).toEqual({ locationId: 1, shortUrl: 'https://go.example/k1', label: 'Board 1 · b1', qrSrc: '/admin/outreach-flyer-links/10/qr.svg' });
  });
  it('leaves out locations with no link or no QR yet, and counts them', () => {
    const rows = [sel({ id: 1 }), sel({ id: 2, link: null }), sel({ id: 3, link: { id: 30, short_link_key: 'k', short_url: 'u', has_qr: false, qr_url: null, put_up_at: null } })];
    expect(expandCopies(rows).map((i) => i.locationId)).toEqual([1]);
    expect(missingQrCount(rows)).toBe(2);
  });
  it('counts sheets: n for full, ceil(n/2) for half', () => {
    for (const [n, full, half] of [[0, 0, 0], [1, 1, 1], [2, 2, 1], [3, 3, 2], [4, 4, 2], [5, 5, 3]] as const) {
      expect(sheetsFull(n)).toBe(full);
      expect(sheetsHalf(n)).toBe(half);
      expect(sheetCount(n, 'full')).toBe(full);
      expect(sheetCount(n, 'half')).toBe(half);
    }
  });
  it('fills half sheets top then bottom, leaving an odd last bottom blank', () => {
    const inst = expandCopies([sel({ id: 1, copies: 2 }), sel({ id: 2 })]);
    const half = paginate(inst, 'half');
    expect(half).toHaveLength(2);
    expect(half[0].map((s) => s?.locationId)).toEqual([1, 1]);
    expect(half[1].map((s) => s?.locationId ?? null)).toEqual([2, null]);
    const full = paginate(inst, 'full');
    expect(full.map((s) => s.length)).toEqual([1, 1, 1]);
    expect(paginate([], 'half')).toEqual([]);
  });
});

describe('missingQrNote', () => {
  it('is null for none, singular for one, plural for more', () => {
    expect(missingQrNote(0)).toBeNull();
    expect(missingQrNote(1)).toBe('1 location has no QR code yet. Generate first.');
    expect(missingQrNote(2)).toBe('2 locations have no QR code yet. Generate first.');
  });
});

describe('photoBox', () => {
  it('photo-top is the content width by 58% (full) or 53% (half) of its height', () => {
    expect(photoBox('photo-top', 'full')).toEqual({ w: 8, h: 10.5 * 0.58 });
    expect(photoBox('photo-top', 'half')).toEqual({ w: 5, h: 8 * 0.53 });
  });
  it('full-photo bleeds across the whole flyer and split takes half the content width', () => {
    expect(photoBox('full-photo', 'full')).toEqual({ w: 8.5, h: 11 });
    expect(photoBox('full-photo', 'half')).toEqual({ w: 5.5, h: 8.5 });
    expect(photoBox('split', 'full')).toEqual({ w: 4, h: 10.5 });
  });
});

describe('coverPosition', () => {
  const box = { w: 4, h: 4 };
  it('centers the focal point and clamps at the edges for a wide image', () => {
    const wide = { w: 2000, h: 1000 }; // displayed 8 x 4 in a 4 x 4 box: x slack, y exact
    expect(coverPosition(wide, box, { x: 0.5, y: 0.5 })).toEqual({ x: 50, y: 50 });
    expect(coverPosition(wide, box, { x: 0, y: 0.5 }).x).toBe(0);
    expect(coverPosition(wide, box, { x: 1, y: 0.5 }).x).toBe(100);
    // Focal 0.25: wants the left offset 2 - 0.25*8 = 0, so 0%; focal 0.75 wants -4 = the far edge.
    expect(coverPosition(wide, box, { x: 0.25, y: 0.5 }).x).toBe(0);
    expect(coverPosition(wide, box, { x: 0.75, y: 0.5 }).x).toBe(100);
    // Focal 0.4 is inside the free range: L = 2 - 3.2 = -1.2, p = -1.2 / -4 = 30%.
    expect(coverPosition(wide, box, { x: 0.4, y: 0.5 }).x).toBeCloseTo(30, 6);
    expect(coverPosition(wide, box, { x: 0.4, y: 0.5 }).y).toBe(50);
  });
  it('does the same on the y axis for a tall image', () => {
    const tall = { w: 1000, h: 2000 };
    expect(coverPosition(tall, box, { x: 0.5, y: 0 }).y).toBe(0);
    expect(coverPosition(tall, box, { x: 0.5, y: 1 }).y).toBe(100);
    expect(coverPosition(tall, box, { x: 0.5, y: 0.6 }).y).toBeCloseTo(70, 6);
    expect(coverPosition(tall, box, { x: 0.1, y: 0.5 }).x).toBe(50);
  });
  it('falls back to the focal point itself before the image size is known, and clamps bad input', () => {
    expect(coverPosition(null, box, { x: 0.3, y: 0.7 })).toEqual({ x: 30, y: 70 });
    expect(coverPosition(null, box, { x: -2, y: 9 })).toEqual({ x: 0, y: 100 });
    expect(coverPosition({ w: 0, h: 0 }, box, { x: 0.3, y: 0.7 })).toEqual({ x: 30, y: 70 });
    expect(objectPosition({ w: 2000, h: 1000 }, box, { x: 0.4, y: 0.5 })).toBe('30.00% 50.00%');
  });
});

describe('focal point input', () => {
  const rect = { left: 100, top: 50, width: 200, height: 100 };
  it('maps a pointer to [0,1] and clamps outside the box', () => {
    expect(focalFromPointer(200, 100, rect)).toEqual({ x: 0.5, y: 0.5 });
    expect(focalFromPointer(0, 1000, rect)).toEqual({ x: 0, y: 1 });
    expect(focalFromPointer(999, -5, rect)).toEqual({ x: 1, y: 0 });
    expect(focalFromPointer(5, 5, { left: 0, top: 0, width: 0, height: 0 })).toEqual({ x: 0.5, y: 0.5 });
  });
  it('nudges 2% per arrow, 10% with Shift, clamped, and ignores other keys', () => {
    expect(nudgeFocal({ x: 0.5, y: 0.5 }, 'ArrowRight', false)).toEqual({ x: 0.52, y: 0.5 });
    expect(nudgeFocal({ x: 0.5, y: 0.5 }, 'ArrowLeft', true)).toEqual({ x: 0.4, y: 0.5 });
    expect(nudgeFocal({ x: 0.5, y: 0.5 }, 'ArrowUp', false)).toEqual({ x: 0.5, y: 0.48 });
    expect(nudgeFocal({ x: 0.5, y: 0.95 }, 'ArrowDown', true)).toEqual({ x: 0.5, y: 1 });
    expect(nudgeFocal({ x: 0.01, y: 0.5 }, 'ArrowLeft', false)).toEqual({ x: 0, y: 0.5 });
    expect(nudgeFocal({ x: 0.5, y: 0.5 }, 'a', false)).toBeNull();
    // Repeated 2% steps do not drift away from two decimals.
    let f = { x: 0.1, y: 0.5 };
    for (let i = 0; i < 7; i++) f = nudgeFocal(f, 'ArrowRight', false)!;
    expect(f.x).toBe(0.24);
  });
  it('words the marker value', () => {
    expect(focalText({ x: 0.5, y: 0.4 })).toBe('x 50%, y 40%');
  });
});

describe('photoWarning', () => {
  it('warns under 1275 x 1650 for the full page, at the boundary', () => {
    expect(photoWarning(1275, 1650, 'full')).toBeNull();
    expect(photoWarning(1274, 1650, 'full')).toContain('1,274 × 1,650 px');
    expect(photoWarning(1275, 1649, 'full')).not.toBeNull();
    expect(photoWarning(1080, 1350, 'full')).toBe('Photo is 1,080 × 1,350 px. A sharp full page needs 1,275 × 1,650 px or more; the half size is fine.');
  });
  it('uses 825 x 1275 for the half size', () => {
    expect(photoWarning(825, 1275, 'half')).toBeNull();
    expect(photoWarning(824, 1275, 'half')).toContain('825 × 1,275');
    expect(photoWarning(1080, 1350, 'half')).toBeNull();
  });
});

describe('wording helpers', () => {
  it('summarises a selection with singular and plural forms', () => {
    expect(selectionSummary([])).toBe('0 locations · 0 flyers');
    expect(selectionSummary([{ copies: 1 }])).toBe('1 location · 1 flyer');
    expect(selectionSummary([{ copies: 2 }, { copies: 4 }, { copies: 0 }, { copies: 0 }, { copies: 0 }])).toBe('5 locations · 6 flyers');
  });
  it('labels, shortens and tags', () => {
    expect(flyerLabel('Mission Library', 'mission')).toBe('Mission Library · mission');
    expect(displayShortUrl('https://go.opencircuitsf.com/ab12/')).toBe('go.opencircuitsf.com/ab12');
    expect(utmSummary('soldering-101')).toBe('utm_source=flyer · utm_medium=print · utm_campaign=soldering-101 · utm_content=<location code>');
  });
  it('limits the tagline to 140 characters, counting a character once', () => {
    expect(validateTagline('x'.repeat(140))).toBeNull();
    expect(validateTagline('x'.repeat(141))).toBe('Tagline must be at most 140 characters.');
    expect(validateTagline('😀'.repeat(140))).toBeNull();
    expect(validateTagline(`  ${'x'.repeat(140)}  `)).toBeNull();
  });
});

describe('full-photo box (#0559)', () => {
  it('text meets WCAG AA against the fill composited over pure white', () => {
    const worst = composite(FULL_PHOTO_BOX.fill, FULL_PHOTO_BOX.alpha, '#ffffff');
    expect(FULL_PHOTO_BOX.alpha).toBeGreaterThanOrEqual(0.8);
    expect(FULL_PHOTO_BOX.alpha).toBeLessThanOrEqual(0.85);
    for (const ink of [FULL_PHOTO_BOX.ink, FULL_PHOTO_BOX.muted, FULL_PHOTO_BOX.accent]) {
      expect(contrastRatio(ink, worst)).toBeGreaterThanOrEqual(4.5);
    }
  });
  it('contrast helpers match known values', () => {
    expect(contrastRatio('#000000', '#ffffff')).toBeCloseTo(21, 5);
    expect(composite('#000000', 0.5, '#ffffff')).toBe('#808080');
  });
  it('quiet zone is at least 4 modules for any code up to the assumed size', () => {
    for (const qr of [0.9, 1.25]) {
      expect(qrQuietIn(qr) / (qr / QR_ASSUMED_MODULES)).toBeGreaterThanOrEqual(4);
    }
  });
  it('defaults to full-photo and keeps hyphenated labels whole', () => {
    expect(DEFAULT_FLYER_TEMPLATE).toBe('full-photo');
    expect(FLYER_TEMPLATES).toContain(DEFAULT_FLYER_TEMPLATE);
    expect(noBreakHyphens('a-b c')).toBe('a‑b c');
    expect(SITE_URL_TEXT).toBe('OpenCircuitSF.com');
  });
});
