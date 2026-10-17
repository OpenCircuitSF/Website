// @vitest-environment jsdom
// The full-photo caption layout (#0561, after #0559): the title box, the details footer, the QR box.
import { readFileSync } from 'node:fs';
import { afterEach, describe, it, expect } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import FlyerFace from './FlyerFace.svelte';
import type { FlyerFacts, FlyerTitlePosition } from '../../lib/types';
import { shortenAtBoundary, type FlyerInstance } from '../../lib/flyers';

const facts: FlyerFacts = { title: 'Soldering 101', date_line: 'Sat, Nov 14 · 10:00 AM', place_line: 'The Shop', cover_image: null, description: 'Learn to solder.' };
const instance: FlyerInstance = {
  locationId: 1,
  shortUrl: 'https://go.example/k1',
  label: 'Cafe-Noir · cafe-noir',
  qrSrc: '/qr.svg',
};
const PARAGRAPH =
  'Wire a strip of addressable RGB LEDs to a small microcontroller, then write the code that makes them chase, pulse and fade. We start from one blinking pixel and build up to an animation of your own. No experience needed, parts are provided, and the strip and board go home with you.';

interface Over {
  facts?: FlyerFacts;
  description?: string;
  descriptionShort?: string;
  titlePosition?: FlyerTitlePosition;
  focalY?: number;
  inst?: FlyerInstance | null;
}

function mount(template: 'full-photo' | 'photo-top' | 'split', size: 'full' | 'half', o: Over = {}) {
  return render(FlyerFace, {
    template,
    size,
    facts: o.facts ?? facts,
    tagline: 'Come solder',
    description: o.description ?? '',
    descriptionShort: o.descriptionShort ?? '',
    titlePosition: o.titlePosition ?? 'auto',
    photo: '/p.webp',
    focal: { x: 0.5, y: o.focalY ?? 0.5 },
    instance: o.inst === undefined ? instance : o.inst,
  });
}

const q = (c: HTMLElement, sel: string) => c.querySelector(sel) as HTMLElement;

afterEach(cleanup);

