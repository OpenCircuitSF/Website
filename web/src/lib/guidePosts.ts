// Decision logic for the admin Guide outreach section (#0660). Pure
// TypeScript, no DOM: the GuidePost*.svelte components stay thin and call in
// here. Topic data comes from the API, never from the SPA's topics module.

import { asSentence, countCharacters, isOverLimit, LINK_TOKEN, toLocalInputValue } from './outreach';
import { linkFailureReasonLabel } from './outreachLinks';
import { formatClicks, formatReactionTotal, postStatusWords } from './outreachResults';
import { formatPacificDateTime } from './pacificTime';
import type {
  GuidePost,
  GuidePostChannel,
  GuidePostCreated,
  GuidePostRow,
  GuideShare,
  GuideSignupCounts,
  GuideTopic,
} from './types';

export { asSentence, formatClicks };

/** The URL the server measures `{link}` as before a short link exists.
 * Mirrors internal/guideposts's LinkMeasuredAs (#0676). */
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
  /** "N reactions", or "—" before the tracker captures the post (#0708). */
  reactions: string;
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
      reactions: formatReactionTotal(p.engagement ?? null),
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

// ── One draft, several channels (#0665) ──

/** The channels ticked when the composer opens: every connected one. */
export function defaultTickedChannels(channels: Pick<GuidePostChannel, 'name' | 'configured'>[]): string[] {
  return channels.filter((c) => c.configured).map((c) => c.name);
}

/** The composer's create button: one draft, several, or create-and-schedule. */
export function createButtonLabel(ticked: number, scheduleValue: string): string {
  if (scheduleValue.trim() !== '') return 'Create and schedule';
  return ticked > 1 ? 'Create drafts' : 'Create draft';
}

/** What happened on one channel during a multi-channel create. */
export interface ChannelOutcome {
  channel: string;
  label: string;
  post: GuidePost | null;
  linkError: string | null;
  error: string | null;
  scheduled: boolean;
  scheduleError: string | null;
}

const errText = (e: unknown) => asSentence(e instanceof Error ? e.message : String(e));

/** Creates one draft per channel, in order, then schedules each one when
 * `schedule` is given. A failure on one channel never stops the next one. The
 * failure is recorded on that channel's outcome. This function never throws. */
export async function createAcross(
  channels: Pick<GuidePostChannel, 'name' | 'label'>[],
  create: (channel: string) => Promise<GuidePostCreated>,
  schedule: ((id: number) => Promise<unknown>) | null,
): Promise<ChannelOutcome[]> {
  const out: ChannelOutcome[] = [];
  for (const c of channels) {
    const o: ChannelOutcome = { channel: c.name, label: c.label, post: null, linkError: null, error: null, scheduled: false, scheduleError: null };
    try {
      const r = await create(c.name);
      o.post = r.post;
      o.linkError = r.link_error;
    } catch (e) {
      o.error = errText(e);
    }
    if (o.post && schedule) {
      try {
        await schedule(o.post.id);
        o.scheduled = true;
      } catch (e) {
        o.scheduleError = errText(e);
      }
    }
    out.push(o);
  }
  return out;
}

/** The status line after a multi-channel create: one sentence per channel. */
export function outcomeSummary(outcomes: ChannelOutcome[], scheduledAt: string | null): string {
  return outcomes
    .map((o) => {
      if (o.error !== null || o.post === null) return `${o.label}: the draft was not created. ${o.error ?? ''}`.trim();
      let s = `${o.label} draft created`;
      if (o.post.short_url) s += ` with ${o.post.short_url}`;
      if (o.linkError !== null)
        s += `, but its short link could not be made (${linkFailureReasonLabel(o.linkError)}); it is made when you schedule or post`;
      if (o.scheduled) s += `${o.linkError !== null ? '. It is' : ', and'} scheduled for ${scheduledAt ? formatPacificDateTime(scheduledAt) : 'the chosen time'}`;
      s += '.';
      if (o.scheduleError !== null) s += ` It was not scheduled: ${o.scheduleError}`;
      return s;
    })
    .join(' ');
}

/** True when every channel's draft was created (scheduling may still have failed). */
export function allCreated(outcomes: ChannelOutcome[]): boolean {
  return outcomes.every((o) => o.post !== null);
}

/** Posts from one create share guide, campaign and body, and were made within this window. */
export const SHARE_WINDOW_MS = 2 * 60 * 1000;

function siblings(a: GuidePostRow, b: GuidePostRow): boolean {
  return (
    a.guide_slug === b.guide_slug &&
    a.campaign_slug === b.campaign_slug &&
    a.body === b.body &&
    a.channel !== b.channel &&
    Math.abs(Date.parse(a.created_at) - Date.parse(b.created_at)) <= SHARE_WINDOW_MS
  );
}

/** Like groupPosts, but posts made by one create are joined into a share
 * (one post per channel). A share is active while any member is not posted, and it
 * takes the place of its earliest member in groupPosts' order. A post on its own
 * is a share of one. */
export function groupShares(posts: GuidePostRow[], guide: string | null): { active: GuidePostRow[][]; posted: GuidePostRow[][] } {
  const mine = guide ? posts.filter((p) => p.guide_slug === guide) : posts.slice();
  const shares: GuidePostRow[][] = [];
  const byCreated = mine.slice().sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at) || a.id - b.id);
  const taken = new Set<number>();
  for (const p of byCreated) {
    if (taken.has(p.id)) continue;
    taken.add(p.id);
    const share = [p];
    for (const q of byCreated) {
      if (taken.has(q.id)) continue;
      if (share.every((m) => siblings(m, q))) {
        share.push(q);
        taken.add(q.id);
      }
    }
    shares.push(share);
  }
  // A share sits where its earliest member falls in groupPosts' order. Inside
  // a share, the most urgent status comes first, then the channel name.
  const { active, posted } = groupPosts(mine, null);
  const order = new Map<number, number>();
  [...active, ...posted].forEach((p, i) => order.set(p.id, i));
  const rank = (p: GuidePostRow) => order.get(p.id) ?? 0;
  const statusRank = (p: GuidePostRow) => ACTIVE_RANK[p.status] ?? 9;
  const place = new Map(shares.map((s) => [s, Math.min(...s.map(rank))]));
  for (const s of shares) s.sort((a, b) => statusRank(a) - statusRank(b) || (a.channel < b.channel ? -1 : a.channel > b.channel ? 1 : 0));
  shares.sort((a, b) => (place.get(a) ?? 0) - (place.get(b) ?? 0));
  return {
    active: shares.filter((s) => s.some((p) => p.status !== 'posted')),
    posted: shares.filter((s) => s.every((p) => p.status === 'posted')),
  };
}

/** A share offers one "Schedule again" for all its channels once every member is posted. */
export function canScheduleAgainTogether(share: Pick<GuidePost, 'status'>[]): boolean {
  return share.length > 1 && share.every((p) => p.status === 'posted');
}

/** "Bluesky and Mastodon", from a share's posts. */
export function shareChannelLabels(share: Pick<GuidePost, 'channel'>[], channels: Pick<GuidePostChannel, 'name' | 'label'>[]): string {
  const labels = share.map((p) => channels.find((c) => c.name === p.channel)?.label ?? p.channel);
  if (labels.length <= 1) return labels.join('');
  return `${labels.slice(0, -1).join(', ')} and ${labels[labels.length - 1]}`;
}
