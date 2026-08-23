// Unit tests for the campaign compose screen's typed-count confirmation and
// Send-button guard state. sendGuardState is the single source of truth for
// whether mail can go out from this screen — its "blocked even with a
// correct count" case is the mutation-proof target named in #0047's plan
// §9: deleting the `unmet.length > 0` branch must turn that case red (the
// implementer performs and reverts that edit during verification; see the
// issue's ## Verification section for the recorded proof).

import { describe, it, expect } from 'vitest';
import type { UnmetRequirement } from './types';
import {
  normalizeCountInput,
  confirmMatches,
  confirmHint,
  sendGuardState,
  sendGuardDescription,
  sendGuardSummaryClass,
  type SendGuardState,
} from './sendConfirm';

describe('normalizeCountInput', () => {
  it.each([
    ['1234', 1234],
    [' 1234 ', 1234],
    ['1,234', 1234],
    ['0482', 482],
    // #0183: the doc comment promises U+2009 (thin space) as a thousands
    // separator, distinct from the ASCII space '1234 ' above already tests
    // via trim(). Dropping U+2009 from the separator character class passed
    // 41/41 before this case existed.
    ['1 234', 1234],
  ])('parses %j', (raw, want) => {
    expect(normalizeCountInput(raw)).toBe(want);
  });

  it.each([
    [''],
    ['abc'],
    ['12.5'],
    ['12 34x'],
    ['-1'],
    // #0183: a 20-digit typed count is a valid \d+ string but not
    // representable as a safe integer -- deleting the Number.isSafeInteger
    // guard let it through as an inexact float instead of rejecting it.
    // Passed 41/41 before this case existed.
    ['9'.repeat(20)],
    // #0183 sweep finding: a lone separator strips to an empty string,
    // which is a DIFFERENT code path than the raw='' early return above --
    // it must still fail the \d+ digit requirement rather than being read
    // as 0. Loosening that regex from \d+ to \d* (allowing an empty match)
    // passed 122/122 before this case existed.
    [','],
  ])('rejects %j', (raw) => {
    expect(normalizeCountInput(raw)).toBeNull();
  });
});

describe('confirmMatches', () => {
  it('is strict — 483 does not confirm 482', () => {
    expect(confirmMatches('483', 482)).toBe(false);
  });

  it('matches an exact typed count', () => {
    expect(confirmMatches('482', 482)).toBe(true);
  });

  it('matches with thousands separators', () => {
    expect(confirmMatches('1,482', 1482)).toBe(true);
  });
});

// #0179: confirmHint guards a send (#0047 §7) -- it is what tells an admin
// their typed count is wrong before the Send gate accepts it. Asserted
// semantically (what the hint CLAIMS -- that it names the expected count),
// not by pinning one phrasing of the sentence.
describe('confirmHint', () => {
  it('is null while the field is untouched (empty) -- no scolding an empty field', () => {
    expect(confirmHint('', 482)).toBeNull();
  });

  // #0183 sweep finding: a whitespace-only field is a DIFFERENT code path
  // than the exact-empty-string case above -- it only reaches "untouched"
  // via `.trim()`. Narrowing that check from `raw.trim() === ''` to
  // `raw === ''` passed 122/122 before this case existed.
  it('is null for a whitespace-only field too, not just exact empty string', () => {
    expect(confirmHint('   ', 482)).toBeNull();
  });

  it('is null once the typed count matches exactly', () => {
    expect(confirmHint('482', 482)).toBeNull();
  });

  it('on a mismatch, names the expected count and not the typed one', () => {
    // 999 and 482 share no digits, so a hint that named the wrong number
    // (the typed value, or any other count) fails toContain('482') here --
    // this is what makes the assertion fail if the hint stops naming the
    // expected count, or names the wrong one.
    const hint = confirmHint('999', 482);
    expect(hint).not.toBeNull();
    expect(hint).toContain('482');
    expect(hint).not.toContain('999');
  });

  it('on a mismatch from non-numeric input, still names the expected count', () => {
    const hint = confirmHint('abc', 482);
    expect(hint).not.toBeNull();
    expect(hint).toContain('482');
  });
});

const noUnmet: UnmetRequirement[] = [];
const oneUnmet: UnmetRequirement[] = [{ code: 'physical_address_missing', message: 'x' }];

