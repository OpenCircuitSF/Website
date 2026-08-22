// Unit tests for the campaign stats screen's pure logic: bucket ordering,
// the reconciled-vs-raw bounced/complained substitution, the 0.3%
// complaint-rate threshold verdict (both sides of the boundary pinned), and
// failed-send display.

import { describe, it, expect } from 'vitest';
import type { CampaignStatsResponse } from './types';
import {
  buildStatBuckets,
  totalSendRows,
  complaintRateVerdict,
  campaignComplaintRateVerdict,
  formatComplaintRate,
  failedSendErrorLabel,
  canViewCampaignStats,
  COMPLAINT_RATE_THRESHOLD_PCT,
} from './campaignStats';

function stats(overrides: Partial<CampaignStatsResponse> = {}): CampaignStatsResponse {
  return {
    campaign_id: 1,
    status: 'sent',
    counts: {
      queued: 0,
      sending: 0,
      sent: 0,
      failed: 0,
      bounced: 0,
      complained: 0,
      skipped: 0,
    },
    reconciled: { bounced: 0, complained: 0 },
    failed_sends: [],
    ...overrides,
  };
}

describe('buildStatBuckets', () => {
  it('returns all seven buckets in the pinned order: sent, queued, sending, failed, bounced, complained, skipped', () => {
    const s = stats({
      counts: { queued: 1, sending: 2, sent: 3, failed: 4, bounced: 999, complained: 999, skipped: 7 },
      reconciled: { bounced: 5, complained: 6 },
    });
    const buckets = buildStatBuckets(s);
    expect(buckets.map((b) => b.key)).toEqual([
      'sent',
      'queued',
      'sending',
      'failed',
      'bounced',
      'complained',
      'skipped',
    ]);
    expect(buckets.map((b) => b.count)).toEqual([3, 1, 2, 4, 5, 6, 7]);
  });

  it('substitutes reconciled bounced/complained, ignoring the always-zero raw counts', () => {
    // Raw counts.bounced/complained are nonzero here only to prove the
    // function does NOT read them — in the real payload they are always 0
    // (see internal/mailing/campaign_stats.go's package doc comment).
    const s = stats({
      counts: { queued: 0, sending: 0, sent: 10, failed: 0, bounced: 42, complained: 42, skipped: 0 },
      reconciled: { bounced: 2, complained: 1 },
    });
    const buckets = buildStatBuckets(s);
    const bounced = buckets.find((b) => b.key === 'bounced');
    const complained = buckets.find((b) => b.key === 'complained');
    expect(bounced?.count).toBe(2);
    expect(complained?.count).toBe(1);
  });

  it('skipped is its own bucket, distinct from failed, and is never folded into it', () => {
    const s = stats({
      counts: { queued: 0, sending: 0, sent: 0, failed: 3, bounced: 0, complained: 0, skipped: 5 },
    });
    const buckets = buildStatBuckets(s);
    const failed = buckets.find((b) => b.key === 'failed');
    const skipped = buckets.find((b) => b.key === 'skipped');
    expect(failed?.count).toBe(3);
    expect(skipped?.count).toBe(5);
    expect(failed?.tone).toBe('danger');
    expect(skipped?.tone).not.toBe('danger');
  });
});

describe('totalSendRows', () => {
  it('sums every raw bucket', () => {
    const s = stats({
      counts: { queued: 1, sending: 2, sent: 3, failed: 4, bounced: 5, complained: 6, skipped: 7 },
    });
    expect(totalSendRows(s)).toBe(28);
  });
});

describe('complaintRateVerdict', () => {
  it('is 0 and not over threshold when sentCount is 0 (never a false positive on a fresh draft)', () => {
    const v = complaintRateVerdict(0, 0);
    expect(v.ratePct).toBe(0);
    expect(v.overThreshold).toBe(false);
  });

  it('is 0 and not over threshold when sentCount is 0 even with a nonzero complained count (impossible in practice, still must not divide-by-zero into a warning)', () => {
    const v = complaintRateVerdict(0, 5);
    expect(v.ratePct).toBe(0);
    expect(v.overThreshold).toBe(false);
  });

  it('pins the boundary just BELOW 0.3% as not over threshold', () => {
    // 29 / 10000 = 0.29%
    const v = complaintRateVerdict(10000, 29);
    expect(v.ratePct).toBeCloseTo(0.29, 5);
    expect(v.overThreshold).toBe(false);
  });

  it('pins the boundary EXACTLY AT 0.3% as over threshold (inclusive ceiling)', () => {
    // 30 / 10000 = 0.30%
    const v = complaintRateVerdict(10000, 30);
    expect(v.ratePct).toBeCloseTo(0.3, 5);
    expect(v.ratePct).toBeCloseTo(COMPLAINT_RATE_THRESHOLD_PCT, 5);
    expect(v.overThreshold).toBe(true);
  });

  it('pins just ABOVE 0.3% as over threshold', () => {
    // 31 / 10000 = 0.31%
    const v = complaintRateVerdict(10000, 31);
    expect(v.ratePct).toBeCloseTo(0.31, 5);
    expect(v.overThreshold).toBe(true);
  });

  it('formats to two decimal places', () => {
    const v = complaintRateVerdict(10000, 30);
    expect(v.formatted).toBe('0.30%');
  });
});

describe('formatComplaintRate', () => {
  it('always renders two decimal places, including whole numbers', () => {
    expect(formatComplaintRate(0)).toBe('0.00%');
    expect(formatComplaintRate(1)).toBe('1.00%');
    expect(formatComplaintRate(0.3)).toBe('0.30%');
  });
});

describe('campaignComplaintRateVerdict', () => {
  it('reads sent from counts and complained from reconciled, not counts.complained', () => {
    const s = stats({
      counts: { queued: 0, sending: 0, sent: 1000, failed: 0, bounced: 0, complained: 999, skipped: 0 },
      reconciled: { bounced: 0, complained: 3 },
    });
    const v = campaignComplaintRateVerdict(s);
    // 3 / 1000 = 0.3% — exactly at threshold, using reconciled.complained (3), not counts.complained (999).
    expect(v.ratePct).toBeCloseTo(0.3, 5);
    expect(v.overThreshold).toBe(true);
  });
});

describe('failedSendErrorLabel', () => {
  it('renders the error message when present', () => {
    expect(failedSendErrorLabel({ id: 1, email: 'a@example.com', error: 'boom', attempts: 1 })).toBe('boom');
  });

  it('falls back to a placeholder when error is undefined', () => {
    expect(failedSendErrorLabel({ id: 1, email: 'a@example.com', attempts: 1 })).toBe(
      'No error message recorded',
    );
  });

  it('falls back to a placeholder when error is an empty or whitespace-only string', () => {
    expect(failedSendErrorLabel({ id: 1, email: 'a@example.com', error: '', attempts: 1 })).toBe(
      'No error message recorded',
    );
    expect(failedSendErrorLabel({ id: 1, email: 'a@example.com', error: '   ', attempts: 1 })).toBe(
      'No error message recorded',
    );
  });
});

describe('canViewCampaignStats', () => {
  it('is false only for draft', () => {
    expect(canViewCampaignStats('draft')).toBe(false);
  });

  it('is true for every other campaign status', () => {
    for (const status of ['scheduled', 'sending', 'sent', 'canceled', 'failed']) {
      expect(canViewCampaignStats(status)).toBe(true);
    }
  });
});
