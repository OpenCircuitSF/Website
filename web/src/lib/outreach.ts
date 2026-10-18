// Decision logic for the workshop Outreach panel (#0544). Pure TypeScript, no
// DOM: OutreachPanel.svelte and OutreachComposer.svelte stay thin and call in
// here. The channel and wave lists are not duplicated here -- the server
// sends them with every panel view.

import { formatPacificWeekdayTime, fromPacificInput, toPacificInput } from './pacificTime';
import type { OutreachChannel, OutreachPost, OutreachWave, WorkshopOutreach } from './types';

/** The placeholder a draft carries until Prepare swaps in the real short URL.
 * Mirrors internal/outreach's LinkToken. */
export const LINK_TOKEN = '{link}';

/** What an unprepared link is assumed to cost in characters. */
export const LINK_ESTIMATE = 30;

/** Mastodon counts every URL as this many characters regardless of length. */
const MASTODON_URL_COST = 23;

const URL_RE = /https?:\/\/\S+/g;

function graphemes(s: string): number {
  if (typeof Intl !== 'undefined' && 'Segmenter' in Intl) {
    const seg = new Intl.Segmenter(undefined, { granularity: 'grapheme' });
    let n = 0;
    for (const _ of seg.segment(s)) n++;
    return n;
  }
  return Array.from(s).length;
}

/**
 * The post's length as the channel counts it. Bluesky and the manual channels
 * count graphemes; Mastodon counts code points with every URL as 23.
 * Before Prepare, each LINK_TOKEN is estimated and `exact` is false so the
 * UI can prefix the number with an approximation sign.
 */
export function countCharacters(
  channel: Pick<OutreachChannel, 'name'>,
  body: string,
  prepared: boolean,
): { count: number; exact: boolean } {
  const tokens = body.split(LINK_TOKEN).length - 1;
  const exact = prepared || tokens === 0;
  if (channel.name === 'mastodon') {
    // Tokens are replaced by a placeholder URL-shaped string of the right cost
    // after the real URLs have been collapsed, so neither is double counted.
    const withoutTokens = body.split(LINK_TOKEN).join('');
    let count = Array.from(withoutTokens.replace(URL_RE, '')).length;
    count += (withoutTokens.match(URL_RE) ?? []).length * MASTODON_URL_COST;
    count += tokens * MASTODON_URL_COST;
    return { count, exact };
  }
  const withoutTokens = body.split(LINK_TOKEN).join('');
  return { count: graphemes(withoutTokens) + tokens * LINK_ESTIMATE, exact };
}

/** True when a counted length exceeds the channel's limit; a limit of 0 means none. */
export function isOverLimit(channel: Pick<OutreachChannel, 'char_limit'>, count: number): boolean {
  return channel.char_limit > 0 && count > channel.char_limit;
}

export interface ComposerActions {
  canSave: boolean;
  canRegenerate: boolean;
  canPrepare: boolean;
  canCopy: boolean;
  canMarkPosted: boolean;
  canDelete: boolean;
  /** #0545: send through the server to Bluesky or Mastodon now. */
  canPostNow: boolean;
  /** #0545: the same call as Post now, for a failed post; the UI labels it Retry. */
  canRetry: boolean;
  canSchedule: boolean;
  canUnschedule: boolean;
}

/**
 * Which composer buttons apply. A queued post is locked: every mutating
 * action is off while it is in flight or awaiting a retry. Automatic sending
 * needs an automatic channel the server has credentials for; an automatic
 * channel without them gets exactly the manual matrix (Copy content, Mark
 * posted). Where it can send, Mark posted remains only as a by-hand fallback
 * for a draft or failed post.
 */
