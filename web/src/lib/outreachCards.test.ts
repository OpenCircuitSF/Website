import { describe, expect, it } from 'vitest';
import { cardButtons, combinedPostResult, copyLabel, flyerLinkFor, groupByWave, showBioLink, pollDelay, postableAutomatic, postDialogCopy, statusLine, summaryLine, visibleNotice } from './outreachCards';
import type { OutreachChannel, OutreachPost, OutreachResults } from './types';

const auto: OutreachChannel = { name: 'bluesky', label: 'Bluesky', utm_source: 'b', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '' };
const manual: OutreachChannel = { ...auto, name: 'reddit', label: 'Reddit', automatic: false, configured: false };
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: null },
];

function post(over: Partial<OutreachPost> = {}): OutreachPost {
  return {
    id: 1, outreach_id: 1, channel: 'bluesky', wave: 'announce', body: 'Hello', status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}

describe('groupByWave', () => {
  it('groups cards by wave in order, one per channel, null when undrafted', () => {
    const g = groupByWave({ channels: [auto, manual], waves, posts: [post()] });
    expect(g.map((x) => x.wave.name)).toEqual(['announce', 'reminder']);
    expect(g[0].cards.map((c) => c.post?.id ?? null)).toEqual([1, null]);
    expect(g[1].cards.every((c) => c.post === null)).toBe(true);
  });
});

describe('statusLine', () => {
  it('covers every variant', () => {
    expect(statusLine(null, auto).kind).toBe('not_drafted');
    const posted = statusLine(post({ status: 'posted', posted_at: '2026-10-17T20:00:00Z', remote_url: 'https://bsky.app/p/1' }), auto);
    expect(posted.kind).toBe('posted');
    expect(posted.text).toMatch(/^Posted /);
    expect(posted.href).toBe('https://bsky.app/p/1');
    const sched = statusLine(post({ status: 'scheduled', scheduled_at: '2026-10-17T20:00:00Z' }), auto);
    expect(sched.text).toMatch(/^Posts automatically /);
    expect(sched.text).toMatch(/Oct/); // the date, not just the weekday (#0574)
    expect(sched.text).toMatch(/17/);
    expect(statusLine(post(), auto).text).toBe('Not scheduled');
    expect(statusLine(post({ status: 'manual' }), manual).text).toBe('Post by hand: copy and paste');
    expect(statusLine(post(), manual).text).toBe('Post by hand: copy and paste');
    expect(statusLine(post({ status: 'failed', last_error: 'secret detail' }), auto).text).toBe('Failed: the post could not be sent');
    expect(statusLine(post({ status: 'queued' }), auto).kind).toBe('queued');
    expect(statusLine(post({ status: 'scheduled', scheduled_at: '2026-10-17T20:00:00Z', held: true }), auto).kind).toBe('on_hold');
  });
});

describe('cardButtons', () => {
  it('automatic draft: edit, post now, schedule', () => {
    const b = cardButtons(post(), auto, true);
    expect([b.edit, b.postNow, b.schedule, b.copy, b.markPosted]).toEqual([true, true, true, false, false]);
  });
  it('scheduled: unschedule', () => {
    const b = cardButtons(post({ status: 'scheduled', scheduled_at: '2026-10-17T20:00:00Z' }), auto, true);
    expect(b.unschedule).toBe(true);
  });
  it('failed: retry', () => {
    const b = cardButtons(post({ status: 'failed' }), auto, true);
    expect(b.retry).toBe(true);
    expect(b.schedule).toBe(false);
  });
  it('manual: copy and mark posted, never post now', () => {
    const b = cardButtons(post({ status: 'manual', channel: 'reddit' }), manual, true);
    expect([b.edit, b.copy, b.markPosted, b.postNow, b.schedule]).toEqual([true, true, true, false, false]);
  });
  it('undrafted: draft only', () => {
    const b = cardButtons(null, auto, true);
    expect(b.draft).toBe(true);
    expect(b.edit).toBe(false);
  });
  it('posted: no actions', () => {
    const b = cardButtons(post({ status: 'posted' }), auto, true);
    expect(Object.values(b).some(Boolean)).toBe(false);
  });
});

describe('summaryLine', () => {
  it('counts drafts, scheduled, posted, clicks', () => {
    const results = { status: 'ok', totals: { clicks: 3, excluded_bot_clicks: 0, unmatched_clicks: 0, flyer_clicks: 0 } } as OutreachResults;
    expect(summaryLine({ posts: [post(), post({ id: 2, status: 'scheduled' }), post({ id: 3, status: 'posted' })] }, results)).toBe(
      'Drafted: announce 3 · 1 scheduled · 1 posted · 3 clicks',
    );
  });
  it('omits clicks without real results', () => {
    expect(summaryLine({ posts: [post()] }, null)).toBe('Drafted: announce 1 · 0 scheduled · 0 posted');
    expect(summaryLine({ posts: [] }, null)).toBe('No posts yet. Start with the announcement.');
  });
  it('reports posts per wave that exist, never a fifteen-post headline', () => {
    const waves = [{ name: 'announce', label: 'Announce', suggested_at: null }, { name: 'reminder', label: 'Reminder', suggested_at: null }, { name: 'last-call', label: 'Last call', suggested_at: null }];
    expect(summaryLine({ waves, posts: [post(), post({ id: 2, channel: 'mastodon' }), post({ id: 3, wave: 'reminder' })] }, null)).toBe('Drafted: Announce 2, Reminder 1 · 0 scheduled · 0 posted');
  });
});

describe('post feedback helpers (#0581)', () => {
  it('pollDelay backs off 1 s, 2 s, 4 s then 10 s', () => {
    expect([0, 1, 2, 3, 9].map(pollDelay)).toEqual([1000, 2000, 4000, 10_000, 10_000]);
  });

  it('postableAutomatic lists automatic cards that can post or retry, in channel order', () => {
    const view = {
      available: true,
      channels: [auto, { ...auto, name: 'mastodon', label: 'Mastodon' }, manual],
      waves: [],
      posts: [
        { ...({} as OutreachPost), id: 1, channel: 'bluesky', wave: 'announce', status: 'draft', prepared: true, held: false, stale: false, scheduled_at: null, last_error: null },
        { ...({} as OutreachPost), id: 2, channel: 'mastodon', wave: 'announce', status: 'failed', prepared: true, held: false, stale: false, scheduled_at: null, last_error: 'x' },
        { ...({} as OutreachPost), id: 3, channel: 'reddit', wave: 'announce', status: 'manual', prepared: true, held: false, stale: false, scheduled_at: null, last_error: null },
      ] as OutreachPost[],
    };
    expect(postableAutomatic(view, 'announce').map((x) => x.post.id)).toEqual([1, 2]);
    expect(postableAutomatic(view, 'reminder')).toEqual([]);
  });

  it('visibleNotice shows only while the post status matches', () => {
    const n = { text: 'Queued.', kind: 'status' as const, status: 'draft' };
    expect(visibleNotice(n, { status: 'draft' })).toBe('Queued.');
    expect(visibleNotice(n, { status: 'posted' })).toBe('');
    expect(visibleNotice(null, { status: 'draft' })).toBe('');
  });

  it('postDialogCopy names the account, or the Settings account when unknown', () => {
    const c = postDialogCopy('post', [{ label: 'Bluesky', account: '@a.test' }, { label: 'Mastodon', account: '' }]);
    expect(c.warning).toContain('cannot be unsent');
    expect(c.accountLine).toEqual(['Posts publicly as @a.test on Bluesky', 'Posts publicly from the Mastodon account set in Settings']);
    expect(c.title).toBe('Post to Bluesky and Mastodon now?');
    expect(postDialogCopy('retry', [{ label: 'Bluesky', account: '' }]).confirmLabel).toBe('Retry');
    const m = postDialogCopy('markPosted', [{ label: 'Instagram', account: '' }]);
    expect(m.warning).toContain('by hand');
    expect(m.accountLine).toEqual([]);
  });

  it('combinedPostResult joins one result per network', () => {
    expect(combinedPostResult([{ label: 'Bluesky', ok: true }, { label: 'Mastodon', ok: false, error: 'rate limited' }])).toBe(
      'Bluesky: queued · Mastodon: could not post (rate limited)',
    );
  });

  it('copyLabel flips to Copied', () => {
    expect(copyLabel('Copy text', true)).toBe('Copied');
    expect(copyLabel('Copy text', false)).toBe('Copy text');
  });
});

describe('Unmark and the bio link (#0588)', () => {
  const posted = post({ status: 'posted', posted_at: '2026-10-17T20:00:00Z' });

  it('only a hand-posted card can be unmarked', () => {
    expect(cardButtons(posted, manual, true).unmark).toBe(true);
    expect(cardButtons(post({ status: 'manual' }), manual, true).unmark).toBe(false);
    expect(cardButtons(posted, auto, true).unmark).toBe(false);
    expect(cardButtons(null, manual, true).unmark).toBe(false);
    // An automatic channel without credentials behaves like a manual one.
    expect(cardButtons(posted, { ...auto, configured: false }, true).unmark).toBe(true);
  });

  it('the unmark dialog says nothing is removed from the network', () => {
    const c = postDialogCopy('unmark', [{ label: 'Instagram', account: '' }]);
    expect(c.title).toBe('Unmark the Instagram post?');
    expect(c.confirmLabel).toBe('Unmark');
    expect(c.warning).toContain('Nothing is removed from Instagram');
  });

  it('shows the bio link row only for a hand-posted post whose body lacks the link', () => {
    const url = 'https://go.example/k';
    expect(showBioLink(post({ body: 'See bio' }), manual)).toBe(true);
    expect(showBioLink(post({ body: `Sign up: ${url}` }), manual)).toBe(false);
    expect(showBioLink(post({ body: 'See bio' }), auto)).toBe(false);
    expect(showBioLink(post({ body: 'See bio', short_url: null }), manual)).toBe(false);
    expect(showBioLink(null, manual)).toBe(false);
  });

  it('the flyer link opens this workshop\'s newest flyer, else makes one for it', () => {
    const flyers = [{ id: 9, workshop_id: 5 }, { id: 4, workshop_id: 5 }, { id: 3, workshop_id: 6 }];
    expect(flyerLinkFor(flyers, 5)).toEqual({ href: '#/outreach?view=flyers&flyer=9', label: 'Open flyer' });
    expect(flyerLinkFor(flyers, 6)).toEqual({ href: '#/outreach?view=flyers&flyer=3', label: 'Open flyer' });
    expect(flyerLinkFor(flyers, 7)).toEqual({ href: '#/outreach?view=flyers&workshop=7', label: 'Make a flyer' });
    expect(flyerLinkFor([], 7).label).toBe('Make a flyer');
  });
});