describe('sendGuardState', () => {
  const cases: Array<{ name: string; input: Parameters<typeof sendGuardState>[0]; want: SendGuardState['kind'] }> = [
    {
      name: 'blocked when unmet is non-empty, even with a correct count',
      input: { status: 'draft', unmet: oneUnmet, audienceCount: 482, confirmRaw: '482', inFlight: false },
      want: 'blocked',
    },
    {
      // #0183: pins the branch order -- no other case sets both `unmet`
      // non-empty AND `audienceCount === 0`, so swapping those two branches
      // in sendGuardState passed 41/41 before this case existed.
      name: 'blocked wins over empty-audience when both unmet is non-empty and audienceCount is 0',
      input: { status: 'draft', unmet: oneUnmet, audienceCount: 0, confirmRaw: '', inFlight: false },
      want: 'blocked',
    },
    {
      name: 'empty-audience at count 0',
      input: { status: 'draft', unmet: noUnmet, audienceCount: 0, confirmRaw: '', inFlight: false },
      want: 'empty-audience',
    },
    {
      name: 'needs-confirm when clear but mistyped',
      input: { status: 'draft', unmet: noUnmet, audienceCount: 482, confirmRaw: '483', inFlight: false },
      want: 'needs-confirm',
    },
    {
      name: 'needs-confirm when clear but untouched',
      input: { status: 'draft', unmet: noUnmet, audienceCount: 482, confirmRaw: '', inFlight: false },
      want: 'needs-confirm',
    },
    {
      name: 'ready when unmet is empty, count > 0, confirmation matches, status is draft',
      input: { status: 'draft', unmet: noUnmet, audienceCount: 482, confirmRaw: '482', inFlight: false },
      want: 'ready',
    },
    {
      name: 'sending while in flight, even if otherwise ready',
      input: { status: 'draft', unmet: noUnmet, audienceCount: 482, confirmRaw: '482', inFlight: true },
      want: 'sending',
    },
    {
      name: 'unavailable when status is scheduled',
      input: { status: 'scheduled', unmet: noUnmet, audienceCount: 482, confirmRaw: '482', inFlight: false },
      want: 'unavailable',
    },
    {
      name: 'unavailable when status is sending',
      input: { status: 'sending', unmet: noUnmet, audienceCount: 482, confirmRaw: '482', inFlight: false },
      want: 'unavailable',
    },
    {
      name: 'unavailable when status is sent',
      input: { status: 'sent', unmet: noUnmet, audienceCount: 482, confirmRaw: '482', inFlight: false },
      want: 'unavailable',
    },
    {
      name: 'unavailable when status is canceled',
      input: { status: 'canceled', unmet: noUnmet, audienceCount: 482, confirmRaw: '482', inFlight: false },
      want: 'unavailable',
    },
    {
      name: 'unavailable when status is failed',
      input: { status: 'failed', unmet: noUnmet, audienceCount: 482, confirmRaw: '482', inFlight: false },
      want: 'unavailable',
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(sendGuardState(c.input).kind).toBe(c.want);
    });
  }
});

// #0119: the blocked Send button's aria-describedby target renders exactly
// this text. Every kind the editor-level guard (CampaignEditor.svelte's
// editorGuard, which never produces 'needs-confirm' or 'sending') can
// actually reach must announce a real, non-empty reason — the whole point
// of this issue is that "blocked, no reason given" is silent to a screen
// reader. 'unavailable' is the sole deliberate exception: the send control
// isn't rendered in that state at all (canSendCampaign is the render gate),
// so there is nothing for it to describe.
describe('sendGuardDescription', () => {
  const reachableAtEditorLevel: SendGuardState['kind'][] = ['blocked', 'empty-audience', 'ready'];

  it.each(reachableAtEditorLevel)('names a non-empty reason for %s', (kind) => {
    const desc = sendGuardDescription({ kind } as SendGuardState);
    expect(desc.length).toBeGreaterThan(0);
  });

  it('blocked names "pre-send checks" — #0119 AC: the description names the pre-send checks', () => {
    expect(sendGuardDescription({ kind: 'blocked' }).toLowerCase()).toContain('pre-send checks');
  });

  it('empty-audience is DISTINCT text from blocked — a plain unmet-list rendering would have missed this reason', () => {
    const blocked = sendGuardDescription({ kind: 'blocked' });
    const emptyAudience = sendGuardDescription({ kind: 'empty-audience' });
    expect(emptyAudience).not.toBe(blocked);
    expect(emptyAudience.toLowerCase()).toContain('audience');
  });

  it('ready describes success, distinct from every blocking reason', () => {
    const ready = sendGuardDescription({ kind: 'ready' });
    expect(ready).not.toBe(sendGuardDescription({ kind: 'blocked' }));
    expect(ready).not.toBe(sendGuardDescription({ kind: 'empty-audience' }));
  });

  it('unavailable is empty — the send control is not rendered in this state', () => {
    expect(sendGuardDescription({ kind: 'unavailable' })).toBe('');
  });

  it('sending and needs-confirm are non-empty too, even though the editor-level guard never reaches them', () => {
    expect(sendGuardDescription({ kind: 'sending' }).length).toBeGreaterThan(0);
    expect(sendGuardDescription({ kind: 'needs-confirm' }).length).toBeGreaterThan(0);
  });
});

describe('sendGuardSummaryClass', () => {
  it('is text-notice only when ready', () => {
    expect(sendGuardSummaryClass({ kind: 'ready' })).toBe('text-notice');
  });

  it.each<SendGuardState['kind']>(['blocked', 'empty-audience', 'sending', 'needs-confirm', 'unavailable'])(
    'is text-muted (never the success color) for %s',
    (kind) => {
      expect(sendGuardSummaryClass({ kind } as SendGuardState)).toBe('text-muted');
    },
  );
});
