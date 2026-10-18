import { describe, expect, it } from 'vitest';
import {
  CHART_GEO,
  buildChart,
  cellClicksLabel,
  channelColor,
  chartDescription,
  dayLabel,
  fillSeries,
  foldedChannelsNote,
  formatClicks,
  formatReactionTotal,
  cellReactionsLabel,
  mastodonCountsNote,
  mentionHref,
  mentionKindLabel,
  reactionsLabel,
  reactionTotal,
  resultsStatusMessage,
  resultsSummaryLine,
  seriesColor,
  utcDays,
  waveLabel,
  yAxisTicks,
} from './outreachResults';
import type { OutreachResults, OutreachResultsStatus } from './types';

const LABELS: Record<string, string> = { bluesky: 'Bluesky', mastodon: 'Mastodon', instagram: 'Instagram', tiktok: 'TikTok', youtube: 'YouTube' };
const channels = ['bluesky', 'mastodon', 'instagram', 'tiktok', 'youtube'].map((name) => ({
  name,
  label: LABELS[name],
  clicks: 0,
  has_daily_series: true,
  prepared: 1,
  reactions: null,
}));

function results(over: Partial<OutreachResults> = {}): OutreachResults {
  return {
    status: 'ok',
    workshop: { id: 5, slug: 's', title: 'Soldering', status: 'published', starts_at: null },
    campaign_slug: 's',
    window: { from: '2026-10-01', to: '2026-10-04' },
    partial_last_day: false,
    fetched_at: '2026-10-03T12:00:00Z',
    totals: { clicks: 9, excluded_bot_clicks: 0, unmatched_clicks: 0, flyer_clicks: 0 },
    channels,
    posts: [],
    series: [],
    flyer_locations: [],
    flyer_all_status: 'ok',
    reactions: null,
    mentions: [],
    ...over,
  };
}

describe('utcDays', () => {
  it('walks across a month end', () => {
    expect(utcDays('2026-10-30', '2026-11-02')).toEqual(['2026-10-30', '2026-10-31', '2026-11-01']);
  });
  it('returns a single day and an empty window', () => {
    expect(utcDays('2026-10-05', '2026-10-06')).toEqual(['2026-10-05']);
    expect(utcDays('2026-10-05', '2026-10-05')).toEqual([]);
    expect(utcDays('nope', '2026-10-05')).toEqual([]);
  });
});

describe('fillSeries', () => {
  it('puts zero in the gaps', () => {
    expect(fillSeries([{ date: '2026-10-02', count: 4 }], ['2026-10-01', '2026-10-02', '2026-10-03'])).toEqual([0, 4, 0]);
  });
});

describe('colors', () => {
  const names = channels.map((c) => c.name);
  it('go by channel order, whatever the series order', () => {
    expect(channelColor('bluesky', names)).toBe('var(--series-1)');
    expect(channelColor('youtube', names)).toBe('var(--series-5)');
    expect(seriesColor({ id: 'mastodon', is_other: false }, names)).toBe('var(--series-2)');
  });
  it('use the neutral token for Other links and unknown channels', () => {
    expect(seriesColor({ id: 'other', is_other: true }, names)).toBe('var(--series-other)');
    expect(channelColor('myspace', names)).toBe('var(--series-other)');
  });
});

describe('yAxisTicks', () => {
  it('is integer, starts at 0 and rounds the max up', () => {
    expect(yAxisTicks(0)).toEqual([0, 1]);
    expect(yAxisTicks(1)).toEqual([0, 1]);
    expect(yAxisTicks(7)).toEqual([0, 2, 4, 6, 8]);
    expect(yAxisTicks(40)).toEqual([0, 10, 20, 30, 40]);
    expect(yAxisTicks(41)).toEqual([0, 20, 40, 60]);
    for (const t of yAxisTicks(137)) expect(Number.isInteger(t)).toBe(true);
  });
});

describe('dayLabel and waveLabel', () => {
  it('formats', () => {
    expect(dayLabel('2026-10-03')).toBe('Oct 3');
    expect(dayLabel('x')).toBe('x');
    expect(waveLabel('last-call')).toBe('Last call');
    expect(waveLabel('announce')).toBe('Announce');
  });
});

