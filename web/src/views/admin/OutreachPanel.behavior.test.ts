// @vitest-environment jsdom
//
// #0544: mounts OutreachPanel.svelte under jsdom with '../../lib/api' mocked,
// following Pending.behavior.test.ts. jsdom is not a browser (CLAUDE.md §1):
// this proves the panel renders what the payload says and wires the right
// calls, not layout or clipboard permissions. navigator.clipboard does not
// exist under jsdom, so it is stubbed.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const startOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const getWorkshopOutreachResults = vi.fn<(id: number) => Promise<OutreachResults>>();
const sendOutreachPost = vi.fn<(id: number) => Promise<OutreachPost>>();
const scheduleOutreachPost = vi.fn<(id: number, at: string) => Promise<OutreachPost>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  startOutreach: (id: number) => startOutreach(id),
  getWorkshopOutreachResults: (id: number) => getWorkshopOutreachResults(id),
  createOutreachPost: vi.fn(),
  updateOutreachPost: vi.fn(),
  prepareOutreachPost: vi.fn(),
  markOutreachPosted: vi.fn(),
  deleteOutreachPost: vi.fn(),
  sendOutreachPost: (id: number) => sendOutreachPost(id),
  scheduleOutreachPost: (id: number, at: string) => scheduleOutreachPost(id, at),
  unscheduleOutreachPost: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachPanel } = await import('./OutreachPanel.svelte');

afterEach(() => {
  cleanup();
  getWorkshopOutreach.mockReset();
  startOutreach.mockReset();
  getWorkshopOutreachResults.mockReset();
  sendOutreachPost.mockReset();
  scheduleOutreachPost.mockReset();
  vi.useRealTimers();
});

const channels = [
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true },
  { name: 'mastodon', label: 'Mastodon', utm_source: 'mastodon', utm_medium: 'social', char_limit: 500, automatic: true, configured: false },
  { name: 'instagram', label: 'Instagram', utm_source: 'instagram', utm_medium: 'social', char_limit: 0, automatic: false, configured: false },
  { name: 'tiktok', label: 'TikTok', utm_source: 'tiktok', utm_medium: 'video', char_limit: 0, automatic: false, configured: false },
  { name: 'youtube', label: 'YouTube', utm_source: 'youtube', utm_medium: 'video', char_limit: 0, automatic: false, configured: false },
];
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: '2026-11-28T18:00:00Z' },
  { name: 'last-call', label: 'Last call', suggested_at: '2026-12-04T18:00:00Z' },
];

function view(over: Partial<WorkshopOutreach> = {}): WorkshopOutreach {
  return {
    available: true,
    unavailable_reason: '',
    workshop: { id: 5, slug: 'soldering-101', title: 'Soldering 101', status: 'published', starts_at: '2026-12-05T18:00:00Z' },
    outreach: { id: 9, workshop_id: 5, campaign_slug: 'soldering-101', created_at: '2026-10-01T00:00:00Z' },
    channels,
    waves,
    posts: [],
    ...over,
  };
}

