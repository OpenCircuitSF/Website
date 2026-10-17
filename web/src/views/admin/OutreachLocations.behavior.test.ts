// @vitest-environment jsdom
//
// #0548: mounts OutreachLocations.svelte under jsdom with '../../lib/api'
// mocked (same pattern as OutreachPanel.behavior.test.ts). jsdom is not a
// browser (AGENTS.md §1): this proves the form wiring, not layout.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachLocation } from '../../lib/types';

const listOutreachLocations = vi.fn<() => Promise<{ locations: OutreachLocation[] }>>();
const createOutreachLocation = vi.fn();
const updateOutreachLocation = vi.fn();
const deleteOutreachLocation = vi.fn();
vi.mock('../../lib/api', () => ({
  listOutreachLocations: () => listOutreachLocations(),
  createOutreachLocation: (...a: unknown[]) => createOutreachLocation(...a),
  updateOutreachLocation: (...a: unknown[]) => updateOutreachLocation(...a),
  deleteOutreachLocation: (...a: unknown[]) => deleteOutreachLocation(...a),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachLocations } = await import('./OutreachLocations.svelte');

afterEach(() => {
  cleanup();
  listOutreachLocations.mockReset();
  createOutreachLocation.mockReset();
  updateOutreachLocation.mockReset();
  deleteOutreachLocation.mockReset();
});

function loc(over: Partial<OutreachLocation> = {}): OutreachLocation {
  return {
    id: 1,
    name: 'Mission Library',
    code: 'mission',
    address: '300 Main',
    notes: '',
    active: true,
    code_locked: false,
    code_locked_at: null,
    created_at: '2026-10-16T00:00:00Z',
    updated_at: null,
    ...over,
  };
}

async function mountWith(rows: OutreachLocation[]): Promise<void> {
  listOutreachLocations.mockResolvedValue({ locations: rows });
  render(OutreachLocations);
  await waitFor(() => expect(listOutreachLocations).toHaveBeenCalled());
}

describe('OutreachLocations', () => {
  it('shows the empty state', async () => {
    await mountWith([]);
    await screen.findByText('No locations yet.');
  });

  it('suggests a code from the name until the code is hand-edited', async () => {
    await mountWith([]);
    await screen.findByText('No locations yet.');
    await fireEvent.click(screen.getByRole('button', { name: 'Add location' }));
    const name = screen.getByLabelText('Name') as HTMLInputElement;
    const code = screen.getByLabelText('Code') as HTMLInputElement;

    await fireEvent.input(name, { target: { value: 'Mission Public Library' } });
    expect(code.value).toBe('mission-public');

    await fireEvent.input(code, { target: { value: 'My Code' } });
    expect(code.value).toBe('my-code');

    await fireEvent.input(name, { target: { value: 'Something Else Entirely' } });
    expect(code.value).toBe('my-code');
  });

  it('avoids an existing code when suggesting', async () => {
    await mountWith([loc({ code: 'mission-public' })]);
    await screen.findByText('Mission Library');
    await fireEvent.click(screen.getByRole('button', { name: 'Add location' }));
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Mission Public Library' } });
    expect((screen.getByLabelText('Code') as HTMLInputElement).value).toBe('mission-public-2');
  });

  it('disables Save while invalid and creates with the form values', async () => {
    createOutreachLocation.mockResolvedValue(loc());
    await mountWith([]);
    await screen.findByText('No locations yet.');
    await fireEvent.click(screen.getByRole('button', { name: 'Add location' }));
    const save = screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Bernal Co-op' } });
    expect(save.disabled).toBe(false);
    await fireEvent.click(save);
    await waitFor(() =>
      expect(createOutreachLocation).toHaveBeenCalledWith({
        name: 'Bernal Co-op',
        code: 'bernal-co-op',
        address: '',
        notes: '',
        active: true,
      }),
    );
  });

  it('disables the code of a locked location and offers no Delete', async () => {
    await mountWith([loc({ code_locked: true })]);
    await screen.findByText('Mission Library');
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect((screen.getByLabelText('Code') as HTMLInputElement).disabled).toBe(true);
    screen.getByText('Flyer links use this code, so it can no longer change.');
  });

  it('offers Delete for an unused location, with an inline two-step confirm', async () => {
    deleteOutreachLocation.mockResolvedValue(undefined);
    await mountWith([loc()]);
    await screen.findByText('Mission Library');
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(deleteOutreachLocation).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole('button', { name: 'Yes' }));
    await waitFor(() => expect(deleteOutreachLocation).toHaveBeenCalledWith(1));
  });

  it('Deactivate sends PATCH {active:false}', async () => {
    updateOutreachLocation.mockResolvedValue(loc({ active: false }));
    await mountWith([loc()]);
    await screen.findByText('Mission Library');
    await fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));
    await waitFor(() => expect(updateOutreachLocation).toHaveBeenCalledWith(1, { active: false }));
  });
  it('styles a server error as an error and a success as a notice, in one always-mounted live region', async () => {
    const { ApiError } = await import('../../lib/api');
    deleteOutreachLocation.mockRejectedValueOnce(new ApiError(409, 'Location is in use', null));
    updateOutreachLocation.mockResolvedValue(loc({ active: false }));
    await mountWith([loc()]);
    await screen.findByText('Mission Library');
    const region = screen.getByRole('status');
    expect(region.classList.contains('text-error')).toBe(false);
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Yes' }));
    await waitFor(() => expect(region.textContent).toBe('Location is in use'));
    expect(region.classList.contains('text-error')).toBe(true);
    await fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));
    await waitFor(() => expect(region.textContent).toContain('deactivated'));
    expect(region.classList.contains('text-error')).toBe(false);
    expect(screen.getByRole('status')).toBe(region);
  });
});
