// Pure, framework-free helpers backing #0061's admin overview dashboard (the
// /admin landing screen). Every decision Dashboard.svelte makes about the
// GET /admin/overview payload — growth sign/formatting, which warnings to
// show and how, the complaint-rate percentage string — routes through here,
// never a comparison or ternary written directly in the component, mirroring
// the convention #0047/#0049 established (see campaignStats.ts's own doc
// comment) and #0094 restates: nothing in a .svelte file is covered by a
// test.

import type { DashboardGrowth, DashboardWarnings } from './types';

/**
 * Signed, comma-grouped net growth, e.g. "+12", "-3", "0". Never a bare
 * number with no sign — the sign is the entire point of a "growth" figure.
 */
export function formatNetGrowth(growth: DashboardGrowth): string {
  const n = growth.net_30d;
  const sign = n > 0 ? '+' : '';
  return `${sign}${n.toLocaleString()}`;
}

/** "12 joined, 3 left" — the two directions the net figure was computed from. */
export function formatGrowthDetail(growth: DashboardGrowth): string {
  return `${growth.confirmed_30d.toLocaleString()} joined, ${growth.unsubscribed_30d.toLocaleString()} left`;
}

/** Same two-decimal percentage string campaignStats.ts's formatComplaintRate uses, kept consistent across both screens. */
export function formatComplaintRatePct(pct: number): string {
  return `${pct.toFixed(2)}%`;
}

/** One row in the warnings list — a stable key (for Svelte's #each), the message, and whether it renders as an alert (true) or a quieter status line (false). */
export interface DashboardWarning {
  key: string;
  message: string;
  alert: boolean;
}

/**
 * Builds the "what needs attention" list from the server's warnings block.
 * Order is fixed (not severity-sorted) so the list doesn't reshuffle between
 * loads — physical address first since it is the one #0061's own notes call
 * out as blocking a send outright, then the two complaint-rate bands (#0227:
 * amber before red, matching the ## Decision table's own ordering), then
 * infrastructure status, then the acknowledged gap.
 *
 * complaint_rate_pct being absent (small sample) is NOT a warning — it is
 * "not enough data yet," rendered as no row at all rather than a false
 * "high"/"review" or a fabricated "0.00%".
 *
 * The two complaint-rate rows are independent, not an escalating pair: both
 * can render together (a rate above 0.3% is also above 0.1%), because both
 * consequences — AWS may re-sandbox the account, and Gmail/Yahoo will
 * filter mail — are true at once. Collapsing them into a single row keyed
 * off the higher band would silently drop the re-sandboxing warning exactly
 * when it matters most.
 */
export function buildWarnings(w: DashboardWarnings): DashboardWarning[] {
  const rows: DashboardWarning[] = [];

  if (w.physical_address_unset) {
    rows.push({
      key: 'physical-address',
      message:
        'No physical mailing address is set. CAN-SPAM requires one in every commercial email — campaigns cannot be sent until this is fixed in Settings.',
      alert: true,
    });
  }

  if (w.complaint_rate_pct !== undefined && w.complaint_rate_review) {
    rows.push({
      key: 'complaint-rate-review',
      message: `Account-wide complaint rate is ${formatComplaintRatePct(w.complaint_rate_pct)}, at or above AWS's 0.1% account-wide threshold — AWS may put this account under review and return it to the sandbox, which would stop sending altogether.`,
      alert: true,
    });
  }

  if (w.complaint_rate_pct !== undefined && w.complaint_rate_high) {
    rows.push({
      key: 'complaint-rate-high',
      message: `Account-wide complaint rate is ${formatComplaintRatePct(w.complaint_rate_pct)}, at or above Gmail/Yahoo's published 0.3% bulk-sender limit — this domain risks throttling or blocking.`,
      alert: true,
    });
  }

  if (w.ses_sandbox_active) {
    rows.push({
      key: 'ses-sandbox',
      message:
        'This environment is configured for SES sandbox mode (200 messages/day, verified recipients only). Request production access before sending to the real list.',
      alert: false,
    });
  }

  if (w.inbound_mail_unavailable) {
    rows.push({
      key: 'inbound-mail',
      message:
        'Inbound mailto: unsubscribe processing is not built yet, so this screen cannot report unprocessed inbound mail.',
      alert: false,
    });
  }

  if (w.outbound_queue_abandoned) {
    rows.push({
      key: 'outbound-queue-abandoned',
      message:
        'At least one transactional email (confirmation, registration, or recovery) exhausted its retries and was never delivered. Check the outbound queue.',
      alert: true,
    });
  }

  return rows;
}

/** Whether any row in buildWarnings' output should render as a blocking alert (vs. an informational note). */
export function hasAlertWarning(warnings: DashboardWarning[]): boolean {
  return warnings.some((w) => w.alert);
}

/**
 * #0279: the text Dashboard.svelte's persistent `role="status"` announcer
 * mutates into. Dashboard.svelte used to render each warning's role with
 * `role={w.alert ? 'alert' : 'status'}` inside a keyed `{#each}` — a
 * dynamic role expression the live-region guard (`#0242`) cannot classify,
 * AND (independent of that) unreliable for the `'status'` half specifically:
 * a keyed `{#each}` mutates the SAME DOM node in place for an existing key
 * across a re-render, but a warning appearing for the first time is a
 * genuine insertion, which `role="status"` does not announce reliably (the
 * same gap the loading placeholders carry). `role="alert"` doesn't have
 * this problem — #0243 established it announces reliably even when
 * `{#if}`/`{#each}`-created — so alert-severity warnings keep their own
 * static `role="alert"` list item and are deliberately NOT included here
 * (including them would double-announce the same text through two live
 * regions at once).
 *
 * Only the non-alert (status-severity) warnings' messages are joined into
 * one string; the persistent region carrying this text already exists
 * before `warnings` first has content (unconditionally rendered, per
 * `#0063`'s decision that a live region must be a node that MUTATES, not
 * one created fresh alongside its first content), so setting this from ''
 * to a real value is a genuine node mutation, not an insertion.
 */
export function statusWarningsAnnouncement(warnings: DashboardWarning[]): string {
  return warnings
    .filter((w) => !w.alert)
    .map((w) => w.message)
    .join(' ');
}
