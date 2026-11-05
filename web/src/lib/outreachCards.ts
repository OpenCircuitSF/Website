// Pure logic for the workshop outreach page's post cards (#0569): grouping by
// wave, the plain status line, the summary line and which buttons a card
// shows. No DOM, so it is unit-testable; the Svelte components stay thin.
import { formatDateTime } from './admin';
import { formatPacificWeekdayTime } from './pacificTime';
import { composerActions } from './outreach';
import { adminOutreachFlyersHash } from './adminRouter';
import { sectionLine, type SectionLine } from './outreachSection';
import type { FlyerRow, OutreachChannel, OutreachPost, OutreachResults, OutreachWave, WorkshopOutreach } from './types';

export interface PostCard {
  channel: OutreachChannel;
  wave: OutreachWave;
  post: OutreachPost | null;
}
export interface WaveGroup {
  wave: OutreachWave;
  cards: PostCard[];
}

/** One group per wave in the server's order, each holding one card per channel. */
export function groupByWave(view: Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts'>): WaveGroup[] {
  return view.waves.map((wave) => ({
    wave,
    cards: view.channels.map((channel) => ({
      channel,
      wave,
      post: view.posts.find((p) => p.channel === channel.name && p.wave === wave.name) ?? null,
    })),
  }));
}

/** True when the channel posts itself (automatic and the server holds credentials). */
export function sendsItself(channel: Pick<OutreachChannel, 'automatic' | 'configured'>): boolean {
  return channel.automatic && channel.configured;
}

/** The channels switched on; view.channels also keeps an off one for its posts (#0628). */
export function onChannels(view: Pick<WorkshopOutreach, 'channels' | 'off_channels'>): OutreachChannel[] {
  const off = view.off_channels ?? [];
  return view.channels.filter((c) => !off.some((o) => o.name === c.name));
}

/** A board column's name, saying so when the channel is off (#0628). */
export function boardChannelLabel(label: string, off: boolean): string {
  return off ? `${label} (off)` : label;
}

/** The line under the board naming every off channel, '' when none is (#0628). */
export function boardOffLine(view: Pick<WorkshopOutreach, 'off_channels' | 'posts'>): string {
  const off = view.off_channels ?? [];
  if (off.length === 0) return '';
  const labels = off.map((c) => c.label);
  const list = labels.length === 1 ? labels[0] : `${labels.slice(0, -1).join(', ')} and ${labels[labels.length - 1]}`;
  const line = labels.length === 1 ? `${list} is off in Settings, so new posts and links skip it.` : `${list} are off in Settings, so new posts and links skip them.`;
  const going = view.posts.some((p) => off.some((o) => o.name === p.channel) && (p.status === 'scheduled' || p.status === 'queued'));
  return going ? `${line} Posts already scheduled there still go out.` : line;
}

export type StatusKind = 'posted' | 'scheduled' | 'not_scheduled' | 'manual' | 'failed' | 'not_drafted' | 'queued' | 'on_hold';
export interface StatusLine {
  kind: StatusKind;
  text: string;
  /** Set only for a posted post with a stored remote URL. */
  href: string | null;
}

/** "Mon, Oct 12, 10:00 AM PT" in Pacific time: weekday and date, since a
 * scheduled time can be weeks out (#0574, #0585). */
export function weekdayTime(iso: string): string {
  return formatPacificWeekdayTime(iso);
}

/** The one plain-English status line for a card. */
export function statusLine(post: OutreachPost | null, channel: Pick<OutreachChannel, 'automatic' | 'configured'>): StatusLine {
  if (post === null) return { kind: 'not_drafted', text: 'Not drafted', href: null };
  if (post.status === 'posted') {
    return {
      kind: 'posted',
      text: `Posted${post.posted_at ? ' ' + formatDateTime(post.posted_at) : ''}`,
      href: post.remote_url,
    };
  }
  if (post.status === 'failed') {
    return { kind: 'failed', text: 'Failed: the post could not be sent', href: null };
  }
  if (post.status === 'queued') return { kind: 'queued', text: 'Posting now…', href: null };
  if (post.held) return { kind: 'on_hold', text: 'On hold: the event is not published', href: null };
  if (post.status === 'scheduled' && post.scheduled_at) {
    return { kind: 'scheduled', text: `Posts automatically ${weekdayTime(post.scheduled_at)}`, href: null };
  }
  if (!sendsItself(channel) || post.status === 'manual') {
    return { kind: 'manual', text: 'Post by hand: copy and paste', href: null };
  }
  return { kind: 'not_scheduled', text: 'Not scheduled', href: null };
}

