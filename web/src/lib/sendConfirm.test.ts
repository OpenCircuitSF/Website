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
