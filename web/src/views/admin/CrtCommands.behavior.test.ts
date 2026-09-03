// @vitest-environment jsdom
//
// #0393: mounts CrtCommands.svelte (the admin CRT-session screen) with
// @testing-library/svelte under jsdom and drives it through render, toggle,
// reorder, and save-edit — the acceptance criterion "The admin CRT section
// renders, toggles, reorders and saves". '../../lib/api' is mocked so this
// test controls the payload and asserts exactly which calls the component
// makes, mirroring Dashboard.behavior.test.ts's own precedent.
import { render, fireEvent, cleanup, waitFor, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { CrtCommand } from '../../lib/types';

const listCrtCommands = vi.fn<() => Promise<{ commands: CrtCommand[] }>>();
const createCrtCommand = vi.fn();
const updateCrtCommand = vi.fn();
const deleteCrtCommand = vi.fn();

vi.mock('../../lib/api', () => ({
  listCrtCommands: (...args: unknown[]) => listCrtCommands(...(args as [])),
  createCrtCommand: (...args: unknown[]) => createCrtCommand(...(args as [])),
  updateCrtCommand: (...args: unknown[]) => updateCrtCommand(...(args as [])),
  deleteCrtCommand: (...args: unknown[]) => deleteCrtCommand(...(args as [])),
  ApiError: class ApiError extends Error {},
}));

// Imported AFTER the mocks above so CrtCommands.svelte's own imports resolve
// to the mocked module (Vitest hoists vi.mock calls).
const { default: CrtCommands } = await import('./CrtCommands.svelte');

afterEach(() => {
  cleanup();
  listCrtCommands.mockReset();
  createCrtCommand.mockReset();
  updateCrtCommand.mockReset();
  deleteCrtCommand.mockReset();
});

function twoRows(): CrtCommand[] {
  return [
    {
      id: 1,
      slug: 'whoami',
      command: '> whoami',
      output: 'open circuit sf',
      source: 'static',
      sort_order: 10,
      active: true,
      created_at: '2026-09-03T00:00:00Z',
    },
    {
      id: 2,
      slug: 'uptime',
      command: '> uptime',
      output: 'soldering irons hot since 2026',
      source: 'static',
      sort_order: 20,
      active: true,
      created_at: '2026-09-03T00:00:00Z',
    },
  ];
}

describe('CrtCommands — render', () => {
  it('lists every command row after loading', async () => {
    listCrtCommands.mockResolvedValue({ commands: twoRows() });
    render(CrtCommands);

    await waitFor(() => expect(screen.getByText('> whoami')).toBeTruthy());
    expect(screen.getByText('> uptime')).toBeTruthy();
  });

  it('shows a retry button on load failure', async () => {
    listCrtCommands.mockRejectedValue(new Error('boom'));
    render(CrtCommands);

    await waitFor(() => expect(screen.getByText('Retry')).toBeTruthy());
  });
});

describe('CrtCommands — toggle active', () => {
  it('PATCHes active:false when the checkbox is unchecked', async () => {
    const rows = twoRows();
    listCrtCommands.mockResolvedValue({ commands: rows });
    updateCrtCommand.mockResolvedValue({ ...rows[0], active: false });
    render(CrtCommands);

    await waitFor(() => expect(screen.getByLabelText('Toggle whoami active')).toBeTruthy());
    const checkbox = screen.getByLabelText('Toggle whoami active');
    await fireEvent.click(checkbox);

    await waitFor(() => expect(updateCrtCommand).toHaveBeenCalledWith(1, { active: false }));
  });
});

describe('CrtCommands — reorder', () => {
  it('moving the second row up PATCHes both rows with swapped sort_order', async () => {
    const rows = twoRows();
    listCrtCommands.mockResolvedValue({ commands: rows });
    updateCrtCommand.mockImplementation((id: number, fields: Partial<CrtCommand>) =>
      Promise.resolve({ ...rows.find((r) => r.id === id), ...fields }),
    );
    render(CrtCommands);

    await waitFor(() => expect(screen.getByLabelText('Move uptime up')).toBeTruthy());
    await fireEvent.click(screen.getByLabelText('Move uptime up'));

    await waitFor(() => expect(updateCrtCommand).toHaveBeenCalledTimes(2));
    expect(updateCrtCommand).toHaveBeenCalledWith(2, { sort_order: 10 });
    expect(updateCrtCommand).toHaveBeenCalledWith(1, { sort_order: 20 });
  });
});

describe('CrtCommands — edit and save', () => {
  it('opening a row editor, changing the command, and saving PATCHes the new value', async () => {
    const rows = twoRows();
    listCrtCommands.mockResolvedValue({ commands: rows });
    updateCrtCommand.mockResolvedValue({ ...rows[0], command: 'renamed --cmd' });
    render(CrtCommands);

    await waitFor(() => expect(screen.getAllByText('Edit')[0]).toBeTruthy());
    await fireEvent.click(screen.getAllByText('Edit')[0]);

    // Two "Command text" fields exist at once: the "Add a command" form
    // above the table, and this row's own inline editor -- the editor's is
    // the one with an id scoped to the row (crt-edit-command-1).
    const commandInput = document.getElementById('crt-edit-command-1') as HTMLInputElement;
    expect(commandInput).toBeTruthy();
    await fireEvent.input(commandInput, { target: { value: 'renamed --cmd' } });

    await fireEvent.click(screen.getByText('Save'));

    await waitFor(() =>
      expect(updateCrtCommand).toHaveBeenCalledWith(
        1,
        expect.objectContaining({ command: 'renamed --cmd', source: 'static', active: true }),
      ),
    );
  });
});

// #0403: the "this can take up to a minute" note shown after a successful
// create/update/delete/reorder/active-toggle -- CRT_SAVE_NOTE
// (lib/crtCommands.ts), rendered by a role="status" <p> that is always
// present in the DOM (sr-only, empty text) so the live region exists before
// its text is ever written into it, per #0063's persistence rule.
describe('CrtCommands — save note (#0403)', () => {
  it('does not show the save note before any action', async () => {
    listCrtCommands.mockResolvedValue({ commands: twoRows() });
    render(CrtCommands);

    await waitFor(() => expect(screen.getByText('> whoami')).toBeTruthy());
    expect(screen.queryByText(/up to a minute/)).toBeNull();
  });

  it('shows the save note after a successful active-toggle', async () => {
    const rows = twoRows();
    listCrtCommands.mockResolvedValue({ commands: rows });
    updateCrtCommand.mockResolvedValue({ ...rows[0], active: false });
    render(CrtCommands);

    await waitFor(() => expect(screen.getByLabelText('Toggle whoami active')).toBeTruthy());
    await fireEvent.click(screen.getByLabelText('Toggle whoami active'));

    await waitFor(() => expect(screen.getByText(/up to a minute/)).toBeTruthy());
    // The live region itself carries the announcement, not a second copy.
    expect(screen.getByText(/up to a minute/).getAttribute('role')).toBe('status');
  });

  it('does not show the save note after a failed save', async () => {
    const rows = twoRows();
    listCrtCommands.mockResolvedValue({ commands: rows });
    updateCrtCommand.mockRejectedValue(new Error('boom'));
    render(CrtCommands);

    await waitFor(() => expect(screen.getAllByText('Edit')[0]).toBeTruthy());
    await fireEvent.click(screen.getAllByText('Edit')[0]);
    await fireEvent.click(screen.getByText('Save'));

    await waitFor(() => expect(screen.getByText('Could not save the command. Please try again.')).toBeTruthy());
    expect(screen.queryByText(/up to a minute/)).toBeNull();
  });

  it('clears a prior save note once a new attempt starts, even if that new attempt fails', async () => {
    const rows = twoRows();
    listCrtCommands.mockResolvedValue({ commands: rows });
    updateCrtCommand.mockResolvedValueOnce({ ...rows[0], active: false });
    render(CrtCommands);

    await waitFor(() => expect(screen.getByLabelText('Toggle whoami active')).toBeTruthy());
    await fireEvent.click(screen.getByLabelText('Toggle whoami active'));
    await waitFor(() => expect(screen.getByText(/up to a minute/)).toBeTruthy());

    updateCrtCommand.mockRejectedValueOnce(new Error('boom'));
    await fireEvent.click(screen.getAllByText('Edit')[0]);
    await fireEvent.click(screen.getByText('Save'));

    await waitFor(() => expect(screen.getByText('Could not save the command. Please try again.')).toBeTruthy());
    expect(screen.queryByText(/up to a minute/)).toBeNull();
  });
});

// #0396: the per-row inline editor's over-budget width warning must be
// announced (role="status"), matching the create form's identical warning.
describe('CrtCommands — per-row width warning is announced (#0396)', () => {
  it('shows a role="status" warning in the row editor once a line crosses the CRT width budget', async () => {
    const rows = twoRows();
    listCrtCommands.mockResolvedValue({ commands: rows });
    render(CrtCommands);

    await waitFor(() => expect(screen.getAllByText('Edit')[0]).toBeTruthy());
    await fireEvent.click(screen.getAllByText('Edit')[0]);

    const outputField = document.getElementById('crt-edit-output-1') as HTMLTextAreaElement;
    expect(outputField).toBeTruthy();

    // 37 characters -- one over CRT_LINE_CHARS (36).
    const overBudget = 'a'.repeat(37);
    await fireEvent.input(outputField, { target: { value: overBudget } });

    await waitFor(() => expect(screen.getByText(/is over the 36-character CRT width/)).toBeTruthy());
    const warning = screen.getByText(/is over the 36-character CRT width/);
    expect(warning.getAttribute('role')).toBe('status');
  });
});
