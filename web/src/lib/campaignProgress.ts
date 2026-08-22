// Pure, framework-free helpers backing #0048's live campaign send progress:
// the CampaignProgress payload shape, the SSE event name, formatting, the
// running/stalled verdict, and the reconciliation rule that keeps the
// campaign's own `status` field — not this stream — as the source of truth.
//
// #0094 (restated by #0047's own lib/ modules) means nothing in a .svelte
// file is covered by a test, so every decision CampaignEditor.svelte's
// #campaign-send-progress region and cancel dialog make about these numbers
// routes through here, never a comparison/ternary written directly in the
// component.

/**
 * One live snapshot of a campaign's send progress, as published over SSE.
 * Matches internal/mailing.CampaignProgress's JSON tags exactly — see that
 * struct's own doc comment (worker.go) for why the two must be kept in sync
 * by hand. Total is fixed at materialization so it never moves backwards;
 * Remaining is queued+sending (email_sends' SEVENTH status, migration
 * 000018); Skipped is its own bucket, distinct from Failed — "the list
 * shrank under us" (an unsubscribe/suppression between materialization and
 * send) is correct behavior, not an error.
 */
export interface CampaignProgress {
  campaign_id: number;
  total: number;
  sent: number;
  failed: number;
  skipped: number;
  remaining: number;
}

/** The SSE `event:` name the send worker publishes under (internal/handlers/campaign_progress.go). */
export const CAMPAIGN_PROGRESS_EVENT = 'campaign.progress';

/**
 * Whether a received CampaignProgress frame belongs to the campaign
 * currently open in the editor. Progress is broadcast to every connected
 * admin (internal/events.Broker.PublishAll) regardless of which campaign
 * they're viewing, so every consumer must filter by id — this is that
 * filter, kept out of the component per #0047's "every decision is a lib/
 * call" convention.
 */
export function isProgressForCampaign(p: CampaignProgress, campaignId: number): boolean {
  return p.campaign_id === campaignId;
}

/** done/total as an integer percentage in [0, 100]. total <= 0 (not yet materialized) reads as 0, never NaN/Infinity. */
export function progressPercent(p: CampaignProgress): number {
  if (p.total <= 0) {
    return 0;
  }
  const done = p.sent + p.failed + p.skipped;
  const pct = Math.round((done / p.total) * 100);
  return Math.max(0, Math.min(100, pct));
}

const fmtInt = (n: number): string => n.toLocaleString();

/** The headline line, e.g. "482 of 1,203 sent". */
export function formatProgressSummary(p: CampaignProgress): string {
  return `${fmtInt(p.sent)} of ${fmtInt(p.total)} sent`;
}

/**
 * The full breakdown, e.g. "482 of 1,203 sent · 12 failed · 3 skipped · 706
 * remaining". Failed/skipped are omitted when zero (no need to tell an
 * operator "0 failed" on a clean run); remaining always shows, including 0,
 * since "0 remaining" is itself the completion signal for the sending case.
 */
export function formatProgressDetail(p: CampaignProgress): string {
  const parts = [formatProgressSummary(p)];
  if (p.failed > 0) {
    parts.push(`${fmtInt(p.failed)} failed`);
  }
  if (p.skipped > 0) {
    parts.push(`${fmtInt(p.skipped)} skipped`);
  }
  parts.push(`${fmtInt(p.remaining)} remaining`);
  return parts.join(' · ');
}

/**
 * Whether the progress region should show live numbers at all for a given
 * campaign status. `sending` always shows (even with no snapshot yet — the
 * region then shows a "waiting for the first update" state, handled by the
 * component). A TERMINAL status only shows numbers if a snapshot actually
 * arrived: #0045's failCampaign does not publish a final frame, so a
 * campaign that reaches `failed`/`canceled` with no snapshot ever received
 * (e.g. the operator loaded the page after the campaign already ended) has
 * nothing true to show — the database, via campaign.completed_at etc., is
 * where that final state lives (#0049's stats view), not this stream.
 */
export function shouldShowProgress(status: string, hasSnapshot: boolean): boolean {
  if (status === 'sending') {
    return true;
  }
  return hasSnapshot && (status === 'sent' || status === 'failed' || status === 'canceled');
}

/**
 * The progress region's heading, keyed by the campaign's CURRENT status —
 * never inferred from whether Remaining reached 0, since a campaign that
 * dies on a terminal SES error (failCampaign) leaves its last live batch's
 * snapshot as the final one without Remaining ever reaching 0 (#0045's
 * review finding, carried into this issue). An unrecognized/empty status
 * returns '' so the component can fall back to rendering nothing rather
 * than a stale or wrong label.
 */
export function progressHeading(status: string): string {
  switch (status) {
    case 'sending':
      return 'Sending…';
    case 'sent':
      return 'Send complete';
    case 'failed':
      return 'Send stopped';
    case 'canceled':
      return 'Send canceled';
    default:
      return '';
  }
}

/** Verdict returned by progressVerdict: whether a live send looks like it's still making progress. */
export type ProgressVerdict = 'running' | 'stalled' | 'not-sending';

// How long without a batch update before a `sending` campaign is called
// "stalled" rather than "running". Not sized against measured machine load
// (CLAUDE.md §5) — it is sized against the worker's own cadence: a 2s poll
// interval (PRD §6.6) plus one batch's send time at the default 50-row
// batch / 10-per-second rate (~5s), several times over, so ordinary
// send-to-send gaps never read as stalled.
const STALLED_AFTER_MS = 30_000;

/**
 * Whether a `sending` campaign's live stream looks stalled: no progress
 * frame received in over STALLED_AFTER_MS. Only meaningful while
 * status === 'sending' — any other status reports 'not-sending' regardless
 * of lastEventAt, since a finished/canceled/failed campaign isn't expected
 * to keep publishing. `lastEventAt`/`now` are epoch milliseconds
 * (`Date.now()`), injected rather than read internally so this stays a pure,
 * directly testable function — the component supplies both.
 */
export function progressVerdict(status: string, lastEventAt: number | null, now: number): ProgressVerdict {
  if (status !== 'sending') {
    return 'not-sending';
  }
  if (lastEventAt === null) {
    // Just opened the stream / just started sending — no data yet is not
    // the same as stalled.
    return 'running';
  }
  return now - lastEventAt > STALLED_AFTER_MS ? 'stalled' : 'running';
}

/**
 * The remaining-recipient count for the cancel confirmation dialog, or
 * `undefined` when it isn't known/applicable — #0047's cancelCopy('sending')
 * deliberately omits this number because only #0048's live stream knows it;
 * this is where #0048 supplies it. `undefined` (never 0-by-default) whenever
 * status isn't 'sending', or no snapshot for THIS campaign has arrived yet,
 * so cancelCopy's fallback wording (no digits) is used instead of a
 * fabricated or stale number.
 */
export function remainingForCancel(
  status: string,
  campaignId: number,
  progress: CampaignProgress | null,
): number | undefined {
  if (status !== 'sending' || progress === null || !isProgressForCampaign(progress, campaignId)) {
    return undefined;
  }
  return progress.remaining;
}
