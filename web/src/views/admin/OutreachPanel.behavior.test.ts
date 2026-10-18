// @vitest-environment jsdom
//
// #0544, #0582: mounts OutreachPanel.svelte under jsdom with '../../lib/api'
// mocked. The panel is the loader: it owns loading, the implicit start,
// polling and the results fetch, and hands the page layout to a body snippet,
// so these tests use a stub body. jsdom is not a browser (AGENTS.md §1).
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';

const getWorkshopOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const startOutreach = vi.fn<(id: number) => Promise<WorkshopOutreach>>();
const getWorkshopOutreachResults = vi.fn<(id: number) => Promise<OutreachResults>>();
const prepareOutreachPost = vi.fn<(id: number) => Promise<OutreachPost>>();
const sendOutreachPost = vi.fn<(id: number) => Promise<OutreachPost>>();
const scheduleOutreachPost = vi.fn<(id: number, at: string) => Promise<OutreachPost>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreach: (id: number) => getWorkshopOutreach(id),
  startOutreach: (id: number) => startOutreach(id),
  getWorkshopOutreachResults: (id: number) => getWorkshopOutreachResults(id),
  createOutreachPost: vi.fn(),
  updateOutreachPost: vi.fn(),
  prepareOutreachPost: (id: number) => prepareOutreachPost(id),
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
  { name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' },
];
const waves = [{ name: 'announce', label: 'Announce', suggested_at: null }];

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

