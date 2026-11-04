// @vitest-environment jsdom
//
// #0613: the Type column and the New workshop modal's Type select.
import { render, fireEvent, cleanup, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AdminWorkshop, EventType } from '../../lib/types';

const listAdminWorkshops = vi.fn();
const listOutreach = vi.fn();
const listEventTypes = vi.fn();
const createWorkshop = vi.fn();

vi.mock('../../lib/api', () => ({
  listAdminWorkshops: (...a: unknown[]) => listAdminWorkshops(...a),
  listOutreach: (...a: unknown[]) => listOutreach(...a),
  listEventTypes: (...a: unknown[]) => listEventTypes(...a),
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
  event_type: { slug: 'hack-night', name: 'Hack Night' },
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
} as AdminWorkshop;

function types(): EventType[] {
  const base = { workshop_count: 0, created_at: '2026-11-04T00:00:00Z', enabled: true, is_default: false };
  return [
    { ...base, id: 1, slug: 'workshop', name: 'Workshop', is_default: true, sort_order: 10 },
    { ...base, id: 2, slug: 'hack-night', name: 'Hack Night', sort_order: 20 },
    { ...base, id: 3, slug: 'talk', name: 'Talk', sort_order: 30, enabled: false },
  ];
}

async function mounted() {
  listAdminWorkshops.mockResolvedValue({ workshops: [workshop] });
  listOutreach.mockResolvedValue({ outreach: [] });
  listEventTypes.mockResolvedValue({ event_types: types() });
  render(Workshops, { workshopId: null, onOpenWorkshop: vi.fn(), onBackToList: vi.fn(), onAnnounced: vi.fn() });
  await screen.findByRole('button', { name: 'Soldering' });
}

async function openNew(): Promise<HTMLSelectElement> {
  await fireEvent.click(screen.getByRole('button', { name: 'New workshop' }));
  return (await screen.findByLabelText('Type')) as HTMLSelectElement;
}

describe('Workshops event type', () => {
  it('shows each listing type in the list', async () => {
    await mounted();
    expect(screen.getByRole('columnheader', { name: 'Type' })).toBeTruthy();
    expect(screen.getByRole('cell', { name: 'Hack Night' })).toBeTruthy();
  });

  it('preselects the default and offers only enabled types in New workshop', async () => {
    await mounted();
    const select = await openNew();
    expect([...select.options].map((o) => o.textContent)).toEqual(['Workshop', 'Hack Night']);
    await waitFor(() => expect(select.value).toBe(String(1)));
  });

  it('sends the chosen type on create', async () => {
    await mounted();
    createWorkshop.mockResolvedValue({ ...workshop, id: 8 });
    const select = await openNew();
    await fireEvent.input(screen.getByLabelText('Title'), { target: { value: 'Night' } });
    await fireEvent.change(select, { target: { value: '2' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create draft' }));
    await waitFor(() => expect(createWorkshop).toHaveBeenCalledWith({ title: 'Night', event_type_id: 2 }));
  });
});