export interface BoardCell {
  channel: string;
  channelLabel: string;
  kind: StatusKind | 'off';
  /** The state in words: never empty, so colour is never the only cue. */
  status: string;
  /** The date and time where there is one, else a short qualifier or null. */
  detail: string | null;
}
export interface BoardRow {
  wave: string;
  ordinal: number;
  label: string;
  /** The section's one line (#0640). */
  line: SectionLine;
  cells: BoardCell[];
}

const when = (iso: string | null | undefined): string | null => (iso ? weekdayTime(iso) : null);

/** One post's cell on the status board. A scheduled cell names its time only
 * when the row's line does not already say it (`showTime`, #0640). */
export function boardCell(post: OutreachPost | null, channel: OutreachChannel, off = false, showTime = true): BoardCell {
  if (off && post === null) return { channel: channel.name, channelLabel: boardChannelLabel(channel.label, true), kind: 'off', status: 'Off', detail: null };
  const kind = statusLine(post, channel).kind;
  const words: Record<StatusKind, [string, string | null]> = {
    posted: ['Posted', when(post?.posted_at)],
    scheduled: ['Scheduled', showTime ? when(post?.scheduled_at) : null],
    on_hold: ['On hold', when(post?.scheduled_at)],
    queued: ['Posting now', null],
    failed: ['Failed', null],
    not_scheduled: ['Drafted', null],
    manual: ['Drafted', 'by hand'],
    not_drafted: ['Not written', null],
  };
  const [status, detail] = words[kind];
  return { channel: channel.name, channelLabel: boardChannelLabel(channel.label, off), kind, status, detail };
}

/**
 * "2 not scheduled" when part of a wave is out or on its way and some of the
 * channels that post themselves are not: no post yet, or a post with no time.
 * Empty when nothing has gone out or been scheduled, since the wave's own
 * status already says that, and when a post has failed or is sending only.
 */
export function notScheduledNote(view: Pick<WorkshopOutreach, 'channels' | 'posts' | 'off_channels'>, wave: string): string {
  const own = onChannels(view).filter(sendsItself).map((c) => view.posts.find((p) => p.channel === c.name && p.wave === wave) ?? null);
  const under = own.filter((p) => p === null || p.status === 'draft' || p.status === 'manual').length;
  // A post that has left drafting counts as started even on an off channel, since the strip reports it.
  const started = view.channels.filter(sendsItself).some((c) => view.posts.some((p) => p.channel === c.name && p.wave === wave && (p.status === 'scheduled' || p.status === 'posted')));
  return under > 0 && started ? `${under} not scheduled` : '';
}

export function sectionRowId(name: string): string { return `outreach-section-${name}`; }
export function sectionHeadId(name: string): string { return `outreach-section-${name}-head`; }
export function sectionPanelId(name: string): string { return `outreach-section-${name}-panel`; }

/** True when every channel has a post in this section and all have gone out (#0625). */
export function sectionFullyPosted(view: Pick<WorkshopOutreach, 'channels' | 'posts' | 'off_channels'>, section: string): boolean {
  const on = onChannels(view);
  return on.length > 0
    && on.every((c) => view.posts.some((p) => p.channel === c.name && p.wave === section && p.status === 'posted'))
    && view.posts.every((p) => p.wave !== section || p.status === 'posted');
}

