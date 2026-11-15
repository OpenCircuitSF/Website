// Decision logic for the admin Guide outreach section (#0660). Pure
// TypeScript, no DOM: the GuidePost*.svelte components stay thin and call in
// here. Topic data comes from the API, never from the SPA's topics module.

import { asSentence, countCharacters, isOverLimit, LINK_TOKEN, toLocalInputValue } from './outreach';
import { linkFailureReasonLabel } from './outreachLinks';
import { formatClicks, postStatusWords } from './outreachResults';
import { formatPacificDateTime } from './pacificTime';
import type { GuidePost, GuidePostChannel, GuidePostRow, GuideShare, GuideSignupCounts, GuideTopic } from './types';

export { asSentence, formatClicks };

/** The URL the server measures `{link}` as before a short link exists.
 * Mirrors internal/guideposts's sampleShortURL. */
export const GUIDE_LINK_SAMPLE = 'https://go.opencircuitsf.com/u/XXXXXXXXXXXX';

/** A body's length with its link included: the real short URL once it
 * exists, else the full-length sample (and `exact` false). */
export function countWithLink(
  channel: Pick<GuidePostChannel, 'name'>,
  body: string,
  shortUrl: string | null,
): { count: number; exact: boolean } {
  const hasToken = body.includes(LINK_TOKEN);
  const filled = body.split(LINK_TOKEN).join(shortUrl ?? GUIDE_LINK_SAMPLE);
  const { count } = countCharacters(channel, filled, true);
  return { count, exact: !hasToken || shortUrl !== null };
}

/** "212 / 300 characters, including the short link", with "≈ " before an estimate. */
export function counterText(channel: GuidePostChannel, body: string, shortUrl: string | null): string {
  const { count, exact } = countWithLink(channel, body, shortUrl);
  const prefix = exact ? '' : '≈ ';
  const n = channel.char_limit > 0 ? `${count} / ${channel.char_limit} characters` : `${count} characters`;
  const link = body.includes(LINK_TOKEN) || shortUrl !== null ? ', including the short link' : '';
  return `${prefix}${n}${link}`;
}

/** True when the body, link included, is over the channel's limit. */
export function overLimit(channel: GuidePostChannel, body: string, shortUrl: string | null): boolean {
  return isOverLimit(channel, countWithLink(channel, body, shortUrl).count);
}

const CAMPAIGN_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/** Blank (the server's default campaign) or a lowercase-hyphen slug of at most 100 characters. */
export function isValidCampaign(s: string): boolean {
  const t = s.trim();
  return t === '' || (t.length <= 100 && CAMPAIGN_RE.test(t));
}

export const campaignHint = 'Lowercase letters, digits and single hyphens, for example guides-2026-11.';

/** The draft text after the guide changes: replaced only when it was not edited. */
export function bodyAfterGuideChange(current: string, prevDefault: string, nextDefault: string): string {
  return current === prevDefault || current.trim() === '' ? nextDefault : current;
}

