// @vitest-environment jsdom
//
// #0569: mounts OutreachWorkshop.svelte (real OutreachPanel, OutreachPostCards,
// OutreachComposer) under jsdom with '../../lib/api' mocked. jsdom is not a
// browser (AGENTS.md §1): no browser was run for this change.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachLinksView, OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const sendOutreachPost = vi.fn();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  startOutreach: vi.fn(),
  getWorkshopOutreachResults: () => Promise.resolve({ status: 'not_started', posts: [], totals: null }),
  getWorkshopOutreachLinks: () =>
    Promise.resolve({
      available: true, unavailable_reason: '', outreach: { id: 9, campaign_slug: 's' }, clicks_status: 'ok', missing: 0, links: [], report: null,
    } as OutreachLinksView),
  createWorkshopOutreachLinks: vi.fn(),
  draftAllOutreach: vi.fn(),
  scheduleAllOutreach: vi.fn(),
  createOutreachPost: vi.fn(),
  sendOutreachPost: (id: number) => sendOutreachPost(id),
  unscheduleOutreachPost: vi.fn(),
  markOutreachPosted: vi.fn(),
  updateOutreachPost: vi.fn(),
  prepareOutreachPost: vi.fn(),
  scheduleOutreachPost: vi.fn(),
  deleteOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

const { default: OutreachWorkshop } = await import('./OutreachWorkshop.svelte');

afterEach(() => {
  cleanup();
  getWorkshopOutreach.mockReset();
  sendOutreachPost.mockReset();
  vi.unstubAllGlobals();
});

const channels = [
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true },
  { name: 'reddit', label: 'Reddit', utm_source: 'reddit', utm_medium: 'social', char_limit: 0, automatic: false, configured: false },
];
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: null },
];

function post(over: Partial<OutreachPost>): OutreachPost {
  return {
    id: 1, outreach_id: 9, channel: 'bluesky', wave: 'announce', body: 'Come solder with us https://go.example/abc', status: 'draft',
    prepared: true, stale: false, held: false, short_link_key: 'abc', short_url: 'https://go.example/abc', destination_url: 'https://x/w',
    utm: { source: 'bluesky', medium: 'social', campaign: 'c', content: 'announce' }, scheduled_at: null, posted_at: null, remote_url: null,
    last_error: null, created_at: '', updated_at: null, ...over,
  };
}

function setup(posts: OutreachPost[]): void {
  getWorkshopOutreach.mockResolvedValue({
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 's', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 's', created_at: '' },
    channels, waves, posts,
  });
  render(OutreachWorkshop, { workshopId: 5 });
}

describe('post cards (#0569)', () => {
  it('shows the full body and each status line, grouped by wave', async () => {
    setup([
      post({ id: 1, status: 'posted', posted_at: '2026-10-17T20:00:00Z', remote_url: 'https://bsky.app/p/1' }),
      post({ id: 2, channel: 'reddit', status: 'manual', body: 'Reddit body text' }),
      post({ id: 3, wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z', body: 'Reminder body' }),
      post({ id: 4, wave: 'reminder', channel: 'reddit', status: 'failed', body: 'Failing body', last_error: 'raw' }),
    ]);
    expect(await screen.findByText(/Come solder with us/)).toBeTruthy();
    expect(screen.getByText('Reddit body text')).toBeTruthy();
    expect(screen.getByText('Reminder body')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'view post' }).getAttribute('href')).toBe('https://bsky.app/p/1');
    expect(screen.getByText(/^Posts automatically /)).toBeTruthy();
    expect(screen.getByText('Post by hand: copy and paste')).toBeTruthy();
    expect(screen.getByText('Failed: the post could not be sent')).toBeTruthy();
    const headings = screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent);
    expect(headings).toEqual(['Announce', 'Reminder']);
  });

  it('shows Not scheduled for an automatic draft and Not drafted with a Draft button', async () => {
    setup([post({ id: 1 })]);
    expect(await screen.findByText('Not scheduled')).toBeTruthy();
    expect(screen.getAllByText('Not drafted').length).toBe(3);
    expect(screen.getAllByRole('button', { name: 'Draft' }).length).toBe(3);
  });

  it('automatic cards offer Post now and Schedule…, manual cards offer Copy text and not Post now', async () => {
    setup([post({ id: 1 }), post({ id: 2, channel: 'reddit', status: 'manual' })]);
    await screen.findByText('Not scheduled');
    expect(screen.getAllByRole('button', { name: 'Post now' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Schedule…' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Copy text' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Mark posted' }).length).toBe(1);
    const manual = screen.getByRole('article', { name: 'Reddit Announce' });
    expect(manual.textContent).not.toContain('Post now');
  });

  it('Edit opens the composer inline in the card', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    expect(screen.queryByLabelText('Post text')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const card = screen.getByRole('article', { name: 'Bluesky Announce' });
    expect(card.querySelector('textarea')).toBeTruthy();
    expect(card.textContent).toMatch(/\/ 300 characters/);
  });

  it('Post now asks for confirmation and does not send when declined', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    const confirmFn = vi.fn(() => false);
    vi.stubGlobal('confirm', confirmFn);
    await fireEvent.click(screen.getByRole('button', { name: 'Post now' }));
    expect(confirmFn).toHaveBeenCalledWith('Post this to Bluesky now?');
    expect(sendOutreachPost).not.toHaveBeenCalled();
    confirmFn.mockReturnValue(true);
    sendOutreachPost.mockResolvedValue(post({ id: 1, status: 'queued' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Post now' }));
    await waitFor(() => expect(sendOutreachPost).toHaveBeenCalledWith(1));
  });

  it('the tracked links table is collapsed by default and counts links', async () => {
    setup([post({ id: 1 })]);
    await screen.findByText('Not scheduled');
    const details = screen.getByText(/Tracked links \(/).closest('details');
    expect(details).toBeTruthy();
    expect(details?.hasAttribute('open')).toBe(false);
  });

  it('shows the summary line', async () => {
    setup([post({ id: 1 }), post({ id: 2, wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z' })]);
    expect(await screen.findByText('2 posts drafted · 1 scheduled · 0 posted')).toBeTruthy();
  });
});
