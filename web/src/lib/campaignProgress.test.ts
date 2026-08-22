import { describe, it, expect } from 'vitest';
import {
  isProgressForCampaign,
  isTerminalSnapshot,
  progressPercent,
  formatProgressSummary,
  formatProgressDetail,
  shouldShowProgress,
  progressHeading,
  progressVerdict,
  remainingForCancel,
  CAMPAIGN_PROGRESS_EVENT,
  type CampaignProgress,
} from './campaignProgress';

function progress(fields: Partial<CampaignProgress>): CampaignProgress {
  return {
    campaign_id: 1,
    total: 100,
    sent: 0,
    failed: 0,
    skipped: 0,
    remaining: 100,
    ...fields,
  };
}

describe('CAMPAIGN_PROGRESS_EVENT', () => {
  it('matches the backend event name', () => {
    expect(CAMPAIGN_PROGRESS_EVENT).toBe('campaign.progress');
  });
});

describe('isProgressForCampaign', () => {
  it('is true when campaign_id matches', () => {
    expect(isProgressForCampaign(progress({ campaign_id: 42 }), 42)).toBe(true);
  });

  it('is false when campaign_id differs — progress is broadcast to every admin', () => {
    expect(isProgressForCampaign(progress({ campaign_id: 42 }), 7)).toBe(false);
  });
});

describe('isTerminalSnapshot', () => {
  // #0048's review, point 1: this predicate is what drives
  // CampaignEditor.svelte's onProgressEvent to resync campaign.status the
  // moment a closing frame arrives, instead of only on a stream reconnect —
  // without it a successful send's own final "0 remaining" frame updates the
  // counts but leaves the heading reading "Sending…" forever.
  it('is true once remaining reaches 0, mid-send or at completion', () => {
    expect(isTerminalSnapshot(progress({ total: 10, sent: 10, remaining: 0 }))).toBe(true);
  });

  it('is true for a failCampaign-style partial snapshot with nothing left queued/sending', () => {
    // failCampaign (worker.go) can transition to 'failed' with some rows
    // already sent, some failed/skipped, and none left queued or sending —
    // remaining: 0 here does not imply sent === total.
    expect(
      isTerminalSnapshot(progress({ total: 10, sent: 4, failed: 1, skipped: 1, remaining: 0 })),
    ).toBe(true);
  });

  it('is false while any row is still queued or sending', () => {
    expect(isTerminalSnapshot(progress({ total: 10, sent: 6, remaining: 4 }))).toBe(false);
  });

  it('is false for the very first frame of a fresh send (nothing sent yet)', () => {
    expect(isTerminalSnapshot(progress({ total: 10, sent: 0, remaining: 10 }))).toBe(false);
  });
});

describe('progressPercent', () => {
  it('is 0 with nothing done', () => {
    expect(progressPercent(progress({ total: 100, sent: 0, failed: 0, skipped: 0 }))).toBe(0);
  });

  it('counts sent+failed+skipped as done, not just sent', () => {
    expect(progressPercent(progress({ total: 100, sent: 40, failed: 5, skipped: 5 }))).toBe(50);
  });

  it('is 100 when every row is accounted for', () => {
    expect(progressPercent(progress({ total: 10, sent: 8, failed: 1, skipped: 1 }))).toBe(100);
  });

  it('is 0, never NaN or negative, when total is 0 (not yet materialized)', () => {
    expect(progressPercent(progress({ total: 0, sent: 0, failed: 0, skipped: 0 }))).toBe(0);
  });

  it('clamps to 100 even if done somehow exceeds total', () => {
    expect(progressPercent(progress({ total: 10, sent: 9, failed: 9, skipped: 0 }))).toBe(100);
  });
});

describe('formatProgressSummary', () => {
  it('reads "N of M sent" with thousands separators', () => {
    expect(formatProgressSummary(progress({ sent: 482, total: 1203 }))).toBe('482 of 1,203 sent');
  });
});

