// @vitest-environment jsdom
//
// #0549: mounts OutreachFlyerEditor.svelte under jsdom with '../../lib/api'
// mocked. Proves the wiring: Generate saves the design, then the selection,
// then asks the server (in that order), the server's remaining count and
// message reach the status line, a refused save does not Generate, and Put up
// toggles. jsdom is not a browser (CLAUDE.md §1): no layout is claimed.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlyerView, OutreachLocation } from '../../lib/types';

const calls: string[] = [];
const getOutreachFlyer = vi.fn<(id: number) => Promise<FlyerView>>();
const updateOutreachFlyer = vi.fn();
const setOutreachFlyerLocations = vi.fn();
const generateOutreachFlyer = vi.fn();
const markFlyerLinkPutUp = vi.fn();
const clearFlyerLinkPutUp = vi.fn();
const listOutreachLocations = vi.fn();

class FakeApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

vi.mock('../../lib/api', () => ({
  getOutreachFlyer: (id: number) => getOutreachFlyer(id),
  updateOutreachFlyer: (...a: unknown[]) => {
    calls.push('update');
    return updateOutreachFlyer(...a);
  },
  setOutreachFlyerLocations: (...a: unknown[]) => {
    calls.push('select');
    return setOutreachFlyerLocations(...a);
  },
  generateOutreachFlyer: (...a: unknown[]) => {
    calls.push('generate');
    return generateOutreachFlyer(...a);
  },
  deleteOutreachFlyer: vi.fn(),
  markFlyerLinkPutUp: (...a: unknown[]) => markFlyerLinkPutUp(...a),
  clearFlyerLinkPutUp: (...a: unknown[]) => clearFlyerLinkPutUp(...a),
  listOutreachLocations: () => listOutreachLocations(),
  mediaUpload: vi.fn(),
  ApiError: FakeApiError,
}));

const { default: OutreachFlyerEditor } = await import('./OutreachFlyerEditor.svelte');

afterEach(() => {
  cleanup();
  calls.length = 0;
  for (const m of [getOutreachFlyer, updateOutreachFlyer, setOutreachFlyerLocations, generateOutreachFlyer, markFlyerLinkPutUp, clearFlyerLinkPutUp, listOutreachLocations]) m.mockReset();
});

function loc(id: number, over: Partial<OutreachLocation> = {}): OutreachLocation {
  return {
    id, name: `Board ${id}`, code: `b${id}`, address: '', notes: '', active: true, code_locked: false,
    code_locked_at: null, created_at: '2026-10-16T00:00:00Z', updated_at: null, ...over,
  };
}

function flyer(selection: FlyerView['selection'] = [], extra: Partial<FlyerView> = {}): FlyerView {
  return {
    flyer: {
      id: 7, outreach_id: 1, workshop_id: 2, workshop_slug: 'soldering-101', workshop_title: 'Soldering 101', photo: null,
      focal_x: 0.5, focal_y: 0.5, template: 'photo-top', tagline: '', created_at: '2026-10-16T00:00:00Z', updated_at: null,
    },
    facts: { title: 'Soldering 101', date_line: 'Sat, Nov 14 · 10:00 AM', place_line: 'The Shop', cover_image: null },
    selection,
    ...extra,
  };
}

const link = (id: number, hasQr: boolean, putUp: string | null = null) => ({
  id, short_link_key: `k${id}`, short_url: `https://go.example/k${id}`, has_qr: hasQr,
  qr_url: hasQr ? `/admin/outreach-flyer-links/${id}/qr.svg` : null, put_up_at: putUp,
});

async function mountWith(v: FlyerView, locs: OutreachLocation[]): Promise<void> {
  getOutreachFlyer.mockResolvedValue(v);
  listOutreachLocations.mockResolvedValue({ locations: locs });
  render(OutreachFlyerEditor, { flyerId: 7 });
  await waitFor(() => expect(screen.getByRole('heading', { name: 'Photo' })).toBeTruthy());
}