export function composerActions(
  post: Pick<OutreachPost, 'status' | 'prepared' | 'stale'>,
  channel: Pick<OutreachChannel, 'automatic' | 'configured'>,
  available: boolean,
): ComposerActions {
  const posted = post.status === 'posted';
  const queued = post.status === 'queued';
  const locked = posted || queued;
  const canSend = channel.automatic && channel.configured && available && post.prepared;
  const draftLike = post.status === 'draft' || post.status === 'scheduled';
  const sendsItself = channel.automatic && channel.configured;
  return {
    canSave: !locked,
    canRegenerate: post.stale && !locked,
    canPrepare: available && !post.prepared && !posted,
    canCopy: post.prepared && !posted,
    canMarkPosted: post.prepared && !locked && (!sendsItself || post.status === 'draft' || post.status === 'failed'),
    canDelete: !locked,
    canPostNow: canSend && draftLike,
    canRetry: canSend && post.status === 'failed',
    canSchedule: canSend && (draftLike || post.status === 'failed'),
    canUnschedule: post.status === 'scheduled',
  };
}

/**
 * The time to prefill the schedule picker with: the wave's suggested time,
 * unless the wave is announce (whose primary action is Post now), the
 * workshop has no start, or the suggestion has already passed. Null leaves the
 * picker empty.
 */
export function defaultScheduleAt(waveName: string, waves: OutreachWave[], now: Date): string | null {
  if (waveName === 'announce') return null;
  const at = waves.find((w) => w.name === waveName)?.suggested_at ?? null;
  if (at === null) return null;
  const t = Date.parse(at);
  return Number.isNaN(t) || t <= now.getTime() ? null : at;
}

/**
 * The hint under an empty schedule picker (#0587). Announce usually goes out
 * now; a suggestion that has passed is named with its date; otherwise the wave
 * has no suggestion at all.
 */
export function scheduleHint(waveName: string, waves: OutreachWave[]): string {
  if (waveName === 'announce') return 'The announcement usually goes out now. Pick a time only if you want to wait.';
  const at = waves.find((w) => w.name === waveName)?.suggested_at ?? null;
  if (at !== null && !Number.isNaN(Date.parse(at))) {
    return `The suggested time (${formatPacificWeekdayTime(at)}) has passed. Pick one.`;
  }
  return 'There is no suggested time for this wave. Pick one.';
}

/** An ISO instant as the value of a datetime-local input, in Pacific time (#0585). */
export function toLocalInputValue(iso: string): string {
  return toPacificInput(iso);
}

/** The inverse of toLocalInputValue: a Pacific wall-clock value as an RFC 3339 UTC instant, or null when unparseable. */
export function fromLocalInputValue(value: string): string | null {
  return fromPacificInput(value);
}

/** True while any post is queued, so the panel keeps refreshing until the publisher has acted. */
export function shouldPoll(view: Pick<WorkshopOutreach, 'posts'>): boolean {
  return view.posts.some((p) => p.status === 'queued');
}

/** The note under a channel that cannot post by itself, or null. */
export function channelStatusNote(channel: Pick<OutreachChannel, 'automatic' | 'configured'>): string | null {
  return channel.automatic && !channel.configured ? 'Not configured — post by hand' : null;
}

/** The UTM chips, in source, medium, campaign, content order. */
export function utmChips(post: Pick<OutreachPost, 'utm'>): { label: string; value: string }[] {
  return [
    { label: 'utm_source', value: post.utm.source },
    { label: 'utm_medium', value: post.utm.medium },
    { label: 'utm_campaign', value: post.utm.campaign },
    { label: 'utm_content', value: post.utm.content },
  ];
}

/** The explanation shown when the panel is unavailable. */
export function unavailableMessage(reason: string): string {
  switch (reason) {
    case 'shortlinks_not_configured':
      return 'Outreach needs the ShortLinks connection, which is not configured. Set it up under Settings → Outreach connections.';
    case 'workshop_not_published':
      return 'Publish this workshop to start outreach.';
    default:
      return 'Outreach is not available right now.';
  }
}

/** Error text for a bad post URL, or null when empty or acceptable. */
export function validateRemoteUrl(s: string): string | null {
  const v = s.trim();
  if (v === '') return null;
  if (v.length > 2048) return 'That URL is too long.';
  try {
    const u = new URL(v);
    if ((u.protocol !== 'http:' && u.protocol !== 'https:') || u.host === '') {
      return 'Enter a full http(s) URL, or leave it blank.';
    }
  } catch {
    return 'Enter a full http(s) URL, or leave it blank.';
  }
  return null;
}
