// @vitest-environment jsdom
//
// #0572: a new cover must not keep the old picture's alt text on Save.
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

async function mounted(): Promise<void> {
  const w = {
    id: 7,
    slug: 'soldering',
    title: 'Soldering',
    status: 'draft',
    interest_ids: [],
    event_type_id: 1,
    event_type: { slug: 'workshop', name: 'Workshop', sort_order: 10 },
    venue_id: null,
    cover_image: '/media/old.jpg',
    cover_alt: 'An old picture',
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
  listVenues.mockResolvedValue({ venues: [] as Venue[] });
  previewWorkshop.mockResolvedValue({ html: '' });
  updateWorkshop.mockClear();
  updateWorkshop.mockResolvedValue(w);
  render(WorkshopEditor, { workshopId: 7, onBack: vi.fn(), onAnnounced: vi.fn(), onCloned: vi.fn() });
  const cover = (await screen.findByLabelText('Cover image (site-relative path)')) as HTMLInputElement;
  await waitFor(() => expect(cover.value).toBe('/media/old.jpg'));
}

async function save(): Promise<Record<string, unknown>> {
  await fireEvent.click(screen.getByRole('button', { name: /^Save/ }));
  await waitFor(() => expect(updateWorkshop).toHaveBeenCalled());
  return updateWorkshop.mock.calls[0][1];
}

describe('WorkshopEditor cover alt text (#0572)', () => {
  it('saves cover_alt empty when the cover path changes and the alt text does not', async () => {
    await mounted();
    await fireEvent.input(screen.getByLabelText('Cover image (site-relative path)'), {
      target: { value: '/media/new.jpg' },
    });
    const body = await save();
    expect(body.cover_image).toBe('/media/new.jpg');
    expect(body.cover_alt).toBe('');
  });

  it('keeps the alt text when the cover is unchanged', async () => {
    await mounted();
    const body = await save();
    expect(body.cover_alt).toBe('An old picture');
  });
});
