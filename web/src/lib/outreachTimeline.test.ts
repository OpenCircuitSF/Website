import { describe, expect, it } from 'vitest';
import { weekdayTime } from './outreachCards';
import { sectionLine } from './outreachSection';
import { sectionGaps, stackLevels, timelineAxis, timelineLanes, timelinePct } from './outreachTimeline';
import type { OutreachChannel, OutreachPost, OutreachTimelineEvent } from './types';

const ch = (name: string, label: string, automatic: boolean, configured = automatic): OutreachChannel => ({
  name, label, utm_source: name, utm_medium: 'social', char_limit: 0, automatic, configured, account: '',
});
const bsky = ch('bluesky', 'Bluesky', true);
const mast = ch('mastodon', 'Mastodon', true);
const ig = ch('instagram', 'Instagram', false);
const tt = ch('tiktok', 'TikTok', false);
const NOW = new Date('2026-11-06T18:00:00Z');
const START = '2026-11-20T03:00:00Z';
const REMINDER_AT = '2026-11-13T18:00:00Z';
const waves = [
  { name: 'announce', label: 'Announce', suggested_at: null },
  { name: 'reminder', label: 'Reminder', suggested_at: REMINDER_AT },
];
let id = 1;
function post(channel: string, wave: string, over: Partial<OutreachPost> = {}): OutreachPost {
  return {
    id: id++, outreach_id: 1, channel, wave, body: 'Hello', status: 'draft', prepared: true, stale: false, customized: false, held: false,
    short_link_key: 'k', short_url: 'https://go.example/k', destination_url: 'https://x', utm: { source: '', medium: '', campaign: '', content: '' },
    scheduled_at: null, posted_at: null, remote_url: null, last_error: null, created_at: '', updated_at: null, ...over,
  };
}
function ev(over: Partial<OutreachTimelineEvent> = {}, wid = 7): OutreachTimelineEvent {
  return {
    workshop: { id: wid, slug: 'led', title: 'LED Programming', status: 'published', starts_at: START },
    outreach: { id: 1, workshop_id: wid, campaign_slug: 'led', created_at: '' },
    channels: [bsky, mast, ig], waves, posts: [], off_channels: [], ...over,
  };
}
const lanes = (events: OutreachTimelineEvent[]) => timelineLanes({ now: NOW.toISOString(), events }, NOW);
const marker = (e: OutreachTimelineEvent, channel: string, wave: string) =>
  lanes([e]).lanes[0].rows.find((r) => r.channel === channel)?.markers.find((m) => m.wave === wave);

describe('timeline axis', () => {
  it('timelineAxis starts now and ends a day after the last dated event', () => {
    const later = ev({ workshop: { id: 8, slug: 'x', title: 'X', status: 'published', starts_at: '2026-12-01T03:00:00Z' } }, 8);
    const a = timelineAxis([ev(), later], NOW);
    expect(a.startMs).toBe(NOW.getTime());
    expect(a.endMs).toBe(Date.parse('2026-12-02T03:00:00Z'));
  });
  it('timelineAxis spans at least 14 days', () => {
    const soon = ev({ workshop: { id: 7, slug: 'x', title: 'X', status: 'published', starts_at: '2026-11-08T03:00:00Z' } });
    expect(timelineAxis([soon], NOW).endMs).toBe(NOW.getTime() + 14 * 86400000);
  });
  it('timelineAxis ticks are Today and each Monday in Pacific time', () => {
    const a = timelineAxis([ev()], NOW);
    expect(a.ticks.map((t) => t.label)).toEqual(['Today', 'Mon, Nov 9', 'Mon, Nov 16']);
    expect(a.ticks[1].at).toBe('2026-11-09T08:00:00Z');
    expect(timelinePct(a.ticks[1].at, a)).toBeCloseTo(a.ticks[1].pct);
    expect(timelinePct('2026-11-01T00:00:00Z', a)).toBeNull();
  });
});

