// One section of the outreach page in a line (#0640): what the posts that send
// themselves have done, in the words the page uses everywhere (drafted,
// scheduled, posted), plus what is left to post by hand. Pure, so it is
// unit-testable; the board, the tabs and the section block all read it.
import { defaultScheduleAt } from './outreach';
import { onChannels, sendsItself, weekdayTime } from './outreachCards';
import { handChannelLabel } from './outreachHandCopy';
import type { OutreachChannel, OutreachPost, WorkshopOutreach } from './types';

type View = Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts' | 'off_channels'>;

export type SectionKind = 'not_written' | 'drafted' | 'scheduled' | 'different_times' | 'mixed' | 'posting' | 'posted' | 'failed' | 'on_hold' | 'hand_only';
export interface SectionLine {
  kind: SectionKind;
  text: string;
  /** The one instant the automatic posts share, when they do. */
  at: string | null;
}
export interface Target {
  post: OutreachPost;
  channel: OutreachChannel;
}

const postOf = (view: View, channel: string, wave: string): OutreachPost | null =>
  view.posts.find((p) => p.channel === channel && p.wave === wave) ?? null;

const newest = (times: (string | null)[]): string | null =>
  times.filter((t): t is string => t !== null).sort().pop() ?? null;

function list(labels: string[]): string {
  return labels.length <= 1 ? (labels[0] ?? '') : `${labels.slice(0, -1).join(', ')} and ${labels[labels.length - 1]}`;
}

/** On hand-posted channels (not automatic at all): how many there are and how many are not yet posted. */
export function sectionHand(view: View, wave: string): { left: number; total: number } {
  const hand = onChannels(view).filter((c) => !c.automatic);
  const left = hand.filter((c) => postOf(view, c.name, wave)?.status !== 'posted').length;
  return { left, total: hand.length };
}

/** The automatic posts this line is about: on channels that are on and send themselves. */
function autos(view: View, wave: string): { channel: OutreachChannel; post: OutreachPost | null }[] {
  return onChannels(view).filter(sendsItself).map((channel) => ({ channel, post: postOf(view, channel.name, wave) }));
}

/** A section's one line (#0640). */
export function sectionLine(view: View, wave: string, now: Date = new Date()): SectionLine {
  const on = onChannels(view);
  const auto = autos(view, wave);
  const hand = sectionHand(view, wave);
  const handSuffix = hand.left > 0 ? ` · ${hand.left} left to post by hand` : '';
  const suggested = defaultScheduleAt(wave, view.waves, now);
  const ahead = suggested && auto.length > 0 ? ` · suggested ${weekdayTime(suggested)}` : '';
  const disconnected = on
    .filter((c) => c.automatic && !c.configured)
    .filter((c) => { const p = postOf(view, c.name, wave); return p !== null && p.status !== 'posted'; })
    .map((c) => ` · ${c.label} is not connected`)
    .join('');
  const line = (kind: SectionKind, text: string, at: string | null = null): SectionLine => ({ kind, text, at });

  const handPosts = on.filter((c) => !c.automatic).map((c) => postOf(view, c.name, wave)).filter((p): p is OutreachPost => p !== null);
  if (auto.length === 0) {
    if (handPosts.length === 0) return line('not_written', 'Not written');
    if (hand.left > 0) return line('hand_only', `Drafted · ${hand.left} left to post by hand`);
    const t = newest(handPosts.map((p) => p.posted_at));
    return line('hand_only', t ? `Posted ${weekdayTime(t)}` : 'Posted', t);
  }
  const posts = auto.map((a) => a.post).filter((p): p is OutreachPost => p !== null);
  if (posts.length === 0 && handPosts.length === 0) return line('not_written', `Not written${ahead}`);
  if (posts.some((p) => p.status === 'queued')) return line('posting', 'Posting now…');
  if (posts.some((p) => p.status === 'failed')) return line('failed', `A post failed${handSuffix}`);
  const times = posts.filter((p) => p.status === 'scheduled' && p.scheduled_at).map((p) => p.scheduled_at as string).sort();
  if (posts.some((p) => p.held) && times.length > 0) {
    return line('on_hold', `Scheduled ${weekdayTime(times[0])} · on hold until the event is published${handSuffix}`);
  }
  const posted = posts.filter((p) => p.status === 'posted');
  if (posted.length === auto.length) {
    const t = newest(posted.map((p) => p.posted_at));
    return line('posted', `${t ? `Posted ${weekdayTime(t)}` : 'Posted'}${handSuffix}${disconnected}`, t);
  }
  if (times.length === auto.length) {
    if (times[0] === times[times.length - 1]) return line('scheduled', `Scheduled ${weekdayTime(times[0])}${handSuffix}${disconnected}`, times[0]);
    return line('different_times', `Scheduled at different times${handSuffix}${disconnected}`);
  }
  if (posted.length > 0 || times.length > 0) {
    const parts = auto.map(({ channel, post }) => {
      if (post?.status === 'posted') return `${channel.label} posted`;
      if (post?.status === 'scheduled' && post.scheduled_at) return `${channel.label} scheduled ${weekdayTime(post.scheduled_at)}`;
      return `${channel.label} not scheduled`;
    });
    return line('mixed', `${parts.join(' · ')}${handSuffix}${disconnected}`);
  }
  return line('drafted', `Drafted · not scheduled${ahead}${handSuffix}${disconnected}`);
}

