// @vitest-environment jsdom
//
// #0546: mounts OutreachResults.svelte under jsdom with '../../lib/api'
// mocked. jsdom is not a browser (CLAUDE.md §1): this proves the structure
// the payload produces (message, table, one polyline per series, a marker),
// not how the chart looks.
import { render, cleanup, waitFor, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachResults, OutreachResultsSummary } from '../../lib/types';

const getWorkshopOutreachResults = vi.fn<(id: number) => Promise<OutreachResults>>();
const listOutreachResults = vi.fn<() => Promise<OutreachResultsSummary>>();
vi.mock('../../lib/api', () => ({
  getWorkshopOutreachResults: (id: number) => getWorkshopOutreachResults(id),
  listOutreachResults: () => listOutreachResults(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachResultsView } = await import('./OutreachResults.svelte');

afterEach(() => {
  cleanup();
  getWorkshopOutreachResults.mockReset();
  listOutreachResults.mockReset();
});

const channels = [
  { name: 'bluesky', label: 'Bluesky', clicks: 12, has_daily_series: true, prepared: 1, reactions: null },
  { name: 'instagram', label: 'Instagram', clicks: 0, has_daily_series: true, prepared: 1, reactions: null },
];

function results(over: Partial<OutreachResults> = {}): OutreachResults {
  return {
    status: 'ok',
    workshop: { id: 5, slug: 'soldering-101', title: 'Soldering 101', status: 'published', starts_at: null },
    campaign_slug: 'soldering-101',
    window: { from: '2026-10-01', to: '2026-10-05' },
    partial_last_day: false,
    fetched_at: '2026-10-04T12:00:00Z',
    totals: { clicks: 12, excluded_bot_clicks: 1, unmatched_clicks: 0, flyer_clicks: 0 },
    channels,
    posts: [
      { id: 1, channel: 'bluesky', wave: 'announce', status: 'posted', short_link_key: 'k1', short_url: 'https://go.example/k1', posted_at: '2026-10-02T15:00:00Z', remote_url: null, clicks: 12, engagement: null },
      { id: 2, channel: 'instagram', wave: 'reminder', status: 'draft', short_link_key: null, short_url: null, posted_at: null, remote_url: null, clicks: null, engagement: null },
    ],
    series: [
      { id: 'bluesky', label: 'Bluesky', channels: ['bluesky'], is_other: false, days: [{ date: '2026-10-02', count: 12 }] },
      { id: 'instagram', label: 'Instagram', channels: ['instagram'], is_other: false, days: [] },
    ],
    flyer_locations: [],
    flyer_all_status: 'ok',
    reactions: null,
    mentions: [],
    ...over,
  };
}

describe('OutreachResults, one workshop', () => {
  it('renders the message and the posts table, and no chart, when ShortLinks is unreachable', async () => {
    getWorkshopOutreachResults.mockResolvedValue(
      results({
        status: 'unreachable',
        totals: null,
        series: [],
        posts: results().posts.map((p) => ({ ...p, clicks: null })),
      }),
    );
    const { container } = render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText(/ShortLinks could not be reached/)).toBeTruthy());
    expect(screen.getAllByText('Bluesky').length).toBeGreaterThan(0);
    expect(screen.getByText('https://go.example/k1')).toBeTruthy();
    expect(container.querySelector('svg')).toBeNull();
    expect(container.querySelector('[role="status"]')?.textContent).toMatch(/could not be reached/);
  });

  it('draws one polyline per series, a post marker and the click count', async () => {
    getWorkshopOutreachResults.mockResolvedValue(results());
    const { container } = render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(container.querySelector('svg')).not.toBeNull());
    expect(container.querySelectorAll('polyline').length).toBe(2);
    expect(container.querySelectorAll('line.marker').length).toBe(1);
    expect(container.querySelector('svg')?.getAttribute('role')).toBe('img');
    // Posts table row: Bluesky post with its clicks.
    const row = screen.getByText('https://go.example/k1').closest('tr')!;
    expect(row.textContent).toContain('Bluesky');
    expect(row.textContent).toContain('12 clicks');
    expect(getWorkshopOutreachResults).toHaveBeenCalledWith(5);
  });

  it('says so when a workshop has no clicks yet, without a chart', async () => {
    getWorkshopOutreachResults.mockResolvedValue(
      results({ totals: { clicks: 0, excluded_bot_clicks: 0, unmatched_clicks: 0, flyer_clicks: 0 }, series: [{ id: 'bluesky', label: 'Bluesky', channels: ['bluesky'], is_other: false, days: [] }] }),
    );
    const { container } = render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText('No clicks in this window yet.')).toBeTruthy());
    expect(container.querySelector('svg')).toBeNull();
  });

  it('shows an inline error, not a thrown one, when our own endpoint fails', async () => {
    getWorkshopOutreachResults.mockRejectedValue(new Error('boom'));
    render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/Could not load results/));
  });
});

