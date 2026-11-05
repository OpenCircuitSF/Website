// @vitest-environment jsdom
//
// #0617: mounts Venues.svelte under jsdom with '../../lib/api' mocked.
import { render, fireEvent, cleanup, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Venue } from '../../lib/types';

const listVenues = vi.fn();
const createVenue = vi.fn();
const updateVenue = vi.fn();
const deleteVenue = vi.fn();

vi.mock('../../lib/api', () => ({
  listVenues: (...a: unknown[]) => listVenues(...a),
  createVenue: (...a: unknown[]) => createVenue(...a),
  updateVenue: (...a: unknown[]) => updateVenue(...a),
  deleteVenue: (...a: unknown[]) => deleteVenue(...a),
  ApiError: class ApiError extends Error {},
}));

const { ApiError } = (await import('../../lib/api')) as unknown as {
  ApiError: new (message: string) => Error;
};
const { default: Venues } = await import('./Venues.svelte');

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function rows(): Venue[] {
  const base = {
    enabled: true,
    is_default: false,
    workshop_count: 0,
    note: null,
    created_at: '2026-11-04T00:00:00Z',
    updated_at: '2026-11-04T00:00:00Z',
  };
  return [
    { ...base, id: 1, name: 'Sequoia Fabrica', address: '1736 18th Street', note: 'Foot of Potrero Hill', is_default: true, sort_order: 10, workshop_count: 7 },
    { ...base, id: 2, name: 'The Shop', address: '1 Main St', sort_order: 20 },
    { ...base, id: 3, name: 'Old Hall', address: null, sort_order: 30, enabled: false, workshop_count: 1 },
  ];
}

async function mounted() {
  listVenues.mockResolvedValue({ venues: rows() });
  render(Venues);
  await screen.findByText('The Shop');
}

function button(name: string): HTMLButtonElement {
  return screen.getByRole('button', { name }) as HTMLButtonElement;
}

function row(name: string): HTMLElement {
  return screen.getByText(name).closest('tr') as HTMLElement;
}

describe('Venues', () => {
  it('lists venues with address and listing count', async () => {
    await mounted();
    expect(row('Sequoia Fabrica').textContent).toContain('1736 18th Street');
    expect(row('Sequoia Fabrica').textContent).toContain('7');
    expect(row('Sequoia Fabrica').textContent).toContain('Default');
    expect(row('The Shop').textContent).toContain('Enabled');
    expect(row('Old Hall').textContent).toContain('Disabled');
  });

  it('adds a venue', async () => {
    await mounted();
    createVenue.mockResolvedValue({ ...rows()[1], id: 4, name: 'New Place', sort_order: 40 });
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: ' New Place ' } });
    await fireEvent.input(screen.getByLabelText('Address'), { target: { value: '9 Road' } });
    await fireEvent.input(screen.getByLabelText('Note'), { target: { value: 'Ring' } });
    await fireEvent.click(button('Add venue'));
    await screen.findByText('New Place');
    expect(createVenue).toHaveBeenCalledWith({ name: 'New Place', address: '9 Road', note: 'Ring', sort_order: 40 });
  });

  it('edits address and note', async () => {
    await mounted();
    updateVenue.mockResolvedValue({ ...rows()[1], address: null, note: 'Side door' });
    await fireEvent.click(button('Edit The Shop'));
    const dialog = await screen.findByRole('dialog', { name: 'Edit venue' });
    const address = dialog.querySelector('#edit-venue-address') as HTMLInputElement;
    const note = dialog.querySelector('#edit-venue-note') as HTMLInputElement;
    expect(address.value).toBe('1 Main St');
    await fireEvent.input(address, { target: { value: '  ' } });
    await fireEvent.input(note, { target: { value: 'Side door' } });
    await fireEvent.click(button('Save'));
    await waitFor(() =>
      expect(updateVenue).toHaveBeenCalledWith(2, { name: 'The Shop', address: '', note: 'Side door', enabled: true }),
    );
  });

  it('moves the default', async () => {
    await mounted();
    updateVenue.mockResolvedValue(rows()[1]);
    await fireEvent.click(button('Make The Shop the default'));
    await waitFor(() => expect(updateVenue).toHaveBeenCalledWith(2, { is_default: true }));
    await waitFor(() => expect(listVenues).toHaveBeenCalledTimes(2));
    expect(button('Make Old Hall the default').disabled).toBe(true);
  });

  it('moves a venue up and down', async () => {
    await mounted();
    updateVenue.mockImplementation((id: number, f: { sort_order: number }) =>
      Promise.resolve({ ...rows().find((r) => r.id === id), sort_order: f.sort_order }),
    );
    await fireEvent.click(button('Move The Shop up'));
    await waitFor(() => expect(updateVenue).toHaveBeenCalledWith(2, { sort_order: 10 }));
    expect(updateVenue).toHaveBeenCalledWith(1, { sort_order: 20 });
    await waitFor(() => expect(button('Move The Shop up').disabled).toBe(true));
    updateVenue.mockClear();
    await fireEvent.click(button('Move The Shop down'));
    await waitFor(() => expect(updateVenue).toHaveBeenCalledTimes(2));
  });

  it('blocks deleting the default or a venue in use', async () => {
    await mounted();
    expect(button('Delete Sequoia Fabrica').disabled).toBe(true);
    expect(button('Delete Old Hall').disabled).toBe(true);
    expect(row('Old Hall').textContent).toContain('Disable it instead');
    expect(button('Delete The Shop').disabled).toBe(false);
  });

  it('shows the server message when disabling is refused', async () => {
    await mounted();
    updateVenue.mockRejectedValue(new ApiError('cannot disable the default venue; choose another default first'));
    await fireEvent.click(button('Edit Sequoia Fabrica'));
    const dialog = await screen.findByRole('dialog', { name: 'Edit venue' });
    await fireEvent.click(dialog.querySelector('input[type="checkbox"]') as HTMLInputElement);
    await fireEvent.click(button('Save'));
    await screen.findByText('cannot disable the default venue; choose another default first');
  });
});