describe('buildChart', () => {
  const g = CHART_GEO; // innerW 588, innerH 172, three days -> x 40, 334, 628
  it('draws the exact polylines for two series', () => {
    const m = buildChart(
      results({
        series: [
          { id: 'bluesky', label: 'Bluesky', channels: ['bluesky'], is_other: false, days: [{ date: '2026-10-02', count: 4 }] },
          { id: 'other', label: 'Other links', channels: [], is_other: true, days: [{ date: '2026-10-03', count: 2 }] },
        ],
      }),
    )!;
    expect(m.max).toBe(4);
    expect(m.empty).toBe(false);
    const base = g.padTop + 172; // y of 0
    expect(m.lines[0]).toEqual({
      id: 'bluesky',
      label: 'Bluesky',
      color: 'var(--series-1)',
      points: `40,${base} 334,${g.padTop} 628,${base}`,
      total: 4,
    });
    expect(m.lines[1].color).toBe('var(--series-other)');
    expect(m.lines[1].points).toBe(`40,${base} 334,${base} 628,${g.padTop + 86}`);
    expect(m.lines[1].total).toBe(2);
    expect(m.xLabels.map((l) => l.label)).toEqual(['Oct 1', 'Oct 2', 'Oct 3']);
    expect(m.yTicks.map((t) => t.value)).toEqual([0, 1, 2, 3, 4]);
    expect(m.rows).toEqual([
      { date: '2026-10-02', label: 'Oct 2', values: [4, 0] },
      { date: '2026-10-03', label: 'Oct 3', values: [0, 2] },
    ]);
  });
  it('centres a single day', () => {
    const m = buildChart(results({ window: { from: '2026-10-05', to: '2026-10-06' } }))!;
    expect(m.dates).toEqual(['2026-10-05']);
    expect(m.xLabels).toEqual([{ x: 40 + 588 / 2, label: 'Oct 5' }]);
  });
  it('flags an all-zero window as empty with a usable axis', () => {
    const m = buildChart(
      results({ series: [{ id: 'bluesky', label: 'Bluesky', channels: ['bluesky'], is_other: false, days: [] }] }),
    )!;
    expect(m.empty).toBe(true);
    expect(m.max).toBe(1);
    expect(m.rows).toEqual([]);
  });
  it('is null unless the status is ok and there is a window', () => {
    expect(buildChart(results({ status: 'unreachable' }))).toBeNull();
    expect(buildChart(results({ window: null }))).toBeNull();
  });
  it('always labels the last day and keeps about six labels on a long window', () => {
    const m = buildChart(results({ window: { from: '2026-09-01', to: '2026-10-01' } }))!;
    expect(m.xLabels.length).toBeLessThanOrEqual(7);
    expect(m.xLabels[m.xLabels.length - 1].label).toBe('Sep 30');
    expect(m.xLabels[0].label).toBe('Sep 1');
  });
  it('groups markers by UTC day and drops posts outside the window', () => {
    const post = (id: number, channel: string, wave: string, posted_at: string | null) => ({
      id, channel, wave, status: 'posted' as const, short_link_key: `k${id}`, short_url: null, posted_at, remote_url: null, clicks: 0, engagement: null,
    });
    const m = buildChart(
      results({
        posts: [
          post(1, 'bluesky', 'announce', '2026-10-02T23:59:00Z'),
          post(2, 'bluesky', 'reminder', '2026-10-02T01:00:00Z'),
          post(3, 'instagram', 'announce', '2026-10-02T12:00:00Z'),
          post(4, 'tiktok', 'announce', '2026-10-03T08:00:00Z'),
          post(5, 'youtube', 'announce', '2026-09-20T08:00:00Z'),
          post(6, 'youtube', 'last-call', '2026-10-04T00:00:00Z'), // window end is exclusive
          post(7, 'mastodon', 'announce', null),
        ],
      }),
    )!;
    expect(m.markers.map((x) => x.date)).toEqual(['2026-10-02', '2026-10-03']);
    expect(m.markers[0].labels).toEqual(['Bluesky · Announce', 'Bluesky · Reminder', 'Instagram · Announce']);
    expect(m.markers[0].color).toBe('var(--text-muted)');
    expect(m.markers[0].x).toBe(334);
    expect(m.markers[1].color).toBe('var(--series-4)');
  });
  it('passes partialLastDay through', () => {
    expect(buildChart(results({ partial_last_day: true }))!.partialLastDay).toBe(true);
    expect(buildChart(results())!.partialLastDay).toBe(false);
  });
});

