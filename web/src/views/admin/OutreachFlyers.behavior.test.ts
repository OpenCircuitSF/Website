// @vitest-environment jsdom
//
// #0588: the flyers page preselects the workshop the "Make a flyer" link came
// from. jsdom is not a browser (AGENTS.md §1): this proves the wiring, not layout.
import { render, cleanup, waitFor, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../lib/api', () => ({
  listOutreachFlyers: () => Promise.resolve({ flyers: [] }),
  listOutreach: () =>
    Promise.resolve({
      outreach: [
        { outreach_id: 11, workshop_id: 5, workshop_slug: 'a', workshop_title: 'Soldering 101', workshop_status: 'published', campaign_slug: 'a', starts_at: null, counts: {}, created_at: '' },
        { outreach_id: 12, workshop_id: 6, workshop_slug: 'b', workshop_title: 'Arduino Night', workshop_status: 'published', campaign_slug: 'b', starts_at: null, counts: {}, created_at: '' },
      ],
    }),
  createOutreachFlyer: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

const { default: OutreachFlyers } = await import('./OutreachFlyers.svelte');

afterEach(cleanup);

describe('OutreachFlyers workshop preselect', () => {
  it('selects the named workshop, else the first', async () => {
    render(OutreachFlyers, { workshopId: 6 });
    await waitFor(() => expect((screen.getByLabelText('Workshop') as HTMLSelectElement).value).toBe('12'));
    cleanup();
    render(OutreachFlyers, { workshopId: 99 });
    await waitFor(() => expect((screen.getByLabelText('Workshop') as HTMLSelectElement).value).toBe('11'));
    cleanup();
    render(OutreachFlyers);
    await waitFor(() => expect((screen.getByLabelText('Workshop') as HTMLSelectElement).value).toBe('11'));
  });
});
