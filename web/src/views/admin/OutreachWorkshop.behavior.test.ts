// @vitest-environment jsdom
//
// #0568, #0582: mounts OutreachWorkshop.svelte (with the real OutreachPanel,
// OutreachAnnouncement, OutreachWaveRow and OutreachBulkActions) under jsdom with '../../lib/api'
// mocked. jsdom is not a browser (AGENTS.md §1): DOM order is real, layout is
// not.
import { render, cleanup, waitFor, screen, fireEvent, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
import { weekdayTime } from '../../lib/outreachCards';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const getWorkshopOutreachResults = vi.fn<(id: number) => Promise<OutreachResults>>();
const startOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const resetOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const scheduleAllOutreach = vi.fn();
const ensureOutreachHandLinks = vi.fn();
const takedownOutreachPost = vi.fn();
const listOutreachFlyers = vi.fn<() => Promise<{ flyers: { id: number; workshop_id: number }[] }>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  startOutreach: (id: number) => startOutreach(id),
  getWorkshopOutreachResults: (id: number) => getWorkshopOutreachResults(id),
  createWorkshopOutreachLinks: vi.fn(),
  ensureOutreachHandLinks: (id: number) => ensureOutreachHandLinks(id),
  draftAllOutreach: vi.fn(),
  resetOutreach: (id: number) => resetOutreach(id),
  listOutreachFlyers: () => listOutreachFlyers(),
  scheduleAllOutreach: (...a: unknown[]) => scheduleAllOutreach(...a),
  createOutreachPost: vi.fn(),
  takedownOutreachPost: (id: number) => takedownOutreachPost(id),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachWorkshop } = await import('./OutreachWorkshop.svelte');

// jsdom has no scrollIntoView, and selecting a section calls it (#0625).
const scrollSpy = vi.fn();
Element.prototype.scrollIntoView = scrollSpy;

afterEach(() => {
  cleanup();
  getWorkshopOutreach.mockReset();
  getWorkshopOutreachResults.mockReset();
  startOutreach.mockReset();
  resetOutreach.mockReset();
  listOutreachFlyers.mockReset();
  scheduleAllOutreach.mockReset();
  takedownOutreachPost.mockReset();
  ensureOutreachHandLinks.mockReset();
  scrollSpy.mockClear();
});

