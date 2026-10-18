// @vitest-environment jsdom
//
// #0568, #0582: mounts OutreachWorkshop.svelte (with the real OutreachPanel,
// OutreachAnnouncement, OutreachWaveRow and OutreachBulkActions) under jsdom with '../../lib/api'
// mocked. jsdom is not a browser (AGENTS.md §1): DOM order is real, layout is
// not.
import { render, cleanup, waitFor, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const getWorkshopOutreachResults = vi.fn<(id: number) => Promise<OutreachResults>>();
const startOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const resetOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  startOutreach: (id: number) => startOutreach(id),
  getWorkshopOutreachResults: (id: number) => getWorkshopOutreachResults(id),
  createWorkshopOutreachLinks: vi.fn(),
  draftAllOutreach: vi.fn(),
  resetOutreach: (id: number) => resetOutreach(id),
  scheduleAllOutreach: vi.fn(),
  createOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachWorkshop } = await import('./OutreachWorkshop.svelte');

afterEach(() => {
  cleanup();
  getWorkshopOutreach.mockReset();
  getWorkshopOutreachResults.mockReset();
  startOutreach.mockReset();
  resetOutreach.mockReset();
});

const channels = [
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' },
];
const waves = [{ name: 'announce', label: 'Announce', suggested_at: null }];
const allWaves = [...waves, { name: 'reminder', label: 'Reminder', suggested_at: null }, { name: 'last-call', label: 'Last call', suggested_at: null }];

function view(over: Partial<WorkshopOutreach> = {}): WorkshopOutreach {
  return {
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 'soldering-101', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 'soldering-101', created_at: '2026-10-01T00:00:00Z' },
    channels, waves, posts: [], ...over,
  };
}