describe('OutreachFlyerEditor', () => {
  it('Generate saves the design, then the selection, then generates, and reports remaining and the message', async () => {
    updateOutreachFlyer.mockResolvedValue(flyer());
    setOutreachFlyerLocations.mockResolvedValue(flyer());
    generateOutreachFlyer.mockResolvedValue(
      flyer([{ location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 2, link: link(10, false) }], {
        remaining: 1,
        message: 'Some links or QR codes could not be fetched.',
      }),
    );
    await mountWith(flyer(), [loc(1), loc(2)]);
    await fireEvent.click(screen.getByLabelText(/Board 1/));
    await fireEvent.change(screen.getByLabelText('Copies'), { target: { value: '2' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Generate links and QR codes' }));
    await waitFor(() => expect(generateOutreachFlyer).toHaveBeenCalledWith(7));
    expect(calls).toEqual(['update', 'select', 'generate']);
    expect(setOutreachFlyerLocations).toHaveBeenCalledWith(7, [{ location_id: 1, copies: 2 }]);
    const status = screen.getByRole('status');
    await waitFor(() => expect(status.textContent).toContain('1 of 1 locations still need a link or QR code. Some links or QR codes could not be fetched.'));
    expect(screen.getByText('No QR code yet')).toBeTruthy();
  });

  it('does not generate when saving the selection is refused, and shows the server message', async () => {
    updateOutreachFlyer.mockResolvedValue(flyer());
    setOutreachFlyerLocations.mockRejectedValue(new FakeApiError(400, 'an inactive location cannot be added'));
    await mountWith(flyer(), [loc(1)]);
    await fireEvent.click(screen.getByLabelText(/Board 1/));
    await fireEvent.click(screen.getByRole('button', { name: 'Generate links and QR codes' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('an inactive location cannot be added'));
    expect(generateOutreachFlyer).not.toHaveBeenCalled();
  });

  it('keeps Generate disabled with nothing selected and with an over-long tagline', async () => {
    await mountWith(flyer(), [loc(1)]);
    const gen = screen.getByRole('button', { name: 'Generate links and QR codes' }) as HTMLButtonElement;
    expect(gen.disabled).toBe(true);
    await fireEvent.click(screen.getByLabelText(/Board 1/));
    expect(gen.disabled).toBe(false);
    await fireEvent.input(screen.getByLabelText('Tagline'), { target: { value: 'x'.repeat(141) } });
    expect(gen.disabled).toBe(true);
    expect(screen.getByText('Tagline must be at most 140 characters.')).toBeTruthy();
  });

  it('lists active locations plus already-selected inactive ones', async () => {
    const selected = { location_id: 3, name: 'Board 3', code: 'b3', address: '', active: false, copies: 1, link: null };
    await mountWith(flyer([selected]), [loc(1), loc(2, { active: false }), loc(3, { active: false })]);
    expect(screen.queryByLabelText(/Board 2/)).toBeNull();
    expect(screen.getByLabelText(/Board 1/)).toBeTruthy();
    expect(screen.getByLabelText(/Board 3/)).toBeTruthy();
    expect(screen.getAllByText('1 location · 1 flyer').length).toBeGreaterThan(0);
  });

  it('offers the print pages in a new tab, and the focal slider moves with the arrow keys', async () => {
    await mountWith(flyer([], { facts: { title: 'T', date_line: 'd', place_line: 'p', cover_image: '/hero-crt-760.webp' } }), []);
    const half = screen.getByRole('link', { name: 'Print all · half-size double' });
    expect(half.getAttribute('href')).toBe('#/outreach?view=print&flyer=7&layout=half');
    expect(half.getAttribute('target')).toBe('_blank');
    expect(half.getAttribute('rel')).toBe('noopener');
    const slider = screen.getByRole('slider', { name: 'Focal point' });
    expect(slider.getAttribute('aria-valuetext')).toBe('x 50%, y 50%');
    await fireEvent.keyDown(slider, { key: 'ArrowRight' });
    await fireEvent.keyDown(slider, { key: 'ArrowDown', shiftKey: true });
    expect(slider.getAttribute('aria-valuetext')).toBe('x 52%, y 60%');
  });

  it('marks a location put up and undoes it', async () => {
    const generated = { location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 1, link: link(10, true) };
    const marked = { ...generated, link: link(10, true, '2026-10-18T17:00:00Z') };
    await mountWith(flyer([generated]), [loc(1)]);
    markFlyerLinkPutUp.mockResolvedValue({});
    getOutreachFlyer.mockResolvedValue(flyer([marked]));
    await fireEvent.click(screen.getByRole('button', { name: 'Mark put up' }));
    await waitFor(() => expect(markFlyerLinkPutUp).toHaveBeenCalledWith(10));
    const undo = await screen.findByRole('button', { name: 'Undo' });
    clearFlyerLinkPutUp.mockResolvedValue({});
    getOutreachFlyer.mockResolvedValue(flyer([generated]));
    await fireEvent.click(undo);
    await waitFor(() => expect(clearFlyerLinkPutUp).toHaveBeenCalledWith(10));
    await screen.findByRole('button', { name: 'Mark put up' });
    const qr = screen.getByRole('link', { name: 'QR (SVG)' });
    expect(qr.getAttribute('href')).toBe('/admin/outreach-flyer-links/10/qr.svg');
    expect(qr.hasAttribute('download')).toBe(true);
  });
});
