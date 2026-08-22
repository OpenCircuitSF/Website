// Pure, framework-free helpers backing the campaign stats screen (#0049):
// bucket ordering, the bounced/complained reconciliation-vs-raw-count
// substitution, the complaint-rate verdict against Gmail's published 0.3%
// limit, percentage formatting, and failed-send display.
//
// #0094 (restated by #0047's own lib/ modules, which this one follows)
// means nothing in a .svelte file is covered by a test — so every decision
// CampaignStats.svelte makes about these numbers routes through here, never
// a comparison/ternary/status literal written directly in the component.
// The acceptance criterion "Handler and component tests pass" is not
// achievable as written (there is no .svelte test harness in this repo);
// this module's own vitest file is what stands in for it, per #0047's own
// carried-in review finding restating the same #0094 fact for this issue.

import type { CampaignStatsResponse, CampaignFailedSend } from './types';

export type { CampaignStatsResponse, CampaignFailedSend };

// ── Bucket ordering (a real decision, not alphabetical or column order) ─────

/** Visual tone for one stat bucket — drives the badge/number color, never a raw status string. */
export type StatBucketTone = 'success' | 'neutral' | 'danger';

/** One row of the stats screen's bucket breakdown. */
export interface StatBucket {
  key: string;
  label: string;
  count: number;
  tone: StatBucketTone;
}

/**
 * Build the ordered bucket list the stats screen renders, from a
 * CampaignStatsResponse. The order is deliberate, not the column order
 * `counts` arrives in: `sent` — the headline successful outcome — leads;
 * `queued`/`sending` (still in flight, not yet an outcome) come next;
 * `failed`/`bounced`/`complained` (the operator-actionable problems) follow;
 * `skipped` is LAST and its own bucket, distinct from `failed` — carried in
 * from #0044's plan: a recipient who unsubscribed or was suppressed between
 * materialization and send is not a delivery failure, and burying it after
 * every failure-class bucket keeps it from reading as one more problem in
 * the list.
 *
 * `bounced`/`complained` are read from `stats.reconciled`, NOT
 * `stats.counts.bounced`/`stats.counts.complained` — see
 * internal/mailing/campaign_stats.go's package doc comment: nothing in this
 * codebase ever writes those two email_sends.status values, so the raw
 * bucket is always zero regardless of how many real bounces/complaints
 * occurred. Rendering the raw (always-zero) figure here would be actively
 * misleading on the one screen whose entire purpose is surfacing them
 * accurately — this substitution is what "reconciliation" means for this
 * screen's presentation, on top of the reconciled read itself already
 * happening server-side.
 */
export function buildStatBuckets(stats: CampaignStatsResponse): StatBucket[] {
  const c = stats.counts;
  const r = stats.reconciled;
  return [
    { key: 'sent', label: 'Sent', count: c.sent, tone: 'success' },
    { key: 'queued', label: 'Queued', count: c.queued, tone: 'neutral' },
    { key: 'sending', label: 'Sending', count: c.sending, tone: 'neutral' },
    { key: 'failed', label: 'Failed', count: c.failed, tone: 'danger' },
    { key: 'bounced', label: 'Bounced', count: r.bounced, tone: 'danger' },
    { key: 'complained', label: 'Complained', count: r.complained, tone: 'danger' },
    { key: 'skipped', label: 'Skipped', count: c.skipped, tone: 'neutral' },
  ];
}

/** Sum of every bucket's count — the total number of email_sends rows for the campaign. */
export function totalSendRows(stats: CampaignStatsResponse): number {
  const c = stats.counts;
  return c.queued + c.sending + c.sent + c.failed + c.bounced + c.complained + c.skipped;
}

// ── Complaint rate (client-side presentation threshold, deliberately) ───────

/**
 * Gmail's published complaint-rate limit, as a percentage — crossing it is
 * what gets a sending domain throttled or blocked (PRD §11's note). This is
 * a PRESENTATION threshold for this historical-stats screen, not a
 * server-side rule — unlike everything else these campaign screens render
 * (`#0047`'s pre-send panel renders the server's `unmet`/`warnings`
 * verbatim precisely so no requirement exists in two places). It is
 * deliberately NOT the same number as PRD §6.9's `send_health_complaint_pct`
 * (0.1, Phase 8, `#0124`, not yet built) — that is a server-side, send-time
 * circuit breaker that pauses an IN-PROGRESS send; this is a read-only
 * verdict rendered on a completed or in-flight campaign's stats screen. Two
 * different mechanisms, two different owners, two different numbers — do
 * not unify them.
 */
