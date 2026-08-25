import { describe, it, expect } from 'vitest';
import {
  isProgressForCampaign,
  isTerminalSnapshot,
  isTerminalCampaignStatus,
  TERMINAL_CAMPAIGN_STATUSES,
  createResyncSequencer,
  progressPercent,
  formatProgressSummary,
  formatProgressDetail,
  shouldShowProgress,
  progressHeading,
  progressVerdict,
  remainingForCancel,
  pausedDeliveryHealthExplanation,
  CAMPAIGN_PROGRESS_EVENT,
  type CampaignProgress,
} from './campaignProgress';

function progress(fields: Partial<CampaignProgress>): CampaignProgress {
  return {
    campaign_id: 1,
    status: 'sending',
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

describe('isTerminalCampaignStatus', () => {
  it('is true for exactly the three statuses a send never resumes from', () => {
    expect(TERMINAL_CAMPAIGN_STATUSES.slice().sort()).toEqual(['canceled', 'failed', 'sent']);
    for (const s of TERMINAL_CAMPAIGN_STATUSES) {
      expect(isTerminalCampaignStatus(s)).toBe(true);
    }
  });

  it('is false for every non-terminal status, and for an unknown/blank one', () => {
    for (const s of ['draft', 'scheduled', 'sending', 'paused_delivery_health', '', 'SENT', 'unknown']) {
      expect(isTerminalCampaignStatus(s)).toBe(false);
    }
  });
});

describe('isTerminalSnapshot', () => {
  // #0048's review, point 1: this predicate is what drives
  // CampaignEditor.svelte's onProgressEvent to resync campaign.status when a
  // send ends, instead of only on a stream reconnect — without it a
  // completed send leaves the heading reading "Sending…" forever, then adds
  // a false "stalled" warning 30s later.
  //
  // #0048's SECOND review, point 3: it must key off the frame's campaign
  // status, not its counts. MarkFailedCampaign never touches email_sends, so
  // a failed campaign publishes remaining > 0 — the `remaining === 0` version
  // of this predicate was false on exactly the paths that most needed it.

  describe('by campaign status — the authoritative signal', () => {
    it('is true for a failed campaign that still has queued rows (the point-3 regression)', () => {
      // The literal shape failCampaign publishes: physical_address_missing /
      // reply_to_missing / a terminal SES error stops the drain, the campaign
      // is 'failed', and the recipients never mailed are still counted in
      // remaining — which is correct, and is why remaining cannot be the test.
      expect(
        isTerminalSnapshot(progress({ status: 'failed', total: 10, sent: 4, remaining: 6 })),
      ).toBe(true);
    });

    it('is true for a canceled campaign with rows still queued', () => {
      expect(
        isTerminalSnapshot(progress({ status: 'canceled', total: 10, sent: 2, remaining: 8 })),
      ).toBe(true);
    });

    it('is true for a completed send', () => {
      expect(
        isTerminalSnapshot(progress({ status: 'sent', total: 10, sent: 10, remaining: 0 })),
      ).toBe(true);
    });

    it('is false for a live send, whatever the counts say', () => {
      expect(
        isTerminalSnapshot(progress({ status: 'sending', total: 10, sent: 6, remaining: 4 })),
      ).toBe(false);
    });

    it('is false for a campaign that never started sending', () => {
      for (const status of ['draft', 'scheduled']) {
        expect(isTerminalSnapshot(progress({ status, total: 10, remaining: 10 }))).toBe(false);
      }
    });

    // #0124: the circuit breaker's own status has the identical
    // "rows deliberately left queued" shape as 'failed' — the worker never
    // drains the remaining recipients, it just stops.
    it('is true for a paused_delivery_health campaign with rows still queued', () => {
      expect(
        isTerminalSnapshot(progress({ status: 'paused_delivery_health', total: 10, sent: 6, remaining: 4 })),
      ).toBe(true);
    });
  });

  describe('the remaining === 0 fallback, and its boundary', () => {
    it("is true for drainLoop's last-batch frame, still 'sending' but drained", () => {
      // The bottom-of-batch publish beats CompleteIfDone's status flip, so
      // this frame really does arrive as sending/remaining:0. Treating it as
      // possibly-closing is what makes the resync fire promptly; the second,
      // 'sent' frame follows and createResyncSequencer orders the two.
      expect(
        isTerminalSnapshot(progress({ status: 'sending', total: 10, sent: 10, remaining: 0 })),
      ).toBe(true);
    });

    it('is FALSE with exactly one row left — the boundary a <= 1 mutant would pass', () => {
      // #0048's second review, point 4: the previous negative cases used
      // remaining 4 and 10, so `p.remaining === 0` -> `p.remaining <= 1`
      // survived the whole suite. This case is what kills that mutant.
      expect(
        isTerminalSnapshot(progress({ status: 'sending', total: 10, sent: 9, remaining: 1 })),
      ).toBe(false);
    });

    it('is false for the very first frame of a fresh send (nothing sent yet)', () => {
      expect(
        isTerminalSnapshot(progress({ status: 'sending', total: 10, sent: 0, remaining: 10 })),
      ).toBe(false);
    });
  });

  describe('degrading when the payload carries no status (#0095 drift)', () => {
    // A bundle running against a server that predates the status field, or a
    // worker-side status read that errored and published "". The fallback
    // must keep the successful-send path working rather than silently
    // reinstating the "Sending…" forever bug.
    it('falls back to the count when status is blank', () => {
      expect(isTerminalSnapshot(progress({ status: '', remaining: 0 }))).toBe(true);
      expect(isTerminalSnapshot(progress({ status: '', remaining: 1 }))).toBe(false);
    });

    it('does not throw when status is missing entirely at runtime', () => {
      const noStatus = { campaign_id: 1, total: 10, sent: 10, failed: 0, skipped: 0, remaining: 0 };
      expect(isTerminalSnapshot(noStatus as unknown as CampaignProgress)).toBe(true);
      expect(
        isTerminalSnapshot({ ...noStatus, sent: 9, remaining: 1 } as unknown as CampaignProgress),
      ).toBe(false);
    });
  });
});

describe('createResyncSequencer', () => {
  // #0048's second review, point 5. Two resyncs are genuinely in flight at
  // once on a successful send (see isTerminalSnapshot's last-batch case); if
  // the earlier one's response — which can predate CompleteIfDone's commit
  // and therefore still read 'sending' — is applied last, the view reverts to
  // "Sending…" with no further frames coming.
  it('accepts the only request in flight', () => {
    const seq = createResyncSequencer();
    const a = seq.begin();
    expect(seq.isCurrent(a)).toBe(true);
  });

  it('discards an earlier request once a later one starts, even if it resolves last', () => {
    const seq = createResyncSequencer();
    const first = seq.begin();
    const second = seq.begin();
    // Responses arrive out of order: second, then first.
    expect(seq.isCurrent(second)).toBe(true);
    expect(seq.isCurrent(first)).toBe(false);
  });

  it('keeps accepting the newest across many overlapping requests', () => {
    const seq = createResyncSequencer();
    const tokens = [seq.begin(), seq.begin(), seq.begin(), seq.begin()];
    expect(tokens.map((t) => seq.isCurrent(t))).toEqual([false, false, false, true]);
  });

  it('issues strictly increasing positive tokens', () => {
    const seq = createResyncSequencer();
    expect([seq.begin(), seq.begin(), seq.begin()]).toEqual([1, 2, 3]);
  });

  it('never treats 0 as a live request', () => {
    // The counter's own initial value, and what any defaulted variable would
    // carry — it must not wave an unsequenced write through, before or after
    // any request has started.
    const seq = createResyncSequencer();
    expect(seq.isCurrent(0)).toBe(false);
    seq.begin();
    expect(seq.isCurrent(0)).toBe(false);
  });

  it('sequences two independent flows separately', () => {
    const a = createResyncSequencer();
    const b = createResyncSequencer();
    const aToken = a.begin();
    b.begin();
    b.begin();
    expect(a.isCurrent(aToken)).toBe(true);
  });

  it('applies only the last-started response in a real out-of-order race', async () => {
    // The exact CampaignEditor.svelte shape: two overlapping best-effort
    // re-reads, the STALE one resolving second.
    const seq = createResyncSequencer();
    let applied: string | null = null;
    const resync = async (value: string, delayMs: number): Promise<void> => {
      const token = seq.begin();
      await new Promise((r) => setTimeout(r, delayMs));
      if (!seq.isCurrent(token)) return;
      applied = value;
    };
    const stale = resync('sending', 30); // started first, resolves LAST
    const fresh = resync('sent', 0); // started second, resolves first
    await Promise.all([stale, fresh]);
    expect(applied).toBe('sent');
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

  // #0124: not in TERMINAL_CAMPAIGN_STATUSES (a paused send can resume),
  // but it needs the identical "only with a snapshot" treatment terminal
  // statuses get — an operator who opens a paused campaign wants to see
  // how far the send got.
  it('shows paused_delivery_health only when a snapshot arrived, like a terminal status', () => {
    expect(shouldShowProgress('paused_delivery_health', true)).toBe(true);
    expect(shouldShowProgress('paused_delivery_health', false)).toBe(false);
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
    expect(progressHeading('paused_delivery_health')).toBe('Paused — delivery health');
  });

  it('is empty for a status the region never shows', () => {
    expect(progressHeading('draft')).toBe('');
    expect(progressHeading('scheduled')).toBe('');
    expect(progressHeading('')).toBe('');
  });
});

describe('pausedDeliveryHealthExplanation', () => {
  it('returns non-empty, stable copy', () => {
    const msg = pausedDeliveryHealthExplanation();
    expect(msg.length).toBeGreaterThan(0);
    expect(pausedDeliveryHealthExplanation()).toBe(msg);
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

  // #0124: a paused campaign has the same "some sent, some queued" shape
  // as a sending one, so the same live count applies.
  it('also returns the live remaining count for paused_delivery_health', () => {
    expect(
      remainingForCancel('paused_delivery_health', 1, progress({ campaign_id: 1, status: 'paused_delivery_health', remaining: 42 })),
    ).toBe(42);
  });
});
