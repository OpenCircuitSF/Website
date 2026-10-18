// Pure helpers behind the Outreach results screens (#0546): shaping the
// server's daily series into an SVG line chart, and the wording for each
// status. No DOM, so it is all unit-testable; the Svelte components stay thin.
//
// Days are UTC throughout, matching ShortLinks' own day buckets. The chart
// geometry and tick helpers follow the charts module in the ShortLinks repo.
import { isSafeLinkHref } from './linkSafety';
import type {
  OutreachMention,
  OutreachReactions,
  OutreachResults,
  OutreachResultsFlyerLocation,
  OutreachResultsPost,
  OutreachResultsStatus,
  OutreachResultsSummaryRow,
} from './types';

export interface ChartGeo {
  width: number;
  height: number;
  padLeft: number;
  padRight: number;
  padTop: number;
  padBottom: number;
}

export const CHART_GEO: ChartGeo = { width: 640, height: 220, padLeft: 40, padRight: 24, padTop: 16, padBottom: 32 };

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/** "2026-10-03" -> "Oct 3". Returns the input unchanged if it is not a date. */
export function dayLabel(date: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!m) return date;
  return `${MONTHS[Number(m[2]) - 1] ?? m[2]} ${Number(m[3])}`;
}

/** Every "YYYY-MM-DD" from `from` up to, not including, `toExclusive`, walking UTC only. */
export function utcDays(from: string, toExclusive: string): string[] {
  const start = Date.parse(`${from}T00:00:00Z`);
  const end = Date.parse(`${toExclusive}T00:00:00Z`);
  if (Number.isNaN(start) || Number.isNaN(end)) return [];
  const out: string[] = [];
  for (let t = start; t < end; t += 86_400_000) out.push(new Date(t).toISOString().slice(0, 10));
  return out;
}

/** One count per date, 0 where the series has no entry (ShortLinks omits zero days). */
export function fillSeries(days: { date: string; count: number }[], dates: string[]): number[] {
  const byDate = new Map<string, number>();
  for (const d of days) byDate.set(d.date, (byDate.get(d.date) ?? 0) + d.count);
  return dates.map((d) => byDate.get(d) ?? 0);
}

/** The color of one channel: the n-th channel in server order gets --series-n. */
export function channelColor(name: string, channelNames: string[]): string {
  const i = channelNames.indexOf(name);
  return i < 0 ? 'var(--series-other)' : `var(--series-${Math.min(i + 1, 6)})`;
}

/** A series' color: its channel's, or the neutral one for "Other links". */
export function seriesColor(series: { id: string; is_other: boolean }, channelNames: string[]): string {
  return series.is_other ? 'var(--series-other)' : channelColor(series.id, channelNames);
}

/** Integer ticks from 0 up to a rounded-up max (the last tick is the chart's top). */
export function yAxisTicks(max: number, count = 4): number[] {
  const top = Math.max(1, Math.ceil(max));
  const raw = Math.max(1, Math.ceil(top / count));
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 5, 10].map((f) => f * mag).find((s) => s >= raw) ?? raw;
  const ticks = [0];
  while (ticks[ticks.length - 1] < top) ticks.push(ticks[ticks.length - 1] + step);
  return ticks;
}

export interface ChartLine {
  id: string;
  label: string;
  color: string;
  /** SVG `points` attribute. */
  points: string;
  total: number;
}

export interface ChartMarker {
  x: number;
  date: string;
  color: string;
  labels: string[];
}

export interface ChartModel {
  geo: ChartGeo & { innerW: number; innerH: number };
  dates: string[];
  /** No clicks anywhere in the window. */
  empty: boolean;
  max: number;
  xLabels: { x: number; label: string; anchor: 'start' | 'middle' | 'end' }[];
  yTicks: { y: number; value: number }[];
  lines: ChartLine[];
  markers: ChartMarker[];
  partialLastDay: boolean;
  /** One entry per date with a click, for the screen-reader table. */
  rows: { date: string; label: string; values: number[] }[];
}