function manualPost(): OutreachPost {
  return {
    id: 3, outreach_id: 9, channel: 'instagram', wave: 'announce', body: 'Hello https://go.example/k1', status: 'manual',
    prepared: true, stale: false, held: false, short_link_key: 'k1', short_url: 'https://go.example/k1',
    destination_url: 'https://site/workshops/soldering-101?utm_source=instagram',
    utm: { source: 'instagram', medium: 'social', campaign: 'soldering-101', content: 'announce' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '2026-10-01T00:00:00Z', updated_at: null,
  };
}

describe('OutreachPanel', () => {
  it('explains when ShortLinks is not configured', async () => {
    getWorkshopOutreach.mockResolvedValue(
      view({ available: false, unavailable_reason: 'shortlinks_not_configured', outreach: null }),
    );
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await waitFor(() => expect(screen.getByText(/SHORTLINKS_BASE_URL/)).toBeTruthy());
    expect(screen.queryByText('Start outreach')).toBeNull();
  });

  it('offers Start outreach, then renders a 5 x 3 grid of cells', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ outreach: null }));
    startOutreach.mockResolvedValue(view());
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    const start = await screen.findByText('Start outreach');
    await fireEvent.click(start);
    await waitFor(() => expect(screen.getAllByText('Not drafted').length).toBe(15));
    expect(startOutreach).toHaveBeenCalledWith(5);
  });

  it('shows Copy content and Mark posted for a prepared manual post', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [manualPost()] }));
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await fireEvent.click(await screen.findByText('Ready to post'));
    const copy = await screen.findByText('Copy content');
    expect(screen.getByText('Mark posted')).toBeTruthy();
    expect(screen.getByText('https://go.example/k1', { selector: 'p' })).toBeTruthy();
    await fireEvent.click(copy);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('Hello https://go.example/k1'));
  });

  // #0546: click counts come from a separate call made after the grid renders.
  function resultsFor(over: Partial<OutreachResults> = {}): OutreachResults {
    return {
      status: 'ok',
      workshop: { id: 5, slug: 'soldering-101', title: 'Soldering 101', status: 'published', starts_at: null },
      campaign_slug: 'soldering-101',
      window: { from: '2026-10-01', to: '2026-10-17' },
      partial_last_day: true,
      fetched_at: '2026-10-16T12:00:00Z',
      totals: { clicks: 12, excluded_bot_clicks: 0, unmatched_clicks: 0 },
      channels: [],
      posts: [
        { id: 3, channel: 'instagram', wave: 'announce', status: 'manual', short_link_key: 'k1', short_url: 'https://go.example/k1', posted_at: null, remote_url: null, clicks: 12 },
      ],
      series: [],
      ...over,
    };
  }

  it('shows clicks in a cell and a View results link when results are ok', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [manualPost()] }));
    getWorkshopOutreachResults.mockResolvedValue(resultsFor());
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    expect(await screen.findByText('12 clicks')).toBeTruthy();
    expect(getWorkshopOutreachResults).toHaveBeenCalledWith(5);
    const link = screen.getByText('View results') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('#/outreach?view=results&workshop=5');
  });

  it('still renders the grid, with a status line, when the results call fails', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [manualPost()] }));
    getWorkshopOutreachResults.mockRejectedValue(new Error('network'));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    expect(await screen.findByText('Ready to post')).toBeTruthy();
    await waitFor(() => expect(screen.getByText('Click counts could not be loaded.')).toBeTruthy());
    expect(screen.queryByText(/clicks?$/)).toBeNull();
  });

  it('explains an unreachable ShortLinks in one line and shows no counts', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [manualPost()] }));
    getWorkshopOutreachResults.mockResolvedValue(resultsFor({ status: 'unreachable', totals: null }));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await waitFor(() => expect(screen.getByText(/ShortLinks could not be reached/)).toBeTruthy());
    expect(screen.queryByText('12 clicks')).toBeNull();
  });

  // #0545: automatic posting. jsdom proves the right controls render for the
  // payload and call the right API functions; it does not prove a post
  // reaches Bluesky or Mastodon (no credentials exist; the server side runs
  // against httptest fakes).
  function autoPost(over: Partial<OutreachPost> = {}): OutreachPost {
    return {
      ...manualPost(),
      id: 4,
      channel: 'bluesky',
      wave: 'reminder',
      status: 'draft',
      body: 'Come solder https://go.example/k2',
      short_url: 'https://go.example/k2',
      short_link_key: 'k2',
      ...over,
    };
  }
  const futureWaves = [
    { name: 'announce', label: 'Announce', suggested_at: null },
    { name: 'reminder', label: 'Reminder', suggested_at: '2099-11-28T18:00:00Z' },
    { name: 'last-call', label: 'Last call', suggested_at: '2099-12-04T18:00:00Z' },
  ];

  it('shows Post now and Schedule for a prepared bluesky draft, with the suggested time prefilled', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ waves: futureWaves, posts: [autoPost()] }));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await fireEvent.click(await screen.findByText('Link ready'));
    expect(await screen.findByText('Post now')).toBeTruthy();
    expect(screen.getByText('Schedule')).toBeTruthy();
    const picker = screen.getByLabelText('Schedule for') as HTMLInputElement;
    expect(picker.value).toMatch(/^2099-11-2[78]T\d{2}:\d{2}$/);
    expect(screen.queryByText('Retry')).toBeNull();
  });

  it('Post now calls the send endpoint and the cell reads Posting…', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ waves: futureWaves, posts: [autoPost()] }));
    sendOutreachPost.mockResolvedValue(autoPost({ status: 'queued' }));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await fireEvent.click(await screen.findByText('Link ready'));
    await fireEvent.click(await screen.findByText('Post now'));
    await waitFor(() => expect(sendOutreachPost).toHaveBeenCalledWith(4));
    expect(await screen.findAllByText('Posting…')).toBeTruthy();
    expect(screen.queryByText('Post now')).toBeNull();
    expect(screen.queryByText('Delete')).toBeNull();
  });

  it('Schedule sends an RFC 3339 instant', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ waves: futureWaves, posts: [autoPost()] }));
    scheduleOutreachPost.mockResolvedValue(autoPost({ status: 'scheduled', scheduled_at: '2099-11-28T18:00:00Z' }));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await fireEvent.click(await screen.findByText('Link ready'));
    await fireEvent.click(await screen.findByText('Schedule'));
    await waitFor(() => expect(scheduleOutreachPost).toHaveBeenCalledTimes(1));
    expect(scheduleOutreachPost.mock.calls[0][0]).toBe(4);
    expect(scheduleOutreachPost.mock.calls[0][1]).toMatch(/^2099-11-2[78]T\d{2}:\d{2}:00Z$/);
    expect(await screen.findByText('Unschedule')).toBeTruthy();
  });

  it('a failed post shows Retry and its error in a status region', async () => {
    getWorkshopOutreach.mockResolvedValue(
      view({ waves: futureWaves, posts: [autoPost({ status: 'failed', last_error: 'Bluesky rejected the post: InvalidRequest' })] }),
    );
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await fireEvent.click(await screen.findByText('Failed'));
    expect(await screen.findByText('Retry', { selector: 'button' })).toBeTruthy();
    const err = screen.getByText('Bluesky rejected the post: InvalidRequest');
    expect(err.getAttribute('role')).toBe('status');
  });

  it('a not-configured automatic channel behaves like a manual one', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [autoPost({ channel: 'mastodon' })] }));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await fireEvent.click(await screen.findByText('Link ready'));
    expect(await screen.findByText('Copy content')).toBeTruthy();
    expect(screen.getByText('Mark posted')).toBeTruthy();
    expect(screen.getByText('Not configured — post by hand')).toBeTruthy();
    expect(screen.queryByText('Post now')).toBeNull();
    expect(screen.queryByText('Schedule')).toBeNull();
  });

  it('a held scheduled post says so', async () => {
    getWorkshopOutreach.mockResolvedValue(
      view({ posts: [autoPost({ status: 'scheduled', scheduled_at: '2099-11-28T18:00:00Z', held: true })] }),
    );
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await fireEvent.click(await screen.findByText('Held'));
    expect(await screen.findByText(/workshop is not published/)).toBeTruthy();
  });

  it('refreshes every 10 s while a post is queued and stops on unmount', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    getWorkshopOutreach.mockResolvedValue(view({ posts: [autoPost({ status: 'queued' })] }));
    const { unmount } = render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await waitFor(() => expect(screen.getAllByText('Posting…').length).toBeGreaterThan(0));
    const before = getWorkshopOutreach.mock.calls.length;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(getWorkshopOutreach.mock.calls.length).toBe(before + 1);
    unmount();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(getWorkshopOutreach.mock.calls.length).toBe(before + 1);
  });

  it('does not poll when nothing is queued', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    getWorkshopOutreach.mockResolvedValue(view({ posts: [autoPost()] }));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a' });
    await screen.findByText('Link ready');
    const before = getWorkshopOutreach.mock.calls.length;
    await vi.advanceTimersByTimeAsync(60_000);
    expect(getWorkshopOutreach.mock.calls.length).toBe(before);
  });
});
