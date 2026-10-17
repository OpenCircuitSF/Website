// @vitest-environment jsdom
//
// #0549: mounts FlyerPrint.svelte under jsdom with '../../lib/api' mocked.
// jsdom is not a browser (AGENTS.md §1): this proves the wiring (which
// instances land on which sheet, labels, QR src, the ready gate, the page
// rule's lifetime), NOT the print layout. The layout is checked in a real
// browser's print preview; lib/flyers.test.ts covers the geometry maths.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlyerSelection, FlyerView } from '../../lib/types';
import { FLYER_FONT_LOADS } from '../../lib/flyers';

const getOutreachFlyer = vi.fn<(id: number) => Promise<FlyerView>>();
vi.mock('../../lib/api', () => ({
  getOutreachFlyer: (id: number) => getOutreachFlyer(id),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: FlyerPrint } = await import('./FlyerPrint.svelte');

afterEach(() => {
  cleanup();
  getOutreachFlyer.mockReset();
  vi.restoreAllMocks();
});

function sel(id: number, copies = 1, withQr = true): FlyerSelection {
  return {
    location_id: id,
    name: `Board ${id}`,
    code: `b${id}`,
    address: '',
    active: true,
    copies,
    link: withQr
      ? { id: id * 10, short_link_key: `k${id}`, short_url: `https://go.example/k${id}`, has_qr: true, qr_url: `/admin/outreach-flyer-links/${id * 10}/qr.svg`, put_up_at: null }
      : null,
  };
}

function view(selection: FlyerSelection[], photo: string | null = null): FlyerView {
  return {
    flyer: {
      id: 7, outreach_id: 1, workshop_id: 2, workshop_slug: 'soldering-101', workshop_title: 'Soldering 101', photo,
      focal_x: 0.5, focal_y: 0.5, template: 'photo-top', tagline: 'Come solder', description: '', description_short: '', title_position: 'auto', created_at: '2026-10-16T00:00:00Z', updated_at: null,
    },
    facts: { title: 'Soldering 101', date_line: 'Sat, Nov 14 · 10:00 AM', place_line: 'The Shop', cover_image: null, description: '' },
    selection,
  };
}

async function mount(v: FlyerView, layout: 'full' | 'half'): Promise<void> {
  getOutreachFlyer.mockResolvedValue(v);
  render(FlyerPrint, { flyerId: 7, layout });
  await waitFor(() => expect(getOutreachFlyer).toHaveBeenCalledWith(7));
  await waitFor(() => expect(document.querySelectorAll('.sheet').length).toBeGreaterThan(0));
}

/** Let the ready gate's font step finish, so its image listeners are attached. */
const gateReady = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

/** Settle every image: load fires on each, as a browser would. */
async function loadAllImages(): Promise<void> {
  await gateReady();
  for (const img of Array.from(document.querySelectorAll('.sheet img'))) await fireEvent.load(img);
}

describe('FlyerPrint', () => {
  it('half layout with three instances makes two sheets, the odd last bottom half blank, with a cut line on each', async () => {
    await mount(view([sel(1, 2), sel(2, 1)]), 'half');
    const sheets = document.querySelectorAll('.sheet');
    expect(sheets).toHaveLength(2);
    expect(sheets[0].querySelectorAll('.slot')).toHaveLength(2);
    expect(sheets[0].querySelectorAll('.rot')).toHaveLength(2);
    expect(sheets[1].querySelectorAll('.slot')).toHaveLength(2);
    expect(sheets[1].querySelectorAll('.rot')).toHaveLength(1);
    expect(sheets[0].querySelector('.cut')).not.toBeNull();
    expect(sheets[1].querySelector('.cut')).not.toBeNull();
    expect((sheets[1].querySelectorAll('.slot')[0] as HTMLElement).style.getPropertyValue('--y')).toBe('5.5in');
    expect((sheets[0].querySelectorAll('.slot')[1] as HTMLElement).style.getPropertyValue('--y')).toBe('11in');
  });

  it('full layout is one sheet per flyer, with each location label and a QR img from the admin endpoint', async () => {
    await mount(view([sel(1, 1), sel(2, 1)]), 'full');
    expect(document.querySelectorAll('.sheet')).toHaveLength(2);
    expect(document.querySelectorAll('.rot')).toHaveLength(0);
    const labels = Array.from(document.querySelectorAll('.sheet .label')).map((n) => n.textContent);
    expect(labels).toEqual(['Board 1 · b1', 'Board 2 · b2']);
    const qr = document.querySelector('.sheet img.qr-img') as HTMLImageElement;
    expect(qr.getAttribute('src')).toBe('/admin/outreach-flyer-links/10/qr.svg');
    expect(qr.alt).toContain('Board 1');
    expect(screen.getAllByText('go.example/k1').length).toBeGreaterThan(0);
  });

  it('leaves out a location without a QR code yet and says so', async () => {
    await mount(view([sel(1), sel(2, 1, false), sel(3, 1, false)]), 'full');
    expect(document.querySelectorAll('.sheet')).toHaveLength(1);
    expect(screen.getByText('2 locations have no QR code yet. Generate first.')).toBeTruthy();
  });

  it('keeps Print disabled until every image has loaded, then enables it and prints', async () => {
    const print = vi.spyOn(window, 'print').mockImplementation(() => {});
    await mount(view([sel(1)], '/hero-crt-760.webp'), 'full');
    const button = screen.getByRole('button', { name: 'Print' }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    await loadAllImages();
    await waitFor(() => expect(button.disabled).toBe(false));
    await fireEvent.click(button);
    expect(print).toHaveBeenCalledTimes(1);
  });

  it('loads every flyer font before Print enables (#0561)', async () => {
    const load = vi.fn(() => Promise.resolve([]));
    Object.defineProperty(document, 'fonts', { configurable: true, value: { load, ready: Promise.resolve() } });
    try {
      await mount(view([sel(1)]), 'full');
      await loadAllImages();
      const button = screen.getByRole('button', { name: 'Print' }) as HTMLButtonElement;
      await waitFor(() => expect(button.disabled).toBe(false));
      expect(load).toHaveBeenCalledTimes(FLYER_FONT_LOADS.length);
      expect(load.mock.calls.map((c) => (c as unknown[])[0])).toEqual([...FLYER_FONT_LOADS]);
    } finally {
      Reflect.deleteProperty(document, 'fonts');
    }
  });

  it('still enables Print when a font load rejects (#0561)', async () => {
    const load = vi.fn(() => Promise.reject(new Error('no font')));
    Object.defineProperty(document, 'fonts', { configurable: true, value: { load, ready: Promise.resolve() } });
    try {
      await mount(view([sel(1)]), 'full');
      await loadAllImages();
      const button = screen.getByRole('button', { name: 'Print' }) as HTMLButtonElement;
      await waitFor(() => expect(button.disabled).toBe(false));
      expect(load).toHaveBeenCalled();
    } finally {
      Reflect.deleteProperty(document, 'fonts');
    }
  });

  it('names an image that failed to load', async () => {
    await mount(view([sel(1)]), 'full');
    const img = document.querySelector('.sheet img.qr-img') as HTMLImageElement;
    await gateReady();
    await fireEvent.error(img);
    await waitFor(() => expect(screen.getByText(/Could not load: QR code for Board 1/)).toBeTruthy());
  });

  it('adds the page rule and body class while mounted and removes them on unmount', async () => {
    await mount(view([sel(1)]), 'full');
    const rule = document.head.querySelector('style[data-flyer-print]');
    expect(rule?.textContent).toBe('@page { size: letter portrait; margin: 0; }');
    expect(document.body.classList.contains('flyer-print')).toBe(true);
    expect(document.body.querySelector(':scope > .flyer-print-root')).not.toBeNull();
    cleanup();
    expect(document.head.querySelector('style[data-flyer-print]')).toBeNull();
    expect(document.body.classList.contains('flyer-print')).toBe(false);
    expect(document.querySelector('.flyer-print-root')).toBeNull();
  });

  it('says why when the flyer cannot be loaded', async () => {
    getOutreachFlyer.mockRejectedValue(new Error('boom'));
    render(FlyerPrint, { flyerId: 7, layout: 'full' });
    await waitFor(() => expect(screen.getByText('Could not load the flyer. Please try again.')).toBeTruthy());
    expect((screen.getByRole('button', { name: 'Print' }) as HTMLButtonElement).disabled).toBe(true);
  });
});
