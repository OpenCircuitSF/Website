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
 * out as blocking a send outright, then delivery health, then
 * infrastructure status, then the acknowledged gap.
 *
 * complaint_rate_pct being absent (small sample) is NOT a warning — it is
 * "not enough data yet," rendered as no row at all rather than a false
 * "high" or a fabricated "0.00%".
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

  if (w.complaint_rate_pct !== undefined && w.complaint_rate_high) {
    rows.push({
      key: 'complaint-rate',
      message: `Account-wide complaint rate is ${formatComplaintRatePct(w.complaint_rate_pct)}, above Gmail/Yahoo's published 0.3% bulk-sender limit — this domain risks throttling or blocking.`,
      alert: true,
    });
  }

  if (w.ses_sandbox_active) {
    rows.push({
      key: 'ses-sandbox',
      message:
        'SES is still in sandbox mode (200 messages/day, verified recipients only). Request production access before sending to the real list.',
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

  return rows;
}

/** Whether any row in buildWarnings' output should render as a blocking alert (vs. an informational note). */
export function hasAlertWarning(warnings: DashboardWarning[]): boolean {
  return warnings.some((w) => w.alert);
}
