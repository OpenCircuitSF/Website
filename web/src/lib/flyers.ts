// Pure logic behind printable flyers (#0549): sheet geometry, filling sheets
// from a location selection, cropping a photo around its focal point, and the
// wording of the editor's hints. No DOM, so all of it is unit-testable; the
// Svelte components stay thin. Lengths are CSS inches (on screen 1in = 96px).
//
// The template list is also in internal/outreach's FlyerTemplates and the
// outreach_flyers_template_check constraint; keep the three in step. The title
// positions are in internal/outreach's FlyerTitlePositions and the
// outreach_flyers_title_position_check constraint (#0561).
import type { FlyerSelection, FlyerTemplate, FlyerTitlePosition } from './types';
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
 * The full-photo glass boxes: a dark brand-toned fill at 72% opacity with
 * light text. The photo behind them is unknown, so legibility is judged
 * against the worst case, the fill composited over pure white (`#4f534f`).
 * Against that: ink `#f2f6ee` 7.15:1, the tagline's `#d5ddd0` 5.62:1 (the
 * worst case), accent `#68ff23` 5.93:1, and the QR label `#10140f` on its own
 * white box 18.6:1. `dim` (`#3c9e0f`, the chevron and the 1px rule) is
 * decorative (PRD §4.2) and measures 2.27:1. There is no backdrop-filter:
 * contrast must not depend on a print path honouring it.
 */
export const FULL_PHOTO_BOX = {
  fill: '#0b100a',
  alpha: 0.72,
  ink: '#f2f6ee',
  muted: '#d5ddd0',
  accent: '#68ff23',
  dim: '#3c9e0f',
} as const;

/**
 * The full-photo caption layout's type scale and geometry, per size (#0561).
 * One object drives both the CSS custom properties and the placement maths.
 * Lengths are inches, type is pt.
 */
export interface CaptionScale {
  inset: number;
  titleSteps: readonly number[];
  tagline: number;
  date: number;
  venue: number;
  desc: number;
  descLines: number;
  brand: number;
  qr: number;
  label: number;
  radius: number;
  capPadT: number;
  capPadLR: number;
  capPadB: number;
  capBar: number;
  footGap: number;
  detPadTB: number;
  detPadLR: number;
  detGap: number;
  boxGap: number;
  subjectClear: number;
}

export const CAPTION: Record<FlyerPrintLayout, CaptionScale> = {
  full: {
    inset: 0.4,
    titleSteps: [54, 48, 42, 36, 30],
    tagline: 19,
    date: 15,
    venue: 10.5,
    desc: 12,
    descLines: 6,
    brand: 10,
    qr: 1.5,
    label: 7.5,
    radius: 0.1,
    capPadT: 0.22,
    capPadLR: 0.28,
    capPadB: 0.26,
    capBar: 0.06,
    footGap: 0.25,
    detPadTB: 0.22,
    detPadLR: 0.26,
    detGap: 0.13,
    boxGap: 0.25,
    subjectClear: 1.0,
  },
  half: {
    inset: 0.32,
    titleSteps: [36, 32, 28, 24, 20],
    tagline: 13,
    date: 11,
    venue: 8,
    desc: 9,
    descLines: 3,
    brand: 8,
    qr: 1.1,
    label: 6.5,
    radius: 0.07,
    capPadT: 0.15,
    capPadLR: 0.2,
    capPadB: 0.18,
    capBar: 0.045,
    footGap: 0.18,
    detPadTB: 0.15,
    detPadLR: 0.18,
    detGap: 0.09,
    boxGap: 0.18,
    subjectClear: 0.75,
  },
};

const faceSize = (size: FlyerPrintLayout): Size => (size === 'half' ? HALF : LETTER);

/** The CSS custom properties (on the `.fp` wrapper) that carry CAPTION, plus the fitted title size. */
export function captionVars(size: FlyerPrintLayout, titlePt: number): string {
  const c = CAPTION[size];
  const inch = (v: number): string => `${v}in`;
  const pt = (v: number): string => `${v}pt`;
  const qrPad = qrQuietIn(c.qr).toFixed(4);
  return [
    ['--inset', inch(c.inset)],
    ['--title-pt', pt(titlePt)],
    ['--tagline', pt(c.tagline)],
    ['--date', pt(c.date)],
    ['--venue', pt(c.venue)],
    ['--desc', pt(c.desc)],
    ['--desc-lines', String(c.descLines)],
    ['--brand', pt(c.brand)],
    ['--qr', inch(c.qr)],
    ['--label', pt(c.label)],
    ['--radius', inch(c.radius)],
    ['--cap-pad', `${c.capPadT}in ${c.capPadLR}in ${c.capPadB}in`],
    ['--cap-bar', inch(c.capBar)],
    ['--foot-gap', inch(c.footGap)],
    ['--det-pad', `${c.detPadTB}in ${c.detPadLR}in`],
    ['--det-gap', inch(c.detGap)],
    ['--qpad', `${qrPad}in`],
  ]
    .map(([k, v]) => `${k}: ${v}`)
    .join('; ');
}

