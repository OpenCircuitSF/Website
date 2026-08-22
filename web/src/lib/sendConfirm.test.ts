// Unit tests for the campaign compose screen's typed-count confirmation and
// Send-button guard state. sendGuardState is the single source of truth for
// whether mail can go out from this screen — its "blocked even with a
// correct count" case is the mutation-proof target named in #0047's plan
// §9: deleting the `unmet.length > 0` branch must turn that case red (the
// implementer performs and reverts that edit during verification; see the
// issue's ## Verification section for the recorded proof).

import { describe, it, expect } from 'vitest';
import type { UnmetRequirement } from './types';
import { normalizeCountInput, confirmMatches, sendGuardState, type SendGuardState } from './sendConfirm';

describe('normalizeCountInput', () => {
  it.each([
    ['1234', 1234],
    [' 1234 ', 1234],
    ['1,234', 1234],
    ['0482', 482],
  ])('parses %j', (raw, want) => {
    expect(normalizeCountInput(raw)).toBe(want);
  });

  it.each([[''], ['abc'], ['12.5'], ['12 34x'], ['-1']])('rejects %j', (raw) => {
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
