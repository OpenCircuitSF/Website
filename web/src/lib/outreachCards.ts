// Pure logic for the workshop outreach page's post cards (#0569): grouping by
// wave, the plain status line, the summary line and which buttons a card
// shows. No DOM, so it is unit-testable; the Svelte components stay thin.
import { formatDateTime } from './admin';
import { formatPacificWeekdayTime } from './pacificTime';
import { composerActions } from './outreach';
import { adminOutreachFlyersHash } from './adminRouter';
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
  if (post.held) return { kind: 'on_hold', text: 'On hold: the workshop is not published', href: null };
  if (post.status === 'scheduled' && post.scheduled_at) {
    return { kind: 'scheduled', text: `Posts automatically ${weekdayTime(post.scheduled_at)}`, href: null };
  }
  if (!sendsItself(channel) || post.status === 'manual') {
    return { kind: 'manual', text: 'Post by hand: copy and paste', href: null };
  }
  return { kind: 'not_scheduled', text: 'Not scheduled', href: null };
}

export interface CardButtons {
  edit: boolean;
  postNow: boolean;
  schedule: boolean;
  unschedule: boolean;
  retry: boolean;
  copy: boolean;
  markPosted: boolean;
  /** Take back a Mark posted (#0588): only a hand-posted channel's posted card. */
  unmark: boolean;
  draft: boolean;
}

/** Which buttons a card shows; the rules themselves come from composerActions. */
export function cardButtons(post: OutreachPost | null, channel: OutreachChannel, available: boolean): CardButtons {
  const none: CardButtons = { edit: false, postNow: false, schedule: false, unschedule: false, retry: false, copy: false, markPosted: false, unmark: false, draft: false };
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
    copy: !auto && !posted && !queued && post.prepared,
    markPosted: !auto && a.canMarkPosted,
    unmark: !auto && posted,
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

export type PostDialogMode = 'post' | 'retry' | 'markPosted' | 'unmark';
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
 * Whether a hand-posted card shows its "Put this in your bio" link row (#0588):
 * only when the post has a short link and the body does not already carry it
 * (TikTok and YouTube captions print the sign-up link themselves).
 */
export function showBioLink(post: Pick<OutreachPost, 'short_url' | 'body'> | null, channel: Pick<OutreachChannel, 'automatic' | 'configured'>): boolean {
  return post !== null && !!post.short_url && !sendsItself(channel) && !post.body.includes(post.short_url);
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
