// Unit tests for #0061's admin overview dashboard pure logic: growth
// formatting and the warnings-list assembly, including the boundary between
// "no data yet" (complaint_rate_pct absent) and "high" (present + over
// threshold).

import { describe, it, expect } from 'vitest';
import type { DashboardGrowth, DashboardWarnings } from './types';
import {
  formatNetGrowth,
  formatGrowthDetail,
  formatComplaintRatePct,
  buildWarnings,
  hasAlertWarning,
  statusWarningsAnnouncement,
} from './dashboard';

function growth(overrides: Partial<DashboardGrowth> = {}): DashboardGrowth {
  return { confirmed_30d: 0, unsubscribed_30d: 0, net_30d: 0, ...overrides };
}

function warnings(overrides: Partial<DashboardWarnings> = {}): DashboardWarnings {
  return {
    complaint_rate_review: false,
    complaint_rate_high: false,
    complaint_sample_size: 0,
    physical_address_unset: false,
    ses_sandbox_active: false,
    inbound_mail_unavailable: false,
    outbound_queue_abandoned: false,
    ...overrides,
  };
}

describe('formatNetGrowth', () => {
  it('prefixes a positive net with +', () => {
    expect(formatNetGrowth(growth({ net_30d: 12 }))).toBe('+12');
  });
  it('leaves a negative net with its own minus sign', () => {
    expect(formatNetGrowth(growth({ net_30d: -3 }))).toBe('-3');
  });
  it('shows a bare 0 for no net change', () => {
    expect(formatNetGrowth(growth({ net_30d: 0 }))).toBe('0');
  });
  it('comma-groups large numbers', () => {
    expect(formatNetGrowth(growth({ net_30d: 1234 }))).toBe('+1,234');
  });
});

describe('formatGrowthDetail', () => {
  it('reports both directions', () => {
    expect(formatGrowthDetail(growth({ confirmed_30d: 12, unsubscribed_30d: 3 }))).toBe('12 joined, 3 left');
  });
});

describe('formatComplaintRatePct', () => {
  it('formats to two decimal places with a percent sign', () => {
    expect(formatComplaintRatePct(0.5)).toBe('0.50%');
    expect(formatComplaintRatePct(1.234)).toBe('1.23%');
  });
});

