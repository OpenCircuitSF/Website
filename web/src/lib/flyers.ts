// Pure logic behind printable flyers (#0549): sheet geometry, filling sheets
// from a location selection, cropping a photo around its focal point, and the
// wording of the editor's hints. No DOM, so all of it is unit-testable; the
// Svelte components stay thin. Lengths are CSS inches (on screen 1in = 96px).
//
// The template list is also in internal/outreach's FlyerTemplates and the
// outreach_flyers_template_check constraint; keep the three in step.
import type { FlyerSelection, FlyerTemplate } from './types';
import type { FlyerPrintLayout } from './adminRouter';

export const FLYER_TEMPLATES: readonly FlyerTemplate[] = ['photo-top', 'full-photo', 'split'];

export const FLYER_TEMPLATE_LABELS: Record<FlyerTemplate, string> = {
  'photo-top': 'Photo top',
  'full-photo': 'Full photo',
  split: 'Split',
};

export interface Size {
  w: number;
  h: number;
}

/** US Letter, portrait: the print sheet. */
export const LETTER: Size = { w: 8.5, h: 11 };
/** One half-size flyer: portrait, two per Letter sheet. */
export const HALF: Size = { w: 5.5, h: 8.5 };
/** Text and the QR code stay this far inside every edge. */
export const SAFE_IN = 0.25;
/** The dashed cut line on a half-size sheet. */
export const CUT_AT_IN = 5.5;

export const MAX_TAGLINE = 140;

/**
 * The @page rule for the print view. It is inserted into <head> while the
 * print view is mounted and removed after: a @page rule in any bundled CSS
 * would apply to every page of the site.
 */
export const PRINT_PAGE_CSS = '@page { size: letter portrait; margin: 0; }';

/** One printed flyer: a location's copy, ready to draw. */
export interface FlyerInstance {
  locationId: number;
  shortUrl: string;
  label: string;
  qrSrc: string;
}

/** "Name · code", the small label beside a QR code. */
export function flyerLabel(name: string, code: string): string {
  return `${name} · ${code}`;
}