/**
 * The most room the title box and the footer can take, in inches, from the
 * clamps CSS applies: a 3-line title and tagline, a 2-line date and venue,
 * `descLines` of description, a 3-line QR label. Placement reserves these, so
 * the two never overlap.
 */
export function captionReserve(size: FlyerPrintLayout): { titleMax: number; footerMax: number } {
  const c = CAPTION[size];
  const titleMax =
    (3 * c.titleSteps[0] * 0.92) / 72 + (3 * c.tagline * 1.2) / 72 + (0.12 * c.tagline) / 72 + c.capPadT + c.capPadB;
  const details =
    2 * c.detPadTB +
    (2 * c.date * 1.2) / 72 +
    0.04 +
    (2 * c.venue * 1.3) / 72 +
    (c.descLines * c.desc * 1.4) / 72 +
    3 * c.detGap +
    1 / 96 +
    (c.brand * 1.2) / 72;
  const qrBox = 2 * qrQuietIn(c.qr) + c.qr + 0.03 + (3 * c.label * 1.2) / 72;
  return { titleMax, footerMax: Math.max(details, qrBox) };
}

/**
 * The focal point's displayed y as a fraction of the face, after the cover
 * crop (the same scale and offset as coverPosition). Without the image size
 * it falls back to the focal y.
 */
export function focalOnFace(img: Size | null, box: Size, focal: Focal): number {
  const fy = clamp01(focal.y);
  if (!img || img.w <= 0 || img.h <= 0 || box.w <= 0 || box.h <= 0) return fy;
  const s = Math.max(box.w / img.w, box.h / img.h);
  const dh = img.h * s;
  const p = coverPosition(img, box, focal).y / 100;
  return (p * (box.h - dh) + fy * dh) / box.h;
}

export interface CaptionPlacement {
  arrangement: 'below' | 'above';
  /** Which edge of the face `offset` is measured from. */
  edge: 'top' | 'bottom';
  /** Inches from that edge to the title box. */
  offset: number;
}

const clampTo = (v: number, lo: number, hi: number): number => Math.min(hi, Math.max(lo, v));

/**
 * Where the title box goes, and which end the details footer takes. `auto`
 * puts the title under the subject when the focal point is in the top half
 * (footer at the bottom) and over it otherwise (footer flipped to the top).
 * Both bounds come from captionReserve, so title and footer cannot overlap.
 */
export function captionPlacement(focal: Focal, img: Size | null, size: FlyerPrintLayout, mode: FlyerTitlePosition): CaptionPlacement {
  const c = CAPTION[size];
  const face = faceSize(size);
  const { titleMax, footerMax } = captionReserve(size);
  const arrangement = mode === 'auto' ? (clamp01(focal.y) < 0.5 ? 'below' : 'above') : mode;
  const faceY = focalOnFace(img, face, focal) * face.h;
  if (arrangement === 'below') {
    const top = clampTo(faceY + c.subjectClear, c.inset, face.h - c.inset - footerMax - c.boxGap - titleMax);
    return { arrangement, edge: 'top', offset: top };
  }
  const edgeY = clampTo(faceY - c.subjectClear, c.inset + footerMax + c.boxGap + titleMax, face.h - c.inset);
  return { arrangement, edge: 'bottom', offset: face.h - edgeY };
}

/**
 * Archivo 900 advances in units per 1000 em, instanced at wght=900 from
 * web/public/fonts/archivo-variable-latin.woff2 (SHA-256 starts
 * 8f704806dbedeaae). Re-derive the table if that file changes. The title is
 * always uppercased, so only capitals, digits and punctuation are listed.
 */
export const ARCHIVO_900_ADVANCE: Record<string, number> = {
  A: 778, B: 778, C: 778, D: 778, E: 722, F: 667, G: 833, H: 833, I: 369, J: 667, K: 833, L: 667, M: 972,
  N: 833, O: 833, P: 722, Q: 833, R: 778, S: 722, T: 722, U: 833, V: 778, W: 1000, X: 778, Y: 778, Z: 722,
  '0': 667, '1': 667, '2': 667, '3': 667, '4': 667, '5': 667, '6': 667, '7': 667, '8': 667, '9': 667,
  ' ': 180, '&': 889, '-': 333, "'": 278, '.': 333, ',': 333, ':': 333, '!': 333, '?': 611, '/': 306,
  '+': 660, '#': 660, '(': 389, ')': 389, '\u2019': 278, '\u2013': 500, '\u2014': 1000, '"': 497, '@': 1010,
};

const TITLE_TRACKING_EM = 0.02;
/** Estimated widths are inflated by this much. At 1.04 or more "PROGRAMMING" steps down at half size. */
export const TITLE_FIT_SAFETY = 1.03;

