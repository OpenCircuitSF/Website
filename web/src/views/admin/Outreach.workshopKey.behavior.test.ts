// @vitest-environment jsdom
//
// #0633: moving from one workshop's outreach page to another remounts the page,
// so nothing typed for one workshop reaches the other. Mounted under jsdom with
// '../../lib/api' mocked; jsdom is not a browser (AGENTS.md §1).
import { render, cleanup, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachResults, WorkshopOutreach } from '../../lib/types';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  getWorkshopOutreachResults: vi.fn(() => Promise.resolve({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults)),
  startOutreach: vi.fn(),
  resetOutreach: vi.fn(),
  draftAllOutreach: vi.fn(),
  ensureOutreachHandLinks: vi.fn(),
  listOutreachFlyers: vi.fn(() => Promise.resolve({ flyers: [] })),
  createOutreachPost: vi.fn(),
  takedownOutreachPost: vi.fn(),
  listOutreach: vi.fn(() => Promise.resolve({ outreach: [] })),
  listAdminWorkshops: vi.fn(() => Promise.resolve({ workshops: [] })),
  getOutreachTimeline: vi.fn(() => Promise.resolve({ now: '2026-11-06T18:00:00Z', events: [] })),
  ApiError: class ApiError extends Error {},
}));

const { default: Outreach } = await import('./Outreach.svelte');

afterEach(() => {
  cleanup();
  getWorkshopOutreach.mockReset();
});

const viewFor = (id: number): WorkshopOutreach => ({
  available: true, unavailable_reason: '',
  workshop: { id, slug: `w${id}`, title: `Workshop ${id}`, status: 'published', starts_at: '2099-12-05T18:00:00Z' },
  outreach: { id: 90 + id, workshop_id: id, campaign_slug: `w${id}`, created_at: '' },
  channels: [{ name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' }],
  waves: [{ name: 'announce', label: 'Announce', suggested_at: null }],
  posts: [{
    id: id * 10, outreach_id: 90 + id, channel: 'bluesky', wave: 'announce', body: `Words for ${id}`, status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null,
  }],
});

describe('Outreach: another workshop is a new page (#0633)', () => {
  it('typing for one workshop does not carry to the next', async () => {
    getWorkshopOutreach.mockImplementation((id) => Promise.resolve(viewFor(id)));
    const { rerender } = render(Outreach, { query: new URLSearchParams('view=workshop&workshop=5') });
    await screen.findByRole('heading', { name: 'Workshop 5' });
    const first = (await screen.findByLabelText('Message')) as HTMLTextAreaElement;
    first.value = 'Typed for five';
    first.dispatchEvent(new Event('input', { bubbles: true }));
    await rerender({ query: new URLSearchParams('view=workshop&workshop=7') });
    await screen.findByRole('heading', { name: 'Workshop 7' });
    const next = (await screen.findByLabelText('Message')) as HTMLTextAreaElement;
    expect(next).not.toBe(first);
    expect(next.value).not.toBe('Typed for five');
  });
});
