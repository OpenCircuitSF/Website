// @vitest-environment jsdom
//
// #0568: mounts OutreachWorkshop.svelte (with the real OutreachPanel,
// OutreachLinks and OutreachBulkActions) under jsdom with '../../lib/api'
// mocked. jsdom is not a browser (AGENTS.md §1): DOM order is real, layout is
// not.
import { render, cleanup, waitFor, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachLinksView, OutreachResults, WorkshopOutreach } from '../../lib/types';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const getWorkshopOutreachResults = vi.fn<(id: number) => Promise<OutreachResults>>();
const resetOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const getWorkshopOutreachLinks = vi.fn<(id: number) => Promise<OutreachLinksView>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  startOutreach: vi.fn(),
  getWorkshopOutreachResults: (id: number) => getWorkshopOutreachResults(id),
  getWorkshopOutreachLinks: (id: number) => getWorkshopOutreachLinks(id),
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
  getWorkshopOutreachLinks.mockReset();
  resetOutreach.mockReset();
});

const channels = [
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true },
];
const waves = [{ name: 'announce', label: 'Announce', suggested_at: null }];

function view(over: Partial<WorkshopOutreach> = {}): WorkshopOutreach {
  return {
    available: true, unavailable_reason: '',
    workshop: { id: 5, slug: 'soldering-101', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 'soldering-101', created_at: '2026-10-01T00:00:00Z' },
    channels, waves, posts: [], ...over,
  };
}

const links: OutreachLinksView = {
  available: true, unavailable_reason: '', outreach: { id: 9, campaign_slug: 'soldering-101' }, clicks_status: 'ok', missing: 0, links: [], report: null,
};

describe('OutreachWorkshop', () => {
  it('renders the header with the title, status and the editor link', async () => {
    getWorkshopOutreach.mockResolvedValue(view());
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started' } as OutreachResults);
    getWorkshopOutreachLinks.mockResolvedValue(links);
    render(OutreachWorkshop, { workshopId: 5 });
    expect(await screen.findByRole('heading', { name: 'Soldering 101' })).toBeTruthy();
    expect(screen.getByText(/published/)).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Edit workshop' }).getAttribute('href')).toBe('#/workshops/5');
    expect(screen.getByRole('link', { name: 'All outreach' }).getAttribute('href')).toBe('#/outreach');
  });

  it('orders summary, bulk actions, posts, collapsed links, results', async () => {
    getWorkshopOutreach.mockResolvedValue(view());
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started' } as OutreachResults);
    getWorkshopOutreachLinks.mockResolvedValue(links);
    render(OutreachWorkshop, { workshopId: 5 });
    const summary = await screen.findByText(/Drafted: |Nothing drafted/);
    const bulk = await screen.findByRole('button', { name: 'Draft all' });
    const posts = await screen.findByRole('heading', { name: 'Posts' });
    const tracked = await screen.findByText(/Tracked links \(/);
    const results = await screen.findByRole('link', { name: 'View results' });
    const f = Node.DOCUMENT_POSITION_FOLLOWING;
    expect(summary.compareDocumentPosition(bulk) & f).toBeTruthy();
    expect(bulk.compareDocumentPosition(posts) & f).toBeTruthy();
    expect(posts.compareDocumentPosition(tracked) & f).toBeTruthy();
    expect(tracked.compareDocumentPosition(results) & f).toBeTruthy();
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
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started' } as OutreachResults);
    getWorkshopOutreachLinks.mockResolvedValue(links);
    render(OutreachWorkshop, { workshopId: 5 });
    await screen.findByRole('button', { name: 'Draft all' });
    expect(screen.queryByRole('button', { name: 'Reset outreach' })).toBeNull();
  });

  it('opens an in-app dialog and calls the reset endpoint on confirm (#0592)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ can_reset: true }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started' } as OutreachResults);
    getWorkshopOutreachLinks.mockResolvedValue(links);
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
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started' } as OutreachResults);
    getWorkshopOutreachLinks.mockResolvedValue(links);
    resetOutreach.mockRejectedValue(new (ApiError as unknown as new (s: number, m: string) => Error)(409, 'Something has already been posted, so outreach cannot be reset.'));
    render(OutreachWorkshop, { workshopId: 5 });
    (await screen.findByRole('button', { name: 'Reset outreach' })).click();
    const dialog = await screen.findByRole('dialog');
    [...dialog.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Reset outreach')!.click();
    await waitFor(() => expect(dialog.querySelector('[role="alert"]')?.textContent).toMatch(/already been posted/));
    expect(screen.getByRole('dialog')).toBeTruthy();
  });
});