describe('chartDescription', () => {
  it('names the total and the inclusive UTC window', () => {
    expect(chartDescription(results())).toBe('9 clicks from Oct 1 to Oct 3 (UTC)');
    expect(chartDescription(results({ partial_last_day: true }))).toBe('9 clicks from Oct 1 to Oct 3 (UTC), today so far');
    expect(chartDescription(results({ status: 'unreachable', totals: null }))).toBe('');
  });
});

describe('resultsStatusMessage', () => {
  it('is null for ok and a sentence for every other status', () => {
    expect(resultsStatusMessage('ok')).toBeNull();
    const others: OutreachResultsStatus[] = ['not_configured', 'unreachable', 'rejected', 'campaign_missing', 'not_started'];
    for (const s of others) expect(resultsStatusMessage(s)).toMatch(/\S/);
    expect(resultsStatusMessage('unreachable')).toBe(
      'ShortLinks could not be reached, so click counts are unavailable right now. Posts are listed below.',
    );
    expect(resultsStatusMessage('rejected')).toContain('Settings → Outreach connections');
    expect(resultsStatusMessage('rejected')).not.toContain('SHORTLINKS_');
  });
});

describe('formatClicks', () => {
  it('handles null, one and many', () => {
    expect(formatClicks(null)).toBe('—');
    expect(formatClicks(0)).toBe('0 clicks');
    expect(formatClicks(1)).toBe('1 click');
    expect(formatClicks(12)).toBe('12 clicks');
  });
});

describe('cellClicksLabel', () => {
  const post = { id: 1, channel: 'bluesky', wave: 'announce', status: 'posted' as const, short_link_key: 'k', short_url: null, posted_at: null, remote_url: null, clicks: 12, engagement: null };
  it('shows clicks only for a prepared post with real results', () => {
    expect(cellClicksLabel({ id: 1, prepared: true }, results({ posts: [post] }))).toBe('12 clicks');
    expect(cellClicksLabel({ id: 1, prepared: true }, null)).toBe('');
    expect(cellClicksLabel({ id: 1, prepared: false }, results({ posts: [post] }))).toBe('');
    expect(cellClicksLabel({ id: 1, prepared: true }, results({ status: 'unreachable', posts: [post] }))).toBe('');
    expect(cellClicksLabel({ id: 2, prepared: true }, results({ posts: [post] }))).toBe('');
    expect(cellClicksLabel({ id: 1, prepared: true }, results({ posts: [{ ...post, clicks: null }] }))).toBe('');
  });
});

describe('foldedChannelsNote', () => {
  const other = (chs: string[]) => ({ id: 'other', label: 'Other links', channels: chs, is_other: true, days: [] });
  it('is null when nothing is folded', () => {
    expect(foldedChannelsNote(results())).toBeNull();
    expect(foldedChannelsNote(results({ series: [other([])] }))).toBeNull();
  });
  it('names the folded channels', () => {
    expect(foldedChannelsNote(results({ series: [other(['mastodon', 'youtube'])] }))).toBe(
      'Mastodon and YouTube are included in Other links: ShortLinks charts the 6 most-clicked links separately.',
    );
    expect(foldedChannelsNote(results({ series: [other(['tiktok'])] }))).toMatch(/^TikTok is included/);
    expect(foldedChannelsNote(results({ series: [other(['bluesky', 'mastodon', 'tiktok'])] }))).toMatch(/^Bluesky, Mastodon and TikTok are/);
  });
});