/** The section open when the page loads: the first not fully posted, else the last (#0625). */
export function defaultOpenSection(view: Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts' | 'off_channels'>): string {
  const first = view.waves.find((w) => !sectionFullyPosted(view, w.name));
  return first?.name ?? view.waves[view.waves.length - 1]?.name ?? '';
}

/** The status board: one row per wave, one cell per channel, in the server's order. */
export function boardRows(view: Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts' | 'off_channels'>): BoardRow[] {
  return groupByWave(view).map(({ wave, cards }, i) => {
    const line = sectionLine(view, wave.name);
    const showTime = line.kind === 'mixed' || line.kind === 'different_times';
    return {
      wave: wave.name,
      ordinal: i + 1,
      label: wave.label,
      line,
      cells: cards.map((c) => boardCell(c.post, c.channel, (view.off_channels ?? []).some((o) => o.name === c.channel.name), showTime)),
    };
  });
}

export interface CardButtons {
  edit: boolean;
  postNow: boolean;
  schedule: boolean;
  unschedule: boolean;
  retry: boolean;
  /** Copy text only (#0626): needs no link, so it does not wait for one. */
  copyText: boolean;
  markPosted: boolean;
  /** Take back a Mark posted (#0588): only a hand-posted channel's posted card. */
  unmark: boolean;
  /** Delete the post from its network and return it to draft (#0604): an automatic channel's posted card. */
  takedown: boolean;
  draft: boolean;
}

/** Which buttons a card shows; the rules themselves come from composerActions. */
export function cardButtons(post: OutreachPost | null, channel: OutreachChannel, available: boolean): CardButtons {
  const none: CardButtons = { edit: false, postNow: false, schedule: false, unschedule: false, retry: false, copyText: false, markPosted: false, unmark: false, takedown: false, draft: false };
  if (post === null) return { ...none, draft: available };
  const a = composerActions(post, channel, available);
  const auto = sendsItself(channel);
  const posted = post.status === 'posted';
  const queued = post.status === 'queued';
  return {
    ...none,
    edit: !posted,
    postNow: auto && a.canPostNow,
    schedule: auto && a.canSchedule && !a.canRetry,
    unschedule: auto && a.canUnschedule,
    retry: auto && a.canRetry,
    copyText: !auto && !posted && !queued,
    markPosted: !auto && a.canMarkPosted,
    unmark: !auto && posted,
    takedown: auto && posted,
  };
}

/** "Drafted: Announce 5, Reminder 2 · 0 scheduled · 0 posted · 0 clicks". Waves with no posts are left out; clicks are shown only when real. With no posts at all it is the empty state, never a campaign slug (#0582). */
export function summaryLine(view: Pick<WorkshopOutreach, 'posts'> & { waves?: OutreachWave[] }, results: OutreachResults | null): string {
  if (view.posts.length === 0) return 'No posts yet. Start with the announcement.';
  const n = (s: string) => view.posts.filter((p) => p.status === s).length;
  const perWave: string[] = [];
  const names = [...new Set([...(view.waves ?? []).map((w) => w.name), ...view.posts.map((p) => p.wave)])];
  for (const name of names) {
    const count = view.posts.filter((p) => p.wave === name).length;
    if (count > 0) perWave.push(`${view.waves?.find((w) => w.name === name)?.label ?? name} ${count}`);
  }
  const parts = [
    `Drafted: ${perWave.join(', ')}`,
    `${n('scheduled')} scheduled`,
    `${n('posted')} posted`,
  ];
  if (results && results.status === 'ok' && results.totals) {
    const c = results.totals.clicks;
    parts.push(`${c} ${c === 1 ? 'click' : 'clicks'}`);
  }
  return parts.join(' · ');
}

/** Polling backoff while a post is queued (#0581): 1 s, 2 s, 4 s, then every 10 s. */
export function pollDelay(attempt: number): number {
  return [1000, 2000, 4000][attempt] ?? 10_000;
}

