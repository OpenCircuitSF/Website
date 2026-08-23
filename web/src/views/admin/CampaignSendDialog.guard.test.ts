// #0186: CampaignSendDialog.svelte now computes its `sending`/`blockedAgain`
// display state and its "may this submit act" decision from
// sendGuardState(...).kind instead of a raw `inFlight` boolean and a bare
// `unmet.length > 0` check. Nothing in a .svelte file is covered by a test
// (#0094), so this file proves the wiring is behavior-preserving BY
// EXECUTION rather than by inspection: it replicates, side by side, the
// component's OLD formulas (exactly as they existed immediately before this
// issue's commit — see git history for CampaignSendDialog.svelte) and its
// NEW guard-based formulas, across every (inFlight, unmet, confirmRaw)
// combination the dialog can actually be handed, and asserts where they
// agree and — for the two deliberate exceptions — exactly how and why they
// differ.
//
// The dialog now threads the real `status` prop through (#0189) instead of
// a hardcoded stand-in, but every case below still fixes status to 'draft'
// — that is the only status this dialog is reachable with today
// (canSendCampaign gates the mount), and it is the only status for which
// `oldSending`/`oldBlockedAgain` below were ever accurate, since they
// predate `status` existing at all. sendGuardState's own tests
// (sendConfirm.test.ts) already cover the `status !== 'draft'` branch.

import { describe, it, expect } from 'vitest';
import type { UnmetRequirement } from '../../lib/types';
import { normalizeCountInput, sendGuardState } from '../../lib/sendConfirm';

const noUnmet: UnmetRequirement[] = [];
const oneUnmet: UnmetRequirement[] = [{ code: 'physical_address_missing', message: 'x' }];
const twoUnmet: UnmetRequirement[] = [
  { code: 'physical_address_missing', message: 'x' },
  { code: 'suppressed_present', message: 'y' },
];

interface Case {
  name: string;
  inFlight: boolean;
  unmet: UnmetRequirement[];
  audienceCount: number;
  confirmRaw: string;
}

// Every combination the dialog can realistically be handed. `audienceCount`
// is always > 0 at open time (the editor only opens the dialog when its own
// guard is 'ready', which requires a non-zero audience). `unmet` can be
// non-empty even with `confirmRaw` already matching: a failed send
// re-populates `unmet` without clearing whatever the operator had typed,
// and a failed send also clears `inFlight` back to false in the same tick
// as `unmet` gets populated — so "blocked, but the typed count still
// matches" (idle) and "in flight again, with unmet still stale from the
// send that just failed" (a same-count retry click) are both real states.
const cases: Case[] = [
  { name: 'idle, untouched input', inFlight: false, unmet: noUnmet, audienceCount: 482, confirmRaw: '' },
  { name: 'idle, wrong count typed', inFlight: false, unmet: noUnmet, audienceCount: 482, confirmRaw: '999' },
  { name: 'idle, correct count typed (ready)', inFlight: false, unmet: noUnmet, audienceCount: 482, confirmRaw: '482' },
  { name: 'idle, blocked, no input yet', inFlight: false, unmet: oneUnmet, audienceCount: 482, confirmRaw: '' },
  {
    name: 'idle, blocked, correct count typed anyway',
    inFlight: false,
    unmet: oneUnmet,
    audienceCount: 482,
    confirmRaw: '482',
  },
  {
    name: 'idle, blocked with two reasons, correct count typed anyway',
    inFlight: false,
    unmet: twoUnmet,
    audienceCount: 482,
    confirmRaw: '482',
  },
  {
    name: 'in flight, no unmet (the ordinary send)',
    inFlight: true,
    unmet: noUnmet,
    audienceCount: 482,
    confirmRaw: '482',
  },
  {
    name: 'in flight, unmet stale from a just-failed prior attempt',
    inFlight: true,
    unmet: oneUnmet,
    audienceCount: 482,
    confirmRaw: '482',
  },
  // A real U+2009 thin space (#0189), as normalizeCountInput's own doc
  // comment says it accepts as a thousands separator — not the ASCII space
  // a since-deleted hand-rolled replica of that function used to check for.
  // Belt-and-braces here, not the discriminating assertion: both sides of
  // this table call the same shared normalizer, so a thin-space regression
  // would flip old and new together and this case would stay green. The
  // positive, discriminating assertion for U+2009 support lives in
  // sendConfirm.test.ts's `parses` table.
  {
    name: 'idle, correct count typed with a real thin-space thousands separator',
    inFlight: false,
    unmet: noUnmet,
    audienceCount: 1482,
    confirmRaw: '1 482',
  },
];

