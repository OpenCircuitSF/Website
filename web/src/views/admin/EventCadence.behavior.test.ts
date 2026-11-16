// @vitest-environment jsdom
//
// #0678: mounts EventCadence.svelte under jsdom with '../../lib/api' mocked.
import { render, fireEvent, cleanup, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { CadenceRule } from '../../lib/types';

const listCadenceRules = vi.fn();
const createCadenceRule = vi.fn();
const updateCadenceRule = vi.fn();
const deleteCadenceRule = vi.fn();
const listEventTypes = vi.fn();
const listVenues = vi.fn();

vi.mock('../../lib/api', () => ({
  listCadenceRules: (...a: unknown[]) => listCadenceRules(...a),
  createCadenceRule: (...a: unknown[]) => createCadenceRule(...a),
  updateCadenceRule: (...a: unknown[]) => updateCadenceRule(...a),
  deleteCadenceRule: (...a: unknown[]) => deleteCadenceRule(...a),
  listEventTypes: (...a: unknown[]) => listEventTypes(...a),
  listVenues: (...a: unknown[]) => listVenues(...a),
  ApiError: class ApiError extends Error {},
}));

const { default: EventCadence } = await import('./EventCadence.svelte');

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function rules(): CadenceRule[] {
  const base = { created_at: '2026-11-15T00:00:00-08:00', updated_at: '2026-11-15T00:00:00-08:00', venue: null };
  return [
    {
      ...base,
      id: 1,
      name: 'Hack night',
      description: 'every Tuesday',
      event_type: { id: 2, slug: 'hack-night', name: 'Hack Night' },
      title_prefix: null,
      frequency: 'weekly',
      weekday: 'tuesday',
      per_month: null,
      active: true,
    },
    {
      ...base,
      id: 2,
      name: 'LEDs',
      description: '1 a month',
      event_type: null,
      title_prefix: 'Programming LEDs',
      frequency: 'monthly',
      weekday: null,
      per_month: 1,
      active: false,
    },
  ];
}

async function mounted(): Promise<void> {
  listCadenceRules.mockResolvedValue({ rules: rules() });
  listEventTypes.mockResolvedValue({
    event_types: [{ id: 2, slug: 'hack-night', name: 'Hack Night', enabled: true, is_default: false, sort_order: 10, workshop_count: 0, created_at: '' }],
  });
  listVenues.mockResolvedValue({ venues: [] });
  render(EventCadence);
  await screen.findByText('LEDs');
}

describe('EventCadence', () => {
  it('lists rules with frequency, match and status', async () => {
    await mounted();
    const hack = screen.getByText('Hack night').closest('tr') as HTMLElement;
    expect(hack.textContent).toContain('every Tuesday');
    expect(hack.textContent).toContain('Hack Night events');
    expect(hack.textContent).toContain('Active');
    const leds = screen.getByText('LEDs').closest('tr') as HTMLElement;
    expect(leds.textContent).toContain('1 a month');
    expect(leds.textContent).toContain('titles starting “Programming LEDs”');
    expect(leds.textContent).toContain('Retired');
  });

  it('creates a weekly rule', async () => {
    await mounted();
    createCadenceRule.mockResolvedValue({ ...rules()[0], id: 3, name: 'Open lab', weekday: 'thursday', description: 'every Thursday' });
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Open lab' } });
    await fireEvent.change(screen.getByLabelText('Event type'), { target: { value: '2' } });
    await fireEvent.change(screen.getByLabelText('Weekday'), { target: { value: 'thursday' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add rule' }));
    await screen.findByText('Open lab');
    expect(createCadenceRule).toHaveBeenCalledWith({
      name: 'Open lab',
      frequency: 'weekly',
      weekday: 'thursday',
      event_type_id: 2,
      active: true,
    });
  });

  it('refuses a weekly rule with no weekday before submitting', async () => {
    await mounted();
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Open lab' } });
    await fireEvent.change(screen.getByLabelText('Event type'), { target: { value: '2' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add rule' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Weekday: a weekly rule needs a weekday.');
    expect(createCadenceRule).not.toHaveBeenCalled();
  });

  it('edits a rule with the whole rule as the payload', async () => {
    await mounted();
    updateCadenceRule.mockImplementation(async (id: number) => ({ ...rules()[1], id, per_month: 2, description: '2 a month' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Edit LEDs' }));
    await fireEvent.input(screen.getByLabelText('Events a month'), { target: { value: '2' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save rule' }));
    await waitFor(() => expect(updateCadenceRule).toHaveBeenCalled());
    expect(updateCadenceRule).toHaveBeenCalledWith(2, {
      name: 'LEDs',
      frequency: 'monthly',
      title_prefix: 'Programming LEDs',
      per_month: 2,
      active: false,
    });
    await screen.findByText('2 a month');
  });

  it('deletes after the confirm', async () => {
    await mounted();
    deleteCadenceRule.mockResolvedValue({ message: 'cadence rule deleted' });
    await fireEvent.click(screen.getByRole('button', { name: 'Delete Hack night' }));
    expect(deleteCadenceRule).not.toHaveBeenCalled();
    await screen.findByRole('dialog', { name: 'Delete cadence rule' });
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }));
    await waitFor(() => expect(screen.queryByText('Hack night')).toBeNull());
    expect(deleteCadenceRule).toHaveBeenCalledWith(1);
  });
});
