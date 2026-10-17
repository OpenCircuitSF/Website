// @vitest-environment jsdom
// The full-photo template (#0559): the boxes, the printed site address, and the QR label.
import { readFileSync } from 'node:fs';
import { afterEach, describe, it, expect } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import FlyerFace from './FlyerFace.svelte';
import type { FlyerFacts } from '../../lib/types';
import type { FlyerInstance } from '../../lib/flyers';

const facts: FlyerFacts = { title: 'Soldering 101', date_line: 'Sat, Nov 14 · 10:00 AM', place_line: 'The Shop', cover_image: null };
const instance: FlyerInstance = {
  locationId: 1,
  shortUrl: 'https://go.example/k1',
  label: 'Cafe-Noir · cafe-noir',
  qrSrc: '/qr.svg',
};

function mount(template: 'full-photo' | 'photo-top' | 'split', size: 'full' | 'half', inst: FlyerInstance | null = instance) {
  return render(FlyerFace, { template, size, facts, tagline: 'Come solder', photo: '/p.webp', focal: { x: 0.5, y: 0.5 }, instance: inst });
}

afterEach(cleanup);

describe('FlyerFace full-photo', () => {
  it.each(['full', 'half'] as const)('puts text and QR in separate boxes over a full-face photo (%s)', (size) => {
    const { container } = mount('full-photo', size);
    expect(container.querySelector('.t-full-photo > .photo img')).toBeTruthy();
    const text = container.querySelector('.fp-text') as HTMLElement;
    const qr = container.querySelector('.fp-qr') as HTMLElement;
    expect(text.textContent).toContain('Soldering 101');
    expect(text.textContent).toContain('Come solder');
    expect(text.textContent).toContain('Sat, Nov 14');
    expect(text.textContent).toContain('The Shop');
    expect(qr.contains(text)).toBe(false);
    expect(qr.querySelector('img.qr-img')).toBeTruthy();
    expect(qr.querySelector('.fp-label')).toBeTruthy();
  });

  it('prints the general site address, never the short link', () => {
    const { container } = mount('full-photo', 'half');
    expect(container.querySelector('.fp-site')?.textContent).toBe('OpenCircuitSF.com');
    expect(container.textContent).not.toContain('go.example');
    expect(container.textContent).not.toContain('open_circuit_sf');
    expect((container.querySelector('img.qr-img') as HTMLImageElement).getAttribute('src')).toBe('/qr.svg');
  });

  it('labels the QR code below it and keeps hyphenated names from wrapping mid-word', () => {
    const { container } = mount('full-photo', 'half');
    const label = container.querySelector('.fp-label') as HTMLElement;
    expect(label.textContent).toBe('Cafe‑Noir · cafe‑noir');
    expect(label.textContent).not.toContain('-');
    const img = container.querySelector('.fp-qr img.qr-img') as HTMLElement;
    expect(img.compareDocumentPosition(label) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(container.querySelector('img.qr-img')?.getAttribute('alt')).toBe('QR code for Cafe-Noir · cafe-noir');
  });

  it('sets the quiet zone padding in inches on the QR box', () => {
    const { container } = mount('full-photo', 'full');
    const style = (container.querySelector('.fp-qr') as HTMLElement).getAttribute('style') ?? '';
    expect(style).toMatch(/--qpad:\s*0\.12\d*in/);
  });

  it('shows a QR placeholder in the editor before Generate', () => {
    const { container } = mount('full-photo', 'full', null);
    expect(container.querySelector('.fp-qr .qr-placeholder')).toBeTruthy();
    expect(container.querySelector('.fp-site')?.textContent).toBe('OpenCircuitSF.com');
  });
});

describe('FlyerFace other templates keep the short link', () => {
  it.each(['photo-top', 'split'] as const)('%s', (t) => {
    const { container } = mount(t, 'full');
    expect(container.querySelector('.fp')).toBeNull();
    expect(container.textContent).toContain('go.example/k1');
  });
});

describe('FlyerFace full-photo CSS declarations (#0560)', () => {
  // jsdom does not apply scoped Svelte styles, so pin the source text.
  const src = readFileSync('src/views/admin/FlyerFace.svelte', 'utf-8');
  const rule = (sel: string) => {
    const m = src.match(new RegExp(sel.replace(/[.]/g, '\\.') + ' \\{([^}]*)\\}'));
    return m ? m[1] : '';
  };

  it('.fp-label breaks an over-long token', () => {
    expect(rule('.t-full-photo .fp-label')).toContain('overflow-wrap: anywhere;');
  });

  it('.fp-text has an rgba fallback on the line before its color-mix fill', () => {
    const lines = rule('.t-full-photo .fp-text').split('\n').map((l) => l.trim());
    const i = lines.findIndex((l) => l.startsWith('background: color-mix('));
    expect(i).toBeGreaterThan(0);
    expect(lines[i - 1]).toBe('background: rgba(11, 16, 10, 0.84);');
  });
});