describe('formatProgressDetail', () => {
  it('omits failed/skipped when both are zero', () => {
    const p = progress({ sent: 10, total: 20, failed: 0, skipped: 0, remaining: 10 });
    expect(formatProgressDetail(p)).toBe('10 of 20 sent · 10 remaining');
  });

  it('includes failed and skipped only when non-zero, in order', () => {
    const p = progress({ sent: 10, total: 20, failed: 2, skipped: 3, remaining: 5 });
    expect(formatProgressDetail(p)).toBe('10 of 20 sent · 2 failed · 3 skipped · 5 remaining');
  });

  it('always shows remaining, including 0', () => {
    const p = progress({ sent: 20, total: 20, failed: 0, skipped: 0, remaining: 0 });
    expect(formatProgressDetail(p)).toBe('20 of 20 sent · 0 remaining');
  });
});

describe('shouldShowProgress', () => {
  it('always shows while sending, even with no snapshot yet', () => {
    expect(shouldShowProgress('sending', false)).toBe(true);
    expect(shouldShowProgress('sending', true)).toBe(true);
  });

  it('shows a terminal status only when a snapshot arrived', () => {
    expect(shouldShowProgress('sent', true)).toBe(true);
    expect(shouldShowProgress('sent', false)).toBe(false);
    expect(shouldShowProgress('failed', true)).toBe(true);
    expect(shouldShowProgress('failed', false)).toBe(false);
    expect(shouldShowProgress('canceled', true)).toBe(true);
    expect(shouldShowProgress('canceled', false)).toBe(false);
  });

  it('never shows for draft/scheduled regardless of snapshot', () => {
    expect(shouldShowProgress('draft', true)).toBe(false);
    expect(shouldShowProgress('scheduled', true)).toBe(false);
  });
});

describe('progressHeading', () => {
  it('has a heading for every status the region can show', () => {
    expect(progressHeading('sending')).toBe('Sending…');
    expect(progressHeading('sent')).toBe('Send complete');
    expect(progressHeading('failed')).toBe('Send stopped');
    expect(progressHeading('canceled')).toBe('Send canceled');
  });

  it('is empty for a status the region never shows', () => {
    expect(progressHeading('draft')).toBe('');
    expect(progressHeading('scheduled')).toBe('');
    expect(progressHeading('')).toBe('');
  });
});

describe('progressVerdict', () => {
  const now = 1_000_000;

  it('is not-sending for any non-sending status, regardless of timing', () => {
    expect(progressVerdict('sent', now - 1, now)).toBe('not-sending');
    expect(progressVerdict('draft', null, now)).toBe('not-sending');
  });

  it('is running with no data yet (just started/just opened)', () => {
    expect(progressVerdict('sending', null, now)).toBe('running');
  });

  it('is running when the last update was recent', () => {
    expect(progressVerdict('sending', now - 5_000, now)).toBe('running');
  });

  it('is stalled once the gap exceeds the threshold', () => {
    expect(progressVerdict('sending', now - 30_001, now)).toBe('stalled');
  });

  it('is running exactly at the threshold boundary (strictly greater triggers stalled)', () => {
    expect(progressVerdict('sending', now - 30_000, now)).toBe('running');
  });
});

describe('remainingForCancel', () => {
  it('is undefined when status is not sending', () => {
    expect(remainingForCancel('scheduled', 1, progress({ campaign_id: 1, remaining: 5 }))).toBeUndefined();
  });

  it('is undefined when there is no snapshot at all', () => {
    expect(remainingForCancel('sending', 1, null)).toBeUndefined();
  });

  it('is undefined when the only snapshot is for a different campaign', () => {
    expect(remainingForCancel('sending', 1, progress({ campaign_id: 2, remaining: 5 }))).toBeUndefined();
  });

  it('returns the live remaining count when sending and the snapshot matches', () => {
    expect(remainingForCancel('sending', 1, progress({ campaign_id: 1, remaining: 706 }))).toBe(706);
  });

  it('returns 0 (not undefined) when remaining is genuinely 0', () => {
    expect(remainingForCancel('sending', 1, progress({ campaign_id: 1, remaining: 0 }))).toBe(0);
  });
});