function post(over: Partial<OutreachPost> = {}): OutreachPost {
  return {
    id: 4, outreach_id: 9, channel: 'bluesky', wave: 'announce', body: 'Come solder', status: 'draft',
    prepared: true, stale: false, customized: false, held: false, short_link_key: 'k2', short_url: 'https://go.example/k2',
    destination_url: 'https://site/workshops/soldering-101?utm_source=bluesky',
    utm: { source: 'bluesky', medium: 'social', campaign: 'soldering-101', content: 'announce' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '2026-10-01T00:00:00Z', updated_at: null,
    ...over,
  };
}

// The stub page: shows the post statuses and a button that reports a change.
let changeTo: OutreachPost = post({ status: 'queued' });
const body = createRawSnippet<[{ view: WorkshopOutreach; results: OutreachResults | null; onChange: (p: OutreachPost) => void }]>((args) => ({
  render: () => `<div><p>BODY ${args().view.posts.map((p) => p.status).join(',')}</p><button type="button">change</button></div>`,
  setup: (el) => {
    el.querySelector('button')!.onclick = () => args().onChange(changeTo);
  },
}));

function resultsFor(over: Partial<OutreachResults> = {}): OutreachResults {
  return {
    status: 'ok',
    workshop: { id: 5, slug: 'soldering-101', title: 'Soldering 101', status: 'published', starts_at: null },
    campaign_slug: 'soldering-101',
    window: { from: '2026-10-01', to: '2026-10-17' },
    partial_last_day: true,
    fetched_at: '2026-10-16T12:00:00Z',
    totals: { clicks: 12, excluded_bot_clicks: 0, unmatched_clicks: 0, flyer_clicks: 0 },
    channels: [],
    posts: [],
    series: [],
    flyer_locations: [],
    flyer_all_status: 'ok',
    reactions: null,
    mentions: [],
    ...over,
  };
}

describe('OutreachPanel', () => {
  it('explains when ShortLinks is not configured and never starts outreach', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ available: false, unavailable_reason: 'shortlinks_not_configured', outreach: null }));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a', autoStart: true, body });
    await waitFor(() => expect(screen.getByText(/Settings → Outreach connections/)).toBeTruthy());
    expect(screen.getByRole('link', { name: 'Open Settings' }).getAttribute('href')).toBe('#/settings?section=outreach');
    expect(startOutreach).not.toHaveBeenCalled();
  });

  it('links to the workshop editor when the workshop is not published, and does not start (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ available: false, unavailable_reason: 'workshop_not_published', outreach: null }));
    render(OutreachPanel, { workshopId: 5, autoStart: true, body });
    const link = await screen.findByRole('link', { name: 'Open the workshop editor' });
    expect(link.getAttribute('href')).toBe('#/workshops/5');
    expect(startOutreach).not.toHaveBeenCalled();
  });

  it('starts outreach once, by itself, for an available workshop with none (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ outreach: null }));
    let release: (v: WorkshopOutreach) => void = () => {};
    startOutreach.mockImplementation(() => new Promise<WorkshopOutreach>((res) => { release = res; }));
    getWorkshopOutreachResults.mockResolvedValue(resultsFor());
    render(OutreachPanel, { workshopId: 5, autoStart: true, body });
    expect(await screen.findByText('Setting up links…')).toBeTruthy();
    expect(screen.queryByText('Start outreach')).toBeNull();
    release(view());
    expect(await screen.findByText(/BODY/)).toBeTruthy();
    expect(startOutreach).toHaveBeenCalledTimes(1);
    expect(startOutreach).toHaveBeenCalledWith(5);
  });

  it('does not start outreach without autoStart (GET and plain mounts stay read-only)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ outreach: null }));
    render(OutreachPanel, { workshopId: 5, body });
    expect(await screen.findByText('Outreach has not started.')).toBeTruthy();
    expect(startOutreach).not.toHaveBeenCalled();
  });

  it('shows a warning with Try again when the start fails, and does not loop (#0582)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ outreach: null }));
    startOutreach.mockRejectedValueOnce(new Error('boom'));
    render(OutreachPanel, { workshopId: 5, autoStart: true, body });
    expect(await screen.findByText('Could not set up the links. Please try again.')).toBeTruthy();
    await new Promise((r) => setTimeout(r, 30));
    expect(startOutreach).toHaveBeenCalledTimes(1);
    startOutreach.mockResolvedValue(view());
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText(/BODY/)).toBeTruthy();
    expect(startOutreach).toHaveBeenCalledTimes(2);
  });

  it('hands the link report to onLoaded after Start (#0568)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ outreach: null }));
    const report = { created: 14, existing: 0, stopped: false, failed: [{ kind: 'post' as const, label: 'Mastodon · Reminder', reason: 'failed' }] };
    startOutreach.mockResolvedValue(view({ link_report: report }));
    const onLoaded = vi.fn();
    render(OutreachPanel, { workshopId: 5, autoStart: true, onLoaded, body });
    await waitFor(() => expect(onLoaded.mock.calls.some((c) => c[0].link_report?.failed.length === 1)).toBe(true));
  });

  it('still renders the page, with a status line, when the results call fails', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [post()] }));
    getWorkshopOutreachResults.mockRejectedValue(new Error('network'));
    render(OutreachPanel, { workshopId: 5, body });
    expect(await screen.findByText('BODY draft')).toBeTruthy();
    await waitFor(() => expect(screen.getByText('Click counts could not be loaded.')).toBeTruthy());
  });

  it('explains an unreachable ShortLinks in one line', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [post()] }));
    getWorkshopOutreachResults.mockResolvedValue(resultsFor({ status: 'unreachable', totals: null }));
    render(OutreachPanel, { workshopId: 5, body });
    await waitFor(() => expect(screen.getByText(/ShortLinks could not be reached/)).toBeTruthy());
  });

  it('refetches results when a change alters a post status (#0552)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [post()] }));
    getWorkshopOutreachResults.mockResolvedValue(resultsFor());
    changeTo = post({ status: 'scheduled' });
    render(OutreachPanel, { workshopId: 5, body });
    await waitFor(() => expect(getWorkshopOutreachResults).toHaveBeenCalledTimes(1));
    await fireEvent.click(await screen.findByRole('button', { name: 'change' }));
    await waitFor(() => expect(getWorkshopOutreachResults).toHaveBeenCalledTimes(2));
  });

  it('discards a stale results response that arrives after a newer one (#0554)', async () => {
    getWorkshopOutreach.mockResolvedValue(view({ posts: [post()] }));
    let releaseStale: (r: OutreachResults) => void = () => {};
    getWorkshopOutreachResults
      .mockImplementationOnce(() => new Promise<OutreachResults>((res) => { releaseStale = res; }))
      .mockResolvedValue(resultsFor({ status: 'unreachable', totals: null }));
    changeTo = post({ status: 'scheduled' });
    render(OutreachPanel, { workshopId: 5, body });
    await waitFor(() => expect(getWorkshopOutreachResults).toHaveBeenCalledTimes(1));
    await fireEvent.click(await screen.findByRole('button', { name: 'change' }));
    expect(await screen.findByText(/ShortLinks could not be reached/)).toBeTruthy();
    releaseStale(resultsFor({ status: 'ok' }));
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.getByText(/ShortLinks could not be reached/)).toBeTruthy();
  });

  it('backs off 1 s, 2 s, 4 s then 10 s while a post is queued and stops on unmount (#0581)', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    getWorkshopOutreach.mockResolvedValue(view({ posts: [post({ status: 'queued' })] }));
    const { unmount } = render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a', body });
    await vi.advanceTimersByTimeAsync(0);
    const calls = () => getWorkshopOutreach.mock.calls.length;
    const before = calls();
    expect(before).toBeGreaterThan(0);
    await vi.advanceTimersByTimeAsync(999);
    expect(calls()).toBe(before);
    await vi.advanceTimersByTimeAsync(1);
    expect(calls()).toBe(before + 1);
    await vi.advanceTimersByTimeAsync(2000);
    expect(calls()).toBe(before + 2);
    await vi.advanceTimersByTimeAsync(4000);
    expect(calls()).toBe(before + 3);
    await vi.advanceTimersByTimeAsync(9999);
    expect(calls()).toBe(before + 3);
    await vi.advanceTimersByTimeAsync(1);
    expect(calls()).toBe(before + 4);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(calls()).toBe(before + 5);
    unmount();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(calls()).toBe(before + 5);
  });

  it('does not poll when nothing is queued', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    getWorkshopOutreach.mockResolvedValue(view({ posts: [post()] }));
    render(OutreachPanel, { workshopId: 5, workshopUpdatedAt: 'a', body });
    await vi.advanceTimersByTimeAsync(0);
    expect(screen.getByText('BODY draft')).toBeTruthy();
    const before = getWorkshopOutreach.mock.calls.length;
    await vi.advanceTimersByTimeAsync(60_000);
    expect(getWorkshopOutreach.mock.calls.length).toBe(before);
  });

  it('calls onLoaded with the loaded view and refetches on reloadKey (#0568)', async () => {
    getWorkshopOutreach.mockResolvedValue(view());
    const onLoaded = vi.fn();
    const { rerender } = render(OutreachPanel, { workshopId: 5, reloadKey: 0, onLoaded, body });
    await waitFor(() => expect(onLoaded).toHaveBeenCalledTimes(1));
    expect(onLoaded.mock.calls[0][0].workshop.id).toBe(5);
    await rerender({ workshopId: 5, reloadKey: 1, onLoaded, body });
    await waitFor(() => expect(getWorkshopOutreach).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(onLoaded).toHaveBeenCalledTimes(2));
  });
});
