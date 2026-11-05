// @vitest-environment jsdom
//
// #0617: the editor's Venue select. Disabled venues are offered only when they
// are this listing's current venue.
import { render, fireEvent, cleanup, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AdminWorkshop, Venue } from '../../lib/types';

const getAdminWorkshop = vi.fn();
const listInterests = vi.fn();
const listEventTypes = vi.fn();
const listVenues = vi.fn();
const updateWorkshop = vi.fn();
const previewWorkshop = vi.fn();
const noop = vi.fn();

vi.mock('../../lib/api', () => ({
  getAdminWorkshop: (...a: unknown[]) => getAdminWorkshop(...a),
  listInterests: (...a: unknown[]) => listInterests(...a),
  listEventTypes: (...a: unknown[]) => listEventTypes(...a),
  listVenues: (...a: unknown[]) => listVenues(...a),
  updateWorkshop: (...a: unknown[]) => updateWorkshop(...a),
  previewWorkshop: (...a: unknown[]) => previewWorkshop(...a),
  createWorkshop: noop,
  deleteWorkshop: noop,
  announceWorkshop: noop,
  mediaUpload: noop,
  getOutreach: vi.fn().mockRejectedValue(new Error('not exercised')),
  listOutreach: vi.fn().mockResolvedValue({ outreach: [] }),
  ApiError: class ApiError extends Error {},
}));

const { default: WorkshopEditor } = await import('./WorkshopEditor.svelte');

afterEach(cleanup);

function venues(): Venue[] {
  const row = {
    workshop_count: 0,
    note: null,
    created_at: '2026-11-04T00:00:00Z',
    updated_at: '2026-11-04T00:00:00Z',
    enabled: true,
    is_default: false,
  };
  return [
    { ...row, id: 1, name: 'Sequoia Fabrica', address: '1736 18th Street', is_default: true, sort_order: 10 },
    { ...row, id: 2, name: 'Old Hall', address: null, sort_order: 20, enabled: false },
    { ...row, id: 3, name: 'Gone Place', address: null, sort_order: 30, enabled: false },
    { ...row, id: 4, name: 'The Shop', address: null, sort_order: 40 },
  ];
}

async function mounted(venueId: number | null): Promise<HTMLSelectElement> {
  const w = {
    id: 7,
    slug: 'soldering',
    title: 'Soldering',
    status: 'draft',
    interest_ids: [],
    event_type_id: 1,
    event_type: { slug: 'workshop', name: 'Workshop', sort_order: 10 },
    venue_id: venueId,
    created_at: '2026-11-04T00:00:00Z',
    updated_at: '2026-11-04T00:00:00Z',
  } as AdminWorkshop;
  getAdminWorkshop.mockResolvedValue(w);
  listInterests.mockResolvedValue({ interests: [] });
  listEventTypes.mockResolvedValue({
    event_types: [
      { id: 1, slug: 'workshop', name: 'Workshop', enabled: true, is_default: true, sort_order: 10, workshop_count: 0, created_at: '' },
    ],
  });
  listVenues.mockResolvedValue({ venues: venues() });
  previewWorkshop.mockResolvedValue({ html: '' });
  updateWorkshop.mockClear();
  updateWorkshop.mockResolvedValue(w);
  render(WorkshopEditor, { workshopId: 7, onBack: vi.fn(), onAnnounced: vi.fn(), onCloned: vi.fn() });
  return (await screen.findByLabelText('Venue')) as HTMLSelectElement;
}

function selectedText(select: HTMLSelectElement): string | null | undefined {
  return select.options[select.selectedIndex]?.textContent;
}

describe('WorkshopEditor venue', () => {
  it('shows the current venue selected', async () => {
    const select = await mounted(4);
    await waitFor(() => expect(selectedText(select)).toBe('The Shop'));
  });

  it('offers No location', async () => {
    const select = await mounted(null);
    expect(select.options[0].textContent).toBe('No location');
    await waitFor(() => expect(selectedText(select)).toBe('No location'));
  });

  it('keeps a disabled current venue selectable and hides other disabled ones', async () => {
    const select = await mounted(2);
    expect([...select.options].map((o) => o.textContent)).toEqual([
      'No location',
      'Sequoia Fabrica, 1736 18th Street',
      'Old Hall (disabled)',
      'The Shop',
    ]);
    await waitFor(() => expect(selectedText(select)).toBe('Old Hall (disabled)'));
  });

  it('saves venue_id null for No location', async () => {
    const select = await mounted(4);
    await waitFor(() => expect(selectedText(select)).toBe('The Shop'));
    select.selectedIndex = 0;
    await fireEvent.change(select);
    await fireEvent.click(screen.getByRole('button', { name: /^Save/ }));
    await waitFor(() => expect(updateWorkshop).toHaveBeenCalled());
    expect(updateWorkshop.mock.calls[0][1].venue_id).toBeNull();
  });
});
