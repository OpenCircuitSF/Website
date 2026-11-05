import { describe, expect, it } from 'vitest';
import { boardCell, boardRows, cardButtons, combinedPostResult, copyLabel, flyerLinkFor, groupByWave, notScheduledNote, showBioLink, pollDelay, postableAutomatic, postDialogCopy, statusLine, weekdayTime, summaryLine, visibleNotice } from './outreachCards';
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
  it('posted: no actions except Delete and redo (#0604)', () => {
    const { takedown, ...rest } = cardButtons(post({ status: 'posted' }), auto, true);
    expect(takedown).toBe(true);
    expect(Object.values(rest).some(Boolean)).toBe(false);
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
    const waves = [{ name: 'announce', label: 'Announce', suggested_at: null }, { name: 'reminder', label: 'Reminder', suggested_at: null }, { name: 'last-call', label: 'Final', suggested_at: null }];
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

describe('Delete and redo (#0604)', () => {
  const hand: OutreachChannel = { ...auto, name: 'instagram', label: 'Instagram', automatic: false, configured: false };
  const base = post({ status: 'posted' });

  it('only a posted automatic card can be deleted and redone', () => {
    expect(cardButtons(base, auto, true).takedown).toBe(true);
    expect(cardButtons(post({ status: 'draft' }), auto, true).takedown).toBe(false);
    expect(cardButtons(post({ status: 'queued' }), auto, true).takedown).toBe(false);
    expect(cardButtons(base, hand, true).takedown).toBe(false);
    expect(cardButtons(null, auto, true).takedown).toBe(false);
  });

  it('the dialog says the post is removed for everyone and returns to draft', () => {
    const c = postDialogCopy('takedown', [{ label: 'Bluesky', account: '' }]);
    expect(c.title).toBe('Delete this post from Bluesky?');
    expect(c.warning).toBe('It will be removed for everyone, and the post goes back to draft so you can post it again.');
    expect(c.confirmLabel).toBe('Delete post');
  });
});

const TIME = '2026-10-17T20:00:00Z';
const mast: OutreachChannel = { ...auto, name: 'mastodon', label: 'Mastodon', char_limit: 500 };
const thread: OutreachChannel = { ...auto, name: 'threads', label: 'Threads' };
const unconfigured: OutreachChannel = { ...auto, name: 'mastodon', label: 'Mastodon', configured: false };

describe('boardCell (#0624)', () => {
  it('says posted with the date and time, automatic or by hand', () => {
    for (const ch of [auto, manual]) {
      const c = boardCell(post({ status: 'posted', posted_at: TIME }), ch);
      expect([c.kind, c.status, c.detail]).toEqual(['posted', 'Posted', weekdayTime(TIME)]);
      expect(c.channelLabel).toBe(ch.label);
    }
  });
  it('says scheduled with the date and time', () => {
    const c = boardCell(post({ status: 'scheduled', scheduled_at: TIME }), auto);
    expect([c.kind, c.status, c.detail]).toEqual(['scheduled', 'Scheduled', weekdayTime(TIME)]);
  });
  it('says on hold with the time it is waiting for', () => {
    const c = boardCell(post({ status: 'scheduled', scheduled_at: TIME, held: true }), auto);
    expect([c.kind, c.status, c.detail]).toEqual(['on_hold', 'On hold', weekdayTime(TIME)]);
  });
  it('says draft and not scheduled for an automatic draft', () => {
    const c = boardCell(post(), auto);
    expect([c.kind, c.status, c.detail]).toEqual(['not_scheduled', 'Draft', 'Not scheduled']);
  });
  it('says post by hand for a hand-posted channel, as a draft or as manual', () => {
    for (const status of ['draft', 'manual'] as const) {
      const c = boardCell(post({ status }), manual);
      expect([c.kind, c.status, c.detail]).toEqual(['manual', 'Post by hand', null]);
    }
  });
  it('says post by hand for an automatic channel with no credentials', () => {
    expect(boardCell(post(), unconfigured).status).toBe('Post by hand');
  });
  it('says failed and posting now with no time', () => {
    expect(boardCell(post({ status: 'failed' }), auto)).toMatchObject({ status: 'Failed', detail: null });
    expect(boardCell(post({ status: 'queued' }), auto)).toMatchObject({ status: 'Posting now', detail: null });
  });
  it('says not written when there is no post', () => {
    expect(boardCell(null, auto)).toMatchObject({ kind: 'not_drafted', status: 'Not written', detail: null });
  });
  it('takes its kind from statusLine for every post state', () => {
    const posts = [
      null, post(), post({ status: 'manual' }), post({ status: 'failed' }), post({ status: 'queued' }),
      post({ status: 'posted', posted_at: TIME }), post({ status: 'scheduled', scheduled_at: TIME }),
      post({ status: 'scheduled', scheduled_at: TIME, held: true }),
    ];
    for (const p of posts) {
      for (const ch of [auto, manual, unconfigured]) {
        expect(boardCell(p, ch).kind).toBe(statusLine(p, ch).kind);
      }
    }
  });
});

describe('boardRows (#0624)', () => {
  const five: OutreachChannel[] = [auto, mast, ...['instagram', 'tiktok', 'youtube_shorts'].map((n) => ({ ...manual, name: n, label: n }))];
  const three = [...waves, { name: 'last-call', label: 'Final', suggested_at: null }];
  it("gives one numbered row per wave and one cell per channel, in the server's order", () => {
    const rows = boardRows({ channels: [mast, auto], waves: three, posts: [] });
    expect(rows.map((r) => [r.ordinal, r.wave, r.label])).toEqual([[1, 'announce', 'Announce'], [2, 'reminder', 'Reminder'], [3, 'last-call', 'Final']]);
    for (const r of rows) expect(r.cells.map((c) => c.channel)).toEqual(['mastodon', 'bluesky']);
  });
  it('covers all five channels, whether or not they have posts', () => {
    const rows = boardRows({ channels: five, waves: three, posts: [post()] });
    for (const r of rows) expect(r.cells).toHaveLength(5);
    expect(rows[0].cells[0].status).toBe('Draft');
    expect(rows[0].cells[1].status).toBe('Not written');
  });
  it('shows not written everywhere when there are no posts', () => {
    const rows = boardRows({ channels: five, waves: three, posts: [] });
    expect(rows.flatMap((r) => r.cells).every((c) => c.status === 'Not written')).toBe(true);
    expect(rows.every((r) => r.note === '')).toBe(true);
  });
});

describe('notScheduledNote (#0624)', () => {
  const channels = [auto, mast];
  const sched = post({ status: 'scheduled', scheduled_at: TIME });
  it('counts a draft beside a scheduled post', () => {
    expect(notScheduledNote({ channels, posts: [sched, post({ id: 2, channel: 'mastodon' })] }, 'announce')).toBe('1 not scheduled');
  });
  it('counts a channel with no post beside a scheduled post', () => {
    expect(notScheduledNote({ channels: [auto, mast, thread], posts: [sched] }, 'announce')).toBe('2 not scheduled');
  });
  it('counts a draft beside a posted post', () => {
    expect(notScheduledNote({ channels, posts: [post({ status: 'posted', posted_at: TIME }), post({ id: 2, channel: 'mastodon' })] }, 'announce')).toBe('1 not scheduled');
  });
  it('is empty when nothing is scheduled or posted', () => {
    expect(notScheduledNote({ channels, posts: [post(), post({ id: 2, channel: 'mastodon' })] }, 'announce')).toBe('');
    expect(notScheduledNote({ channels, posts: [] }, 'announce')).toBe('');
  });
  it('is empty when every automatic channel is scheduled or posted', () => {
    expect(notScheduledNote({ channels, posts: [sched, post({ id: 2, channel: 'mastodon', status: 'posted', posted_at: TIME })] }, 'announce')).toBe('');
  });
  it('ignores hand-posted channels', () => {
    expect(notScheduledNote({ channels: [auto, manual], posts: [sched, post({ id: 2, channel: 'reddit' })] }, 'announce')).toBe('');
  });
  it('does not count a failed or queued post', () => {
    for (const status of ['failed', 'queued'] as const) {
      expect(notScheduledNote({ channels, posts: [sched, post({ id: 2, channel: 'mastodon', status })] }, 'announce')).toBe('');
    }
  });
});
