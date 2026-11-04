// @vitest-environment jsdom
//
// #0613: mounts EventTypes.svelte under jsdom with '../../lib/api' mocked.
import { render, fireEvent, cleanup, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { EventType } from '../../lib/types';

const listEventTypes = vi.fn();
const createEventType = vi.fn();
const updateEventType = vi.fn();
const deleteEventType = vi.fn();

vi.mock('../../lib/api', () => ({
  listEventTypes: (...a: unknown[]) => listEventTypes(...a),
  createEventType: (...a: unknown[]) => createEventType(...a),
  updateEventType: (...a: unknown[]) => updateEventType(...a),
  deleteEventType: (...a: unknown[]) => deleteEventType(...a),
  ApiError: class ApiError extends Error {},
}));

const { ApiError } = (await import('../../lib/api')) as unknown as {
  ApiError: new (message: string) => Error;
};
const { default: EventTypes } = await import('./EventTypes.svelte');

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function rows(): EventType[] {
  const base = { enabled: true, is_default: false, workshop_count: 0, created_at: '2026-11-04T00:00:00Z' };
  return [
    { ...base, id: 1, slug: 'workshop', name: 'Workshop', is_default: true, sort_order: 10, workshop_count: 3 },
    { ...base, id: 2, slug: 'hack-night', name: 'Hack Night', sort_order: 20 },
    { ...base, id: 3, slug: 'talk', name: 'Talk', sort_order: 30, enabled: false, workshop_count: 1 },
  ];
}

async function mounted() {
  listEventTypes.mockResolvedValue({ event_types: rows() });
  render(EventTypes);
  await screen.findByText('hack-night');
}

function button(name: string): HTMLButtonElement {
  return screen.getByRole('button', { name }) as HTMLButtonElement;
}

describe('EventTypes', () => {
  it('lists types with status and counts', async () => {
    await mounted();
    const talk = screen.getByText('talk').closest('tr') as HTMLElement;
    expect(talk.textContent).toContain('Disabled');
    expect(talk.textContent).toContain('1');
    expect((screen.getByText('workshop').closest('tr') as HTMLElement).textContent).toContain('Default');
    expect((screen.getByText('hack-night').closest('tr') as HTMLElement).textContent).toContain('Enabled');
  });

  it('adds a type', async () => {
    await mounted();
    createEventType.mockResolvedValue({ ...rows()[1], id: 4, slug: 'field-day', name: 'Field Day', sort_order: 40 });
    await fireEvent.input(screen.getByLabelText('Slug'), { target: { value: 'field-day' } });
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Field Day' } });
    await fireEvent.click(button('Add type'));
    await screen.findByText('field-day');
    expect(createEventType).toHaveBeenCalledWith('field-day', 'Field Day', 40);
  });

  it('moves a type up', async () => {
    await mounted();
    const [a, b] = rows();
    updateEventType.mockImplementation(async (id: number, f: { sort_order: number }) => ({
      ...(id === 2 ? b : a),
      sort_order: f.sort_order,
    }));
    await fireEvent.click(button('Move Hack Night up'));
    await waitFor(() => expect(updateEventType).toHaveBeenCalledTimes(2));
    expect(updateEventType).toHaveBeenCalledWith(2, { sort_order: 10 });
    expect(updateEventType).toHaveBeenCalledWith(1, { sort_order: 20 });
    await waitFor(() => expect(screen.getAllByRole('row')[1].textContent).toContain('hack-night'));
  });

  it('disables a type from the edit dialog, warning first', async () => {
    await mounted();
    updateEventType.mockResolvedValue({ ...rows()[1], enabled: false });
    await fireEvent.click(button('Edit Hack Night'));
    const status = screen.getByRole('status');
    expect(status.textContent).toBe('');
    await fireEvent.click(screen.getByLabelText('Enabled'));
    expect(status.textContent).toContain('Disabling stops new listings');
    await fireEvent.click(button('Save'));
    await waitFor(() =>
      expect(updateEventType).toHaveBeenCalledWith(2, { name: 'Hack Night', enabled: false }),
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('makes a type the default', async () => {
    await mounted();
    updateEventType.mockResolvedValue(rows()[1]);
    await fireEvent.click(button('Make Hack Night the default'));
    await waitFor(() => expect(updateEventType).toHaveBeenCalledWith(2, { is_default: true }));
    expect(button('Make Workshop the default').disabled).toBe(true);
    expect(button('Make Talk the default').disabled).toBe(true);
  });

  it('blocks delete with a reason when in use or default', async () => {
    await mounted();
    expect(button('Delete Workshop').disabled).toBe(true);
    expect(button('Delete Talk').disabled).toBe(true);
    expect(button('Delete Hack Night').disabled).toBe(false);
    expect(screen.getByText(/another default first/)).toBeTruthy();
    expect(screen.getByText(/Disable it instead/)).toBeTruthy();
  });

  it('shows the server message when a delete is refused', async () => {
    await mounted();
    deleteEventType.mockRejectedValue(new ApiError('refused by the server'));
    await fireEvent.click(button('Delete Hack Night'));
    await fireEvent.click(button('Confirm delete'));
    expect((await screen.findByRole('alert')).textContent).toContain('refused by the server');
  });
});