describe('OutreachResults, Flyers by location (#0549)', () => {
  const rows = [
    { link_id: 10, location_id: 1, name: 'Mission Library', code: 'mission', short_url: 'https://go.example/a', put_up_at: '2026-10-18T17:00:00Z', clicks: 5, clicks_all_workshops: 9 },
    { link_id: 11, location_id: 2, name: 'Cafe', code: 'cafe', short_url: 'https://go.example/b', put_up_at: null, clicks: 0, clicks_all_workshops: 0 },
  ];

  it('shows the table with scans for this workshop and for all workshops, and "Not marked"', async () => {
    getWorkshopOutreachResults.mockResolvedValue(results({ flyer_locations: rows }));
    render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText('Flyers by location')).toBeTruthy());
    const mission = screen.getByText('Mission Library').closest('tr')!;
    expect(mission.textContent).toContain('5 clicks');
    expect(mission.textContent).toContain('9 clicks');
    expect(mission.textContent).not.toContain('Not marked');
    expect(screen.getByText('Cafe').closest('tr')!.textContent).toContain('Not marked');
  });

  it('shows a dash, not a number, when the all-workshops count is unknown, and says why', async () => {
    getWorkshopOutreachResults.mockResolvedValue(
      results({ flyer_all_status: 'unreachable', flyer_locations: rows.map((r) => ({ ...r, clicks_all_workshops: null })) }),
    );
    render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText('Scans across all workshops are unavailable right now.')).toBeTruthy());
    expect(screen.getByText('Mission Library').closest('tr')!.textContent).toContain('—');
  });

  it('has no flyers table when the workshop has no flyer links', async () => {
    getWorkshopOutreachResults.mockResolvedValue(results());
    render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText('https://go.example/k1')).toBeTruthy());
    expect(screen.queryByText('Flyers by location')).toBeNull();
  });
});

describe('OutreachResults, all workshops', () => {
  it('renders one row per summary row', async () => {
    listOutreachResults.mockResolvedValue({
      status: 'ok',
      rows: [
        { workshop_id: 5, workshop_slug: 'a', workshop_title: 'Soldering 101', starts_at: null, posts_total: 6, posts_posted: 4, clicks: 31, status: 'ok', reactions: { likes: 3, reposts: 1, replies: 2, quotes: 1 } },
        { workshop_id: 6, workshop_slug: 'b', workshop_title: 'Arduino Night', starts_at: null, posts_total: 3, posts_posted: 0, clicks: 0, status: 'ok', reactions: null },
      ],
      mentions: [],
    });
    const { container } = render(OutreachResultsView, { workshopId: null });
    await waitFor(() => expect(screen.getByText('Soldering 101')).toBeTruthy());
    expect(container.querySelectorAll('tbody tr').length).toBe(2);
    expect(screen.getByText('4 of 6 posted')).toBeTruthy();
    expect(screen.getByText('31 clicks')).toBeTruthy();
    expect((screen.getByText('Arduino Night') as HTMLAnchorElement).getAttribute('href')).toBe('#/outreach?view=results&workshop=6');
  });

  it('keeps the rows and shows the message when ShortLinks is down', async () => {
    listOutreachResults.mockResolvedValue({
      status: 'unreachable',
      rows: [{ workshop_id: 5, workshop_slug: 'a', workshop_title: 'Soldering 101', starts_at: null, posts_total: 6, posts_posted: 4, clicks: null, status: 'unreachable', reactions: { likes: 1, reposts: 0, replies: 0, quotes: 0 } }],
      mentions: [],
    });
    render(OutreachResultsView, { workshopId: null });
    await waitFor(() => expect(screen.getByText(/ShortLinks could not be reached/)).toBeTruthy());
    expect(screen.getByText('Soldering 101')).toBeTruthy();
    expect(screen.getByText('—')).toBeTruthy();
  });
});