export const COMPLAINT_RATE_THRESHOLD_PCT = 0.3;

/** The complaint-rate verdict: the rate itself, whether it crosses the threshold, and its display string. */
export interface ComplaintRateVerdict {
  ratePct: number;
  overThreshold: boolean;
  formatted: string;
}

/**
 * Compute the complaint rate and its threshold verdict. The denominator is
 * `sentCount` (successfully delivered messages) — Gmail's own definition of
 * the metric is complaints ÷ messages sent, not complaints ÷ total audience
 * (which would include queued/failed/skipped rows that were never
 * delivered and therefore could never generate a complaint).
 *
 * `sentCount <= 0` reads as rate 0 and NOT over threshold — a campaign that
 * has not sent anything yet cannot have crossed a delivery-based limit, and
 * a divide-by-zero must never read as `overThreshold: true` (which would
 * put a scary warning on a freshly-created draft with zero sends).
 *
 * The boundary is INCLUSIVE: a rate exactly AT 0.3% already reads as
 * crossing it, not merely approaching it — Gmail's limit is a ceiling, and
 * treating "landed exactly on the ceiling" as still-safe would be the wrong
 * side of a compliance number to round in the operator's favor.
 */
export function complaintRateVerdict(sentCount: number, complainedCount: number): ComplaintRateVerdict {
  const ratePct = sentCount > 0 ? (complainedCount / sentCount) * 100 : 0;
  return {
    ratePct,
    overThreshold: ratePct >= COMPLAINT_RATE_THRESHOLD_PCT,
    formatted: formatComplaintRate(ratePct),
  };
}

/** Format a complaint-rate percentage to two decimal places, e.g. "0.30%". */
export function formatComplaintRate(ratePct: number): string {
  return `${ratePct.toFixed(2)}%`;
}

/** Convenience: run complaintRateVerdict straight off a CampaignStatsResponse. */
export function campaignComplaintRateVerdict(stats: CampaignStatsResponse): ComplaintRateVerdict {
  return complaintRateVerdict(stats.counts.sent, stats.reconciled.complained);
}

// ── Failed-send display ──────────────────────────────────────────────────────

/**
 * The error text to show for one failed send — `error` can be an empty
 * string. `email_sends.error` is nullable server-side and the handler
 * coalesces NULL to `""` in Go, but the JSON struct tag is
 * `json:"error,omitempty"`, so that empty string is OMITTED from the
 * response entirely, not sent as `""`. Either way `types.ts` declares
 * `error?: string`, so both the omitted and (if it ever arrives) the
 * empty-string case land here, and an empty error line reads as a blank
 * table cell rather than useful information.
 */
export function failedSendErrorLabel(f: CampaignFailedSend): string {
  const msg = f.error?.trim();
  return msg ? msg : 'No error message recorded';
}

// ── Whether the stats screen has anything to say about a campaign ──────────

/**
 * Whether a campaign at the given status can possibly have send history
 * worth viewing. `draft` never materializes email_sends rows — a campaign
 * only reaches `sending` (and therefore gets rows) via the worker's
 * ClaimStart, and nothing in migration 000017/000018's state machine ever
 * moves a campaign FROM sending/sent/failed/canceled BACK to `draft` once
 * materialized (CampaignStore.Send only accepts draft/failed as sources,
 * DemoteToDraft only ever demotes a never-materialized `scheduled` row —
 * see worker_store.go's own doc comments). `scheduled` is included as
 * viewable too: it is a genuinely reachable but always-empty state (nothing
 * materializes before ClaimStart), so showing an honest all-zero screen is
 * simpler and no less correct than hiding the link and inventing a second
 * "not yet" state.
 */
export function canViewCampaignStats(status: string): boolean {
  return status !== 'draft';
}