describe('reactions (#0547)', () => {
  const z = { likes: 0, reposts: 0, replies: 0, quotes: 0 };
  it('totals and labels', () => {
    expect(reactionTotal({ likes: 3, reposts: 1, replies: 2, quotes: 1 })).toBe(7);
    expect(reactionsLabel(null)).toBe('');
    expect(reactionsLabel(z)).toBe('No reactions yet');
    expect(reactionsLabel({ likes: 3, reposts: 1, replies: 2, quotes: 1 })).toBe('3 likes, 1 repost, 2 replies, 1 quote');
    expect(reactionsLabel({ ...z, likes: 1 })).toBe('1 like');
    expect(reactionsLabel({ ...z, replies: 1, quotes: 4 })).toBe('1 reply, 4 quotes');
  });
  it('formats a total', () => {
    expect(formatReactionTotal(null)).toBe('—');
    expect(formatReactionTotal(z)).toBe('0 reactions');
    expect(formatReactionTotal({ ...z, likes: 1 })).toBe('1 reaction');
    expect(formatReactionTotal({ ...z, likes: 2, quotes: 1 })).toBe('3 reactions');
  });
  const eng = { likes: 3, reposts: 0, replies: 0, quotes: 0, captured_at: '2026-10-16T12:00:00Z' };
  const base = { short_link_key: 'k', short_url: null, posted_at: null, remote_url: null, clicks: null, status: 'posted' as const, wave: 'announce' };
  it('cell label ignores the results status', () => {
    const posts = [{ ...base, id: 1, channel: 'bluesky', engagement: eng }, { ...base, id: 2, channel: 'bluesky', engagement: null }];
    for (const status of ['ok', 'not_configured', 'unreachable', 'rejected'] as const) {
      expect(cellReactionsLabel({ id: 1 }, results({ status, posts }))).toBe('3 likes');
    }
    expect(cellReactionsLabel({ id: 2 }, results({ posts }))).toBe('');
    expect(cellReactionsLabel({ id: 9 }, results({ posts }))).toBe('');
    expect(cellReactionsLabel({ id: 1 }, null)).toBe('');
  });
  it('shows the Mastodon note only when a Mastodon post has engagement', () => {
    const m = { ...base, id: 1, channel: 'mastodon', engagement: eng };
    expect(mastodonCountsNote({ posts: [m] })).toMatch(/as seen by our instance/);
    expect(mastodonCountsNote({ posts: [{ ...m, engagement: null }] })).toBeNull();
    expect(mastodonCountsNote({ posts: [{ ...m, channel: 'bluesky' }] })).toBeNull();
  });
});

describe('mentions (#0547)', () => {
  it('labels kinds', () => {
    expect([mentionKindLabel('mention'), mentionKindLabel('reply'), mentionKindLabel('quote')]).toEqual(['Mention', 'Reply', 'Quote']);
  });
  it('links https only', () => {
    expect(mentionHref({ url: 'https://bsky.app/profile/alice.example.test/post/1' })).toBe('https://bsky.app/profile/alice.example.test/post/1');
    for (const url of [null, '', 'javascript:alert(1)', 'http://example.test/x', 'mailto:a@example.test', '//example.test/x', 'data:text/html,x', 'HTTPS://example.test/\u0000']) {
      expect(mentionHref({ url })).toBeNull();
    }
  });
});

describe('resultsSummaryLine (#0568)', () => {
  it('reads the totals when the results are ok', () => {
    expect(resultsSummaryLine(results({ totals: { clicks: 42, excluded_bot_clicks: 0, unmatched_clicks: 0, flyer_clicks: 5 } }))).toBe(
      '42 clicks since outreach started (5 from flyers).',
    );
    expect(resultsSummaryLine(results({ totals: { clicks: 1, excluded_bot_clicks: 0, unmatched_clicks: 0, flyer_clicks: 0 } }))).toBe(
      '1 click since outreach started.',
    );
  });
  it('is empty unless the numbers are real', () => {
    expect(resultsSummaryLine(null)).toBe('');
    expect(resultsSummaryLine(results({ status: 'unreachable' }))).toBe('');
    expect(resultsSummaryLine(results({ totals: null }))).toBe('');
  });
});