/** "last-call" -> "Last call". The server sends wave names, not labels. */
export function waveLabel(name: string): string {
  const s = name.replace(/-/g, ' ');
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function round1(n: number): string {
  return (Math.round(n * 10) / 10).toString();
}

/** Shape a results payload for drawing. Null when there is nothing to draw: status is not ok, or no window. */
export function buildChart(results: OutreachResults, geo: ChartGeo = CHART_GEO): ChartModel | null {
  if (results.status !== 'ok' || !results.window) return null;
  const dates = utcDays(results.window.from, results.window.to);
  if (dates.length === 0) return null;
  const innerW = geo.width - geo.padLeft - geo.padRight;
  const innerH = geo.height - geo.padTop - geo.padBottom;
  const n = dates.length;
  const x = (i: number) => (n === 1 ? geo.padLeft + innerW / 2 : geo.padLeft + (i * innerW) / (n - 1));
  const names = results.channels.map((c) => c.name);

  const filled = results.series.map((s) => ({ s, values: fillSeries(s.days, dates) }));
  const peak = Math.max(1, ...filled.flatMap((f) => f.values));
  const yTicksValues = yAxisTicks(peak);
  const max = yTicksValues[yTicksValues.length - 1];
  const y = (v: number) => geo.padTop + innerH - (v / max) * innerH;

  const lines: ChartLine[] = filled.map(({ s, values }) => ({
    id: s.id,
    label: s.label,
    color: seriesColor(s, names),
    points: values.map((v, i) => `${round1(x(i))},${round1(y(v))}`).join(' '),
    total: values.reduce((a, b) => a + b, 0),
  }));

  // About six x labels, always including the last day.
  const step = Math.max(1, Math.ceil((n - 1) / 5));
  const idx: number[] = [];
  for (let i = 0; i < n - 1; i += step) idx.push(i);
  if (idx.length > 0 && n - 1 - idx[idx.length - 1] < Math.ceil(step / 2)) idx.pop();
  idx.push(n - 1);
  // The last label is right-aligned to the plot edge, so it is never clipped (#0590).
  const xLabels = idx.map((i) => ({ x: x(i), label: dayLabel(dates[i]), anchor: (i === n - 1 && n > 1 ? 'end' : 'middle') as 'end' | 'middle' }));

  // A marker per UTC day on which something was posted, inside the window.
  const channelLabel = new Map(results.channels.map((c) => [c.name, c.label]));
  const byDate = new Map<string, OutreachResultsPost[]>();
  for (const p of results.posts) {
    if (!p.posted_at) continue;
    const t = Date.parse(p.posted_at);
    if (Number.isNaN(t)) continue;
    const date = new Date(t).toISOString().slice(0, 10);
    if (!dates.includes(date)) continue;
    byDate.set(date, [...(byDate.get(date) ?? []), p]);
  }
  const markers: ChartMarker[] = [...byDate.entries()]
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(([date, ps]) => {
      const chans = new Set(ps.map((p) => p.channel));
      return {
        x: x(dates.indexOf(date)),
        date,
        color: chans.size === 1 ? channelColor(ps[0].channel, names) : 'var(--text-muted)',
        labels: ps.map((p) => `${channelLabel.get(p.channel) ?? p.channel} · ${waveLabel(p.wave)}`),
      };
    });

  const rows = dates
    .map((date, i) => ({ date, label: dayLabel(date), values: filled.map((f) => f.values[i]) }))
    .filter((r) => r.values.some((v) => v > 0));

  return {
    geo: { ...geo, innerW, innerH },
    dates,
    empty: lines.every((l) => l.total === 0),
    max,
    xLabels,
    yTicks: yTicksValues.map((value) => ({ y: y(value), value })),
    lines,
    markers,
    partialLastDay: results.partial_last_day,
    rows,
  };
}

/** The accessible name for the chart: total and window, e.g. "12 clicks from Oct 1 to Oct 16 (UTC), today so far". */
export function chartDescription(results: OutreachResults): string {
  if (!results.window || !results.totals) return '';
  const days = utcDays(results.window.from, results.window.to);
  const last = days.length > 0 ? days[days.length - 1] : results.window.from;
  const tail = results.partial_last_day ? ', today so far' : '';
  return `${formatClicks(results.totals.clicks)} from ${dayLabel(results.window.from)} to ${dayLabel(last)} (UTC)${tail}`;
}

/** Plain-language explanation for a status, or null when the numbers are real. */
export function resultsStatusMessage(status: OutreachResultsStatus): string | null {
  switch (status) {
    case 'ok':
      return null;
    case 'not_configured':
      return 'Click results need the ShortLinks connection, which is not configured. Set it up under Settings → Outreach connections.';
    case 'unreachable':
      return 'ShortLinks could not be reached, so click counts are unavailable right now. Posts are listed below.';
    case 'rejected':
      return "ShortLinks rejected this server's API token — check it under Settings → Outreach connections.";
    case 'campaign_missing':
      return "This workshop's links were not found in ShortLinks.";
    case 'not_started':
      return 'Outreach has not started for this workshop.';
  }
}

/** "—" for unknown, else "1 click" / "N clicks". */
export function formatClicks(n: number | null): string {
  if (n === null) return '—';
  return n === 1 ? '1 click' : `${n} clicks`;
}

/** What a panel cell shows under its status: clicks for a prepared post when results are real, else nothing. */
export function cellClicksLabel(post: { id: number; prepared: boolean }, results: OutreachResults | null): string {
  if (!results || results.status !== 'ok' || !post.prepared) return '';
  const match = results.posts.find((p) => p.id === post.id);
  if (!match || match.clicks === null) return '';
  return formatClicks(match.clicks);
}

function joinNames(names: string[]): string {
  if (names.length <= 1) return names.join('');
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
}

/** Says which channels are drawn inside "Other links", or null when none are. */
export function foldedChannelsNote(results: OutreachResults): string | null {
  const other = results.series.find((s) => s.is_other);
  if (!other || other.channels.length === 0) return null;
  const labels = new Map(results.channels.map((c) => [c.name, c.label]));
  const names = other.channels.map((c) => labels.get(c) ?? c);
  const verb = names.length === 1 ? 'is' : 'are';
  return `${joinNames(names)} ${verb} included in Other links: ShortLinks charts the 6 most-clicked links separately.`;
}

// ── Engagement and mentions (#0547) ──────────────────────────────────────────

/** The sum of the four counts. */
export function reactionTotal(r: OutreachReactions): number {
  return r.likes + r.reposts + r.replies + r.quotes;
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** "" for null, "No reactions yet" when all zero, else the non-zero parts: "3 likes, 1 repost, 2 replies, 1 quote". */
export function reactionsLabel(r: OutreachReactions | null): string {
  if (!r) return '';
  if (reactionTotal(r) === 0) return 'No reactions yet';
  const parts: string[] = [];
  if (r.likes > 0) parts.push(plural(r.likes, 'like', 'likes'));
  if (r.reposts > 0) parts.push(plural(r.reposts, 'repost', 'reposts'));
  if (r.replies > 0) parts.push(plural(r.replies, 'reply', 'replies'));
  if (r.quotes > 0) parts.push(plural(r.quotes, 'quote', 'quotes'));
  return parts.join(', ');
}

/** "—" for unknown, else "1 reaction" / "N reactions". */
export function formatReactionTotal(r: OutreachReactions | null): string {
  if (!r) return '—';
  const n = reactionTotal(r);
  return n === 1 ? '1 reaction' : `${n} reactions`;
}

/**
 * What a panel cell shows for a post's engagement. Unlike the clicks label it
 * does not depend on the results status: engagement comes from our own
 * database, so it is real even when ShortLinks is down or unconfigured.
 */
export function cellReactionsLabel(post: { id: number }, results: OutreachResults | null): string {
  if (!results) return '';
  const match = results.posts.find((p) => p.id === post.id);
  if (!match || !match.engagement) return '';
  return reactionsLabel(match.engagement);
}

/** "Mention" / "Reply" / "Quote". */
export function mentionKindLabel(kind: OutreachMention['kind']): string {
  return kind === 'reply' ? 'Reply' : kind === 'quote' ? 'Quote' : 'Mention';
}

/** The caveat to show when any Mastodon post has engagement, else null. */
export function mastodonCountsNote(results: { posts: OutreachResultsPost[] }): string | null {
  const any = results.posts.some((p) => p.channel === 'mastodon' && p.engagement !== null);
  return any ? 'Mastodon counts are as seen by our instance, so they can lag what other servers show.' : null;
}

/**
 * A mention's link, only when it is a safe absolute https URL. The server
 * checks this when it stores the URL; this repeats the check because the text
 * and link are third-party content.
 */
export function mentionHref(m: { url: string | null }): string | null {
  if (!m.url) return null;
  if (!/^https:\/\//i.test(m.url.trim())) return null;
  return isSafeLinkHref(m.url) ? m.url.trim() : null;
}

/** "42 clicks since outreach started (5 from flyers).", or '' unless the numbers are real (#0568). */
export function resultsSummaryLine(results: OutreachResults | null): string {
  if (!results || results.status !== 'ok' || !results.totals) return '';
  const { clicks, flyer_clicks } = results.totals;
  const base = `${clicks} ${clicks === 1 ? 'click' : 'clicks'} since outreach started`;
  return flyer_clicks > 0 ? `${base} (${flyer_clicks} from flyers).` : `${base}.`;
}

// ── Batch 10 (#0590) ─────────────────────────────────────────────────────────

/**
 * The overview's posts cell: "Posted: Bluesky, Mastodon · 4 scheduled". Only
 * channels `isEnabled` accepts are named (the ones switched on in Settings,
 * #0588); the default accepts every channel the row reports.
 */
export function postsCellText(
  row: Pick<OutreachResultsSummaryRow, 'posted_channels' | 'scheduled'>,
  labels: Record<string, string>,
  isEnabled: (channel: string) => boolean = () => true,
): string {
  const names = (row.posted_channels ?? []).filter(isEnabled).map((n) => labels[n] ?? n);
  const posted = names.length > 0 ? `Posted: ${names.join(', ')}` : 'Nothing posted yet';
  const n = row.scheduled ?? 0;
  return n > 0 ? `${posted} · ${n} scheduled` : posted;
}

/** One legend entry per line of the chart, as the legend prints it: the line's own total. */
export function legendTotals(results: OutreachResults): { id: string; label: string; total: number }[] {
  const model = buildChart(results);
  return model ? model.lines.map((l) => ({ id: l.id, label: l.label, total: l.total })) : [];
}

/**
 * Says so when a legend total and the channel table's total for the same
 * channel disagree, so the page never prints two unlabelled numbers for one
 * channel (#0590, I2). Null when every shared channel agrees.
 */
export function clickDefinitionNote(results: OutreachResults): string | null {
  const table = new Map(results.channels.map((c) => [c.name, c.clicks]));
  const differs = legendTotals(results).some((l) => {
    const t = table.get(l.id);
    return t !== undefined && t !== null && t !== l.total;
  });
  return differs
    ? 'The chart counts clicks on each day it shows; the table counts every click on the channel’s links, so the two can differ.'
    : null;
}

/** The all-workshops flyer column adds nothing until a second workshop has a link at some location (#0590, I4). */
export function showFlyerAllColumn(rows: OutreachResultsFlyerLocation[]): boolean {
  return rows.some((r) => (r.workshops_at_location ?? 1) > 1);
}