/** A datetime-local value for tomorrow's Pacific date at `hour:00`. */
export function defaultScheduleAt(now: Date, hour: number): string {
  const today = toLocalInputValue(now.toISOString()).slice(0, 10);
  const [y, m, d] = today.split('-').map(Number);
  const next = new Date(Date.UTC(y, m - 1, d + 1));
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${next.getUTCFullYear()}-${pad(next.getUTCMonth() + 1)}-${pad(next.getUTCDate())}T${pad(hour)}:00`;
}

export type PostAction = 'edit' | 'schedule' | 'unschedule' | 'postNow' | 'retry' | 'delete' | 'open' | 'again';

/** The actions a post offers, in display order. Schedule, Post now and Retry need a connected channel. */
export function postActions(
  post: Pick<GuidePost, 'status' | 'remote_url'>,
  configured: boolean,
): { action: PostAction; enabled: boolean }[] {
  let list: PostAction[];
  switch (post.status) {
    case 'draft':
      list = ['edit', 'schedule', 'postNow', 'delete'];
      break;
    case 'scheduled':
      list = ['edit', 'unschedule', 'postNow'];
      break;
    case 'failed':
      list = ['edit', 'retry', 'schedule', 'delete'];
      break;
    case 'posted':
      list = post.remote_url ? ['open', 'again'] : ['again'];
      break;
    default:
      list = [];
  }
  const needsChannel: PostAction[] = ['schedule', 'postNow', 'retry'];
  return list.map((action) => ({ action, enabled: needsChannel.includes(action) ? configured : true }));
}

export const ACTION_LABELS: Record<PostAction, string> = {
  edit: 'Edit',
  schedule: 'Schedule…',
  unschedule: 'Unschedule',
  postNow: 'Post now…',
  retry: 'Retry…',
  delete: 'Delete…',
  open: 'Open post',
  again: 'Schedule again…',
};

/** The note under an unconnected channel, or null when it is connected. */
export function notConnectedNote(channel: Pick<GuidePostChannel, 'label' | 'configured'>): string | null {
  if (channel.configured) return null;
  return `${channel.label} is not connected. You can write drafts; connect it under Settings → Outreach to schedule or post.`;
}

/** A post's status in words, with its time when scheduled or posted. */
export function statusLine(post: Pick<GuidePost, 'status' | 'scheduled_at' | 'posted_at'>): string {
  if (post.status === 'queued') return 'Posting…';
  const words = postStatusWords(post.status);
  if (post.status === 'scheduled' && post.scheduled_at) return `${words} · ${formatPacificDateTime(post.scheduled_at)}`;
  if (post.status === 'posted' && post.posted_at) return `${words} · ${formatPacificDateTime(post.posted_at)}`;
  return words;
}

/** The warning after a draft whose short link could not be made, or null. */
export function linkErrorMessage(reason: string | null): string | null {
  if (reason === null) return null;
  return `Draft saved, but its short link could not be made (${linkFailureReasonLabel(reason)}). It is made when you schedule or post.`;
}

/** Why click counts are missing, or null when they are not. */
export function clicksErrorMessage(reason: string | null): string | null {
  if (reason === null) return null;
  if (reason === 'not_configured')
    return 'Click counts need the ShortLinks connection, which is not configured. Set it up under Settings → Outreach.';
  if (reason === 'rejected')
    return "ShortLinks rejected this server's API token, so click counts are missing. Check it under Settings → Outreach.";
  return `Some click counts are unavailable right now (${linkFailureReasonLabel(reason)}).`;
}

/** Why signup counts are missing, or null. */
export function signupsErrorMessage(err: string | null): string | null {
  return err === null ? null : 'Signup counts could not be loaded right now.';
}

/** "N signups, M confirmed", or "—" when unknown. */
export function formatSignups(s: GuideSignupCounts | null): string {
  if (s === null) return '—';
  return `${s.requested} ${s.requested === 1 ? 'signup' : 'signups'}, ${s.confirmed} confirmed`;
}

export type ShareSort = 'oldest' | 'newest' | 'clicks' | 'catalog';

export const SHARE_SORTS: { id: ShareSort; label: string }[] = [
  { id: 'oldest', label: 'Oldest share first' },
  { id: 'newest', label: 'Newest share first' },
  { id: 'clicks', label: 'Most clicks' },
  { id: 'catalog', label: 'Guide order' },
];

/** The overview rows in the chosen order; stable, and the input is not mutated. */
export function sortShares(rows: GuideShare[], sort: ShareSort): GuideShare[] {
  const indexed = rows.map((row, i) => ({ row, i }));
  const t = (r: GuideShare) => (r.last_posted_at ? Date.parse(r.last_posted_at) : null);
  const cmp = (a: { row: GuideShare; i: number }, b: { row: GuideShare; i: number }): number => {
    if (sort === 'catalog') return a.i - b.i;
    if (sort === 'clicks') {
      const ca = a.row.clicks;
      const cb = b.row.clicks;
      if (ca === null && cb === null) return a.i - b.i;
      if (ca === null) return 1;
      if (cb === null) return -1;
      return cb - ca || a.i - b.i;
    }
    const ta = t(a.row);
    const tb = t(b.row);
    if (ta === null && tb === null) return a.i - b.i;
    if (ta === null) return sort === 'oldest' ? -1 : 1;
    if (tb === null) return sort === 'oldest' ? 1 : -1;
    return (sort === 'oldest' ? ta - tb : tb - ta) || a.i - b.i;
  };
  return indexed.sort(cmp).map((x) => x.row);
}

/** "Never", or when the guide was last posted. */
export function shareLastPosted(row: Pick<GuideShare, 'last_posted_at'>): string {
  return row.last_posted_at ? formatPacificDateTime(row.last_posted_at) : 'Never';
}

const ACTIVE_RANK: Record<string, number> = { queued: 0, failed: 1, scheduled: 2, draft: 3 };

/** Posts split into active (queued, failed, scheduled, drafts) and posted, optionally for one guide. */
export function groupPosts(posts: GuidePostRow[], guide: string | null): { active: GuidePostRow[]; posted: GuidePostRow[] } {
  const mine = guide ? posts.filter((p) => p.guide_slug === guide) : posts.slice();
  const ms = (s: string | null) => (s ? Date.parse(s) : 0);
  const active = mine
    .filter((p) => p.status !== 'posted')
    .sort((a, b) => {
      const r = (ACTIVE_RANK[a.status] ?? 9) - (ACTIVE_RANK[b.status] ?? 9);
      if (r !== 0) return r;
      if (a.status === 'scheduled') return ms(a.scheduled_at) - ms(b.scheduled_at);
      if (a.status === 'draft') return ms(b.created_at) - ms(a.created_at);
      return 0;
    });
  const posted = mine.filter((p) => p.status === 'posted').sort((a, b) => ms(b.posted_at) - ms(a.posted_at));
  return { active, posted };
}

function addSignups(a: GuideSignupCounts | null, b: GuideSignupCounts | null): GuideSignupCounts | null {
  if (a === null || b === null) return null;
  return { requested: a.requested + b.requested, confirmed: a.confirmed + b.confirmed };
}

export interface CampaignTotal {
  campaign: string;
  posts: number;
  clicks: number | null;
  signups: GuideSignupCounts | null;
}

/** Clicks and signups summed per campaign over posts that have a short link; null when any is unknown. */
export function campaignTotals(posts: GuidePostRow[]): CampaignTotal[] {
  const by = new Map<string, CampaignTotal>();
  for (const p of posts) {
    if (!p.short_url) continue;
    const cur = by.get(p.campaign_slug);
    if (!cur) {
      by.set(p.campaign_slug, { campaign: p.campaign_slug, posts: 1, clicks: p.clicks, signups: p.signups });
      continue;
    }
    cur.posts += 1;
    cur.clicks = cur.clicks === null || p.clicks === null ? null : cur.clicks + p.clicks;
    cur.signups = addSignups(cur.signups, p.signups);
  }
  return [...by.values()].sort((a, b) => (a.campaign < b.campaign ? 1 : a.campaign > b.campaign ? -1 : 0));
}

export interface PostResultRow {
  id: number;
  guide: string;
  channel: string;
  campaign: string;
  status: string;
  posted_at: string | null;
  clicks: number | null;
  signups: GuideSignupCounts | null;
  short_url: string;
}

/** One row per linked post: newest posted first, then unposted. */
export function postResultRows(
  posts: GuidePostRow[],
  topics: Pick<GuideTopic, 'slug' | 'title'>[],
  channels: Pick<GuidePostChannel, 'name' | 'label'>[] = [],
): PostResultRow[] {
  const title = (slug: string) => topics.find((t) => t.slug === slug)?.title ?? slug;
  const label = (name: string) => channels.find((c) => c.name === name)?.label ?? name;
  return posts
    .filter((p) => p.short_url)
    .map((p) => ({
      id: p.id,
      guide: title(p.guide_slug),
      channel: label(p.channel),
      campaign: p.campaign_slug,
      status: postStatusWords(p.status),
      posted_at: p.posted_at,
      clicks: p.clicks,
      signups: p.signups,
      short_url: p.short_url as string,
    }))
    .sort((a, b) => {
      if (a.posted_at && b.posted_at) return Date.parse(b.posted_at) - Date.parse(a.posted_at);
      if (a.posted_at) return -1;
      if (b.posted_at) return 1;
      return 0;
    });
}

/** True while any post is queued, so the page keeps refreshing. */
export function hasQueued(posts: Pick<GuidePost, 'status'>[]): boolean {
  return posts.some((p) => p.status === 'queued');
}
