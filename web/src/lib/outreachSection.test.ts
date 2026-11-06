import { describe, expect, it } from 'vitest';
import { weekdayTime } from './outreachCards';
import { hasVideoByHandLine, lineParts, scheduleTargets, sectionLine, sectionTimeText, unscheduleTargets, withHandNames } from './outreachSection';
import type { OutreachChannel, OutreachPost, WorkshopOutreach } from './types';

const ch = (name: string, label: string, automatic: boolean, configured = automatic): OutreachChannel => ({
  name, label, utm_source: name, utm_medium: 'social', char_limit: 0, automatic, configured, account: '',
});
const bsky = ch('bluesky', 'Bluesky', true);
const mast = ch('mastodon', 'Mastodon', true);
const ig = ch('instagram', 'Instagram', false);
const tt = ch('tiktok', 'TikTok', false);
const yt = ch('youtube_shorts', 'YouTube Shorts', false);
const all = [bsky, mast, ig, tt, yt];
const NOW = new Date('2026-11-01T12:00:00Z');
const SUGGESTED = '2026-11-10T18:00:00Z';
const T1 = '2026-11-12T02:00:00Z';
const T2 = '2026-11-13T02:00:00Z';
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: SUGGESTED },
];
let id = 1;
function post(channel: string, over: Partial<OutreachPost> = {}): OutreachPost {
  return {
    id: id++, outreach_id: 1, channel, wave: 'reminder', body: 'Hello', status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}
type V = Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts' | 'off_channels'>;
const view = (posts: OutreachPost[], channels = all, off: string[] = []): V => ({
  channels, waves, posts, off_channels: all.filter((c) => off.includes(c.name)).map(({ name, label, automatic }) => ({ name, label, automatic })),
});
const line = (v: V) => sectionLine(v, 'reminder', NOW);
const sched = (t = T1) => ({ status: 'scheduled' as const, scheduled_at: t });
const posted = (t = T1) => ({ status: 'posted' as const, posted_at: t });

describe('sectionLine', () => {
  it('Not written · suggested <when>', () => {
    expect(line(view([]))).toEqual({ kind: 'not_written', text: `Not written · suggested ${weekdayTime(SUGGESTED)}`, at: null });
  });
  it('Drafted · not scheduled · suggested <when> · 3 left to post by hand', () => {
    const l = line(view([post('bluesky'), post('mastodon')]));
    expect(l.kind).toBe('drafted');
    expect(l.text).toBe(`Drafted · not scheduled · suggested ${weekdayTime(SUGGESTED)} · 3 left to post by hand`);
  });
  it('Scheduled <when>', () => {
    const l = line(view([post('bluesky', sched()), post('mastodon', sched())], [bsky, mast]));
    expect(l).toEqual({ kind: 'scheduled', text: `Scheduled ${weekdayTime(T1)}`, at: T1 });
  });
  it('Scheduled at different times', () => {
    const l = line(view([post('bluesky', sched()), post('mastodon', sched(T2))], [bsky, mast]));
    expect(l).toEqual({ kind: 'different_times', text: 'Scheduled at different times', at: null });
  });
  it('Scheduled <first> · on hold until the event is published', () => {
    const l = line(view([post('bluesky', { ...sched(), held: true }), post('mastodon', { ...sched(T2), held: true })], [bsky, mast]));
    expect(l.kind).toBe('on_hold');
    expect(l.text).toBe(`Scheduled ${weekdayTime(T1)} · on hold until the event is published`);
  });
  it('Posting now…', () => {
    expect(line(view([post('bluesky', { status: 'queued' }), post('mastodon', { status: 'failed' })])).text).toBe('Posting now…');
  });
  it('A post failed', () => {
    const l = line(view([post('bluesky', { status: 'failed' }), post('mastodon', sched())], [bsky, mast]));
    expect([l.kind, l.text]).toEqual(['failed', 'A post failed']);
  });
  it('Posted <newest posted_at>', () => {
    const l = line(view([post('bluesky', posted()), post('mastodon', posted(T2))], [bsky, mast]));
    expect(l).toEqual({ kind: 'posted', text: `Posted ${weekdayTime(T2)}`, at: T2 });
  });
  it('<Label> posted · <Label> scheduled <when>, and not scheduled', () => {
    expect(line(view([post('bluesky', posted()), post('mastodon', sched(T2))], [bsky, mast])))
      .toMatchObject({ kind: 'mixed', text: `Bluesky posted · Mastodon scheduled ${weekdayTime(T2)}` });
    expect(line(view([post('bluesky', posted())], [bsky, mast]))).toMatchObject({ kind: 'mixed', text: 'Bluesky posted · Mastodon not scheduled' });
  });
  it('Drafted · N left to post by hand, and Posted <when> by hand', () => {
    const hand = [ig, tt];
    expect(line(view([post('instagram')], hand))).toMatchObject({ kind: 'hand_only', text: 'Drafted · 2 left to post by hand' });
    expect(line(view([post('instagram', posted()), post('tiktok', posted(T2))], hand))).toMatchObject({ kind: 'hand_only', text: `Posted ${weekdayTime(T2)}` });
  });
  it('says Not written with nothing else when every channel is off (#0636)', () => {
    expect(line(view([], all, all.map((c) => c.name))).text).toBe('Not written');
  });
  it('names a disconnected channel beside a posted section (#0636)', () => {
    const off = ch('bluesky', 'Bluesky', true, false);
    expect(line(view([post('bluesky', sched()), post('mastodon', posted())], [off, mast])).text).toBe(`Posted ${weekdayTime(T1)} · Bluesky is not connected`);
    expect(line(view([post('bluesky', sched())], [off, mast], ['mastodon'])).text).toBe('Not written');
  });
  it('counts hand rows over on channels only (#0636)', () => {
    const l = line(view([post('bluesky', sched()), post('mastodon', sched())], all, ['tiktok', 'youtube_shorts']));
    expect(l.text).toBe(`Scheduled ${weekdayTime(T1)} · 1 left to post by hand`);
    const off = ch('bluesky', 'Bluesky', true, false);
    expect(line(view([post('bluesky', sched()), post('mastodon', posted())], [off, mast])).text).not.toContain('by hand');
  });
  it('never says suggested once anything is scheduled or posted', () => {
    for (const v of [view([post('bluesky', sched()), post('mastodon')]), view([post('bluesky', posted()), post('mastodon')])]) {
      expect(line(v).text).not.toContain('suggested');
    }
  });
});

describe('scheduleTargets', () => {
  it('takes draft, scheduled and failed; not posted or queued', () => {
    for (const status of ['draft', 'scheduled', 'failed'] as const) expect(scheduleTargets(view([post('bluesky', { status })]), 'reminder')).toHaveLength(1);
    for (const status of ['posted', 'queued'] as const) expect(scheduleTargets(view([post('bluesky', { status })]), 'reminder')).toHaveLength(0);
  });
  it('never takes a hand channel', () => {
    expect(scheduleTargets(view([post('instagram')]), 'reminder')).toHaveLength(0);
  });
  it('takes a scheduled post on an off channel (#0588)', () => {
    const v = view([post('bluesky', sched()), post('mastodon')], all, ['bluesky']);
    expect(scheduleTargets(v, 'reminder').map((t) => t.channel.name)).toEqual(['bluesky', 'mastodon']);
    expect(unscheduleTargets(v, 'reminder').map((t) => t.channel.name)).toEqual(['bluesky']);
  });
  it('unscheduleTargets takes a scheduled post on a disconnected channel, never a hand one', () => {
    const off = ch('mastodon', 'Mastodon', true, false);
    const v = view([post('bluesky', sched()), post('mastodon', sched()), post('instagram', sched())], [bsky, off, ig]);
    expect(scheduleTargets(v, 'reminder').map((t) => t.channel.name)).toEqual(['bluesky']);
    expect(unscheduleTargets(v, 'reminder').map((t) => t.channel.name)).toEqual(['bluesky', 'mastodon']);
  });
});

describe('sectionTimeText', () => {
  const two = (a: Partial<OutreachPost>, b: Partial<OutreachPost>) => sectionTimeText(view([post('bluesky', a), post('mastodon', b)], [bsky, mast]), 'reminder', NOW);
  it('says each kind', () => {
    expect(two({}, {})).toBe('Not scheduled. Bluesky and Mastodon go out together.');
    expect(two(sched(), sched())).toBe(`Scheduled for ${weekdayTime(T1)} on Bluesky and Mastodon.`);
    expect(two(sched(), sched(T2))).toBe(`Bluesky: ${weekdayTime(T1)}. Mastodon: ${weekdayTime(T2)}. Change time… gives both one time.`);
    expect(two(posted(), {})).toBe(`Bluesky posted ${weekdayTime(T1)}. Mastodon: not scheduled.`);
    expect(two({ ...sched(), held: true }, sched())).toBe(`Scheduled for ${weekdayTime(T1)} · on hold until the event is published.`);
    expect(two({ status: 'queued' }, {})).toBe('Posting now…');
    expect(two(posted(), posted(T2))).toBe(`Posted ${weekdayTime(T2)} on Bluesky and Mastodon.`);
    expect(two({ status: 'failed' }, {})).toBe('The Bluesky post failed. Retry posts it now.');
    expect(sectionTimeText(view([], [ig]), 'reminder', NOW)).toBe('');
  });
});

describe('hasVideoByHandLine', () => {
  it('names the on hand channels', () => {
    expect(hasVideoByHandLine(view([]))).toBe('Instagram Reels, TikTok and YouTube Shorts are posted by hand; the site never posts them.');
    expect(hasVideoByHandLine(view([], all, ['tiktok', 'youtube_shorts']))).toBe('Instagram Reels is posted by hand; the site never posts it.');
    expect(hasVideoByHandLine(view([], [bsky, mast]))).toBe('');
  });
});

describe('lineParts', () => {
  it('splits around the first date', () => {
    expect(lineParts('Scheduled Thu, Nov 12 at 6:00 PM PT · 3 left to post by hand')).toEqual(['Scheduled ', 'Thu, Nov 12 at 6:00 PM PT', ' · 3 left to post by hand']);
    expect(lineParts('Not written')).toEqual(['Not written', '', '']);
  });
});

describe('withHandNames (#0646)', () => {
  it('names the channels after the count, saying the count once', () => {
    expect(withHandNames('Posted Thu · 3 left to post by hand · Bluesky is not connected', ['Instagram Reels', 'TikTok', 'YouTube Shorts']))
      .toBe('Posted Thu · 3 left to post by hand: Instagram Reels, TikTok and YouTube Shorts · Bluesky is not connected');
  });
  it('appends the count and names when the line has no count', () => {
    expect(withHandNames('Not written', ['Instagram Reels'])).toBe('Not written · 1 left to post by hand: Instagram Reels');
  });
  it('leaves the line alone when nothing is left', () => {
    expect(withHandNames('Posted Thu', [])).toBe('Posted Thu');
  });
});