const channels = [
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' },
];
const waves = [{ name: 'announce', label: 'Announce', suggested_at: null }];
const allWaves = [...waves, { name: 'reminder', label: 'Reminder', suggested_at: null }, { name: 'last-call', label: 'Final', suggested_at: null }];

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
    expect(screen.getByRole('link', { name: 'Edit event' }).getAttribute('href')).toBe('#/workshops/5');
    expect(screen.getByRole('link', { name: 'All outreach' }).getAttribute('href')).toBe('#/outreach');
  });

  it('orders the status board, Announcement, wave rows, Schedule all, Post by hand, Flyers, Links and clicks (#0624)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ waves: allWaves }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    const board = await screen.findByRole('table', { name: 'Posts and when they go out' });
    const announcement = await screen.findByRole('heading', { name: /Announce/ });
    const reminder = await screen.findByText('Reminder', { selector: 'strong' });
    const lastCall = await screen.findByText('Final', { selector: 'strong' });
    const schedule = await screen.findByRole('button', { name: 'Schedule all' });
    const byHand = await screen.findByText('Post by hand', { selector: 'strong' });
    const flyers = await screen.findByText('Flyers', { selector: 'strong' });
    const links = await screen.findByText('Links and clicks', { selector: 'strong' });
    const results = await screen.findByRole('link', { name: 'View results' });
    const order = [board, announcement, reminder, lastCall, schedule, byHand, flyers, links, results];
    for (let i = 0; i < order.length - 1; i++) {
      expect(order[i].compareDocumentPosition(order[i + 1]) & Node.DOCUMENT_POSITION_FOLLOWING, `element ${i}`).toBeTruthy();
    }
    expect(screen.getByRole('link', { name: 'Open flyers' }).getAttribute('href')).toMatch(/flyers/);
  });

  it('shows the status board in place of the summary line (#0624)', async () => {
    const at = '2026-11-20T18:00:00Z';
    getWorkshopOutreach.mockResolvedValue(view({
      channels: [...channels, { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' }],
      waves: allWaves,
      posts: [{
        id: 1, outreach_id: 9, channel: 'bluesky', wave: 'announce', body: 'Hello', status: 'scheduled', prepared: true, stale: false, customized: false, held: false,
        short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
        scheduled_at: at, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null,
      }],
    }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    const { container } = render(OutreachWorkshop, { workshopId: 5 });
    const board = await screen.findByRole('table', { name: 'Posts and when they go out' });
    expect(container.querySelector('p.summary')).toBeNull();
    expect(within(board).getByRole('columnheader', { name: 'Instagram' })).toBeTruthy();
    const details = (await screen.findByText('Post by hand', { selector: 'strong' })).closest('details') as HTMLDetailsElement;
    expect(details.open).toBe(false);
    expect(board.querySelector('tr[data-wave="announce"] td[data-channel="bluesky"]')?.textContent).toContain(weekdayTime(at));
  });

  it('has no tracked-links table, Draft all or Start outreach on the page (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ waves: allWaves }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    await screen.findByRole('heading', { name: /Announce/ });
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
    await screen.findByRole('heading', { name: /Announce/ });
    expect(startOutreach).toHaveBeenCalledTimes(1);
    expect(startOutreach).toHaveBeenCalledWith(5);
  });

  it('never starts outreach for an unavailable workshop (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ available: false, unavailable_reason: 'workshop_not_published', outreach: null }));
    render(OutreachWorkshop, { workshopId: 5 });
    await screen.findByRole('link', { name: 'Open the event editor' });
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

  describe('hand-posted links (#0623)', () => {
    const noResults = { status: 'not_started', posts: [], totals: null } as unknown as OutreachResults;
    const report = (over = {}) => ({ created: 0, existing: 3, failed: [], stopped: false, drafted: 0, ...over });

    it('makes hand-posted links once when the view says they are missing', async () => {
      getWorkshopOutreach.mockResolvedValue(view({ hand_links_pending: true }));
      getWorkshopOutreachResults.mockResolvedValue(noResults);
      ensureOutreachHandLinks.mockResolvedValue(report());
      render(OutreachWorkshop, { workshopId: 5 });
      await screen.findByRole('heading', { name: 'Soldering 101' });
      await waitFor(() => expect(ensureOutreachHandLinks).toHaveBeenCalledWith(5));
      expect(ensureOutreachHandLinks).toHaveBeenCalledTimes(1);
    });

    it('does not call it when nothing is missing', async () => {
      getWorkshopOutreach.mockResolvedValue(view({ hand_links_pending: false }));
      getWorkshopOutreachResults.mockResolvedValue(noResults);
      render(OutreachWorkshop, { workshopId: 5 });
      await screen.findByRole('heading', { name: 'Soldering 101' });
      await waitFor(() => expect(getWorkshopOutreachResults).toHaveBeenCalled());
      expect(ensureOutreachHandLinks).not.toHaveBeenCalled();
    });

    it('keeps the page and shows a note when making the links fails', async () => {
      getWorkshopOutreach.mockResolvedValue(view({ hand_links_pending: true }));
      getWorkshopOutreachResults.mockResolvedValue(noResults);
      ensureOutreachHandLinks.mockRejectedValue(new Error('boom'));
      render(OutreachWorkshop, { workshopId: 5 });
      expect(await screen.findByText(/ShortLinks for hand-posted channels could not be made/)).toBeTruthy();
      expect(screen.getByRole('heading', { name: 'Soldering 101' })).toBeTruthy();
      expect(ensureOutreachHandLinks).toHaveBeenCalledTimes(1);
    });

    it('reloads the view after links were created', async () => {
      getWorkshopOutreach.mockResolvedValueOnce(view({ hand_links_pending: true })).mockResolvedValue(view({ hand_links_pending: false }));
      getWorkshopOutreachResults.mockResolvedValue(noResults);
      ensureOutreachHandLinks.mockResolvedValue(report({ created: 6, drafted: 4 }));
      render(OutreachWorkshop, { workshopId: 5 });
      await waitFor(() => expect(getWorkshopOutreach).toHaveBeenCalledTimes(2));
      await waitFor(() => expect(getWorkshopOutreachResults.mock.calls.length).toBeGreaterThanOrEqual(2));
      expect(ensureOutreachHandLinks).toHaveBeenCalledTimes(1);
    });
  });

  it('points at Settings when ShortLinks is not connected', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ available: false, unavailable_reason: 'shortlinks_not_configured', outreach: null }));
    render(OutreachWorkshop, { workshopId: 5 });
    await waitFor(() => expect(screen.getByRole('link', { name: 'Open Settings' }).getAttribute('href')).toBe('#/settings?section=outreach'));
  });

  it('points at the editor when the workshop is not published', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ available: false, unavailable_reason: 'workshop_not_published', outreach: null }));
    render(OutreachWorkshop, { workshopId: 5 });
    const link = await screen.findByRole('link', { name: 'Open the event editor' });
    expect(link.getAttribute('href')).toBe('#/workshops/5');
  });

  it('hides Reset outreach unless the view says can_reset (#0592)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ can_reset: false }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    render(OutreachWorkshop, { workshopId: 5 });
    await screen.findByRole('heading', { name: /Announce/ });
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

  it('clears the "Outreach was reset" note on the next state change (#0593)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ can_reset: true, waves: allWaves }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    resetOutreach.mockResolvedValue(view({ can_reset: true }));
    render(OutreachWorkshop, { workshopId: 5 });
    (await screen.findByRole('button', { name: 'Reset outreach' })).click();
    const dialog = await screen.findByRole('dialog');
    [...dialog.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Reset outreach')!.click();
    expect(await screen.findByText(/Outreach was reset/)).toBeTruthy();
    const plan = {
      dry_run: true,
      to_schedule: [{ post_id: 11, channel: 'bluesky', channel_label: 'Bluesky', wave: 'announce', wave_label: 'Announce', scheduled_at: '2099-12-04T18:00:00Z' }],
      skipped: [],
    };
    scheduleAllOutreach.mockResolvedValueOnce(plan).mockResolvedValueOnce({ ...plan, dry_run: false });
    await fireEvent.click(await screen.findByRole('button', { name: 'Schedule all' }));
    expect(screen.queryByText(/Outreach was reset/)).not.toBeNull();
    await fireEvent.click(await screen.findByRole('button', { name: 'Schedule 1 post' }));
    await waitFor(() => expect(screen.queryByText(/Outreach was reset/)).toBeNull());
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

  describe('Instagram flyer link (#0588)', () => {
    const igChannels = [
      ...channels,
      { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false, account: '' },
    ];
    const igPost = {
      id: 3, outreach_id: 9, channel: 'instagram', wave: 'announce', body: 'Link in bio.', status: 'manual', prepared: true, stale: false, customized: false,
      held: false, short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
      scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null,
    } as OutreachPost;

    async function openFlyerLink(): Promise<HTMLAnchorElement> {
      getWorkshopOutreach.mockResolvedValue(view({ channels: igChannels, posts: [igPost] }));
      getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [] } as unknown as OutreachResults);
      render(OutreachWorkshop, { workshopId: 5 });
      await waitFor(() => expect(document.querySelector('a.open-flyer')).toBeTruthy());
      return document.querySelector('a.open-flyer') as HTMLAnchorElement;
    }

    it("opens this workshop's flyer when it has one", async () => {
      listOutreachFlyers.mockResolvedValue({ flyers: [{ id: 7, workshop_id: 5 }, { id: 2, workshop_id: 6 }] });
      await openFlyerLink();
      await waitFor(() => expect((document.querySelector('a.open-flyer') as HTMLAnchorElement).textContent).toBe('Open flyer'));
      expect((document.querySelector('a.open-flyer') as HTMLAnchorElement).getAttribute('href')).toBe('#/outreach?view=flyers&flyer=7');
    });

    it('offers Make a flyer for this workshop when it has none, and when the list cannot load', async () => {
      listOutreachFlyers.mockResolvedValue({ flyers: [{ id: 2, workshop_id: 6 }] });
      const a = await openFlyerLink();
      await waitFor(() => expect(a.textContent).toBe('Make a flyer'));
      expect(a.getAttribute('href')).toBe('#/outreach?view=flyers&workshop=5');
      cleanup();
      listOutreachFlyers.mockRejectedValue(new Error('down'));
      const b = await openFlyerLink();
      expect(b.textContent).toBe('Make a flyer');
    });
  });

  it('an unknown workshop shows a not-found state with a way back, no Retry and no stale links (#0591)', async () => {
    const { ApiError } = await import('../../lib/api');
    getWorkshopOutreach.mockRejectedValue(new ApiError(404, 'workshop not found', null));
    render(OutreachWorkshop, { workshopId: 999 });
    expect(await screen.findByRole('heading', { name: 'Event not found' })).toBeTruthy();
    expect(screen.getByText('This event no longer exists.')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'All outreach' }).getAttribute('href')).toBe('#/outreach');
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Edit event' })).toBeNull();
    expect(screen.queryByText('Loading…')).toBeNull();
    expect(screen.queryByText('workshop not found')).toBeNull();
  });

  it('any other load failure still offers Retry (#0591)', async () => {
    const { ApiError } = await import('../../lib/api');
    getWorkshopOutreach.mockRejectedValue(new ApiError(500, 'boom', null));
    render(OutreachWorkshop, { workshopId: 5 });
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeTruthy();
  });
});