/** "https://go.example/abc" -> "go.example/abc". */
export function displayShortUrl(url: string): string {
  return url.replace(/^https?:\/\//i, '').replace(/\/+$/, '');
}

/** A selection entry can print once its link has a cached QR code. */
export function isPrintable(s: FlyerSelection): boolean {
  return s.link !== null && s.link.has_qr && s.link.qr_url !== null;
}

/**
 * The printed instances in location order: each printable selection repeated
 * `copies` times, copies consecutive. A location without a link or QR yet is
 * left out (see missingQrCount). The server orders the selection by name.
 */
export function expandCopies(selection: FlyerSelection[]): FlyerInstance[] {
  const out: FlyerInstance[] = [];
  for (const s of selection) {
    if (!isPrintable(s) || !s.link || s.link.qr_url === null) continue;
    for (let i = 0; i < s.copies; i++) {
      out.push({
        locationId: s.location_id,
        shortUrl: s.link.short_url,
        label: flyerLabel(s.name, s.code),
        qrSrc: s.link.qr_url,
      });
    }
  }
  return out;
}

/** How many selected locations cannot print yet. */
export function missingQrCount(selection: FlyerSelection[]): number {
  return selection.filter((s) => !isPrintable(s)).length;
}

/** The toolbar note for locations left out of a print, or null when none are. */
export function missingQrNote(n: number): string | null {
  if (n <= 0) return null;
  return n === 1 ? '1 location has no QR code yet. Generate first.' : `${n} locations have no QR code yet. Generate first.`;
}

/** Sheets for n flyers at one per sheet. */
export function sheetsFull(n: number): number {
  return Math.max(0, n);
}

/** Sheets for n flyers at two per sheet; an odd last flyer leaves its bottom half blank. */
export function sheetsHalf(n: number): number {
  return Math.ceil(Math.max(0, n) / 2);
}

export function sheetCount(n: number, layout: FlyerPrintLayout): number {
  return layout === 'half' ? sheetsHalf(n) : sheetsFull(n);
}

/**
 * Lay instances out on sheets. Full: one slot per sheet. Half: two slots per
 * sheet, filled top then bottom in order; an odd total leaves the last
 * bottom slot null (blank, with its cut line still drawn).
 */
export function paginate(instances: FlyerInstance[], layout: FlyerPrintLayout): (FlyerInstance | null)[][] {
  const per = layout === 'half' ? 2 : 1;
  const sheets: (FlyerInstance | null)[][] = [];
  for (let i = 0; i < instances.length; i += per) {
    const sheet: (FlyerInstance | null)[] = [];
    for (let j = 0; j < per; j++) sheet.push(instances[i + j] ?? null);
    sheets.push(sheet);
  }
  return sheets;
}

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

const clean = (v: number): number => Math.round(v * 1e6) / 1e6 + 0;

/**
 * Where a half-size flyer ends up on its half-sheet slot. Each flyer is a
 * 5.5 x 8.5 in portrait box placed at top: 5.5in with `rotate(-90deg)` about
 * its top-left corner. CSS rotate(θ) maps (x, y) to (x cosθ - y sinθ,
 * x sinθ + y cosθ), so θ = -90° maps (x, y) to (y, -x); the 5.5in offset
 * then brings the box into the slot. The bounding box of the four corners
 * must be exactly the slot, [0, 8.5] x [0, 5.5].
 */
export function rotatedHalfBounds(): Rect {
  const corners: [number, number][] = [
    [0, 0],
    [HALF.w, 0],
    [0, HALF.h],
    [HALF.w, HALF.h],
  ];
  const pts = corners.map(([x, y]) => ({ x: y, y: -x + CUT_AT_IN }));
  const xs = pts.map((p) => p.x);
  const ys = pts.map((p) => p.y);
  const x = Math.min(...xs);
  const y = Math.min(...ys);
  return { x: clean(x), y: clean(y), w: clean(Math.max(...xs) - x), h: clean(Math.max(...ys) - y) };
}

/** The flyer's content box: the flyer inside its safe-area padding. */
export function contentBox(size: FlyerPrintLayout): Size {
  const f = size === 'half' ? HALF : LETTER;
  return { w: f.w - 2 * SAFE_IN, h: f.h - 2 * SAFE_IN };
}

/** Share of the content height the photo takes on the photo-top template. */
export function photoTopShare(size: FlyerPrintLayout): number {
  return size === 'half' ? 0.53 : 0.58;
}

/**
 * The photo's box in inches for a template and size. Only its proportions
 * matter (the crop is computed against it). Full photo bleeds across the
 * whole flyer; split takes the left half of the content box.
 */
export function photoBox(template: FlyerTemplate, size: FlyerPrintLayout): Size {
  const flyer = size === 'half' ? HALF : LETTER;
  const c = contentBox(size);
  switch (template) {
    case 'full-photo':
      return { w: flyer.w, h: flyer.h };
    case 'split':
      return { w: c.w / 2, h: c.h };
    default:
      return { w: c.w, h: c.h * photoTopShare(size) };
  }
}

export interface Focal {
  x: number;
  y: number;
}

const clamp01 = (v: number): number => (Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : 0.5);

/**
 * The `object-position` (as percentages) that, with `object-fit: cover`,
 * keeps the focal point as close to the center of the box as the image
 * allows without leaving a gap at an edge. For scale s = max(bw/iw, bh/ih)
 * and displayed size dw = iw*s, the left offset that centers the focal point
 * is L = bw/2 - fx*dw, and CSS's x% is L / (bw - dw), clamped to [0, 1]
 * (0.5 on an axis where the image exactly fits). Without the natural size
 * (the image has not loaded) it falls back to the focal point itself.
 */
export function coverPosition(img: Size | null, box: Size, focal: Focal): { x: number; y: number } {
  const fx = clamp01(focal.x);
  const fy = clamp01(focal.y);
  if (!img || img.w <= 0 || img.h <= 0 || box.w <= 0 || box.h <= 0) return { x: fx * 100, y: fy * 100 };
  const s = Math.max(box.w / img.w, box.h / img.h);
  const axis = (b: number, d: number, f: number): number => {
    if (Math.abs(d - b) < 1e-9) return 50;
    const p = (b / 2 - f * d) / (b - d);
    return Math.min(1, Math.max(0, p)) * 100;
  };
  return { x: axis(box.w, img.w * s, fx), y: axis(box.h, img.h * s, fy) };
}

/** The CSS `object-position` value for coverPosition. */
export function objectPosition(img: Size | null, box: Size, focal: Focal): string {
  const p = coverPosition(img, box, focal);
  return `${p.x.toFixed(2)}% ${p.y.toFixed(2)}%`;
}

/** A pointer position inside the photo preview, as a focal point in [0, 1]. */
export function focalFromPointer(x: number, y: number, rect: { left: number; top: number; width: number; height: number }): Focal {
  if (rect.width <= 0 || rect.height <= 0) return { x: 0.5, y: 0.5 };
  return { x: clamp01((x - rect.left) / rect.width), y: clamp01((y - rect.top) / rect.height) };
}

/**
 * Move the focal point with an arrow key: 2% a press, 10% with Shift.
 * Returns null for any other key, so the caller leaves it alone.
 */
export function nudgeFocal(f: Focal, key: string, shift: boolean): Focal | null {
  const step = shift ? 0.1 : 0.02;
  const move = (v: number, d: number): number => clamp01(Math.round((v + d) * 100) / 100);
  switch (key) {
    case 'ArrowLeft':
      return { x: move(f.x, -step), y: f.y };
    case 'ArrowRight':
      return { x: move(f.x, step), y: f.y };
    case 'ArrowUp':
      return { x: f.x, y: move(f.y, -step) };
    case 'ArrowDown':
      return { x: f.x, y: move(f.y, step) };
    default:
      return null;
  }
}

/** "x 50%, y 40%": the focal marker's value text. */
export function focalText(f: Focal): string {
  return `x ${Math.round(f.x * 100)}%, y ${Math.round(f.y * 100)}%`;
}

/** Minimum photo pixels for a sharp print: 150 dpi at the flyer's size. */
export const PHOTO_MIN_PX: Record<FlyerPrintLayout, Size> = {
  full: { w: 1275, h: 1650 },
  half: { w: 825, h: 1275 },
};

const grouped = (n: number): string => n.toLocaleString('en-US');

/** A warning when the photo is too small for a sharp print at this layout, else null. */
export function photoWarning(w: number, h: number, layout: FlyerPrintLayout): string | null {
  const min = PHOTO_MIN_PX[layout];
  if (w >= min.w && h >= min.h) return null;
  const have = `Photo is ${grouped(w)} × ${grouped(h)} px.`;
  return layout === 'full'
    ? `${have} A sharp full page needs ${grouped(min.w)} × ${grouped(min.h)} px or more; the half size is fine.`
    : `${have} A sharp half size needs ${grouped(min.w)} × ${grouped(min.h)} px or more.`;
}

/** "5 locations · 6 flyers". */
export function selectionSummary(selection: { copies: number }[]): string {
  const locations = selection.length;
  const flyers = selection.reduce((n, s) => n + s.copies, 0);
  return `${locations} ${locations === 1 ? 'location' : 'locations'} · ${flyers} ${flyers === 1 ? 'flyer' : 'flyers'}`;
}

/** The tracking tags every flyer link carries, for the editor's hint. */
export function utmSummary(slug: string): string {
  return `utm_source=flyer · utm_medium=print · utm_campaign=${slug} · utm_content=<location code>`;
}

/** An error for a tagline over the limit, else null. Counts characters, not UTF-16 units. */
export function validateTagline(tagline: string): string | null {
  return [...tagline.trim()].length > MAX_TAGLINE ? `Tagline must be at most ${MAX_TAGLINE} characters.` : null;
}

/** The general site address printed on the full-photo flyer (the QR code carries the tracked link). */
export const SITE_URL_TEXT = 'OpenCircuitSF.com';

/** The template a new flyer starts with. Go's create handler defaults to the same value. */
export const DEFAULT_FLYER_TEMPLATE: FlyerTemplate = 'full-photo';

/**
 * The full-photo text box: a dark brand-toned fill at 84% opacity with light
 * text. The photo behind it is unknown, so legibility is judged against the
 * worst case, the fill composited over pure white.
 */
export const FULL_PHOTO_BOX = {
  fill: '#0b100a',
  alpha: 0.84,
  ink: '#f2f6ee',
  muted: '#d5ddd0',
  accent: '#68ff23',
} as const;

/** The QR box's quiet zone: at least this many modules of opaque white on every side. */
export const QR_QUIET_MODULES = 4;
/**
 * The module count assumed when sizing the quiet zone. Location links give
 * small codes (version 2-4, 25-33 modules); 41 (version 6) is a deliberately
 * generous bound, so the padding is never under 4 real modules.
 */
export const QR_ASSUMED_MODULES = 41;

/** The opaque white padding, in inches, around a QR image `qrIn` inches wide. */
export function qrQuietIn(qrIn: number): number {
  return (qrIn * QR_QUIET_MODULES) / QR_ASSUMED_MODULES;
}

/** Replace hyphens with U+2011 so a label wraps only at spaces ("cafe-noir" stays whole). */
export function noBreakHyphens(s: string): string {
  return s.replace(/-/g, '\u2011');
}

function channel(v: number): number {
  const c = v / 255;
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

function rgb(hex: string): [number, number, number] {
  const h = hex.replace('#', '');
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
}

/** WCAG relative luminance of a #rrggbb color. */
export function luminance(hex: string): number {
  const [r, g, b] = rgb(hex);
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

/** WCAG contrast ratio between two #rrggbb colors. */
export function contrastRatio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** `fill` at `alpha` over an opaque `under`, as #rrggbb. */
export function composite(fill: string, alpha: number, under: string): string {
  const f = rgb(fill);
  const u = rgb(under);
  return '#' + f.map((v, i) => Math.round(v * alpha + u[i] * (1 - alpha)).toString(16).padStart(2, '0')).join('');
}