describe('buildWarnings', () => {
  it('is empty when nothing needs attention', () => {
    expect(buildWarnings(warnings())).toEqual([]);
  });

  it('includes physical_address as an alert when unset', () => {
    const rows = buildWarnings(warnings({ physical_address_unset: true }));
    expect(rows).toHaveLength(1);
    expect(rows[0].key).toBe('physical-address');
    expect(rows[0].alert).toBe(true);
  });

  it('includes ses_sandbox as a non-alert note when active', () => {
    const rows = buildWarnings(warnings({ ses_sandbox_active: true }));
    expect(rows).toHaveLength(1);
    expect(rows[0].key).toBe('ses-sandbox');
    expect(rows[0].alert).toBe(false);
  });

  it('includes inbound_mail_unavailable as a non-alert note when true', () => {
    const rows = buildWarnings(warnings({ inbound_mail_unavailable: true }));
    expect(rows).toHaveLength(1);
    expect(rows[0].key).toBe('inbound-mail');
    expect(rows[0].alert).toBe(false);
  });

  it('includes outbound_queue_abandoned as an alert when true (#0126)', () => {
    const rows = buildWarnings(warnings({ outbound_queue_abandoned: true }));
    expect(rows).toHaveLength(1);
    expect(rows[0].key).toBe('outbound-queue-abandoned');
    expect(rows[0].alert).toBe(true);
  });

  it('includes the red complaint-rate row as an alert only when BOTH high is true AND a percentage is present', () => {
    // High with no percentage (should not happen server-side, but the
    // client must not fabricate one) — omitted.
    expect(buildWarnings(warnings({ complaint_rate_high: true }))).toEqual([]);

    // Percentage present but not high — omitted (no alert-worthy signal).
    expect(buildWarnings(warnings({ complaint_rate_pct: 0.1, complaint_rate_high: false }))).toEqual([]);

    // Both present and high — included, with the percentage in the message.
    const rows = buildWarnings(warnings({ complaint_rate_pct: 0.42, complaint_rate_high: true }));
    expect(rows).toHaveLength(1);
    expect(rows[0].key).toBe('complaint-rate-high');
    expect(rows[0].alert).toBe(true);
    expect(rows[0].message).toContain('0.42%');
  });

  it('includes the amber complaint-rate-review row as an alert only when BOTH review is true AND a percentage is present', () => {
    expect(buildWarnings(warnings({ complaint_rate_review: true }))).toEqual([]);
    expect(buildWarnings(warnings({ complaint_rate_pct: 0.05, complaint_rate_review: false }))).toEqual([]);

    const rows = buildWarnings(warnings({ complaint_rate_pct: 0.15, complaint_rate_review: true }));
    expect(rows).toHaveLength(1);
    expect(rows[0].key).toBe('complaint-rate-review');
    expect(rows[0].alert).toBe(true);
    expect(rows[0].message).toContain('0.15%');
  });

  it('renders BOTH bands together when a rate clears both thresholds — they are independent, not an escalating pair', () => {
    const rows = buildWarnings(warnings({ complaint_rate_pct: 0.5, complaint_rate_review: true, complaint_rate_high: true }));
    expect(rows.map((r) => r.key)).toEqual(['complaint-rate-review', 'complaint-rate-high']);
    expect(rows.every((r) => r.alert)).toBe(true);
  });

  it('orders physical address first, then amber complaint review, then red complaint high, then SES sandbox, then inbound mail, then outbound queue abandoned', () => {
    const rows = buildWarnings(
      warnings({
        physical_address_unset: true,
        complaint_rate_pct: 1,
        complaint_rate_review: true,
        complaint_rate_high: true,
        ses_sandbox_active: true,
        inbound_mail_unavailable: true,
        outbound_queue_abandoned: true,
      }),
    );
    expect(rows.map((r) => r.key)).toEqual([
      'physical-address',
      'complaint-rate-review',
      'complaint-rate-high',
      'ses-sandbox',
      'inbound-mail',
      'outbound-queue-abandoned',
    ]);
  });
});

describe('hasAlertWarning', () => {
  it('is false when no row is an alert', () => {
    expect(hasAlertWarning(buildWarnings(warnings({ ses_sandbox_active: true })))).toBe(false);
  });
  it('is true when at least one row is an alert', () => {
    expect(hasAlertWarning(buildWarnings(warnings({ physical_address_unset: true, ses_sandbox_active: true })))).toBe(
      true,
    );
  });
});

describe('statusWarningsAnnouncement (#0279)', () => {
  it('is empty when there are no warnings at all', () => {
    expect(statusWarningsAnnouncement(buildWarnings(warnings()))).toBe('');
  });

  it('is empty when every warning is alert-severity -- alert already announces reliably on its own', () => {
    const rows = buildWarnings(warnings({ physical_address_unset: true, outbound_queue_abandoned: true }));
    expect(rows.every((r) => r.alert)).toBe(true);
    expect(statusWarningsAnnouncement(rows)).toBe('');
  });

  it('joins only the non-alert warnings, excluding any alert-severity ones present at the same time', () => {
    const rows = buildWarnings(
      warnings({ physical_address_unset: true, ses_sandbox_active: true, inbound_mail_unavailable: true }),
    );
    const text = statusWarningsAnnouncement(rows);
    expect(text).not.toContain('No physical mailing address is set');
    expect(text).toContain('This environment is configured for SES sandbox mode');
    expect(text).toContain('Inbound mailto: unsubscribe processing is not built yet');
  });

  it('changes when the set of non-alert warnings changes, so a persistent region\'s text genuinely mutates', () => {
    const before = statusWarningsAnnouncement(buildWarnings(warnings({ ses_sandbox_active: true })));
    const after = statusWarningsAnnouncement(buildWarnings(warnings({ ses_sandbox_active: false })));
    expect(before).not.toBe(after);
    expect(after).toBe('');
  });
});
