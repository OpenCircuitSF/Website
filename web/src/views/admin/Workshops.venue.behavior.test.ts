// @vitest-environment jsdom
//
// #0617: the New event dialog's Venue select.
import { render, fireEvent, cleanup, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AdminWorkshop, Venue } from '../../lib/types';

const listAdminWorkshops = vi.fn();
const listOutreach = vi.fn();
const listEventTypes = vi.fn();
const listVenues = vi.fn();
const createWorkshop = vi.fn();

vi.mock('../../lib/api', () => ({
  listAdminWorkshops: (...a: unknown[]) => listAdminWorkshops(...a),
  listOutreach: (...a: unknown[]) => listOutreach(...a),
  listEventTypes: (...a: unknown[]) => listEventTypes(...a),
  listVenues: (...a: unknown[]) => listVenues(...a),
  createWorkshop: (...a: unknown[]) => createWorkshop(...a),
  ApiError: class ApiError extends Error {},
}));

const { default: Workshops } = await import('./Workshops.svelte');

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

const workshop = {
  id: 7,
  slug: 'soldering',
  title: 'Soldering',
  status: 'draft',
  interest_ids: [],
  event_type_id: 2,
  event_type: { slug: 'hack-night', name: 'Hack Night', sort_order: 10 },
  venue_id: null,
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
} as AdminWorkshop;

function venues(): Venue[] {
  const row = {
    workshop_count: 0,
    note: null,
    address: null,
    created_at: '2026-11-04T00:00:00Z',
    updated_at: '2026-11-04T00:00:00Z',
    enabled: true,
    is_default: false,
  };
  return [
    { ...row, id: 1, name: 'The Shop', sort_order: 10 },
    { ...row, id: 2, name: 'Sequoia Fabrica', is_default: true, sort_order: 20 },
    { ...row, id: 3, name: 'Old Hall', sort_order: 30, enabled: false },
  ];
}

async function openNew(): Promise<void> {
  listAdminWorkshops.mockResolvedValue({ workshops: [workshop] });
  listOutreach.mockResolvedValue({ outreach: [] });
  listEventTypes.mockResolvedValue({ event_types: [] });
  render(Workshops, { workshopId: null, onOpenWorkshop: vi.fn(), onBackToList: vi.fn(), onAnnounced: vi.fn() });
  await screen.findByRole('button', { name: 'Soldering' });
  await fireEvent.click(screen.getByRole('button', { name: 'New event' }));
  await fireEvent.input(await screen.findByLabelText('Title'), { target: { value: 'Night' } });
}

function selectedText(select: HTMLSelectElement): string | null | undefined {
  return select.options[select.selectedIndex]?.textContent;
}

describe('Workshops venue', () => {
  it('preselects the default venue', async () => {
    listVenues.mockResolvedValue({ venues: venues() });
    await openNew();
    const select = (await screen.findByLabelText('Venue')) as HTMLSelectElement;
    expect([...select.options].map((o) => o.textContent)).toEqual(['No location', 'The Shop', 'Sequoia Fabrica']);
    await waitFor(() => expect(selectedText(select)).toBe('Sequoia Fabrica'));
    createWorkshop.mockResolvedValue({ ...workshop, id: 8 });
    await fireEvent.click(screen.getByRole('button', { name: 'Create draft' }));
    await waitFor(() => expect(createWorkshop).toHaveBeenCalledWith({ title: 'Night', venue_id: 2 }));
  });

  it('sends venue_id null when No location is chosen', async () => {
    listVenues.mockResolvedValue({ venues: venues() });
    await openNew();
    const select = (await screen.findByLabelText('Venue')) as HTMLSelectElement;
    await waitFor(() => expect(selectedText(select)).toBe('Sequoia Fabrica'));
    select.selectedIndex = 0;
    await fireEvent.change(select);
    createWorkshop.mockResolvedValue({ ...workshop, id: 8 });
    await fireEvent.click(screen.getByRole('button', { name: 'Create draft' }));
    await waitFor(() => expect(createWorkshop).toHaveBeenCalledWith({ title: 'Night', venue_id: null }));
  });

  it('omits venue_id when venues failed to load', async () => {
    listVenues.mockRejectedValue(new Error('offline'));
    await openNew();
    await waitFor(() => expect(listVenues).toHaveBeenCalled());
    expect(screen.queryByLabelText('Venue')).toBeNull();
    createWorkshop.mockResolvedValue({ ...workshop, id: 8 });
    await fireEvent.click(screen.getByRole('button', { name: 'Create draft' }));
    await waitFor(() => expect(createWorkshop).toHaveBeenCalledWith({ title: 'Night' }));
  });
});
