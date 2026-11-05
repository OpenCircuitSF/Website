import { describe, expect, it } from 'vitest';
import { composeBody, isHashtagParagraph, messageTargets, planSaveMessage, restoredTail, sharedMessage, splitBody, waveExcerpt, waveRowStatus, waveStrip } from './outreachMessage';
import { boardRows, sectionFullyPosted, weekdayTime } from './outreachCards';
import type { OutreachChannel, OutreachPost, OutreachWave } from './types';

const ch = (name: string, automatic: boolean, configured: boolean, limit = 0): OutreachChannel => ({
  name, label: name, utm_source: name, utm_medium: 'social', char_limit: limit, automatic, configured, account: '',
});
const channels = [ch('bluesky', true, true, 300), ch('mastodon', true, true, 500), ch('instagram', false, false)];
const waves: OutreachWave[] = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: null },
];
const MURL = 'https://go.example/m';
const bsky = 'Join us for Soldering 101.\n\nLearn to solder.\n\n#soldering #sf';
const mast = `Join us for Soldering 101.\n\nLearn to solder.\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`;

function post(over: Partial<OutreachPost>): OutreachPost {
  return {
    id: 1, outreach_id: 1, channel: 'bluesky', wave: 'announce', body: bsky, status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/b', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}
const bPost = post({});
const mPost = post({ id: 2, channel: 'mastodon', body: mast, short_url: MURL });
const view = (posts: OutreachPost[]) => ({ channels, waves, posts });
const autoChannels = channels.slice(0, 2);
const autoView = (posts: OutreachPost[]) => ({ channels: autoChannels, waves, posts });

describe('isHashtagParagraph', () => {
  it('accepts hashtag-only lines and rejects prose', () => {
    expect(isHashtagParagraph('#soldering #sf')).toBe(true);
    expect(isHashtagParagraph('#café')).toBe(true);
    expect(isHashtagParagraph('Come to #soldering')).toBe(false);
    expect(isHashtagParagraph('')).toBe(false);
  });
});

describe('splitBody and composeBody', () => {
  it('peels the hashtag line from Bluesky', () => {
    expect(splitBody(bsky, null)).toEqual({ message: 'Join us for Soldering 101.\n\nLearn to solder.', tail: ['#soldering #sf'] });
  });
  it('peels the link line and the hashtags from Mastodon', () => {
    const s = splitBody(mast, MURL);
    expect(s.message).toBe('Join us for Soldering 101.\n\nLearn to solder.');
    expect(s.tail).toEqual([`Spots are limited, so you can sign up here: ${MURL}`, '#soldering #sf']);
  });
  it('peels a paragraph that still holds the link token', () => {
    expect(splitBody('Hello\n\nSign up: {link}', null).tail).toEqual(['Sign up: {link}']);
  });
  it('round-trips', () => {
    const s = splitBody(mast, MURL);
    expect(composeBody(s.message, s.tail)).toBe(mast);
  });
  it('treats a final hashtag-only paragraph as tail even when typed (documented ambiguity, #0597), and still round-trips it', () => {
    const body = 'Join us.\n\nLearn to solder.\n\n#typed';
    const s = splitBody(body, null);
    expect(s).toEqual({ message: 'Join us.\n\nLearn to solder.', tail: ['#typed'] });
    expect(composeBody(s.message, s.tail)).toBe(body);
  });
  it('a body with no tail is all message', () => {
    expect(splitBody('Just words.\n\nMore words.', null)).toEqual({ message: 'Just words.\n\nMore words.', tail: [] });
  });
});

describe('messageTargets and sharedMessage', () => {
  it('skips customized, posted, queued, manual and other-wave posts', () => {
    const v = view([
      bPost,
      post({ id: 2, channel: 'mastodon', customized: true }),
      post({ id: 3, channel: 'instagram', status: 'manual' }),
      post({ id: 4, wave: 'reminder' }),
    ]);
    expect(messageTargets(v, 'announce').map((t) => t.post.id)).toEqual([1]);
    expect(messageTargets(view([post({ status: 'posted' })]), 'announce')).toEqual([]);
    expect(messageTargets(view([post({ status: 'queued' })]), 'announce')).toEqual([]);
  });
  it('the shared message is the first target message', () => {
    expect(sharedMessage(view([bPost, mPost]), 'announce')).toBe('Join us for Soldering 101.\n\nLearn to solder.');
    expect(sharedMessage(view([]), 'announce')).toBe('');
  });
});

describe('planSaveMessage', () => {
  it('composes each post with its own tail and flags the over-limit ones', () => {
    const plan = planSaveMessage(view([bPost, mPost]), 'announce', 'New words.');
    expect(plan.map((i) => i.body)).toEqual([
      'New words.\n\n#soldering #sf',
      `New words.\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`,
    ]);
    expect(plan.every((i) => !i.over)).toBe(true);
    const long = planSaveMessage(view([bPost, mPost]), 'announce', 'x'.repeat(400));
    expect(long.map((i) => i.over)).toEqual([true, false]);
  });
  it('never drops the Mastodon link line or the hashtags, even when the message itself holds the URL (#0582 review)', () => {
    const plan = planSaveMessage(view([bPost, mPost]), 'announce', `New words.\n\nSee ${MURL}`);
    expect(plan[1].body).toBe(`New words.\n\nSee ${MURL}\n\nSpots are limited, so you can sign up here: ${MURL}\n\n#soldering #sf`);
    const unprepared = { ...mPost, prepared: false, short_url: null, body: mast.replace(MURL, '{link}') };
    expect(planSaveMessage(view([unprepared]), 'announce', 'New words.')[0].body).toBe(
      'New words.\n\nSpots are limited, so you can sign up here: {link}\n\n#soldering #sf',
    );
  });
  it('leaves customized posts out', () => {
    expect(planSaveMessage(view([bPost, { ...mPost, customized: true }]), 'announce', 'a').map((i) => i.post.id)).toEqual([1]);
  });
});

describe('waveRowStatus', () => {
  it('Not written with a write action when the wave has no posts', () => {
    expect(waveRowStatus(autoView([bPost]), 'reminder')).toEqual({ text: 'Not written · not scheduled', action: 'write' });
  });
  it('reads Drafted, scheduled, posting, failed and posted', () => {
    const r = (over: Partial<OutreachPost>) => waveRowStatus(autoView([post({ wave: 'reminder', ...over })]), 'reminder');
    expect(r({})).toEqual({ text: 'Drafted · not scheduled', action: 'open' });
    expect(r({ status: 'queued' }).text).toBe('Posting now…');
    expect(r({ status: 'failed' }).text).toBe('Drafted · a post failed');
    expect(r({ status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z' }).text).toMatch(/^Posts automatically /);
    expect(r({ status: 'scheduled', scheduled_at: '2026-11-30T20:00:00Z', held: true }).text).toMatch(/^Scheduled .+ · on hold/);
    expect(r({ status: 'posted' }).text).toBe('Posted 1 of 1 · 1 not scheduled');
  });
  describe('with real dates (#0622)', () => {
    const now = new Date('2026-11-01T12:00:00Z');
    const reminderAt = '2026-11-28T18:00:00Z';
    const at = (suggested: string | null, posts: OutreachPost[]) => ({
      channels: autoChannels,
      waves: [waves[0], { ...waves[1], suggested_at: suggested }],
      posts,
    });
    it('names a future suggested time only while nothing is scheduled or posted', () => {
      expect(waveRowStatus(at(reminderAt, []), 'reminder', now).text).toMatch(/^Not written · not scheduled · suggested /);
      expect(waveRowStatus(at(reminderAt, [post({ wave: 'reminder' })]), 'reminder', now).text).toMatch(/^Drafted · not scheduled · suggested /);
    });
    it('never shows a suggested time that has passed', () => {
      expect(waveRowStatus(at('2026-10-01T18:00:00Z', []), 'reminder', now).text).toBe('Not written · not scheduled');
      expect(waveRowStatus(at('2026-10-01T18:00:00Z', [post({ wave: 'reminder' })]), 'reminder', now).text).toBe('Drafted · not scheduled');
    });
    it('drops the suggested time once a post is scheduled', () => {
      const text = waveRowStatus(at(reminderAt, [post({ wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-20T20:00:00Z' })]), 'reminder', now).text;
      expect(text).toMatch(/^Posts automatically /);
      expect(text).not.toContain('suggested');
    });
    it('drops the suggested time once a post is posted, and gives the posted date', () => {
      const text = waveRowStatus(at(reminderAt, [post({ wave: 'reminder', status: 'posted', posted_at: '2026-10-30T20:00:00Z' })]), 'reminder', now).text;
      expect(text).toMatch(/^Posted /);
      expect(text).not.toContain(' of ');
      expect(text).not.toContain('suggested');
    });
    it('says from when scheduled times differ', () => {
      const v = at(null, [
        post({ wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-20T20:00:00Z' }),
        post({ id: 2, channel: 'mastodon', wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-21T20:00:00Z' }),
      ]);
      expect(waveRowStatus(v, 'reminder', now).text).toMatch(/^Posts automatically from /);
    });
    it('adds the posted count beside a later scheduled time', () => {
      const v = at(null, [
        post({ wave: 'reminder', status: 'posted', posted_at: '2026-10-30T20:00:00Z' }),
        post({ id: 2, channel: 'mastodon', wave: 'reminder', status: 'scheduled', scheduled_at: '2026-11-21T20:00:00Z' }),
      ]);
      expect(waveRowStatus(v, 'reminder', now).text).toMatch(/^Posts automatically .+ · posted 1 of 2$/);
    });
  });
  it('counts only automatic posts', () => {
    const v = autoView([post({ wave: 'reminder', status: 'posted' }), post({ id: 2, wave: 'reminder', channel: 'mastodon' })]);
    expect(waveRowStatus(v, 'reminder').text).toBe('Posted 1 of 2 · 1 not scheduled');
  });
});

describe('waveStrip', () => {
  it('numbers waves in the order the server sends them', () => {
    const v = { channels, posts: [], waves: [waves[1], { name: 'last-call', label: 'Final', suggested_at: null }, waves[0]] };
    expect(waveStrip(v, 'reminder')).toMatchObject({ ordinal: 1, label: 'Reminder' });
    expect(waveStrip(v, 'last-call')).toMatchObject({ ordinal: 2, label: 'Final' });
    expect(waveStrip(v, 'announce')).toMatchObject({ ordinal: 3, label: 'Announce' });
  });
  it('says not scheduled for the announcement before it has a time', () => {
    expect(waveStrip(autoView([bPost]), 'announce').text).toBe('Drafted · not scheduled');
    expect(waveStrip(autoView([]), 'announce').text).toBe('Not written · not scheduled');
  });
});

describe('a message ending in a hashtag-only paragraph (#0593)', () => {
  it('keeps that paragraph in the message when the post also has its own hashtag line', () => {
    const body = 'Hello.\n\n#soldering\n\n#a #b';
    expect(splitBody(body, null)).toEqual({ message: 'Hello.\n\n#soldering', tail: ['#a #b'] });
  });
  it('round-trips through compose', () => {
    const planned = planSaveMessage(view([bPost]), 'announce', 'Hello.\n\n#workshop');
    expect(splitBody(planned[0].body, null).message).toBe('Hello.\n\n#workshop');
  });
});

describe('restoredTail (#0593)', () => {
  it('keeps a post\'s own tail', () => {
    expect(restoredTail(view([bPost, mPost]), mPost)).toEqual([`Spots are limited, so you can sign up here: ${MURL}`, '#soldering #sf']);
  });
  it('restores the hashtags for Bluesky from a non-customized sibling, with no link', () => {
    const lost = { ...bPost, customized: true, body: 'Only my words.' };
    expect(restoredTail(view([lost, mPost]), lost)).toEqual(['#soldering #sf']);
  });
  it('restores Mastodon\'s link line (own short URL) and hashtags when both were lost', () => {
    const lost = { ...mPost, customized: true, body: 'Only my words.', short_url: 'https://go.example/m2' };
    const sibling = { ...mPost, id: 3, channel: 'mastodon', body: mast };
    expect(restoredTail(view([bPost, lost, sibling]), lost)).toEqual(['Spots are limited, so you can sign up here: https://go.example/m2', '#soldering #sf']);
  });
  it('turns an unprepared sibling\'s link token into this post\'s own short URL', () => {
    const lost = { ...mPost, customized: true, body: 'Only my words.', short_url: 'https://go.example/m2' };
    const sibling = { ...mPost, id: 3, channel: 'mastodon', body: mast.split(MURL).join('{link}'), short_url: null };
    expect(restoredTail(view([bPost, lost, sibling]), lost)).toEqual(['Spots are limited, so you can sign up here: https://go.example/m2', '#soldering #sf']);
  });
  it('makes a plain link line when no sibling carries one', () => {
    const lost = { ...mPost, customized: true, body: 'Only my words.' };
    expect(restoredTail(view([bPost, lost]), lost)).toEqual([`Sign up here: ${MURL}`, '#soldering #sf']);
  });
});

describe('a partly scheduled section (#0624)', () => {
  const TIME = '2026-10-17T20:00:00Z';
  const three = [ch('bluesky', true, true), ch('mastodon', true, true), ch('threads', true, true)];
  const sched = (over: Partial<OutreachPost> = {}) => post({ status: 'scheduled', scheduled_at: TIME, ...over });
  const text = (channels: OutreachChannel[], posts: OutreachPost[]) => waveRowStatus({ channels, waves, posts }, 'announce').text;
  it('says how many are not scheduled beside the time', () => {
    expect(text(autoChannels, [sched(), post({ id: 2, channel: 'mastodon' })])).toMatch(/^Posts automatically .+ · 1 not scheduled$/);
  });
  it('adds it after the posted count', () => {
    expect(text(three, [sched(), post({ id: 2, channel: 'mastodon', status: 'posted', posted_at: TIME }), post({ id: 3, channel: 'threads' })]))
      .toMatch(/^Posts automatically .+ · posted 1 of 3 · 1 not scheduled$/);
  });
  it('adds it to an on hold section', () => {
    expect(text(autoChannels, [sched({ held: true }), post({ id: 2, channel: 'mastodon' })])).toMatch(/^Scheduled .+ · on hold until the event is published · 1 not scheduled$/);
  });
  it('does not read as fully posted when a channel has no post', () => {
    expect(text(autoChannels, [post({ status: 'posted', posted_at: TIME })])).toMatch(/^Posted .+ · 1 not scheduled$/);
  });
  it('adds nothing when every channel is scheduled', () => {
    expect(text(autoChannels, [sched(), sched({ id: 2, channel: 'mastodon' })])).not.toContain('not scheduled');
  });
  it("leaves an unscheduled section's wording alone", () => {
    expect(text(autoChannels, [post({}), post({ id: 2, channel: 'mastodon' })])).toBe('Drafted · not scheduled');
  });
  it('the strip and the board row give the same count', () => {
    const v = { channels: autoChannels, waves, posts: [sched({ wave: 'reminder' }), post({ id: 2, channel: 'mastodon', wave: 'reminder' })] };
    const note = boardRows(v)[1].note;
    expect(note).not.toBe('');
    expect(waveStrip(v, 'reminder').text.endsWith(note)).toBe(true);
  });
});

describe('hand-posted channels in the section status (#0626)', () => {
  const ig = (over: Partial<OutreachPost> = {}) => post({ id: 3, channel: 'instagram', status: 'manual', ...over });
  const T1 = '2026-10-17T20:00:00Z';
  const T2 = '2026-10-18T20:00:00Z';
  const live = (over: Partial<OutreachPost> = {}) => post({ status: 'posted', posted_at: T1, ...over });
  const handOnly = [ch('instagram', false, false), ch('tiktok', false, false)];
  const text = (v: Parameters<typeof waveRowStatus>[0]) => waveRowStatus(v, 'announce').text;

  it('says how many are left once a section is written', () => {
    expect(text(view([bPost, mPost, ig()]))).toBe('Drafted · not scheduled · 1 left to post by hand');
  });
  it('a hand channel with no post counts as left', () => {
    expect(text(view([bPost, mPost]))).toBe('Drafted · not scheduled · 1 left to post by hand');
  });
  it('reads Posted with the newest time only when hand-posted channels are done too', () => {
    const v = view([live(), live({ id: 2, channel: 'mastodon' }), ig({ status: 'posted', posted_at: T2 })]);
    expect(text(v)).toBe(`Posted ${weekdayTime(T2)}`);
  });
  it('keeps the automatic date and adds the count while a hand post is left', () => {
    const v = view([live(), live({ id: 2, channel: 'mastodon' }), ig()]);
    expect(text(v)).toBe(`Posted ${weekdayTime(T1)} · 1 left to post by hand`);
  });
  it('a section with only hand-posted channels reads Drafted until all are marked, then Posted', () => {
    const two = (a: OutreachPost['status'], b: OutreachPost['status']) => ({
      channels: handOnly,
      waves,
      posts: [
        ig({ status: a, posted_at: a === 'posted' ? T1 : null }),
        post({ id: 4, channel: 'tiktok', status: b, posted_at: b === 'posted' ? T2 : null }),
      ],
    });
    expect(text(two('manual', 'manual'))).toBe('Drafted · 2 left to post by hand');
    expect(text(two('posted', 'manual'))).toBe('Drafted · 1 left to post by hand');
    expect(text(two('posted', 'posted'))).toBe(`Posted ${weekdayTime(T2)}`);
  });
  it('an unconnected Bluesky counts as hand-posted', () => {
    const v = { channels: [ch('bluesky', true, false, 300), ch('mastodon', true, true, 500)], waves, posts: [bPost, mPost] };
    expect(text(v)).toBe('Drafted · not scheduled · 1 left to post by hand');
  });
  it('a section with nothing written has no hand count', () => {
    expect(text(view([]))).toBe('Not written · not scheduled');
  });
  it('reads as plain Posted exactly when sectionFullyPosted is true', () => {
    const views = [
      view([live(), live({ id: 2, channel: 'mastodon' }), ig({ status: 'posted', posted_at: T2 })]),
      view([live(), live({ id: 2, channel: 'mastodon' }), ig()]),
      view([live(), live({ id: 2, channel: 'mastodon' })]),
      view([live(), ig({ status: 'posted', posted_at: T2 })]),
      view([bPost, mPost, ig({ status: 'posted', posted_at: T2 })]),
      { channels: handOnly, waves, posts: [ig({ status: 'posted', posted_at: T1 }), post({ id: 4, channel: 'tiktok', status: 'posted', posted_at: T2 })] },
      { channels: handOnly, waves, posts: [ig({ status: 'posted', posted_at: T1 })] },
    ];
    const verdicts = views.map((v) => {
      const t = text(v);
      expect(t.startsWith('Posted') && !t.includes('left to post') && !t.includes('not scheduled')).toBe(sectionFullyPosted(v, 'announce'));
      return sectionFullyPosted(v, 'announce');
    });
    expect(verdicts).toContain(true);
    expect(verdicts).toContain(false);
  });
});

describe('waveExcerpt (#0624)', () => {
  it("takes the first written post's message on one line, without hashtags or link lines", () => {
    expect(waveExcerpt(view([mPost]), 'announce')).toBe('Join us for Soldering 101. Learn to solder.');
  });
  it('still shows a post that has gone out', () => {
    expect(waveExcerpt(view([post({ status: 'posted' })]), 'announce')).toBe('Join us for Soldering 101. Learn to solder.');
  });
  it('shortens long text with an ellipsis without splitting an emoji', () => {
    const out = waveExcerpt(view([post({ body: '🔌'.repeat(100) })]), 'announce');
    expect(Array.from(out)).toHaveLength(80);
    expect(out.endsWith('🔌…')).toBe(true);
  });
  it('is empty for a section with no posts', () => {
    expect(waveExcerpt(view([bPost]), 'reminder')).toBe('');
  });
});

describe('waveRowStatus with a switched-off channel', () => {
  it('does not count an off channel as left to post by hand (#0628)', () => {
    const tiktok = ch('tiktok', false, false);
    const v = { channels: [...channels, tiktok], waves, posts: [post({ status: 'posted', posted_at: '2026-10-30T20:00:00Z' }), post({ id: 2, channel: 'mastodon', status: 'posted', posted_at: '2026-10-30T20:00:00Z' })] };
    expect(waveRowStatus(v, 'announce').text).toContain('2 left to post by hand');
    expect(waveRowStatus({ ...v, off_channels: [{ name: 'tiktok', label: 'tiktok', automatic: false }] }, 'announce').text).toContain('1 left to post by hand');
  });

  const T = '2026-10-30T20:00:00Z';
  const SUGGESTED = '2026-11-20T18:00:00Z';
  const NOW = new Date('2026-11-01T00:00:00Z');
  const off = (...names: string[]) => names.map((name) => ({ name, label: name, automatic: name === 'bluesky' || name === 'mastodon' }));
  const handNames = ['instagram', 'tiktok', 'youtube'];
  const wavesAhead: OutreachWave[] = [waves[0], { ...waves[1], suggested_at: SUGGESTED }];
  // Bluesky and Mastodon are off, each with a posted Announce, so both keep their columns.
  const bothAutoOff = (handStatus: OutreachPost['status']) => ({
    channels: [...autoChannels, ...handNames.map((n) => ch(n, false, false))],
    waves: wavesAhead,
    off_channels: off('bluesky', 'mastodon'),
    posts: [
      post({ id: 1, channel: 'bluesky', status: 'posted', posted_at: T }),
      post({ id: 2, channel: 'mastodon', status: 'posted', posted_at: T }),
      ...handNames.map((n, i) => post({ id: 3 + i, channel: n, wave: 'reminder', status: handStatus, posted_at: handStatus === 'posted' ? T : null })),
    ],
  });

  it('says Posted when only off automatic channels are missing (#0628)', () => {
    const v = bothAutoOff('posted');
    expect(waveRowStatus(v, 'reminder', NOW).text).toBe(`Posted ${weekdayTime(T)}`);
    expect(sectionFullyPosted(v, 'reminder')).toBe(true);
  });

  it('does not say not scheduled when every automatic channel is off (#0628)', () => {
    expect(waveRowStatus(bothAutoOff('manual'), 'reminder', NOW).text).toBe(`Drafted · 3 left to post by hand · suggested ${weekdayTime(SUGGESTED)}`);
  });

  it("keeps the not scheduled note when an off channel's post is scheduled (#0628)", () => {
    const v = {
      channels: autoChannels,
      waves,
      off_channels: off('mastodon'),
      posts: [
        post({ id: 1, channel: 'bluesky', wave: 'reminder', status: 'draft' }),
        post({ id: 2, channel: 'mastodon', wave: 'reminder', status: 'scheduled', scheduled_at: T }),
      ],
    };
    expect(waveRowStatus(v, 'reminder', NOW).text).toBe(`Posts automatically ${weekdayTime(T)} · 1 not scheduled`);
    expect(boardRows(v).find((r) => r.wave === 'reminder')?.note).toBe('1 not scheduled');
  });

  it("keeps the not scheduled note when an off channel's post has gone out (#0628)", () => {
    const v = {
      channels: autoChannels,
      waves,
      off_channels: off('mastodon'),
      posts: [post({ id: 2, channel: 'mastodon', wave: 'reminder', status: 'posted', posted_at: T })],
    };
    expect(waveRowStatus(v, 'reminder', NOW).text).toBe(`Posted ${weekdayTime(T)} · 1 not scheduled`);
    expect(boardRows(v).find((r) => r.wave === 'reminder')?.note).toBe('1 not scheduled');
  });
});