describe('FlyerFace full-photo caption layout', () => {
  it.each(['full', 'half'] as const)('splits the title box from the details and QR boxes (%s)', (size) => {
    const { container } = mount('full-photo', size);
    expect(container.querySelector('.t-full-photo > .photo img')).toBeTruthy();
    const cap = q(container, '.fp-cap');
    expect(cap.textContent).toContain('Soldering 101');
    expect(cap.textContent).toContain('Come solder');
    expect(cap.textContent).not.toContain('Sat, Nov 14');
    const det = q(container, '.fp-details');
    for (const t of ['Sat, Nov 14', 'The Shop', 'Learn to solder.', '> open_circuit_sf', 'OpenCircuitSF.com']) expect(det.textContent).toContain(t);
    expect(q(container, '.fp-brand').compareDocumentPosition(q(container, '.fp-site')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const qr = q(container, '.fp-qr');
    expect(qr.parentElement).toBe(det.parentElement);
    expect(qr.parentElement?.classList.contains('fp-footer')).toBe(true);
    expect(qr.querySelector('img.qr-img')).toBeTruthy();
    expect(container.textContent).not.toContain('go.example');
    expect((container.querySelector('img.qr-img') as HTMLImageElement).getAttribute('src')).toBe('/qr.svg');
  });

  it('labels the QR code below it and keeps hyphenated names from wrapping mid-word', () => {
    const { container } = mount('full-photo', 'half');
    const label = q(container, '.fp-label');
    expect(label.textContent).toBe('Cafe\u2011Noir · cafe\u2011noir');
    expect(label.textContent).not.toContain('-');
    expect(q(container, '.fp-qr img.qr-img').compareDocumentPosition(label) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(container.querySelector('img.qr-img')?.getAttribute('alt')).toBe('QR code for Cafe-Noir · cafe-noir');
  });

  it('sets the quiet zone padding in inches on the face wrapper', () => {
    const { container } = mount('full-photo', 'full');
    expect(q(container, '.fp').getAttribute('style') ?? '').toMatch(/--qpad:\s*0\.14\d*in/);
  });

  it('shows a QR placeholder in the editor before Generate', () => {
    const { container } = mount('full-photo', 'full', { inst: null });
    expect(container.querySelector('.fp-qr .qr-placeholder')).toBeTruthy();
    expect(q(container, '.fp-site').textContent).toBe('OpenCircuitSF.com');
  });

  it('describes from the facts, the override wins, and empty omits the paragraph', () => {
    expect(q(mount('full-photo', 'full').container, '.fp-desc').textContent).toBe('Learn to solder.');
    cleanup();
    expect(q(mount('full-photo', 'full', { description: 'Mine.' }).container, '.fp-desc').textContent).toBe('Mine.');
    cleanup();
    expect(mount('full-photo', 'full', { facts: { ...facts, description: '' } }).container.querySelector('.fp-desc')).toBeNull();
  });

  it('uses the short text on the half face, or a shortened description without cutting a word', () => {
    expect(q(mount('full-photo', 'half', { description: PARAGRAPH, descriptionShort: 'Short one.' }).container, '.fp-desc').textContent).toBe('Short one.');
    cleanup();
    const text = q(mount('full-photo', 'half', { description: PARAGRAPH }).container, '.fp-desc').textContent ?? '';
    expect(text).toBe(shortenAtBoundary(PARAGRAPH, 140));
    expect(text.length).toBeLessThanOrEqual(140);
    const words = text.replace(/\u2026$/, '').split(' ');
    expect(PARAGRAPH.split(' ')).toEqual(expect.arrayContaining(words));
  });

  it('fits the title size and only allows mid-word breaks for a pathological word', () => {
    const t = (title: string) => mount('full-photo', 'full', { facts: { ...facts, title } }).container;
    const micro = t('Microcontrollers');
    expect(q(micro, '.fp').getAttribute('style')).toContain('--title-pt: 42pt');
    cleanup();
    expect(q(t('Programming LEDs'), '.fp-title').classList.contains('break-anywhere')).toBe(false);
    cleanup();
    expect(q(t('Supercalifragilisticexpialidocious'), '.fp-title').classList.contains('break-anywhere')).toBe(true);
  });

  it('places the title under the subject for a high focal point and over it for a low one', () => {
    const hi = mount('full-photo', 'full', { focalY: 0.2 }).container;
    expect(q(hi, '.fp-cap').getAttribute('style')).toContain('top:');
    expect(q(hi, '.fp-footer').classList.contains('at-top')).toBe(false);
    cleanup();
    const lo = mount('full-photo', 'full', { focalY: 0.8 }).container;
    expect(q(lo, '.fp-cap').getAttribute('style')).toContain('bottom:');
    expect(q(lo, '.fp-footer').classList.contains('at-top')).toBe(true);
    cleanup();
    const forced = mount('full-photo', 'full', { focalY: 0.8, titlePosition: 'below' }).container;
    expect(q(forced, '.fp-cap').getAttribute('style')).toContain('top:');
    expect(q(forced, '.fp-footer').classList.contains('at-top')).toBe(false);
  });
});

describe('FlyerFace other templates keep the short link', () => {
  it.each(['photo-top', 'split'] as const)('%s', (t) => {
    const { container } = mount(t, 'full');
    expect(container.querySelector('.fp')).toBeNull();
    expect(container.textContent).toContain('go.example/k1');
  });
});

describe('FlyerFace full-photo CSS declarations (#0560, #0561)', () => {
  // jsdom does not apply scoped Svelte styles, so pin the source text.
  const src = readFileSync('src/views/admin/FlyerFace.svelte', 'utf-8');
  const rule = (sel: string) => {
    const m = src.match(new RegExp(sel.replace(/[.]/g, '\\.') + ' \\{([^}]*)\\}'));
    return m ? m[1] : '';
  };

  it('.fp-label breaks an over-long token', () => {
    expect(rule('.t-full-photo .fp-label')).toContain('overflow-wrap: anywhere;');
  });

  it.each(['.fp-cap', '.fp-details'])('%s has an rgba fallback on the line before its color-mix fill', (sel) => {
    const lines = rule('.t-full-photo ' + sel).split('\n').map((l) => l.trim());
    const i = lines.findIndex((l) => l.startsWith('background: color-mix('));
    expect(i).toBeGreaterThan(0);
    expect(lines[i - 1]).toBe('background: rgba(11, 16, 10, 0.72);');
  });

  it('.fp-title never breaks inside a word by default', () => {
    const r = rule('.t-full-photo .fp-title');
    expect(r).toContain('overflow-wrap: normal;');
    expect(r).toContain('text-transform: uppercase;');
    expect(r).toContain('font-weight: 900;');
  });
});