/** Automatic, configured cards in a wave that can post right now (Post now or Retry), in the server's channel order. */
export function postableAutomatic(
  view: Pick<WorkshopOutreach, 'channels' | 'waves' | 'posts' | 'available'>,
  wave: string,
): { post: OutreachPost; channel: OutreachChannel }[] {
  const out: { post: OutreachPost; channel: OutreachChannel }[] = [];
  for (const channel of view.channels) {
    const post = view.posts.find((p) => p.channel === channel.name && p.wave === wave);
    if (!post) continue;
    const b = cardButtons(post, channel, view.available);
    if (b.postNow || b.retry) out.push({ post, channel });
  }
  return out;
}

/** A card's own feedback line (#0581). `status` is the post status it was written under. */
export interface CardNotice {
  text: string;
  kind: 'status' | 'error';
  status: string | null;
}

/** The notice text, only while the post is still in the state it was written for, so "Queued…" vanishes once polling brings back posted or failed. */
export function visibleNotice(n: CardNotice | null, post: Pick<OutreachPost, 'status'> | null): string {
  if (!n) return '';
  return (post?.status ?? null) === n.status ? n.text : '';
}

export type PostDialogMode = 'post' | 'retry' | 'markPosted' | 'unmark' | 'takedown';
export interface PostDialogCopy {
  title: string;
  warning: string;
  accountLine: string[];
  confirmLabel: string;
}

/** Wording for the post confirmation dialog (#0581). */
export function postDialogCopy(mode: PostDialogMode, channels: { label: string; account: string }[]): PostDialogCopy {
  if (mode === 'markPosted') {
    const label = channels[0]?.label ?? 'the channel';
    return {
      title: `Mark as posted on ${label}?`,
      warning: `Records that you posted this on ${label} by hand. Edit and Copy go away.`,
      accountLine: [],
      confirmLabel: 'Mark posted',
    };
  }
  if (mode === 'unmark') {
    const label = channels[0]?.label ?? 'the channel';
    return {
      title: `Unmark the ${label} post?`,
      warning: `Puts this back to a post you still have to make by hand. Edit and Copy come back. Nothing is removed from ${label}; delete the post there yourself if you took it down.`,
      accountLine: [],
      confirmLabel: 'Unmark',
    };
  }
  if (mode === 'takedown') {
    const label = channels[0]?.label ?? 'the channel';
    return {
      title: `Delete this post from ${label}?`,
      warning: 'It will be removed for everyone, and the post goes back to draft so you can post it again.',
      accountLine: [],
      confirmLabel: 'Delete post',
    };
  }
  const accountLine = channels.map((c) =>
    c.account ? `Posts publicly as ${c.account} on ${c.label}` : `Posts publicly from the ${c.label} account set in Settings`,
  );
  const names = channels.map((c) => c.label).join(' and ');
  return {
    title: mode === 'retry' ? `Retry posting to ${names}?` : `Post to ${names} now?`,
    warning: 'This goes out publicly, right now, and cannot be unsent.',
    accountLine,
    confirmLabel: mode === 'retry' ? 'Retry' : 'Post now',
  };
}

/** "Bluesky: queued · Mastodon: could not post (reason)". */
export function combinedPostResult(results: { label: string; ok: boolean; error?: string }[]): string {
  return results.map((r) => `${r.label}: ${r.ok ? 'queued' : `could not post${r.error ? ` (${r.error})` : ''}`}`).join(' · ');
}

/** The Copy buttons flip to "Copied" for two seconds. */
export function copyLabel(base: string, copied: boolean): string {
  return copied ? 'Copied' : base;
}

/**
 * Where the Instagram card's flyer link goes (#0588): this workshop's newest
 * flyer when it has one, otherwise the flyers page with this workshop
 * preselected in the "new flyer" form. `flyers` is newest first.
 */
export function flyerLinkFor(flyers: Pick<FlyerRow, 'id' | 'workshop_id'>[], workshopId: number): { href: string; label: string } {
  const own = flyers.find((f) => f.workshop_id === workshopId);
  return own
    ? { href: adminOutreachFlyersHash(own.id), label: 'Open flyer' }
    : { href: adminOutreachFlyersHash(undefined, workshopId), label: 'Make a flyer' };
}