describe('OutreachWorkshop', () => {
  it('renders the header with the title, status and the editor link', async () => {
    getWorkshopOutreach.mockResolvedValue(view());
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    expect(await screen.findByRole('heading', { name: 'Soldering 101' })).toBeTruthy();
    expect(screen.getByText(/published/)).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Edit workshop' }).getAttribute('href')).toBe('#/workshops/5');
    expect(screen.getByRole('link', { name: 'All outreach' }).getAttribute('href')).toBe('#/outreach');
  });

  it('orders summary, Announcement, wave rows, Schedule all, Post by hand, Flyers, Links and clicks (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ waves: allWaves }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    const summary = await screen.findByText('No posts yet. Start with the announcement.', { selector: 'p.summary' });
    const announcement = await screen.findByRole('heading', { name: 'Announcement' });
    const reminder = await screen.findByText('Reminder', { selector: 'strong' });
    const lastCall = await screen.findByText('Last call', { selector: 'strong' });
    const schedule = await screen.findByRole('button', { name: 'Schedule all' });
    const byHand = await screen.findByText('Post by hand', { selector: 'strong' });
    const flyers = await screen.findByText('Flyers', { selector: 'strong' });
    const links = await screen.findByText('Links and clicks', { selector: 'strong' });
    const results = await screen.findByRole('link', { name: 'View results' });
    const order = [summary, announcement, reminder, lastCall, schedule, byHand, flyers, links, results];
    for (let i = 0; i < order.length - 1; i++) {
      expect(order[i].compareDocumentPosition(order[i + 1]) & Node.DOCUMENT_POSITION_FOLLOWING, `element ${i}`).toBeTruthy();
    }
    expect(screen.getByRole('link', { name: 'Open flyers' }).getAttribute('href')).toMatch(/flyers/);
  });

  it('has no tracked-links table, Draft all or Start outreach on the page (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ waves: allWaves }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    await screen.findByRole('heading', { name: 'Announcement' });
    expect(screen.queryByText(/Tracked links/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Draft all' })).toBeNull();
    expect(screen.queryByText(/Start outreach/)).toBeNull();
    expect(screen.queryByText(/campaign/i)).toBeNull();
  });

  it('starts outreach by itself, once, for an available workshop with none (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ outreach: null }));
    startOutreach.mockResolvedValue(view());
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    await screen.findByRole('heading', { name: 'Announcement' });
    expect(startOutreach).toHaveBeenCalledTimes(1);
    expect(startOutreach).toHaveBeenCalledWith(5);
  });

  it('never starts outreach for an unavailable workshop (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ available: false, unavailable_reason: 'workshop_not_published', outreach: null }));
    render(OutreachWorkshop, { workshopId: 5 });
    await screen.findByRole('link', { name: 'Open the workshop editor' });
    expect(startOutreach).not.toHaveBeenCalled();
  });

  it('shows the link report from Start as a warning in the Links and clicks row (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ outreach: null }));
    startOutreach.mockResolvedValue(
      view({ link_report: { created: 14, existing: 0, stopped: false, failed: [{ kind: 'post', label: 'Mastodon · Reminder', reason: 'failed' }] } }),
    );
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    const warn = await screen.findByText(/Mastodon · Reminder \(failed\)/);
    expect(warn.className).toContain('text-warn');
    expect(warn.closest('.row-details')?.textContent).toContain('Links and clicks');
  });

  it('lists only non-automatic channels under Post by hand (#0582)', async () => {
    const post = (over: Partial<OutreachPost>): OutreachPost => ({
      id: 1, outreach_id: 9, channel: 'bluesky', wave: 'announce', body: 'Bluesky words', status: 'draft', prepared: true, stale: false, customized: false,
      held: false, short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x',
      utm: { source: '', medium: '', campaign: '', content: '' }, scheduled_at: null, posted_at: null, remote_url: null, last_error: null,
      created_at: '', updated_at: null, ...over,
    });
    getWorkshopOutreach.mockResolvedValue(
      view({
        channels: [...channels, { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' }],
        posts: [post({}), post({ id: 2, channel: 'instagram', status: 'manual', body: 'Instagram words' })],
      }),
    );
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    const byHand = (await screen.findByText('Post by hand', { selector: 'strong' })).closest('details')!;
    expect(byHand.textContent).toContain('Instagram words');
    expect(byHand.textContent).not.toContain('Bluesky words');
    expect(byHand.hasAttribute('open')).toBe(false);
  });

  it('points at Settings when ShortLinks is not connected', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ available: false, unavailable_reason: 'shortlinks_not_configured', outreach: null }));
    render(OutreachWorkshop, { workshopId: 5 });
    await waitFor(() => expect(screen.getByRole('link', { name: 'Open Settings' }).getAttribute('href')).toBe('#/settings?section=outreach'));
  });

  it('points at the editor when the workshop is not published', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ available: false, unavailable_reason: 'workshop_not_published', outreach: null }));
    render(OutreachWorkshop, { workshopId: 5 });
    const link = await screen.findByRole('link', { name: 'Open the workshop editor' });
    expect(link.getAttribute('href')).toBe('#/workshops/5');
  });

  it('hides Reset outreach unless the view says can_reset (#0592)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ can_reset: false }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    await screen.findByRole('heading', { name: 'Announcement' });
    expect(screen.queryByRole('button', { name: 'Reset outreach' })).toBeNull();
  });

  it('opens an in-app dialog and calls the reset endpoint on confirm (#0592)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ can_reset: true }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    resetOutreach.mockResolvedValue(view({ can_reset: true }));
    const confirmSpy = vi.spyOn(window, 'confirm');
    render(OutreachWorkshop, { workshopId: 5 });
    (await screen.findByRole('button', { name: 'Reset outreach' })).click();
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toMatch(/Removed/);
    expect(dialog.textContent).toMatch(/Kept/);
    expect(resetOutreach).not.toHaveBeenCalled();
    const inDialog = dialog.querySelectorAll('button');
    const confirmBtn = [...inDialog].find((b) => b.textContent?.trim() === 'Reset outreach')!;
    confirmBtn.click();
    await waitFor(() => expect(resetOutreach).toHaveBeenCalledWith(5));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(getWorkshopOutreach.mock.calls.length).toBeGreaterThan(1);
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  it('shows the server message in the dialog when reset is refused (#0592)', async () => {
    const { ApiError } = await import('../../lib/api');
    getWorkshopOutreach.mockResolvedValue(view({ can_reset: true }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    resetOutreach.mockRejectedValue(new (ApiError as unknown as new (s: number, m: string) => Error)(409, 'Something has already been posted, so outreach cannot be reset.'));
    render(OutreachWorkshop, { workshopId: 5 });
    (await screen.findByRole('button', { name: 'Reset outreach' })).click();
    const dialog = await screen.findByRole('dialog');
    [...dialog.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Reset outreach')!.click();
    await waitFor(() => expect(dialog.querySelector('[role="alert"]')?.textContent).toMatch(/already been posted/));
    expect(screen.getByRole('dialog')).toBeTruthy();
  });
});