/** Posts a section's one time applies to: automatic, sending themselves, not yet out, with a link (#0640 decision 8). */
export function scheduleTargets(view: View, wave: string): Target[] {
  const out: Target[] = [];
  for (const channel of view.channels) {
    if (!sendsItself(channel)) continue;
    const post = postOf(view, channel.name, wave);
    if (!post || !post.prepared) continue;
    if (post.status === 'draft' || post.status === 'scheduled' || post.status === 'failed') out.push({ post, channel });
  }
  return out;
}

/** The scheduled subset of scheduleTargets. */
export function unscheduleTargets(view: View, wave: string): Target[] {
  return scheduleTargets(view, wave).filter((t) => t.post.status === 'scheduled');
}

/** The section block's time line (#0640, used from 0640d). */
export function sectionTimeText(view: View, wave: string, now: Date = new Date()): string {
  const l = sectionLine(view, wave, now);
  const auto = autos(view, wave);
  const names = list(auto.map((a) => a.channel.label));
  const firstTime = () => {
    const t = auto.map((a) => a.post?.scheduled_at ?? null).filter((x): x is string => x !== null).sort()[0];
    return t ? weekdayTime(t) : '';
  };
  switch (l.kind) {
    case 'drafted':
      return auto.length > 1 ? `Not scheduled. ${names} go out together.` : 'Not scheduled.';
    case 'scheduled':
      return `Scheduled for ${weekdayTime(l.at as string)} on ${names}.`;
    case 'different_times':
      return `${auto.map((a) => `${a.channel.label}: ${a.post?.scheduled_at ? weekdayTime(a.post.scheduled_at) : 'not scheduled'}.`).join(' ')} Change time… gives both one time.`;
    case 'mixed':
      return auto.map(({ channel, post }) => {
        if (post?.status === 'posted') return `${channel.label} posted${post.posted_at ? ` ${weekdayTime(post.posted_at)}` : ''}.`;
        if (post?.status === 'scheduled' && post.scheduled_at) return `${channel.label}: scheduled ${weekdayTime(post.scheduled_at)}.`;
        return `${channel.label}: not scheduled.`;
      }).join(' ');
    case 'on_hold':
      return `Scheduled for ${firstTime()} · on hold until the event is published.`;
    case 'posting':
      return 'Posting now…';
    case 'posted':
      return `Posted ${l.at ? `${weekdayTime(l.at)} ` : ''}on ${names}.`;
    case 'failed': {
      const f = auto.find((a) => a.post?.status === 'failed');
      return `The ${f?.channel.label ?? ''} post failed. Retry posts it now.`;
    }
    default:
      return '';
  }
}

/** The line under the board naming the on channels posted by hand, '' when none is on. */
export function hasVideoByHandLine(view: Pick<WorkshopOutreach, 'channels' | 'off_channels'>): string {
  const hand = onChannels(view).filter((c) => !c.automatic).map(handChannelLabel);
  if (hand.length === 0) return '';
  return hand.length === 1
    ? `${hand[0]} is posted by hand; the site never posts it.`
    : `${list(hand)} are posted by hand; the site never posts them.`;
}

/** Splits a line around its first date so the date can be kept on one line: [before, when, after]. */
export function lineParts(text: string): [string, string, string] {
  const m = /(Sun|Mon|Tue|Wed|Thu|Fri|Sat), .*? PT/.exec(text);
  if (!m) return [text, '', ''];
  return [text.slice(0, m.index), m[0], text.slice(m.index + m[0].length)];
}
