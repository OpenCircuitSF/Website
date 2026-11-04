// @vitest-environment jsdom
//
// #0613: the editor's Type select. Disabled types are offered only when they
// are this listing's current type.
import { render, fireEvent, cleanup, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AdminWorkshop, EventType } from '../../lib/types';

const getAdminWorkshop = vi.fn();
const listInterests = vi.fn();
const listEventTypes = vi.fn();
const updateWorkshop = vi.fn();
const previewWorkshop = vi.fn();
const noop = vi.fn();

vi.mock('../../lib/api', () => ({
  getAdminWorkshop: (...a: unknown[]) => getAdminWorkshop(...a),
  listInterests: (...a: unknown[]) => listInterests(...a),
  listEventTypes: (...a: unknown[]) => listEventTypes(...a),
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

const base = {
  id: 7,
  slug: 'soldering',
  title: 'Soldering',
  status: 'draft',
  interest_ids: [],
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
};

function types(): EventType[] {
  const row = { workshop_count: 0, created_at: '2026-11-04T00:00:00Z', enabled: true, is_default: false };
  return [
    { ...row, id: 1, slug: 'workshop', name: 'Workshop', is_default: true, sort_order: 10 },
    { ...row, id: 2, slug: 'talk', name: 'Talk', sort_order: 20, enabled: false },
    { ...row, id: 3, slug: 'social', name: 'Social', sort_order: 30, enabled: false },
    { ...row, id: 4, slug: 'hack-night', name: 'Hack Night', sort_order: 40 },
  ];
}

async function mounted(typeId: number, typeName: string): Promise<HTMLSelectElement> {
  const w = { ...base, event_type_id: typeId, event_type: { slug: 'x', name: typeName } } as AdminWorkshop;
  getAdminWorkshop.mockResolvedValue(w);
  listInterests.mockResolvedValue({ interests: [] });
  listEventTypes.mockResolvedValue({ event_types: types() });
  previewWorkshop.mockResolvedValue({ html: '' });
  updateWorkshop.mockResolvedValue(w);
  render(WorkshopEditor, { workshopId: 7, onBack: vi.fn(), onAnnounced: vi.fn(), onCloned: vi.fn() });
  return (await screen.findByLabelText('Type')) as HTMLSelectElement;
}

describe('WorkshopEditor event type', () => {
  it('keeps a disabled current type selectable and labelled', async () => {
    const select = await mounted(2, 'Talk');
    expect([...select.options].map((o) => o.textContent)).toContain('Talk (disabled)');
    await waitFor(() => expect(select.value).toBe('2'));
  });

  it('does not offer other disabled types', async () => {
    const select = await mounted(2, 'Talk');
    expect([...select.options].map((o) => o.textContent)).not.toContain('Social (disabled)');
    expect([...select.options].map((o) => o.textContent)).toEqual(['Workshop', 'Talk (disabled)', 'Hack Night']);
  });

  it('sends the chosen type on save', async () => {
    const select = await mounted(1, 'Workshop');
    updateWorkshop.mockClear();
    await fireEvent.change(select, { target: { value: '4' } });
    await fireEvent.click(screen.getByRole('button', { name: /^Save/ }));
    await waitFor(() => expect(updateWorkshop).toHaveBeenCalled());
    expect(updateWorkshop.mock.calls[0][0]).toBe(7);
    expect(updateWorkshop.mock.calls[0][1].event_type_id).toBe(4);
  });
});
