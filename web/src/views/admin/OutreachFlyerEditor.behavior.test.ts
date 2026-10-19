// @vitest-environment jsdom
//
// #0549: mounts OutreachFlyerEditor.svelte under jsdom with '../../lib/api'
// mocked. Proves the wiring: Print (#0589) opens a blank tab inside the click,
// saves the design, then the selection, generates only when a link is missing
// (in that order), then points the tab at the print view; a partial generate
// or a failure closes the tab and reports in the status line, and Put up
// toggles. jsdom is not a browser (AGENTS.md §1): no layout is claimed.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FlyerView, OutreachLocation } from '../../lib/types';
import { markFreshFlyer, clearFreshFlyer } from '../../lib/flyers';

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

// Most tests open a flyer made in this session (#0593); the reopen test clears it.
beforeEach(() => markFreshFlyer(7));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  calls.length = 0;
  for (const m of [getOutreachFlyer, updateOutreachFlyer, setOutreachFlyerLocations, generateOutreachFlyer, markFlyerLinkPutUp, clearFlyerLinkPutUp, listOutreachLocations]) m.mockReset();
});

function loc(id: number, over: Partial<OutreachLocation> = {}): OutreachLocation {
  return {
    id, name: `Board ${id}`, code: `b${id}`, address: '', notes: '', active: true, code_locked: false,
    code_locked_at: null, created_at: '2026-10-16T00:00:00Z', updated_at: null, ...over,
  };
}

function fullPhoto(): FlyerView {
  const v = flyer();
  return { ...v, flyer: { ...v.flyer, template: 'full-photo' } };
}

