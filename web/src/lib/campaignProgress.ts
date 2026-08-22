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
  /**
   * The campaign's own email_campaigns.status as of this publish —
   * 'sending' mid-drain, 'sent'/'failed'/'canceled' once it is over. Read
   * fresh by the worker on every publish (publishProgress, worker.go). This
   * is the ONLY trustworthy terminality signal; see isTerminalSnapshot.
   * Treated as possibly-absent at runtime even though the type says string:
   * nothing mechanically enforces that this bundle and the running server
   * agree on the payload (#0095), so an unknown/missing value must degrade
   * to "not terminal by status" rather than throw.
   */
  status: string;
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

/** The three campaign statuses from which a send never resumes (migration 000017's vocabulary). */
export const TERMINAL_CAMPAIGN_STATUSES: readonly string[] = ['sent', 'failed', 'canceled'] as const;

/** Whether a campaign status means the send is over for good. 'draft'/'scheduled'/'sending' are not. */
export function isTerminalCampaignStatus(status: string): boolean {
  return TERMINAL_CAMPAIGN_STATUSES.includes(status);
}

/**
 * Whether a received CampaignProgress frame may be the LAST one for its
 * send — the trigger for re-reading the campaign from the server
 * (`resyncCampaignStatus()` in CampaignEditor.svelte), so the heading and
 * the stalled verdict move off "Sending…" the moment the send ends rather
 * than waiting for a stream drop that, on a healthy connection, never comes.
 *
 * # Why this reads `status`, not `remaining`
 *
 * `#0048`'s first attempt was `p.remaining === 0`, and that predicate is
 * true to its own arithmetic — `remaining` is queued+sending across all
 * workers, so it cannot read 0 while any row is claimed. What it is not is
 * a test of TERMINALITY. `MarkFailedCampaign` (worker_store.go) updates
 * `email_campaigns` alone and never touches `email_sends`, so on
 * `physical_address_missing`, `reply_to_missing`, and every terminal-class
 * SES error the campaign reaches 'failed' with its unsent rows still
 * 'queued' — `remaining > 0` on a send that has definitively stopped. The
 * predicate returned false, no resync fired, and a failed campaign rendered
 * "Sending…" indefinitely and then a false "stalled" warning naming the
 * wrong cause (`#0048`'s second review, point 3). A failed campaign
 * legitimately has recipients who will never be mailed; the counts are right
 * and it is the inference that was wrong. So the worker now states the
 * campaign's status in the frame and this reads it.
 *
 * # Why `remaining === 0` survives as a fallback
 *
 * Nothing mechanically keeps this bundle and the running server agreeing on
 * the payload (`#0095`). If `status` is ever absent — an old server, a
 * cached bundle, a status read that errored worker-side and published "" —
 * falling back to the count keeps the successful-send path (where
 * `remaining` really does reach 0) working instead of silently reinstating
 * the original bug. The cost of a false positive is one extra GET whose
 * answer is authoritative anyway, so the fallback is cheap and the disjunct
 * is deliberately permissive: this is a "worth re-checking with the server"
 * signal, not a claim about the campaign's state. Nothing downstream renders
 * from it — `progressHeading`/`progressVerdict` still key off the campaign's
 * own status, never off this.
 *
 * It does mean a successful send fires this twice: `drainLoop`'s
 * bottom-of-batch publish already reports `remaining: 0` before
 * `CompleteIfDone` flips the status, then `CompleteIfDone` publishes again
 * with 'sent'. Both resyncs are in flight at once and may resolve out of
 * order — see `createResyncSequencer`, which is what stops the earlier
 * (still-'sending') response from landing last and reinstating the bug.
 */
export function isTerminalSnapshot(p: CampaignProgress): boolean {
  return isTerminalCampaignStatus(p.status) || p.remaining === 0;
}

/**
 * A last-writer-wins guard for overlapping background re-reads of the same
 * resource. `begin()` starts a request and returns its token; `isCurrent()`
 * reports whether that token is still the most recently started one, i.e.
 * whether its response is safe to apply.
 *
 * `#0048`'s second review, point 5. `resyncCampaignStatus()` assigns
 * `campaign = await getCampaign(...)` with no sequencing, and a successful
 * send genuinely fires it twice a few round trips apart (see
 * `isTerminalSnapshot`). The first request can read the database before
 * `CompleteIfDone` commits and come back with `'sending'`; if that response
 * is applied AFTER the second one's `'sent'`, the view reverts to "Sending…"
 * with no further frames coming — exactly the bug both earlier passes were
 * fixing. Ordering by START order is sound because a later-started read sees
 * a database at least as fresh as an earlier-started one, whatever order the
 * responses arrive in.
 *
 * Deliberately not "skip if one is already in flight": that would drop the
 * SECOND request, which is the one carrying the terminal status.
 */
export interface ResyncSequencer {
  /** Start a request; returns its token — always a positive integer. */
  begin(): number;
  /** Whether token's response is still the newest and may be applied. */
  isCurrent(token: number): boolean;
}

export function createResyncSequencer(): ResyncSequencer {
  let latest = 0;
  return {
    begin(): number {
      latest += 1;
      return latest;
    },
    // `token > 0` is not decoration: tokens count from 1 precisely so that
    // 0 — the counter's own initial value, and what any uninitialised or
    // defaulted variable would carry — can never be mistaken for a live
    // request and wave an unsequenced write through.
    isCurrent(token: number): boolean {
      return token > 0 && token === latest;
    },
  };
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
 * arrived — an operator who opened the page after the campaign already ended
 * has received no frames, and this stream has nothing true to say about it.
 * The database, via campaign.completed_at etc., is where that final state
 * lives (#0049's stats view), not here.
 */
export function shouldShowProgress(status: string, hasSnapshot: boolean): boolean {
  if (status === 'sending') {
    return true;
  }
  return hasSnapshot && isTerminalCampaignStatus(status);
}

/**
 * The progress region's heading, keyed by the campaign's CURRENT status —
 * never inferred from whether `remaining` reached 0. A campaign that dies on
 * a terminal SES error (failCampaign) is 'failed' with rows still queued and
 * `remaining > 0`, and one whose last batch drained is still 'sending' until
 * CompleteIfDone commits, so the counts label neither correctly. What keeps
 * the status this reads FRESH is the resync isTerminalSnapshot triggers. An
 * unrecognized/empty status returns '' so the component falls back to
 * rendering nothing rather than a stale or wrong label.
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