describe('timeline markers', () => {
  it('a posted post sits at its posted time', () => {
    const at = '2026-11-10T18:00:00Z';
    const m = marker(ev({ posts: [post('bluesky', 'announce', { status: 'posted', posted_at: at })] }), 'bluesky', 'announce');
    expect(m).toMatchObject({ kind: 'posted', status: 'Posted', at, earlier: false, suggested: false });
  });
  it('a post posted before today sits in the Earlier gutter', () => {
    const m = marker(ev({ posts: [post('bluesky', 'announce', { status: 'posted', posted_at: '2026-11-01T18:00:00Z' })] }), 'bluesky', 'announce');
    expect(m).toMatchObject({ kind: 'posted', earlier: true, pct: 0 });
  });
  it('a scheduled post sits at its scheduled time and says Scheduled', () => {
    const m = marker(ev({ posts: [post('mastodon', 'reminder', { status: 'scheduled', scheduled_at: REMINDER_AT })] }), 'mastodon', 'reminder');
    expect(m).toMatchObject({ kind: 'scheduled', status: 'Scheduled', at: REMINDER_AT });
  });
  it('an unscheduled draft sits at its suggested time, or today when there is none', () => {
    const e = ev({ posts: [post('bluesky', 'reminder'), post('bluesky', 'announce')] });
    expect(marker(e, 'bluesky', 'reminder')).toMatchObject({ kind: 'drafted', status: 'Drafted', at: REMINDER_AT, suggested: true });
    expect(marker(e, 'bluesky', 'reminder')?.label).toContain(`suggested ${weekdayTime(REMINDER_AT)}`);
    expect(marker(e, 'bluesky', 'announce')).toMatchObject({ kind: 'drafted', at: NOW.toISOString(), suggested: false, pct: 0 });
  });
  it('an on channel with no post in a section is a Not written marker', () => {
    expect(marker(ev(), 'mastodon', 'reminder')).toMatchObject({ kind: 'missing', status: 'Not written' });
  });
  it('a hand-posted draft says Drafted by hand', () => {
    const l = lanes([ev({ posts: [post('instagram', 'reminder', { status: 'manual' })] })]).lanes[0];
    const row = l.rows.find((r) => r.channel === 'instagram');
    expect(row?.label).toBe('Instagram Reels');
    expect(row?.markers.find((m) => m.wave === 'reminder')).toMatchObject({ kind: 'drafted', status: 'Drafted', detail: 'by hand' });
  });
  it('a switched-off channel with no posts gets no row', () => {
    const e = ev({ channels: [bsky, mast, tt], off_channels: [{ name: 'tiktok', label: 'TikTok', automatic: false }] });
    expect(lanes([e]).lanes[0].rows.map((r) => r.channel)).toEqual(['bluesky', 'mastodon']);
  });
  it('an event with no outreach gets one Suggested row and Outreach not started', () => {
    const l = lanes([ev({ outreach: null })]).lanes[0];
    expect(l.started).toBe(false);
    expect(l.chip.label).toBe('Not started');
    expect(l.rows).toHaveLength(1);
    expect(l.rows[0].label).toBe('Suggested');
    expect(l.rows[0].markers.map((m) => m.kind)).toEqual(['missing', 'missing']);
    expect(l.gaps).toEqual([{ wave: '', label: 'Outreach', text: 'Outreach not started', handLeft: [], href: '#/outreach?view=workshop&workshop=7' }]);
  });
  it('markers and gaps link to the event page and section', () => {
    const l = lanes([ev()]).lanes[0];
    expect(l.href).toBe('#/outreach?view=workshop&workshop=7');
    expect(l.rows[0].markers.find((m) => m.wave === 'reminder')?.href).toBe('#/outreach?view=workshop&workshop=7&section=reminder');
    expect(l.gaps.map((g) => g.href)).toEqual([
      '#/outreach?view=workshop&workshop=7&section=announce',
      '#/outreach?view=workshop&workshop=7&section=reminder',
    ]);
  });
  it('stackLevels lifts a marker that would overlap the one before it', () => {
    expect(stackLevels([0, 3, 20, 22, 24])).toEqual([0, 1, 0, 1, 2]);
    expect(stackLevels([0, 10, 20])).toEqual([0, 0, 0]);
  });
});

describe('sectionGaps', () => {
  it('sectionGaps uses the section line and names hand channels left', () => {
    const e = ev({ channels: [bsky, mast, ig, tt], posts: [post('tiktok', 'reminder', { status: 'posted', posted_at: '2026-11-05T18:00:00Z' })] });
    const g = sectionGaps(e, NOW).find((x) => x.wave === 'reminder');
    expect(g?.label).toBe('Reminder');
    expect(g?.text).toBe(sectionLine(e, 'reminder', NOW).text);
    expect(g?.text).toContain(`suggested ${weekdayTime(REMINDER_AT)}`);
    expect(g?.handLeft).toEqual(['Instagram Reels']);
  });
});