function flyer(selection: FlyerView['selection'] = [], extra: Partial<FlyerView> = {}): FlyerView {
  return {
    flyer: {
      id: 7, outreach_id: 1, workshop_id: 2, workshop_slug: 'soldering-101', workshop_title: 'Soldering 101', photo: null,
      focal_x: 0.5, focal_y: 0.5, template: 'photo-top', tagline: '', description: '', description_short: '', title_position: 'auto', created_at: '2026-10-16T00:00:00Z', updated_at: null,
    },
    facts: { title: 'Soldering 101', date_line: 'Sat, Nov 14 · 10:00 AM', place_line: 'The Shop', cover_image: null, description: 'Workshop summary.' },
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

const printUrl = '#/outreach?view=print&flyer=7&layout=full';

// A stand-in for the blank tab Print opens synchronously in the click (Safari
// blocks a window.open after an await). Opening records 'open' in calls, so a
// test can prove it happened before the first API call.
function fakeTab() {
  const tab = {
    opener: {} as unknown,
    location: { href: '' },
    document: { title: '', body: { textContent: '' } },
    close: vi.fn(),
  };
  const open = vi.spyOn(window, 'open').mockImplementation(() => {
    calls.push('open');
    return tab as unknown as Window;
  });
  return { tab, open };
}

describe('OutreachFlyerEditor', () => {
  it('a new flyer defaults to the cover image, every active location and one copy (#0589)', async () => {
    const v = flyer([], { facts: { title: 'T', date_line: 'd', place_line: 'p', cover_image: '/cover.webp', description: '' } });
    await mountWith(v, [loc(1), loc(2), loc(3, { active: false })]);
    expect((screen.getByRole('checkbox', { name: /Board 1/ }) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByRole('checkbox', { name: /Board 2/ }) as HTMLInputElement).checked).toBe(true);
    expect(screen.queryByRole('checkbox', { name: /Board 3/ })).toBeNull();
    expect(document.querySelector('.focal-box img')?.getAttribute('src')).toBe('/cover.webp');
    expect(document.querySelectorAll('.preview img').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: 'Print 2 flyers' })).toBeTruthy();
  });

  it('a flyer saved with no photo and no locations does not get the new-flyer defaults on reopen (#0593)', async () => {
    clearFreshFlyer(7);
    const v = flyer([], { facts: { title: 'T', date_line: 'd', place_line: 'p', cover_image: '/cover.webp', description: '' } });
    await mountWith(v, [loc(1), loc(2)]);
    expect((screen.getByRole('checkbox', { name: /Board 1/ }) as HTMLInputElement).checked).toBe(false);
    expect(screen.getByRole('button', { name: 'Print 0 flyers' })).toBeTruthy();
  });

  it('keeps description, title position and copies under a More disclosure, and the preview can be folded (#0593)', async () => {
    await mountWith(fullPhoto(), [loc(1)]);
    const more = document.querySelector('details.more') as HTMLDetailsElement;
    expect(more.querySelector('summary')?.textContent).toContain('More');
    expect(more.open).toBe(false);
    expect(more.querySelector('#fl-desc-input')).toBeTruthy();
    expect(more.querySelector('input[name="fl-titlepos"]')).toBeTruthy();
    const toggle = screen.getByRole('button', { name: 'Hide preview' });
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    await fireEvent.click(toggle);
    expect(screen.getByRole('button', { name: 'Show preview' }).getAttribute('aria-expanded')).toBe('false');
    expect(document.getElementById('fl-preview-body')?.classList.contains('collapsed')).toBe(true);
  });

  it('Print N flyers saves, generates missing links, then opens the print view, naming each step', async () => {
    const { tab, open } = fakeTab();
    updateOutreachFlyer.mockResolvedValue(flyer());
    const selection = (hasQr: boolean) => [{ location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 2, link: hasQr ? link(10, true) : null }];
    setOutreachFlyerLocations.mockResolvedValue(flyer(selection(false)));
    generateOutreachFlyer.mockResolvedValue(flyer(selection(true), { remaining: 0 }));
    await mountWith(flyer(), [loc(1)]);
    await fireEvent.change(screen.getByLabelText('Copies for Board 1'), { target: { value: '2' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Print 2 flyers' }));
    await waitFor(() => expect(tab.location.href.endsWith(printUrl)).toBe(true));
    // Opened blank, synchronously in the click, before any request.
    expect(open).toHaveBeenCalledWith('', '_blank');
    expect(calls).toEqual(['open', 'update', 'select', 'generate']);
    expect(tab.location.href).toBe(new URL(printUrl, window.location.href).href);
    expect(tab.opener).toBeNull();
    expect(tab.close).not.toHaveBeenCalled();
    expect(setOutreachFlyerLocations).toHaveBeenCalledWith(7, [{ location_id: 1, copies: 2 }]);
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('print view is open'));
    expect(screen.queryByRole('button', { name: /Generate links/ })).toBeNull();
  });

  it('skips generate when every link and QR code already exists', async () => {
    const { tab, open } = fakeTab();
    const sel = [{ location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 1, link: link(10, true) }];
    updateOutreachFlyer.mockResolvedValue(flyer(sel));
    setOutreachFlyerLocations.mockResolvedValue(flyer(sel));
    await mountWith(flyer(sel), [loc(1)]);
    await fireEvent.click(screen.getByRole('button', { name: 'Print 1 flyer' }));
    await waitFor(() => expect(tab.location.href.endsWith(printUrl)).toBe(true));
    expect(open).toHaveBeenCalledTimes(1);
    expect(generateOutreachFlyer).not.toHaveBeenCalled();
  });

  it('a partial generate opens nothing and reports the remaining count and message', async () => {
    const { tab } = fakeTab();
    const sel = [{ location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 1, link: null }];
    updateOutreachFlyer.mockResolvedValue(flyer());
    setOutreachFlyerLocations.mockResolvedValue(flyer(sel));
    generateOutreachFlyer.mockResolvedValue(flyer(sel, { remaining: 1, message: 'Some links could not be fetched.' }));
    await mountWith(flyer(), [loc(1)]);
    await fireEvent.click(screen.getByRole('button', { name: 'Print 1 flyer' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('1 of 1 locations still need a link or QR code'));
    expect(screen.getByRole('status').textContent).toContain('Some links could not be fetched.');
    // The blank tab is closed, never pointed at the print view.
    expect(tab.close).toHaveBeenCalledTimes(1);
    expect(tab.location.href).toBe('');
  });

  it('a failed save closes the blank tab; a refused open falls back to this tab', async () => {
    const { tab } = fakeTab();
    updateOutreachFlyer.mockRejectedValue(new FakeApiError(500, 'database is down'));
    await mountWith(flyer(), [loc(1)]);
    await fireEvent.click(screen.getByRole('button', { name: 'Print 1 flyer' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('database is down'));
    expect(tab.close).toHaveBeenCalledTimes(1);
    expect(tab.location.href).toBe('');
    cleanup();
    vi.restoreAllMocks();

    vi.spyOn(window, 'open').mockReturnValue(null);
    const sel = [{ location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 1, link: link(10, true) }];
    updateOutreachFlyer.mockResolvedValue(flyer(sel));
    setOutreachFlyerLocations.mockResolvedValue(flyer(sel));
    window.location.hash = '';
    await mountWith(flyer(sel), [loc(1)]);
    await fireEvent.click(screen.getByRole('button', { name: 'Print 1 flyer' }));
    await waitFor(() => expect(window.location.hash).toBe(printUrl));
    window.location.hash = '';
  });

  it('does not generate when saving the selection is refused, and shows the server message', async () => {
    const { tab } = fakeTab();
    updateOutreachFlyer.mockResolvedValue(flyer());
    setOutreachFlyerLocations.mockRejectedValue(new FakeApiError(400, 'an inactive location cannot be added'));
    await mountWith(flyer(), [loc(1)]);
    await fireEvent.click(screen.getByRole('button', { name: 'Print 1 flyer' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('an inactive location cannot be added'));
    expect(generateOutreachFlyer).not.toHaveBeenCalled();
    expect(tab.close).toHaveBeenCalledTimes(1);
  });

  it('disables Print with nothing ticked (with the reason shown) and with an over-long tagline', async () => {
    await mountWith(flyer(), []);
    const print = screen.getByRole('button', { name: 'Print 0 flyers' }) as HTMLButtonElement;
    expect(print.disabled).toBe(true);
    screen.getByText('Tick at least one location to print.');
    cleanup();
    await mountWith(flyer(), [loc(1)]);
    const print2 = screen.getByRole('button', { name: 'Print 1 flyer' }) as HTMLButtonElement;
    expect(print2.disabled).toBe(false);
    await fireEvent.input(screen.getByLabelText('Tagline'), { target: { value: 'x'.repeat(141) } });
    expect(print2.disabled).toBe(true);
    expect(screen.getByText('Tagline must be at most 140 characters.')).toBeTruthy();
  });

  it('renders generated links as a table with the five columns', async () => {
    const generated = { location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 1, link: link(10, true) };
    await mountWith(flyer([generated]), [loc(1)]);
    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent?.trim());
    expect(headers).toEqual(['Location', 'Code', 'Short link', 'QR', 'Put up']);
    const row = screen.getAllByRole('row')[1];
    expect(row.textContent).toContain('Board 1');
    expect(row.textContent).toContain('b1');
    expect(screen.getByRole('link', { name: /Download QR for Board 1/ }).getAttribute('href')).toBe('/admin/outreach-flyer-links/10/qr.svg');
  });

  it('lists active locations plus already-selected inactive ones', async () => {
    const selected = { location_id: 3, name: 'Board 3', code: 'b3', address: '', active: false, copies: 1, link: null };
    await mountWith(flyer([selected]), [loc(1), loc(2, { active: false }), loc(3, { active: false })]);
    expect(screen.queryByRole('checkbox', { name: /Board 2/ })).toBeNull();
    expect(screen.getByRole('checkbox', { name: /Board 1/ })).toBeTruthy();
    expect(screen.getByRole('checkbox', { name: /Board 3/ })).toBeTruthy();
    expect(screen.getAllByText('1 location · 1 flyer').length).toBeGreaterThan(0);
  });

  it('offers the print pages in a new tab, and the focal slider moves with the arrow keys', async () => {
    await mountWith(flyer([], { facts: { title: 'T', date_line: 'd', place_line: 'p', cover_image: '/hero-crt-760.webp', description: '' } }), []);
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

  it('toggles Put up on and off', async () => {
    const generated = { location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 1, link: link(10, true) };
    const marked = { ...generated, link: link(10, true, '2026-10-18T17:00:00Z') };
    await mountWith(flyer([generated]), [loc(1)]);
    markFlyerLinkPutUp.mockResolvedValue({});
    getOutreachFlyer.mockResolvedValue(flyer([marked]));
    const box = screen.getByRole('checkbox', { name: 'Put up: Board 1' }) as HTMLInputElement;
    expect(box.checked).toBe(false);
    await fireEvent.click(box);
    await waitFor(() => expect(markFlyerLinkPutUp).toHaveBeenCalledWith(10));
    await waitFor(() => expect((screen.getByRole('checkbox', { name: 'Put up: Board 1' }) as HTMLInputElement).checked).toBe(true));
    clearFlyerLinkPutUp.mockResolvedValue({});
    getOutreachFlyer.mockResolvedValue(flyer([generated]));
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Put up: Board 1' }));
    await waitFor(() => expect(clearFlyerLinkPutUp).toHaveBeenCalledWith(10));
  });

  it('a refused Put up puts the checkbox back and says why', async () => {
    const generated = { location_id: 1, name: 'Board 1', code: 'b1', address: '', active: true, copies: 1, link: link(10, true) };
    await mountWith(flyer([generated]), [loc(1)]);
    markFlyerLinkPutUp.mockRejectedValue(new FakeApiError(500, 'could not mark it'));
    const box = screen.getByRole('checkbox', { name: 'Put up: Board 1' }) as HTMLInputElement;
    await fireEvent.click(box);
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('could not mark it'));
    expect(box.checked).toBe(false);
  });

  it('sheet counts follow the ticked copies before anything is saved', async () => {
    await mountWith(flyer(), [loc(1), loc(2)]);
    expect(document.querySelector('.preview')?.textContent).toContain('Full page · 2 sheets');
    expect(document.querySelector('.preview')?.textContent).toContain('Half-size double · 1 sheet');
  });

  it('sends the description, short text and title position, trimmed (#0561)', async () => {
    updateOutreachFlyer.mockResolvedValue(fullPhoto());
    setOutreachFlyerLocations.mockResolvedValue(fullPhoto());
    await mountWith(fullPhoto(), [loc(1)]);
    expect((screen.getByLabelText('Description') as HTMLTextAreaElement).placeholder).toBe('Workshop summary.');
    await fireEvent.input(screen.getByLabelText('Description'), { target: { value: '  A long text.  ' } });
    await fireEvent.input(screen.getByLabelText('Short description (half size)'), { target: { value: ' Short. ' } });
    await fireEvent.click(screen.getByLabelText('Over the subject (details at the top)'));
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(updateOutreachFlyer).toHaveBeenCalled());
    const patch = updateOutreachFlyer.mock.calls[0][1];
    expect(patch).toMatchObject({ description: 'A long text.', description_short: 'Short.', title_position: 'above' });
  });

  it('disables Save at 361 characters and shows the error (#0561)', async () => {
    await mountWith(fullPhoto(), [loc(1)]);
    await fireEvent.input(screen.getByLabelText('Description'), { target: { value: 'x'.repeat(361) } });
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText('Description must be at most 360 characters.')).toBeTruthy();
  });

  it('re-enables Save and Print after switching away from full-photo with an over-long description (#0562)', async () => {
    await mountWith(fullPhoto(), [loc(1)]);
    await fireEvent.input(screen.getByLabelText('Description'), { target: { value: 'x'.repeat(361) } });
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.click(screen.getByLabelText('Photo top'));
    expect(screen.queryByLabelText('Description')).toBeNull();
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(false);
    updateOutreachFlyer.mockResolvedValue(flyer());
    setOutreachFlyerLocations.mockResolvedValue(flyer());
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(updateOutreachFlyer).toHaveBeenCalled());
    const sent = updateOutreachFlyer.mock.calls[0][1];
    expect(sent.template).toBe('photo-top');
    expect('description' in sent).toBe(false);
    expect('description_short' in sent).toBe(false);
  });

  it('omits the caption controls on photo-top (#0561)', async () => {
    await mountWith(flyer(), [loc(1)]);
    expect(screen.queryByLabelText('Description')).toBeNull();
    expect(screen.queryByLabelText('Short description (half size)')).toBeNull();
    expect(screen.queryByText('Title position')).toBeNull();
  });

  it('an unknown flyer shows a not-found state with a way back (#0591)', async () => {
    getOutreachFlyer.mockRejectedValue(new FakeApiError(404, 'flyer not found'));
    listOutreachLocations.mockResolvedValue({ locations: [] });
    render(OutreachFlyerEditor, { flyerId: 999 });
    expect(await screen.findByRole('heading', { name: 'Flyer not found' })).toBeTruthy();
    expect(screen.getByText('This flyer no longer exists.')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'All flyers' }).getAttribute('href')).toBe('#/outreach?view=flyers');
    expect(screen.queryByText('flyer not found')).toBeNull();
  });
});