describe('OutreachResults, engagement and mentions (#0547)', () => {
  const eng = (likes: number) => ({ likes, reposts: 0, replies: 0, quotes: 0, captured_at: '2026-10-16T12:00:00Z' });
  const mention = (over: Record<string, unknown> = {}) => ({
    id: 1, channel: 'bluesky', kind: 'reply' as const, author: '@alice.example.test', text: 'See you there', url: 'https://bsky.app/profile/alice.example.test/post/1',
    created_at: '2026-10-16T10:00:00Z', post_id: 1, wave: 'announce', workshop_id: 5, workshop_title: 'Soldering 101', ...over,
  });

  it('shows the reactions line, a Reactions column and the mentions feed', async () => {
    const base = results();
    getWorkshopOutreachResults.mockResolvedValue(
      results({
        reactions: { likes: 3, reposts: 1, replies: 0, quotes: 0 },
        posts: [{ ...base.posts[0], engagement: eng(3) }, base.posts[1]],
        mentions: [mention()],
      }),
    );
    const { container } = render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText('Mentions and replies')).toBeTruthy());
    expect(container.textContent).toContain('3 likes, 1 repost');
    const row = screen.getByText('https://go.example/k1').closest('tr')!;
    expect(row.textContent).toContain('3 likes');
    expect(screen.getByText('See you there')).toBeTruthy();
    const open = screen.getByText('Open') as HTMLAnchorElement;
    expect(open.getAttribute('href')).toBe('https://bsky.app/profile/alice.example.test/post/1');
    expect(open.getAttribute('rel')).toBe('noopener noreferrer');
    expect(open.getAttribute('target')).toBe('_blank');
  });

  it('shows reactions even when ShortLinks is unreachable', async () => {
    const base = results();
    getWorkshopOutreachResults.mockResolvedValue(
      results({ status: 'unreachable', totals: null, series: [], posts: [{ ...base.posts[0], clicks: null, engagement: eng(2) }, base.posts[1]] }),
    );
    render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText(/ShortLinks could not be reached/)).toBeTruthy());
    expect(screen.getByText('https://go.example/k1').closest('tr')!.textContent).toContain('2 likes');
  });

  it('shows the Mastodon note only when a Mastodon post has engagement', async () => {
    const base = results();
    const masto = { ...base.posts[0], id: 9, channel: 'mastodon', engagement: eng(1) };
    getWorkshopOutreachResults.mockResolvedValue(results({ posts: [base.posts[0], masto] }));
    const { unmount } = render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText(/Mastodon counts are as seen by our instance/)).toBeTruthy());
    unmount();
    getWorkshopOutreachResults.mockResolvedValue(results({ posts: [base.posts[0], { ...masto, engagement: null }] }));
    render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText('Mentions and replies')).toBeTruthy());
    expect(screen.queryByText(/as seen by our instance/)).toBeNull();
  });

  it('renders third-party text as text and gives a javascript: url no link', async () => {
    getWorkshopOutreachResults.mockResolvedValue(
      results({ mentions: [mention({ text: '<img src=x onerror=alert(1)> hi', author: '@<b>evil</b>', url: 'javascript:alert(1)' })] }),
    );
    const { container } = render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText('Mentions and replies')).toBeTruthy());
    expect(container.querySelector('.mentions img')).toBeNull();
    expect(container.querySelector('.mentions b')).toBeNull();
    expect(container.querySelector('.mentions')!.textContent).toContain('<img src=x onerror=alert(1)> hi');
    expect(screen.queryByText('Open')).toBeNull();
    expect(container.querySelector('.mentions a')).toBeNull();
  });

  it('has an empty state for no mentions', async () => {
    getWorkshopOutreachResults.mockResolvedValue(results());
    render(OutreachResultsView, { workshopId: 5 });
    await waitFor(() => expect(screen.getByText('No mentions or replies yet.')).toBeTruthy());
  });

  it('all-workshops view: a Reactions column and a feed linking each tied item to its workshop', async () => {
    listOutreachResults.mockResolvedValue({
      status: 'ok',
      rows: [{ workshop_id: 5, workshop_slug: 'a', workshop_title: 'Soldering 101', starts_at: null, posts_total: 6, posts_posted: 4, clicks: 31, status: 'ok', reactions: { likes: 2, reposts: 0, replies: 0, quotes: 1 } }],
      mentions: [mention({ id: 7, text: 'tied one' }), mention({ id: 8, text: 'untied one', post_id: null, wave: null, workshop_id: null, workshop_title: null, url: null })],
    });
    const { container } = render(OutreachResultsView, { workshopId: null });
    await waitFor(() => expect(screen.getByText('tied one')).toBeTruthy());
    expect(screen.getByText('3 reactions')).toBeTruthy();
    const links = [...container.querySelectorAll('.mentions a')].map((a) => a.getAttribute('href'));
    expect(links).toContain('#/outreach?view=results&workshop=5');
    const untied = screen.getByText('untied one').closest('li')!;
    expect(untied.querySelector('a')).toBeNull();
  });

  it('lists every channel with its exact clicks and reactions, and says "1 click" (#0552)', async () => {
    getWorkshopOutreachResults.mockResolvedValue(
      results({
        totals: { clicks: 13, excluded_bot_clicks: 0, unmatched_clicks: 1, flyer_clicks: 0 },
        channels: [
          { ...channels[0], clicks: 12, reactions: { likes: 3, reposts: 0, replies: 0, quotes: 0 } },
          { ...channels[1], clicks: 1 },
        ],
      }),
    );
    render(OutreachResultsView, { workshopId: 5 });
    const table = (await screen.findByText('Totals by channel')).closest('table')!;
    const rows = Array.from(table.querySelectorAll('tbody tr')).map((r) => r.textContent);
    expect(rows[0]).toContain('12 clicks');
    expect(rows[0]).toContain('3 likes');
    expect(rows[1]).toContain('1 click');
    expect(rows[1]).not.toContain('1 clicks');
    expect(screen.getByText(/1 click on other links/)).toBeTruthy();
  });

  it('discards a response for a workshop that is no longer selected (#0552)', async () => {
    let resolveFirst!: (r: OutreachResults) => void;
    getWorkshopOutreachResults.mockImplementationOnce(() => new Promise((res) => (resolveFirst = res)));
    getWorkshopOutreachResults.mockResolvedValueOnce(
      results({ workshop: { id: 6, slug: 'b', title: 'Second workshop', status: 'published', starts_at: null } }),
    );
    const { rerender } = render(OutreachResultsView, { workshopId: 5 });
    await rerender({ workshopId: 6 });
    await waitFor(() => expect(screen.getByText('Results: Second workshop')).toBeTruthy());
    resolveFirst(results());
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.getByText('Results: Second workshop')).toBeTruthy();
    expect(screen.queryByText('Results: Soldering 101')).toBeNull();
  });
});