/** The dialog's formulas as they existed immediately before #0186. */
function oldSending(c: Case): boolean {
  return c.inFlight;
}
function oldBlockedAgain(c: Case): boolean {
  return c.unmet.length > 0;
}
/** handleSubmit's old bail-out: checked inFlight and the typed count, but
 *  never looked at `unmet` at all. Calls the real `normalizeCountInput`
 *  (sendConfirm.ts) rather than replicating its separator-stripping regex —
 *  a hand-rolled copy of that regex previously used an ASCII space where the
 *  real function uses a U+2009 thin space, and came within one test case of
 *  being silently wrong about it (see #0184's review notes and #0189). */
function oldMaySubmit(c: Case): boolean {
  if (c.inFlight) return false;
  const n = normalizeCountInput(c.confirmRaw);
  return n !== null && n === c.audienceCount;
}

/** The dialog's formulas since #0186, via sendGuardState. */
function guardOf(c: Case) {
  return sendGuardState({
    status: 'draft',
    unmet: c.unmet,
    audienceCount: c.audienceCount,
    confirmRaw: c.confirmRaw,
    inFlight: c.inFlight,
  });
}
function newSending(c: Case): boolean {
  return guardOf(c).kind === 'sending';
}
function newBlockedAgain(c: Case): boolean {
  return guardOf(c).kind === 'blocked';
}
function newMaySubmit(c: Case): boolean {
  return guardOf(c).kind === 'ready';
}

describe('CampaignSendDialog guard wiring (#0186) — old raw-boolean formulas vs. sendGuardState', () => {
  it.each(cases)('%s: `sending` is unchanged', (c) => {
    expect(newSending(c)).toBe(oldSending(c));
  });

  it.each(cases)('%s: `blockedAgain` matches old, EXCEPT the documented sending-wins case', (c) => {
    const isTheSendingWinsCase = c.inFlight && c.unmet.length > 0;
    if (isTheSendingWinsCase) {
      expect(oldBlockedAgain(c)).toBe(true); // old: showed "no longer ready to send" WHILE also disabled/sending
      expect(newBlockedAgain(c)).toBe(false); // new: sending wins — no "retry" copy shown mid-request
      expect(newSending(c)).toBe(true); // the button/input correctly read as busy instead
    } else {
      expect(newBlockedAgain(c)).toBe(oldBlockedAgain(c));
    }
  });

  it.each(cases)('%s: may-submit matches old, EXCEPT the documented double-send-gap fix', (c) => {
    const isTheDoubleSendGapCase = !c.inFlight && c.unmet.length > 0 && newMaySubmit({ ...c, unmet: [] });
    if (isTheDoubleSendGapCase) {
      expect(oldMaySubmit(c)).toBe(true); // the pre-#0186 bug: onConfirm would fire again despite `unmet`
      expect(newMaySubmit(c)).toBe(false); // guard.kind is 'blocked', not 'ready' — refused
    } else {
      expect(newMaySubmit(c)).toBe(oldMaySubmit(c));
    }
  });

  it('sanity: exactly one case exercises the sending-wins divergence, and exactly two exercise the double-send-gap fix', () => {
    const sendingWins = cases.filter((c) => c.inFlight && c.unmet.length > 0);
    const doubleSendGap = cases.filter(
      (c) => !c.inFlight && c.unmet.length > 0 && newMaySubmit({ ...c, unmet: [] }),
    );
    expect(sendingWins.map((c) => c.name)).toEqual(['in flight, unmet stale from a just-failed prior attempt']);
    expect(doubleSendGap.map((c) => c.name)).toEqual([
      'idle, blocked, correct count typed anyway',
      'idle, blocked with two reasons, correct count typed anyway',
    ]);
  });
});