/** The width of `s` in em at Archivo 900, uppercased, with -0.02em tracking after every character. */
export function titleWidthEm(s: string): number {
  let w = 0;
  for (const ch of s.toLocaleUpperCase('en-US')) {
    w += (ARCHIVO_900_ADVANCE[ch] ?? 1000) / 1000 - TITLE_TRACKING_EM;
  }
  return w;
}

/** The room for title text inside the caption box, in inches. */
export function titleAvailIn(size: FlyerPrintLayout): number {
  const c = CAPTION[size];
  return faceSize(size).w - 2 * c.inset - c.capBar - 2 * c.capPadLR;
}

export interface TitleFit {
  pt: number;
  lines: number;
  /** Even the smallest step cannot hold the widest word; CSS may then break inside it. */
  breakAnywhere: boolean;
}

/**
 * The largest title size, from the size's steps, at which the widest word
 * fits the line and a greedy wrap takes at most 3 lines. A pure function over
 * glyph advances, so it needs no layout (jsdom has none) and no loaded font.
 */
export function titleFit(title: string, size: FlyerPrintLayout): TitleFit {
  const steps = CAPTION[size].titleSteps;
  const avail = titleAvailIn(size);
  const words = title.toLocaleUpperCase('en-US').split(/\s+/).filter(Boolean);
  const widths = words.map(titleWidthEm);
  const widest = widths.length ? Math.max(...widths) : 0;
  const space = (180 / 1000 - TITLE_TRACKING_EM) as number;
  const wrap = (limit: number): number => {
    let lines = widths.length ? 1 : 0;
    let cur = 0;
    for (const w of widths) {
      if (cur === 0) cur = w;
      else if (cur + space + w <= limit) cur += space + w;
      else {
        lines++;
        cur = w;
      }
    }
    return lines;
  };
  for (const pt of steps) {
    const limit = (avail * 72) / pt / TITLE_FIT_SAFETY;
    if (widest <= limit) {
      const lines = wrap(limit);
      if (lines <= 3) return { pt, lines, breakAnywhere: false };
    }
  }
  const pt = steps[steps.length - 1];
  const limit = (avail * 72) / pt / TITLE_FIT_SAFETY;
  return { pt, lines: wrap(limit), breakAnywhere: widest > limit };
}

export const MAX_FLYER_DESCRIPTION = 360;
export const MAX_FLYER_DESCRIPTION_SHORT = 140;

/** An error for a description over `max` characters (code points), else null. */
export function validateFlyerDescription(text: string, max: number, what: string): string | null {
  return [...text.trim()].length > max ? `${what} must be at most ${max} characters.` : null;
}

/**
 * Trim and collapse whitespace, then fit `max` characters without cutting
 * inside a word: at a sentence end when that keeps at least half of `max`,
 * otherwise at a word boundary with an ellipsis.
 */
export function shortenAtBoundary(text: string, max: number): string {
  const t = text.trim().replace(/\s+/g, ' ');
  if (t.length <= max) return t;
  let sentence = -1;
  for (let i = 0; i + 1 <= max && i < t.length - 1; i++) {
    if ('.!?'.includes(t[i]) && t[i + 1] === ' ') sentence = i;
  }
  if (sentence >= 0 && sentence + 1 >= max / 2) return t.slice(0, sentence + 1);
  const cut = t.lastIndexOf(' ', max - 1);
  if (cut > 0) return t.slice(0, cut).replace(/[\s,;:]+$/, '') + '\u2026';
  const space = t.indexOf(' ');
  return (space < 0 ? t : t.slice(0, space)) + '\u2026';
}

/** The description text for the full and half faces. An empty string means no paragraph. */
export function flyerDescriptions(factsDescription: string, override: string, overrideShort: string): { full: string; half: string } {
  const full = shortenAtBoundary(override.trim() || factsDescription.trim(), MAX_FLYER_DESCRIPTION);
  const half = overrideShort.trim() || shortenAtBoundary(full, MAX_FLYER_DESCRIPTION_SHORT);
  return { full, half };
}

export const FLYER_TITLE_POSITIONS: readonly FlyerTitlePosition[] = ['auto', 'below', 'above'];

export const FLYER_TITLE_POSITION_LABELS: Record<FlyerTitlePosition, string> = {
  auto: 'Auto (from the focal point)',
  below: 'Under the subject (details at the bottom)',
  above: 'Over the subject (details at the top)',
};

/**
 * Fonts the print view loads explicitly before it enables Print. With
 * font-display: swap, document.fonts.ready alone can resolve before a face
 * that has not been requested yet starts loading, and Archivo 900 is used
 * only by the full-photo title.
 */
export const FLYER_FONT_LOADS: readonly string[] = [
  '900 54pt Archivo',
  '500 19pt Archivo',
  '400 12pt Archivo',
  '700 10pt "JetBrains Mono"',
  '400 10pt "JetBrains Mono"',
];

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