describe('OutreachWorkshop: Delete and redo (#0606)', () => {
  const posted = (): OutreachPost =>
    ({ id: 3, outreach_id: 9, channel: 'bluesky', wave: 'announce', status: 'posted', body: 'Old body', customized: false, posted_at: '2026-10-18T17:00:00Z', remote_url: 'https://bsky.app/profile/x/post/abc', attempt_generation: 1 }) as unknown as OutreachPost;

  it('a posted Bluesky post in the announcement offers Delete and redo, and confirming takes it down and reloads', async () => {
    const draft = { ...posted(), status: 'draft', body: 'New body', posted_at: null, remote_url: null } as unknown as OutreachPost;
    getWorkshopOutreach.mockResolvedValueOnce(view({ posts: [posted()] })).mockResolvedValue(view({ posts: [draft] }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    takedownOutreachPost.mockResolvedValue(draft);
    render(OutreachWorkshop, { workshopId: 5 });
    const btn = await screen.findByRole('button', { name: 'Delete and redo…' });
    await fireEvent.click(btn);
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('Delete this post from Bluesky?');
    expect(takedownOutreachPost).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole('button', { name: 'Delete post' }));
    await waitFor(() => expect(takedownOutreachPost).toHaveBeenCalledWith(3));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Delete and redo…' })).toBeNull());
    expect(getWorkshopOutreach.mock.calls.length).toBeGreaterThan(1);
  });

  it('with Bluesky and Mastodon both posted, each offers Delete and redo, and taking one down leaves the other button (#0606 review)', async () => {
    const both = [...channels, { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true, configured: true, account: '' }];
    const masto = { ...posted(), id: 4, channel: 'mastodon', remote_url: 'https://mastodon.social/@x/1' } as unknown as OutreachPost;
    const draft = { ...posted(), status: 'draft', posted_at: null, remote_url: null } as unknown as OutreachPost;
    getWorkshopOutreach
      .mockResolvedValueOnce(view({ channels: both, posts: [posted(), masto] }))
      .mockResolvedValue(view({ channels: both, posts: [draft, masto] }));
    getWorkshopOutreachResults.mockResolvedValue({ status: 'not_started', posts: [], totals: null } as unknown as OutreachResults);
    takedownOutreachPost.mockResolvedValue(draft);
    render(OutreachWorkshop, { workshopId: 5 });
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Delete and redo…' })).toHaveLength(2));
    const bsky = screen.getByRole('article', { name: 'Bluesky Announce preview' });
    await fireEvent.click(within(bsky).getByRole('button', { name: 'Delete and redo…' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Delete post' }));
    await waitFor(() => expect(takedownOutreachPost).toHaveBeenCalledWith(3));
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Delete and redo…' })).toHaveLength(1));
    const mas = screen.getByRole('article', { name: 'Mastodon Announce preview' });
    expect(within(mas).getByRole('button', { name: 'Delete and redo…' })).toBeTruthy();
  });
});

describe('one open section (#0625)', () => {
  const two = [...channels, { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true, configured: true, account: '' }];
  let id = 100;
  const post = (channel: string, wave: string, status: OutreachPost['status'] = 'draft'): OutreachPost => ({
    id: id++, outreach_id: 9, channel, wave, body: `${wave} words`, status, prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: status === 'posted' ? '2026-10-18T17:00:00Z' : null, remote_url: null, last_error: null, created_at: '', updated_at: null,
  });
  const written = (posted: string[] = []) =>
    allWaves.flatMap((w) => two.map((c) => post(c.name, w.name, posted.includes(w.name) ? 'posted' : 'draft')));
  const noResults = { status: 'not_started', posts: [], totals: null } as unknown as OutreachResults;
  const fullView = (posted: string[] = [], over: Partial<WorkshopOutreach> = {}) => view({ channels: two, waves: allWaves, posts: written(posted), ...over });
  const panel = (w: string) => document.getElementById(`outreach-section-${w}-panel`) as HTMLElement;
  const rowBtn = (w: string) => document.getElementById(`outreach-section-${w}-head`) as HTMLButtonElement;
  const openNames = () => allWaves.map((w) => w.name).filter((w) => !panel(w).hidden);

  async function mount(v: WorkshopOutreach): Promise<HTMLElement> {
    getWorkshopOutreach.mockResolvedValue(v);
    getWorkshopOutreachResults.mockResolvedValue(noResults);
    render(OutreachWorkshop, { workshopId: 5 });
    return screen.findByRole('table', { name: 'Posts and when they go out' });
  }
  const boardBtn = (board: HTMLElement, name: RegExp) => within(board).getByRole('button', { name });
  const lastScrollWas = (w: string) => {
    expect(scrollSpy.mock.contexts.at(-1)).toBe(document.getElementById(`outreach-section-${w}`));
    expect(scrollSpy.mock.calls.at(-1)?.[0]).toEqual({ block: 'start' });
  };

  it('Announce is a row like the others (#0625)', async () => {
    await mount(fullView());
    const heads = screen.getAllByRole('heading', { level: 3 });
    expect(heads).toHaveLength(3);
    for (const h of heads) expect(h.querySelector('button')).not.toBeNull();
  });

  it('opens the first section not fully posted (#0625)', async () => {
    await mount(fullView(['announce']));
    expect(openNames()).toEqual(['reminder']);
  });

  it('exactly one section is open after each selection (#0625)', async () => {
    const board = await mount(fullView());
    await fireEvent.click(boardBtn(board, /Final/));
    await waitFor(() => expect(openNames()).toEqual(['last-call']));
    await fireEvent.click(rowBtn('reminder'));
    await waitFor(() => expect(openNames()).toEqual(['reminder']));
    await fireEvent.click(boardBtn(board, /Announce/));
    await waitFor(() => expect(openNames()).toEqual(['announce']));
  });

  it('the open section does not move when a reload shows it fully posted (#0625)', async () => {
    getWorkshopOutreach.mockResolvedValueOnce(fullView([], { hand_links_pending: true })).mockResolvedValue(fullView(['announce']));
    getWorkshopOutreachResults.mockResolvedValue(noResults);
    ensureOutreachHandLinks.mockResolvedValue({ created: 1, existing: 0, failed: [], stopped: false, drafted: 0 });
    const { container } = render(OutreachWorkshop, { workshopId: 5 });
    await waitFor(() => expect(container.querySelector('tr[data-wave="announce"] .board-status')?.textContent).toBe('Posted'));
    expect(openNames()).toEqual(['announce']);
  });

  it('selecting a board row opens it, brings it into view and moves focus to its row button (#0625)', async () => {
    const board = await mount(fullView());
    await fireEvent.click(boardBtn(board, /Reminder/));
    await waitFor(() => expect(document.activeElement).toBe(rowBtn('reminder')));
    expect(openNames()).toEqual(['reminder']);
    lastScrollWas('reminder');
  });

  it('selecting the board row of the open section still brings it into view (#0625)', async () => {
    const board = await mount(fullView());
    await fireEvent.click(boardBtn(board, /Announce/));
    await waitFor(() => expect(scrollSpy).toHaveBeenCalledTimes(1));
    expect(openNames()).toEqual(['announce']);
    lastScrollWas('announce');
  });

  it('selecting a row button below the open section also brings it into view (#0625)', async () => {
    await mount(fullView());
    await fireEvent.click(rowBtn('last-call'));
    await waitFor(() => expect(scrollSpy).toHaveBeenCalledTimes(1));
    lastScrollWas('last-call');
    expect(document.activeElement).toBe(rowBtn('last-call'));
  });

  it('typed text survives switching, and the row and board say it is not saved (#0625)', async () => {
    const board = await mount(fullView());
    const field = panel('announce').querySelector('textarea') as HTMLTextAreaElement;
    await fireEvent.input(field, { target: { value: 'My unsaved words' } });
    await fireEvent.click(boardBtn(board, /Final/));
    await waitFor(() => expect(openNames()).toEqual(['last-call']));
    const row = document.getElementById('outreach-section-announce') as HTMLElement;
    expect(row.querySelector('.unsaved-note')?.textContent).toContain('not saved yet');
    expect(board.querySelector('tr[data-wave="announce"] .board-row-head')?.textContent).toContain('Not saved yet');
    await fireEvent.click(boardBtn(board, /Announce/));
    await waitFor(() => expect(openNames()).toEqual(['announce']));
    expect(panel('announce').querySelector('textarea')).toBe(field);
    expect(field.value).toBe('My unsaved words');
    expect(row.querySelector('.unsaved-note')?.textContent).toBe('');
  });

  it('a failed reload keeps the page and typed text on screen, and Retry recovers (#0625)', async () => {
    const v = fullView([], { can_reset: true });
    const board = await mount(v);
    const field = panel('announce').querySelector('textarea') as HTMLTextAreaElement;
    await fireEvent.input(field, { target: { value: 'My unsaved words' } });
    resetOutreach.mockResolvedValue(v);
    getWorkshopOutreach.mockRejectedValue(new Error('network'));
    await fireEvent.click(screen.getByRole('button', { name: 'Reset outreach' }));
    const dialog = await screen.findByRole('dialog');
    [...dialog.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Reset outreach')!.click();
    await screen.findByRole('button', { name: 'Retry' });
    expect(field.isConnected).toBe(true);
    expect(field.value).toBe('My unsaved words');
    expect(board.isConnected).toBe(true);
    getWorkshopOutreach.mockResolvedValue(v);
    await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull());
    expect(field.isConnected).toBe(true);
    expect(field.value).toBe('My unsaved words');
  });

  it('a section removed by a reload stops saying it is not saved (#0625)', async () => {
    const v = fullView([], { can_reset: true });
    const board = await mount(v);
    await fireEvent.input(panel('reminder').querySelector('textarea') as HTMLTextAreaElement, { target: { value: 'Typed' } });
    expect(board.querySelector('tr[data-wave="reminder"] .board-row-head')?.textContent).toContain('Not saved yet');
    resetOutreach.mockResolvedValue(v);
    getWorkshopOutreach.mockResolvedValue({ ...v, posts: [] });
    await fireEvent.click(screen.getByRole('button', { name: 'Reset outreach' }));
    const dialog = await screen.findByRole('dialog');
    [...dialog.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Reset outreach')!.click();
    await waitFor(() => expect(panel('reminder').querySelector('textarea')).toBeNull());
    await waitFor(() => expect(board.querySelector('tr[data-wave="reminder"] .board-row-head')?.textContent).not.toContain('Not saved yet'));
    expect(document.querySelector('#outreach-section-reminder .unsaved-note')?.textContent).toBe('');
  });

  it('Reset outreach goes back to the first section (#0625)', async () => {
    const board = await mount(fullView([], { can_reset: true }));
    resetOutreach.mockResolvedValue(fullView([], { can_reset: true }));
    await fireEvent.click(boardBtn(board, /Final/));
    await waitFor(() => expect(openNames()).toEqual(['last-call']));
    await fireEvent.click(screen.getByRole('button', { name: 'Reset outreach' }));
    const dialog = await screen.findByRole('dialog');
    [...dialog.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Reset outreach')!.click();
    await waitFor(() => expect(resetOutreach).toHaveBeenCalledWith(5));
    await waitFor(() => expect(openNames()).toEqual(['announce']));
  });
});
